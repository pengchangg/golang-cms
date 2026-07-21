package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"regexp"
	"sort"
	"strings"
	"time"

	"cms/internal/audit"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type SystemAuthorizer interface {
	RequireSystemPermission(context.Context, Principal, string) error
}
type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}
type UserRepository interface {
	List(context.Context, database.Querier, UserFilter) (UserList, error)
	Get(context.Context, database.Querier, string, bool) (User, error)
	SetStatus(context.Context, database.Querier, string, UserStatus, time.Time) error
	RevokeSessions(context.Context, database.Querier, string, time.Time) error
	LockEnabledEmergencyAdmins(context.Context, database.Querier) (int, error)
	LockRoleIDs(context.Context, database.Querier, []string) (int, error)
	CreateSMSUser(context.Context, database.Querier, User, string, string, time.Time) error
	UpdatePhone(context.Context, database.Querier, string, string, string, time.Time) error
}

type UserService struct {
	db         database.Querier
	tx         TransactionRunner
	repository UserRepository
	authorizer SystemAuthorizer
	audit      audit.Writer
	now        func() time.Time
	newID      func() (string, error)
	newUserID  func() (string, error)
}

type UserDependencies struct {
	DB         database.Querier
	Transactor TransactionRunner
	Authorizer SystemAuthorizer
	Audit      audit.Writer
	Repository UserRepository
}

func NewUserService(d UserDependencies) *UserService {
	repository := d.Repository
	if repository == nil {
		repository = Repository{}
	}
	return &UserService{db: d.DB, tx: d.Transactor, repository: repository, authorizer: d.Authorizer, audit: d.Audit, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: newEventID, newUserID: newUserID}
}

func (s *UserService) List(ctx context.Context, principal Principal, filter UserFilter) (UserList, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.view"); err != nil {
		return UserList{}, err
	}
	return s.repository.List(ctx, s.db, filter)
}

func (s *UserService) Get(ctx context.Context, principal Principal, id string) (User, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.manage"); err != nil {
		return User{}, err
	}
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "roles.view"); err != nil {
		return User{}, err
	}
	return s.repository.Get(ctx, s.db, id, false)
}

func (s *UserService) Create(ctx context.Context, principal Principal, meta RequestMeta, request CreateUserRequest) (User, error) {
	details := validateCreateUser(request)
	phoneE164, phoneMasked, phoneErr := normalizeMainlandPhone(request.Phone)
	if phoneErr != nil {
		details = append(details, map[string]any{"path": "/phone", "code": "invalid_format", "message": "phone 必须是有效的大陆手机号"})
	}
	if len(details) != 0 {
		return User{}, validationFailed(details)
	}
	roleIDs := append([]string(nil), request.RoleIDs...)
	sort.Strings(roleIDs)
	var result User
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.manage"); err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "roles.manage"); err != nil {
			return err
		}
		count, err := s.repository.LockRoleIDs(ctx, q, roleIDs)
		if err != nil {
			return err
		}
		if count != len(roleIDs) {
			return validationFailed([]map[string]any{{"path": "/role_ids", "code": "unknown_role", "message": "包含不存在的角色"}})
		}
		id, err := s.newUserID()
		if err != nil {
			return err
		}
		now := s.now()
		user := User{UserSummary: UserSummary{ID: id, DisplayName: request.DisplayName, PhoneMasked: &phoneMasked, AuthMethods: []AuthMethod{AuthMethodSMS}, Status: UserEnabled, CreatedAt: now, UpdatedAt: now}, Phone: &phoneE164, RoleIDs: roleIDs}
		if err := s.repository.CreateSMSUser(ctx, q, user, phoneE164, phoneMasked, now); err != nil {
			if errors.Is(err, errPhoneConflict) {
				return &apperror.Error{Kind: apperror.KindConflict, Code: "phone_conflict", Message: "手机号已存在"}
			}
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "user_created", id, map[string]any{"phone_masked": phoneMasked, "role_ids": roleIDs}); err != nil {
			return err
		}
		result, err = s.repository.Get(ctx, q, id, false)
		return err
	})
	return result, err
}

func (s *UserService) UpdatePhone(ctx context.Context, principal Principal, meta RequestMeta, id string, request UpdatePhoneRequest) (User, error) {
	phoneE164, phoneMasked, err := normalizeMainlandPhone(request.Phone)
	if err != nil {
		return User{}, validationFailed([]map[string]any{{"path": "/phone", "code": "invalid_format", "message": "phone 必须是有效的大陆手机号"}})
	}
	var result User
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.manage"); err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "roles.view"); err != nil {
			return err
		}
		user, err := s.repository.Get(ctx, q, id, true)
		if err != nil {
			return err
		}
		if user.Phone == nil {
			return &apperror.Error{Kind: apperror.KindConflict, Code: "sms_credential_required", Message: "用户不是手机号账户"}
		}
		oldMasked := ""
		if user.PhoneMasked != nil {
			oldMasked = *user.PhoneMasked
		}
		if *user.Phone == phoneE164 {
			result = user
			return nil
		}
		now := s.now()
		if err := s.repository.UpdatePhone(ctx, q, id, phoneE164, phoneMasked, now); err != nil {
			if errors.Is(err, errPhoneConflict) {
				return &apperror.Error{Kind: apperror.KindConflict, Code: "phone_conflict", Message: "手机号已存在"}
			}
			return err
		}
		if err := s.repository.RevokeSessions(ctx, q, id, now); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "user_phone_updated", id, map[string]any{"phone_masked": map[string]any{"from": oldMasked, "to": phoneMasked}}); err != nil {
			return err
		}
		result, err = s.repository.Get(ctx, q, id, false)
		return err
	})
	return result, err
}

func (s *UserService) SetStatus(ctx context.Context, principal Principal, meta RequestMeta, id string, status UserStatus) (User, error) {
	if status != UserEnabled && status != UserDisabled {
		return User{}, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": "/status", "code": "invalid_value", "message": "status 必须是 enabled 或 disabled"}}}
	}
	var result User
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.manage"); err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "roles.view"); err != nil {
			return err
		}
		user, err := s.repository.Get(ctx, q, id, false)
		if err != nil {
			return err
		}
		if status == UserDisabled && user.Status == UserEnabled && user.EmergencyAdmin {
			// 按固定顺序先锁定全部应急管理员，防止并发禁用分别通过计数检查或形成交叉锁。
			count, err := s.repository.LockEnabledEmergencyAdmins(ctx, q)
			if err != nil {
				return err
			}
			user, err = s.repository.Get(ctx, q, id, true)
			if err != nil {
				return err
			}
			if user.Status == UserEnabled && user.EmergencyAdmin && count <= 1 {
				return &apperror.Error{Kind: apperror.KindConflict, Code: "last_emergency_admin_required", Message: "必须保留至少一个启用的应急管理员"}
			}
		} else {
			user, err = s.repository.Get(ctx, q, id, true)
			if err != nil {
				return err
			}
		}
		now := s.now()
		if user.Status != status {
			if err := s.repository.SetStatus(ctx, q, id, status, now); err != nil {
				return err
			}
		}
		if status == UserDisabled {
			if err := s.repository.RevokeSessions(ctx, q, id, now); err != nil {
				return err
			}
		}
		eventID, err := s.newID()
		if err != nil {
			return err
		}
		actorID, resourceID := principal.UserID, id
		actorName := principal.DisplayName
		if err := s.audit.Append(ctx, q, audit.Event{ID: eventID, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, ActorDisplayName: &actorName, Action: "user_status_updated", ResourceType: "user", ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: map[string]any{"status": map[string]any{"from": user.Status, "to": status}}}); err != nil {
			return err
		}
		result, err = s.repository.Get(ctx, q, id, false)
		return err
	})
	return result, err
}

func (s *UserService) GetWith(ctx context.Context, q database.Querier, id string, lock bool) (User, error) {
	return s.repository.Get(ctx, q, id, lock)
}

func newEventID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "evt_" + hex.EncodeToString(value[:]), nil
}

func newUserID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return "usr_" + hex.EncodeToString(value[:]), nil
}

var mainlandPhone = regexp.MustCompile(`^(?:\+86)?(1[3-9][0-9]{9})$`)

func normalizeMainlandPhone(value string) (string, string, error) {
	match := mainlandPhone.FindStringSubmatch(strings.TrimSpace(value))
	if match == nil {
		return "", "", errors.New("invalid mainland phone")
	}
	national := match[1]
	return "+86" + national, national[:3] + "****" + national[7:], nil
}

func validateCreateUser(request CreateUserRequest) []map[string]any {
	details := []map[string]any{}
	if len([]rune(strings.TrimSpace(request.DisplayName))) < 1 || len([]rune(request.DisplayName)) > 120 {
		details = append(details, map[string]any{"path": "/display_name", "code": "out_of_range", "message": "display_name 长度必须为 1 至 120"})
	}
	seen := map[string]bool{}
	for i, roleID := range request.RoleIDs {
		if roleID == "" || seen[roleID] {
			details = append(details, map[string]any{"path": "/role_ids/" + strconvItoa(i), "code": "invalid_value", "message": "role_id 不得为空或重复"})
			break
		}
		seen[roleID] = true
	}
	if request.RoleIDs == nil {
		details = append(details, map[string]any{"path": "/role_ids", "code": "required", "message": "role_ids 必填"})
	}
	return details
}

func validationFailed(details []map[string]any) error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: details}
}

func strconvItoa(value int) string {
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

func (s *UserService) appendAudit(ctx context.Context, q database.Querier, principal Principal, meta RequestMeta, action, resourceID string, changes map[string]any) error {
	eventID, err := s.newID()
	if err != nil {
		return err
	}
	actorID, actorName := principal.UserID, principal.DisplayName
	return s.audit.Append(ctx, q, audit.Event{ID: eventID, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, ActorDisplayName: &actorName, Action: action, ResourceType: "user", ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}
