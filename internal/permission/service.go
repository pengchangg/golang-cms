package permission

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	mysql "github.com/go-sql-driver/mysql"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}
type UserAccessor interface {
	GetWith(context.Context, database.Querier, string, bool) (identity.User, error)
}
type ModelValidator interface {
	ValidateActiveModels(context.Context, database.Querier, []string) error
}

var ErrInvalidModels = errors.New("模型不存在或已归档")

type Service struct {
	db         database.Querier
	tx         TransactionRunner
	repository Repository
	authorizer Authorizer
	audit      audit.Writer
	users      UserAccessor
	models     ModelValidator
	now        func() time.Time
	newID      func(string) (string, error)
}
type Dependencies struct {
	DB         database.Querier
	Transactor TransactionRunner
	Authorizer Authorizer
	Audit      audit.Writer
	Users      UserAccessor
	Models     ModelValidator
}

func NewService(d Dependencies) *Service {
	return &Service{db: d.DB, tx: d.Transactor, repository: Repository{}, authorizer: d.Authorizer, audit: d.Audit, users: d.Users, models: d.Models, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: randomID}
}

func (s *Service) ListRoles(ctx context.Context, principal identity.Principal) ([]Role, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesView); err != nil {
		return nil, err
	}
	return s.repository.ListRoles(ctx, s.db)
}
func (s *Service) GetRole(ctx context.Context, principal identity.Principal, id string) (Role, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesView); err != nil {
		return Role{}, err
	}
	return s.repository.GetRole(ctx, s.db, id, false)
}

func (s *Service) CreateRole(ctx context.Context, principal identity.Principal, meta RequestMeta, request CreateRoleRequest) (Role, error) {
	if details := validateRole(request.Key, request.DisplayName, request.Description); len(details) != 0 {
		return Role{}, validation(details)
	}
	id, err := s.newID("rol_")
	if err != nil {
		return Role{}, err
	}
	now := s.now()
	role := Role{ID: id, Key: request.Key, DisplayName: request.DisplayName, Description: request.Description, SystemPermissions: []string{}, ModelPermissions: []identity.ModelPermissions{}, CreatedAt: now, UpdatedAt: now}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		if err := s.repository.CreateRole(ctx, q, role); err != nil {
			var mysqlError *mysql.MySQLError
			if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
				return &apperror.Error{Kind: apperror.KindConflict, Code: "key_conflict", Message: "角色 key 已存在"}
			}
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "role_created", "role", id, map[string]any{"key": request.Key})
	})
	return role, err
}

func (s *Service) UpdateRole(ctx context.Context, principal identity.Principal, meta RequestMeta, id string, request UpdateRoleRequest) (Role, error) {
	if request.DisplayName == nil && request.Description == nil {
		return Role{}, validation([]map[string]any{detail("", "required", "至少提交一个可修改属性")})
	}
	var result Role
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, id, true)
		if err != nil {
			return err
		}
		changes := map[string]any{}
		if request.DisplayName != nil {
			if len([]rune(*request.DisplayName)) < 1 || len([]rune(*request.DisplayName)) > 120 {
				return validation([]map[string]any{detail("/display_name", "out_of_range", "display_name 长度必须为 1 至 120")})
			}
			changes["display_name"] = map[string]any{"from": role.DisplayName, "to": *request.DisplayName}
			role.DisplayName = *request.DisplayName
		}
		if request.Description != nil {
			if len([]rune(*request.Description)) > 1000 {
				return validation([]map[string]any{detail("/description", "out_of_range", "description 长度不能超过 1000")})
			}
			changes["description"] = map[string]any{"from": role.Description, "to": *request.Description}
			role.Description = *request.Description
		}
		role.UpdatedAt = s.now()
		if err := s.repository.UpdateRole(ctx, q, role); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "role_updated", "role", id, changes); err != nil {
			return err
		}
		result, err = s.repository.GetRole(ctx, q, id, false)
		return err
	})
	return result, err
}

func (s *Service) DeleteRole(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		if _, err := s.repository.GetRole(ctx, q, id, true); err != nil {
			return err
		}
		if err := s.repository.DeleteRole(ctx, q, id); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "role_deleted", "role", id, map[string]any{})
	})
}

func (s *Service) ReplaceUserRoles(ctx context.Context, principal identity.Principal, meta RequestMeta, userID string, roleIDs []string) (identity.User, error) {
	if duplicates(roleIDs) {
		return identity.User{}, validation([]map[string]any{detail("/role_ids", "duplicate", "role_ids 不得重复")})
	}
	roleIDs = sorted(roleIDs)
	var result identity.User
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		if s.users == nil {
			return &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "用户服务未装配"}
		}
		user, err := s.users.GetWith(ctx, q, userID, true)
		if err != nil {
			return err
		}
		count, err := s.repository.LockRoleIDs(ctx, q, roleIDs)
		if err != nil {
			return err
		}
		if count != len(roleIDs) {
			return validation([]map[string]any{detail("/role_ids", "unknown_role", "包含不存在的角色")})
		}
		if err := s.repository.ReplaceUserRoles(ctx, q, userID, roleIDs, s.now()); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "user_roles_replaced", "user", userID, map[string]any{"role_ids": map[string]any{"from": user.RoleIDs, "to": roleIDs}}); err != nil {
			return err
		}
		result, err = s.users.GetWith(ctx, q, userID, false)
		return err
	})
	return result, err
}

func (s *Service) ReplaceSystemPermissions(ctx context.Context, principal identity.Principal, meta RequestMeta, roleID string, values []string) (Role, error) {
	if path, ok := invalidCodes(values, ValidSystemPermission); ok {
		return Role{}, validation([]map[string]any{detail(path, "invalid_value", "包含未知或重复的系统权限")})
	}
	values = sorted(values)
	var result Role
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, roleID, true)
		if err != nil {
			return err
		}
		now := s.now()
		if err := s.repository.ReplaceSystemPermissions(ctx, q, roleID, values, now); err != nil {
			return err
		}
		if err := s.repository.TouchRole(ctx, q, roleID, now); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "role_system_permissions_replaced", "role", roleID, map[string]any{"permissions": map[string]any{"from": role.SystemPermissions, "to": values}}); err != nil {
			return err
		}
		result, err = s.repository.GetRole(ctx, q, roleID, false)
		return err
	})
	return result, err
}

func (s *Service) ReplaceModelPermissions(ctx context.Context, principal identity.Principal, meta RequestMeta, roleID string, grants []identity.ModelPermissions) (Role, error) {
	modelSeen := map[string]bool{}
	modelIDs := []string{}
	for i, grant := range grants {
		if grant.ModelID == "" || modelSeen[grant.ModelID] {
			return Role{}, validation([]map[string]any{detail("/grants/"+itoa(i)+"/model_id", "invalid_value", "model_id 为空或重复")})
		}
		modelSeen[grant.ModelID] = true
		modelIDs = append(modelIDs, grant.ModelID)
		if path, ok := invalidCodes(grant.Permissions, ValidModelPermission); ok {
			return Role{}, validation([]map[string]any{detail("/grants/"+itoa(i)+path, "invalid_value", "包含未知或重复的模型权限")})
		}
		grants[i].Permissions = sorted(grant.Permissions)
	}
	sort.Slice(grants, func(i, j int) bool { return grants[i].ModelID < grants[j].ModelID })
	sort.Strings(modelIDs)
	var result Role
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, roleID, true)
		if err != nil {
			return err
		}
		if len(modelIDs) != 0 {
			if s.models == nil {
				return &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "模型验证器未装配"}
			}
			if err := s.models.ValidateActiveModels(ctx, q, modelIDs); err != nil {
				if errors.Is(err, ErrInvalidModels) {
					return validation([]map[string]any{detail("/grants", "unknown_model", "包含不存在或已归档的模型")})
				}
				return err
			}
		}
		now := s.now()
		if err := s.repository.ReplaceModelPermissions(ctx, q, roleID, grants, now); err != nil {
			return err
		}
		if err := s.repository.TouchRole(ctx, q, roleID, now); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "role_model_permissions_replaced", "role", roleID, map[string]any{"grants": map[string]any{"from": role.ModelPermissions, "to": grants}}); err != nil {
			return err
		}
		result, err = s.repository.GetRole(ctx, q, roleID, false)
		return err
	})
	return result, err
}

var roleKey = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validateRole(key, name, description string) []map[string]any {
	result := []map[string]any{}
	if !roleKey.MatchString(key) {
		result = append(result, detail("/key", "invalid_format", "key 格式不合法"))
	}
	if len([]rune(name)) < 1 || len([]rune(name)) > 120 {
		result = append(result, detail("/display_name", "out_of_range", "display_name 长度必须为 1 至 120"))
	}
	if len([]rune(description)) > 1000 {
		result = append(result, detail("/description", "out_of_range", "description 长度不能超过 1000"))
	}
	return result
}
func invalidCodes(values []string, valid func(string) bool) (string, bool) {
	seen := map[string]bool{}
	for i, value := range values {
		if !valid(value) || seen[value] {
			return "/permissions/" + itoa(i), true
		}
		seen[value] = true
	}
	return "", false
}
func duplicates(values []string) bool {
	seen := map[string]bool{}
	for _, value := range values {
		if value == "" || seen[value] {
			return true
		}
		seen[value] = true
	}
	return false
}
func detail(path, code, message string) map[string]any {
	return map[string]any{"path": path, "code": code, "message": message}
}
func validation(details []map[string]any) error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: details}
}
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	result := ""
	for value > 0 {
		result = string(rune('0'+value%10)) + result
		value /= 10
	}
	return result
}
func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
func (s *Service) appendAudit(ctx context.Context, q database.Querier, principal identity.Principal, meta RequestMeta, action, resourceType, resourceID string, changes map[string]any) error {
	id, err := s.newID("evt_")
	if err != nil {
		return err
	}
	actorID := principal.UserID
	actorName := principal.DisplayName
	return s.audit.Append(ctx, q, audit.Event{ID: id, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, ActorDisplayName: &actorName, Action: action, ResourceType: resourceType, ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}
