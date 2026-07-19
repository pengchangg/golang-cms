package identity

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type serialTransactor struct{ mutex sync.Mutex }

func (t *serialTransactor) WithinTx(ctx context.Context, _ *sql.TxOptions, fn func(database.Querier) error) error {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return fn(nil)
}

type memoryUsers struct {
	users           map[string]User
	revokedSessions map[string]int
}

func (m *memoryUsers) List(context.Context, database.Querier, UserFilter) (UserList, error) {
	return UserList{}, nil
}
func (m *memoryUsers) Get(_ context.Context, _ database.Querier, id string, _ bool) (User, error) {
	user, ok := m.users[id]
	if !ok {
		return User{}, notFound("用户不存在")
	}
	return user, nil
}
func (m *memoryUsers) SetStatus(_ context.Context, _ database.Querier, id string, status UserStatus, now time.Time) error {
	user := m.users[id]
	user.Status = status
	user.UpdatedAt = now
	m.users[id] = user
	return nil
}
func (m *memoryUsers) RevokeSessions(_ context.Context, _ database.Querier, id string, _ time.Time) error {
	if m.revokedSessions == nil {
		m.revokedSessions = map[string]int{}
	}
	m.revokedSessions[id]++
	return nil
}
func (m *memoryUsers) LockEnabledEmergencyAdmins(context.Context, database.Querier) (int, error) {
	count := 0
	for _, user := range m.users {
		if user.Status == UserEnabled && user.EmergencyAdmin {
			count++
		}
	}
	return count, nil
}

type allowAuthorizer struct{}

func (allowAuthorizer) RequireSystemPermission(context.Context, Principal, string) error { return nil }

type recordingAuthorizer struct {
	permissions []string
	denied      string
}

func (a *recordingAuthorizer) RequireSystemPermission(_ context.Context, _ Principal, permission string) error {
	a.permissions = append(a.permissions, permission)
	if permission == a.denied {
		return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
	}
	return nil
}

type discardAudit struct{}

func (discardAudit) Append(context.Context, database.Querier, audit.Event) error { return nil }

func TestSetStatusProtectsLastEmergencyAdminConcurrently(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{
		"usr_1": {UserSummary: UserSummary{ID: "usr_1", Status: UserEnabled, EmergencyAdmin: true}},
		"usr_2": {UserSummary: UserSummary{ID: "usr_2", Status: UserEnabled, EmergencyAdmin: true}},
	}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newID = func() (string, error) { return "evt_test", nil }
	principal := Principal{UserID: "usr_actor"}
	meta := RequestMeta{RequestID: "req", IP: "127.0.0.1"}

	start := make(chan struct{})
	errorsFound := make(chan error, 2)
	for _, id := range []string{"usr_1", "usr_2"} {
		go func(userID string) {
			<-start
			_, err := service.SetStatus(context.Background(), principal, meta, userID, UserDisabled)
			errorsFound <- err
		}(id)
	}
	close(start)

	succeeded, protected := 0, 0
	for range 2 {
		err := <-errorsFound
		if err == nil {
			succeeded++
			continue
		}
		var appError *apperror.Error
		if !errors.As(err, &appError) || appError.Code != "last_emergency_admin_required" {
			t.Fatalf("SetStatus() error = %v", err)
		}
		protected++
	}
	if succeeded != 1 || protected != 1 {
		t.Fatalf("成功=%d, 受保护=%d", succeeded, protected)
	}
}

func TestSetStatusRevokesSessionsOnlyWhenDisabling(t *testing.T) {
	repository := &memoryUsers{
		users:           map[string]User{"usr_1": {UserSummary: UserSummary{ID: "usr_1", Status: UserEnabled}}},
		revokedSessions: map[string]int{},
	}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newID = func() (string, error) { return "evt_test", nil }

	if _, err := service.SetStatus(context.Background(), Principal{UserID: "usr_actor"}, RequestMeta{}, "usr_1", UserDisabled); err != nil {
		t.Fatalf("禁用用户失败: %v", err)
	}
	if repository.revokedSessions["usr_1"] != 1 {
		t.Fatalf("撤销 session 次数 = %d，期望 1", repository.revokedSessions["usr_1"])
	}
	if _, err := service.SetStatus(context.Background(), Principal{UserID: "usr_actor"}, RequestMeta{}, "usr_1", UserEnabled); err != nil {
		t.Fatalf("重新启用用户失败: %v", err)
	}
	if repository.revokedSessions["usr_1"] != 1 {
		t.Fatalf("重新启用恢复或再次修改了 session，撤销次数 = %d", repository.revokedSessions["usr_1"])
	}
}

func TestSetStatusRequiresUsersManageAndRolesView(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{"usr_1": {UserSummary: UserSummary{ID: "usr_1", Status: UserEnabled}}}}
	authorizer := &recordingAuthorizer{denied: "roles.view"}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: authorizer, Audit: discardAudit{}})

	_, err := service.SetStatus(context.Background(), Principal{UserID: "usr_actor"}, RequestMeta{}, "usr_1", UserDisabled)
	if err == nil {
		t.Fatal("缺少 roles.view 时 SetStatus() 应拒绝请求")
	}
	if len(authorizer.permissions) != 2 || authorizer.permissions[0] != "users.manage" || authorizer.permissions[1] != "roles.view" {
		t.Fatalf("权限检查顺序 = %v", authorizer.permissions)
	}
	if repository.users["usr_1"].Status != UserEnabled {
		t.Fatal("权限检查失败后用户状态发生变化")
	}
}
