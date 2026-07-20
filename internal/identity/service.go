package identity

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
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
}

type UserService struct {
	db         database.Querier
	tx         TransactionRunner
	repository UserRepository
	authorizer SystemAuthorizer
	audit      audit.Writer
	now        func() time.Time
	newID      func() (string, error)
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
	return &UserService{db: d.DB, tx: d.Transactor, repository: repository, authorizer: d.Authorizer, audit: d.Audit, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: newEventID}
}

func (s *UserService) List(ctx context.Context, principal Principal, filter UserFilter) (UserList, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.view"); err != nil {
		return UserList{}, err
	}
	return s.repository.List(ctx, s.db, filter)
}

func (s *UserService) Get(ctx context.Context, principal Principal, id string) (User, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "users.view"); err != nil {
		return User{}, err
	}
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "roles.view"); err != nil {
		return User{}, err
	}
	return s.repository.Get(ctx, s.db, id, false)
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
