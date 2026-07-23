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
type RoleAuthorizer interface {
	Authorizer
	CurrentPrincipal(context.Context, database.Querier, identity.Principal) (identity.Principal, error)
}
type UserAccessor interface {
	GetWith(context.Context, database.Querier, string, bool) (identity.User, error)
}
type ModelValidator interface {
	ActiveModelIDs(context.Context) ([]string, error)
	ValidateActiveModels(context.Context, database.Querier, []string) error
}
type ConfigNamespaceValidator interface {
	ValidateActiveConfigNamespaces(context.Context, database.Querier, []string) error
}

var ErrInvalidModels = errors.New("模型不存在或已归档")
var ErrInvalidConfigNamespaces = errors.New("配置命名空间不存在或已归档")

type Service struct {
	db                       database.Querier
	tx                       TransactionRunner
	repository               Repository
	authorizer               RoleAuthorizer
	audit                    audit.Writer
	users                    UserAccessor
	models                   ModelValidator
	configNamespaces         ConfigNamespaceValidator
	activeConfigNamespaceIDs ActiveConfigNamespaceProvider
	now                      func() time.Time
	newID                    func(string) (string, error)
}
type Dependencies struct {
	DB                       database.Querier
	Transactor               TransactionRunner
	Authorizer               RoleAuthorizer
	Audit                    audit.Writer
	Users                    UserAccessor
	Models                   ModelValidator
	ConfigNamespaces         ConfigNamespaceValidator
	ActiveConfigNamespaceIDs ActiveConfigNamespaceProvider
}

func NewService(d Dependencies) *Service {
	return &Service{db: d.DB, tx: d.Transactor, repository: Repository{}, authorizer: d.Authorizer, audit: d.Audit, users: d.Users, models: d.Models, configNamespaces: d.ConfigNamespaces, activeConfigNamespaceIDs: d.ActiveConfigNamespaceIDs, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: randomID}
}

func (s *Service) ListRoles(ctx context.Context, principal identity.Principal) ([]Role, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesView); err != nil {
		return nil, err
	}
	roles, err := s.repository.ListRoles(ctx, s.db)
	if err != nil {
		return nil, err
	}
	if err := s.populateHighRiskGrants(ctx, roles); err != nil {
		return nil, err
	}
	return roles, nil
}
func (s *Service) GetRole(ctx context.Context, principal identity.Principal, id string) (Role, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesView); err != nil {
		return Role{}, err
	}
	role, err := s.repository.GetRole(ctx, s.db, id, false)
	if err != nil {
		return Role{}, err
	}
	roles := []Role{role}
	if err := s.populateHighRiskGrants(ctx, roles); err != nil {
		return Role{}, err
	}
	return roles[0], nil
}

func (s *Service) CreateRole(ctx context.Context, principal identity.Principal, meta RequestMeta, request CreateRoleRequest) (Role, error) {
	if details := validateRole(request.Key, request.DisplayName, request.Description); len(details) != 0 {
		return Role{}, validation(details)
	}
	configPermissions, namespaceIDs, err := validateConfigNamespaceGrants(request.ConfigNamespacePermissions, "/config_namespace_permissions")
	if err != nil {
		return Role{}, err
	}
	id, err := s.newID("rol_")
	if err != nil {
		return Role{}, err
	}
	now := s.now()
	role := Role{ID: id, Key: request.Key, Kind: RoleKindCustom, DisplayName: request.DisplayName, Description: request.Description, SystemPermissions: []string{}, ModelPermissions: []identity.ModelPermissions{}, ConfigNamespacePermissions: configPermissions, CreatedAt: now, UpdatedAt: now}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		refreshed, err := s.authorizer.CurrentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		principal = refreshed
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		if !principal.CanDelegate(identity.PermissionSet{ConfigNamespacePermissions: configPermissions}) {
			return permissionDenied()
		}
		if err := s.validateActiveConfigNamespaces(ctx, q, namespaceIDs, "/config_namespace_permissions"); err != nil {
			return err
		}
		if err := s.repository.CreateRole(ctx, q, role); err != nil {
			var mysqlError *mysql.MySQLError
			if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
				return &apperror.Error{Kind: apperror.KindConflict, Code: "key_conflict", Message: "角色 key 已存在"}
			}
			return err
		}
		if err := s.repository.ReplaceConfigNamespacePermissions(ctx, q, id, configPermissions, now); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "role_created", "role", id, map[string]any{"key": request.Key, "config_namespace_permissions": configPermissions})
	})
	return role, err
}

func (s *Service) UpdateRole(ctx context.Context, principal identity.Principal, meta RequestMeta, id string, request UpdateRoleRequest) (Role, error) {
	if request.DisplayName == nil && request.Description == nil && request.ConfigNamespacePermissions == nil {
		return Role{}, validation([]map[string]any{detail("", "required", "至少提交一个可修改属性")})
	}
	var configPermissions []identity.ConfigNamespacePermissions
	var namespaceIDs []string
	if request.ConfigNamespacePermissions != nil {
		var err error
		configPermissions, namespaceIDs, err = validateConfigNamespaceGrants(*request.ConfigNamespacePermissions, "/config_namespace_permissions")
		if err != nil {
			return Role{}, err
		}
	}
	var result Role
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		refreshed, err := s.authorizer.CurrentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		principal = refreshed
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, id, true)
		if err != nil {
			return err
		}
		if role.Kind == RoleKindHighRisk {
			return builtinRoleImmutable()
		}
		now := s.now()
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
		if request.ConfigNamespacePermissions != nil {
			requested := identity.PermissionSet{System: role.SystemPermissions, Models: role.ModelPermissions, ConfigNamespacePermissions: configPermissions}
			if err := s.authorizeRoleGrantReplacement(ctx, q, principal, role, requested); err != nil {
				return err
			}
			if err := s.validateActiveConfigNamespaces(ctx, q, namespaceIDs, "/config_namespace_permissions"); err != nil {
				return err
			}
			changes["config_namespace_permissions"] = map[string]any{"from": role.ConfigNamespacePermissions, "to": configPermissions}
			if err := s.repository.ReplaceConfigNamespacePermissions(ctx, q, id, configPermissions, now); err != nil {
				return err
			}
			role.ConfigNamespacePermissions = configPermissions
		}
		role.UpdatedAt = now
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
		refreshed, err := s.authorizer.CurrentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		principal = refreshed
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, id, true)
		if err != nil {
			return err
		}
		if role.Kind == RoleKindHighRisk {
			return builtinRoleImmutable()
		}
		if !principal.CanManageSecurityTier() {
			assigned, err := s.repository.IsRoleAssignedToUser(ctx, q, role.ID, principal.UserID)
			if err != nil {
				return err
			}
			current := identity.PermissionSet{System: role.SystemPermissions, Models: role.ModelPermissions, ConfigNamespacePermissions: role.ConfigNamespacePermissions}
			if !canReplaceRoleGrants(principal, assigned, current, current) {
				return permissionDenied()
			}
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
		refreshed, err := s.authorizer.CurrentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		principal = refreshed
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
		currentRoles, requestedRoles, err := s.repository.LockRoleTransition(ctx, q, user.RoleIDs, roleIDs)
		if err != nil {
			return err
		}
		if requestedRoles.Count != len(roleIDs) {
			return validation([]map[string]any{detail("/role_ids", "unknown_role", "包含不存在的角色")})
		}
		if err := authorizeRoleReplacement(principal, user, currentRoles, requestedRoles); err != nil {
			return err
		}
		if err := s.repository.ReplaceUserRoles(ctx, q, userID, roleIDs, s.now()); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "user_roles_replaced", "user", userID, map[string]any{"role_ids": map[string]any{"from": user.RoleIDs, "to": roleIDs}, "high_risk_role": map[string]any{"from": user.HighRiskRole, "to": requestedRoles.HighRisk}}); err != nil {
			return err
		}
		result, err = s.users.GetWith(ctx, q, userID, false)
		return err
	})
	return result.VisibleTo(principal), err
}

func (s *Service) ReplaceSystemPermissions(ctx context.Context, principal identity.Principal, meta RequestMeta, roleID string, values []string) (Role, error) {
	if path, ok := invalidCodes(values, ValidSystemPermission); ok {
		return Role{}, validation([]map[string]any{detail(path, "invalid_value", "包含未知或重复的系统权限")})
	}
	values = sorted(values)
	var result Role
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		refreshed, err := s.authorizer.CurrentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		principal = refreshed
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, roleID, true)
		if err != nil {
			return err
		}
		if role.Kind == RoleKindHighRisk {
			return builtinRoleImmutable()
		}
		if err := s.authorizeRoleGrantReplacement(ctx, q, principal, role, identity.PermissionSet{System: values, Models: role.ModelPermissions, ConfigNamespacePermissions: role.ConfigNamespacePermissions}); err != nil {
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
		refreshed, err := s.authorizer.CurrentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		principal = refreshed
		if err := s.authorizer.RequireSystemPermission(ctx, principal, RolesManage); err != nil {
			return err
		}
		role, err := s.repository.GetRole(ctx, q, roleID, true)
		if err != nil {
			return err
		}
		if role.Kind == RoleKindHighRisk {
			return builtinRoleImmutable()
		}
		if err := s.authorizeRoleGrantReplacement(ctx, q, principal, role, identity.PermissionSet{System: role.SystemPermissions, Models: grants, ConfigNamespacePermissions: role.ConfigNamespacePermissions}); err != nil {
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

func (s *Service) populateHighRiskGrants(ctx context.Context, roles []Role) error {
	var highRisk *Role
	for i := range roles {
		if roles[i].Kind == RoleKindHighRisk {
			highRisk = &roles[i]
			break
		}
	}
	if highRisk == nil {
		return nil
	}
	if s.models == nil {
		return &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "模型验证器未装配"}
	}
	modelIDs, err := s.models.ActiveModelIDs(ctx)
	if err != nil {
		return err
	}
	var configNamespaceIDs []string
	if s.activeConfigNamespaceIDs != nil {
		configNamespaceIDs, err = s.activeConfigNamespaceIDs.ActiveConfigNamespaceIDs(ctx)
		if err != nil {
			return err
		}
	}
	highRisk.SystemPermissions, highRisk.ModelPermissions, highRisk.ConfigNamespacePermissions = emergencyPermissions(modelIDs, configNamespaceIDs)
	return nil
}

func canManageHighRiskRole(principal identity.Principal) bool {
	return principal.EmergencyAdmin || principal.HighRiskRole
}

func authorizeRoleReplacement(principal identity.Principal, target identity.User, currentRoles, requestedRoles identity.LockedRoleSelection) error {
	if target.EmergencyAdmin && !principal.EmergencyAdmin {
		return permissionDenied()
	}
	if principal.UserID == target.ID && !principal.CanManageSecurityTier() {
		return permissionDenied()
	}
	if (target.HighRiskRole || currentRoles.HighRisk || requestedRoles.HighRisk) && !canManageHighRiskRole(principal) {
		return permissionDenied()
	}
	if !principal.CanDelegate(currentRoles.Permissions) || !principal.CanDelegate(requestedRoles.Permissions) {
		return permissionDenied()
	}
	return nil
}

func (s *Service) authorizeRoleGrantReplacement(ctx context.Context, q database.Querier, principal identity.Principal, role Role, permissions identity.PermissionSet) error {
	if principal.CanManageSecurityTier() {
		return nil
	}
	assigned, err := s.repository.IsRoleAssignedToUser(ctx, q, role.ID, principal.UserID)
	if err != nil {
		return err
	}
	current := identity.PermissionSet{System: role.SystemPermissions, Models: role.ModelPermissions, ConfigNamespacePermissions: role.ConfigNamespacePermissions}
	if !canReplaceRoleGrants(principal, assigned, current, permissions) {
		return permissionDenied()
	}
	return nil
}

func canReplaceRoleGrants(principal identity.Principal, assigned bool, current, requested identity.PermissionSet) bool {
	return principal.CanManageSecurityTier() || !assigned && principal.CanDelegate(current) && principal.CanDelegate(requested)
}

func builtinRoleImmutable() error {
	return &apperror.Error{Kind: apperror.KindConflict, Code: "builtin_role_immutable", Message: "内置高危角色不可修改或删除"}
}

func permissionDenied() error {
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
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

func validateConfigNamespaceGrants(grants []identity.ConfigNamespacePermissions, basePath string) ([]identity.ConfigNamespacePermissions, []string, error) {
	namespaceSeen := map[string]bool{}
	namespaceIDs := make([]string, 0, len(grants))
	result := append([]identity.ConfigNamespacePermissions(nil), grants...)
	for i, grant := range result {
		path := basePath + "/" + itoa(i)
		if grant.ConfigNamespaceID == "" || namespaceSeen[grant.ConfigNamespaceID] {
			return nil, nil, validation([]map[string]any{detail(path+"/config_namespace_id", "invalid_value", "config_namespace_id 为空或重复")})
		}
		if len(grant.Permissions) == 0 {
			return nil, nil, validation([]map[string]any{detail(path+"/permissions", "required", "配置命名空间权限不能为空")})
		}
		namespaceSeen[grant.ConfigNamespaceID] = true
		namespaceIDs = append(namespaceIDs, grant.ConfigNamespaceID)
		if invalidPath, invalid := invalidCodes(grant.Permissions, ValidConfigNamespacePermission); invalid {
			return nil, nil, validation([]map[string]any{detail(path+invalidPath, "invalid_value", "包含未知或重复的配置命名空间权限")})
		}
		result[i].Permissions = sorted(grant.Permissions)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ConfigNamespaceID < result[j].ConfigNamespaceID })
	sort.Strings(namespaceIDs)
	return result, namespaceIDs, nil
}

func (s *Service) validateActiveConfigNamespaces(ctx context.Context, q database.Querier, namespaceIDs []string, path string) error {
	if len(namespaceIDs) == 0 {
		return nil
	}
	if s.configNamespaces == nil {
		return &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "配置命名空间验证器未装配"}
	}
	if err := s.configNamespaces.ValidateActiveConfigNamespaces(ctx, q, namespaceIDs); err != nil {
		if errors.Is(err, ErrInvalidConfigNamespaces) {
			return validation([]map[string]any{detail(path, "unknown_config_namespace", "包含不存在或已归档的配置命名空间")})
		}
		return err
	}
	return nil
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
