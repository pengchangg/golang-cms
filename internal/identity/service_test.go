package identity

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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
	roleCount       *int
	highRiskRole    bool
	rolePermissions PermissionSet
	createErr       error
	updatePhoneErr  error
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
func (m *memoryUsers) LockRoleIDs(_ context.Context, _ database.Querier, ids []string) (LockedRoleSelection, error) {
	if m.roleCount != nil {
		return LockedRoleSelection{Count: *m.roleCount, HighRisk: m.highRiskRole, Permissions: m.rolePermissions}, nil
	}
	return LockedRoleSelection{Count: len(ids), HighRisk: m.highRiskRole, Permissions: m.rolePermissions}, nil
}
func (m *memoryUsers) CreateSMSUser(_ context.Context, _ database.Querier, user User, _, _ string, _ time.Time) error {
	if m.createErr != nil {
		return m.createErr
	}
	m.users[user.ID] = user
	return nil
}
func (m *memoryUsers) UpdatePhone(_ context.Context, _ database.Querier, id, phoneE164, phoneMasked string, now time.Time) error {
	if m.updatePhoneErr != nil {
		return m.updatePhoneErr
	}
	user := m.users[id]
	user.Phone = &phoneE164
	user.PhoneMasked = &phoneMasked
	user.UpdatedAt = now
	m.users[id] = user
	return nil
}

type allowAuthorizer struct{}

func (allowAuthorizer) RequireSystemPermission(context.Context, Principal, string) error { return nil }
func (allowAuthorizer) CurrentPrincipal(_ context.Context, _ database.Querier, principal Principal) (Principal, error) {
	return principal, nil
}

type recordingAuthorizer struct {
	permissions []string
	denied      string
}

type revokedAuthorizer struct{}

func (revokedAuthorizer) CurrentPrincipal(_ context.Context, _ database.Querier, principal Principal) (Principal, error) {
	principal.SystemPermissions = nil
	principal.ModelPermissions = nil
	principal.HighRiskRole = false
	return principal, nil
}
func (revokedAuthorizer) RequireSystemPermission(_ context.Context, principal Principal, permission string) error {
	for _, granted := range principal.SystemPermissions {
		if granted == permission {
			return nil
		}
	}
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}

func (a *recordingAuthorizer) RequireSystemPermission(_ context.Context, _ Principal, permission string) error {
	a.permissions = append(a.permissions, permission)
	if permission == a.denied {
		return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
	}
	return nil
}
func (a *recordingAuthorizer) CurrentPrincipal(_ context.Context, _ database.Querier, principal Principal) (Principal, error) {
	return principal, nil
}

type discardAudit struct{}

func (discardAudit) Append(context.Context, database.Querier, audit.Event) error { return nil }

type recordingAudit struct{ events []audit.Event }

func (a *recordingAudit) Append(_ context.Context, _ database.Querier, event audit.Event) error {
	a.events = append(a.events, event)
	return nil
}

func TestSetStatusProtectsLastEmergencyAdminConcurrently(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{
		"usr_1": {UserSummary: UserSummary{ID: "usr_1", Status: UserEnabled, EmergencyAdmin: true}},
		"usr_2": {UserSummary: UserSummary{ID: "usr_2", Status: UserEnabled, EmergencyAdmin: true}},
	}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newID = func() (string, error) { return "evt_test", nil }
	principal := Principal{UserID: "usr_actor", EmergencyAdmin: true}
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

func TestSetStatusLastEmergencyAdminReturnsHTTPConflict(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{
		"usr_emergency": {UserSummary: UserSummary{ID: "usr_emergency", Status: UserEnabled, EmergencyAdmin: true}},
	}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newID = func() (string, error) { return "evt_test", nil }
	mux := http.NewServeMux()
	NewHandler(service, func(*http.Request) (Principal, error) {
		return Principal{UserID: "usr_actor", EmergencyAdmin: true}, nil
	}).RegisterRoutes(mux)

	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/users/usr_emergency", bytes.NewBufferString(`{"status":"disabled"}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"last_emergency_admin_required"`) {
		t.Fatalf("PATCH /users/{id} status=%d body=%s", response.Code, response.Body.String())
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

func TestCreateSMSUserNormalizesPhoneAndRequiresBothManagePermissions(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{}}
	authorizer := &recordingAuthorizer{}
	auditWriter := &recordingAudit{}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: authorizer, Audit: auditWriter})
	service.newUserID = func() (string, error) { return "usr_sms", nil }
	service.newID = func() (string, error) { return "evt_test", nil }
	service.now = func() time.Time { return time.Date(2026, 7, 21, 1, 2, 3, 0, time.UTC) }

	user, err := service.Create(context.Background(), Principal{UserID: "usr_actor", DisplayName: "管理员"}, RequestMeta{RequestID: "req", IP: "127.0.0.1"}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000", RoleIDs: []string{"rol_b", "rol_a"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if len(authorizer.permissions) != 2 || authorizer.permissions[0] != "users.manage" || authorizer.permissions[1] != "roles.manage" {
		t.Fatalf("权限检查顺序 = %v", authorizer.permissions)
	}
	if user.Phone != nil {
		t.Fatalf("普通管理员响应泄露完整手机号: %v", user.Phone)
	}
	if user.PhoneMasked == nil || *user.PhoneMasked != "138****8000" {
		t.Fatalf("phone_masked = %v", user.PhoneMasked)
	}
	if len(user.AuthMethods) != 1 || user.AuthMethods[0] != AuthMethodSMS || user.EmergencyAdmin {
		t.Fatalf("认证方式或应急管理员标记错误: %+v", user.UserSummary)
	}
	if len(user.RoleIDs) != 2 || user.RoleIDs[0] != "rol_a" || user.RoleIDs[1] != "rol_b" {
		t.Fatalf("role_ids = %v", user.RoleIDs)
	}
	assertAuditMasksPhone(t, auditWriter.events, "13800138000")
}

func TestCreateSMSUserRejectsUnknownRoleBeforeWriting(t *testing.T) {
	count := 0
	repository := &memoryUsers{users: map[string]User{}, roleCount: &count}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newUserID = func() (string, error) { return "usr_sms", nil }

	_, err := service.Create(context.Background(), Principal{}, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "+8613800138000", RoleIDs: []string{"rol_missing"}})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "validation_failed" {
		t.Fatalf("Create() error = %v", err)
	}
	if len(repository.users) != 0 {
		t.Fatal("角色不存在时仍创建了用户")
	}
}

func TestCreateSMSUserReportsPhoneConflict(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{}, createErr: errPhoneConflict}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newUserID = func() (string, error) { return "usr_sms", nil }

	_, err := service.Create(context.Background(), Principal{}, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000", RoleIDs: []string{}})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "phone_conflict" || appError.Kind != apperror.KindConflict {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestCreateSMSUserStopsWhenRolesManageDenied(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{}}
	authorizer := &recordingAuthorizer{denied: "roles.manage"}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: authorizer, Audit: discardAudit{}})
	service.newUserID = func() (string, error) { return "usr_sms", nil }

	if _, err := service.Create(context.Background(), Principal{}, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000", RoleIDs: []string{}}); err == nil {
		t.Fatal("缺少 roles.manage 时 Create() 应拒绝请求")
	}
	if len(repository.users) != 0 {
		t.Fatal("权限检查失败后仍创建了用户")
	}
}

func TestCreateSMSUserRechecksRevokedPrincipalInsideTransaction(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: revokedAuthorizer{}, Audit: discardAudit{}})
	service.newUserID = func() (string, error) { return "usr_sms", nil }
	stale := Principal{SystemPermissions: []string{"users.manage", "roles.manage"}, HighRiskRole: true}

	_, err := service.Create(context.Background(), stale, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000", RoleIDs: []string{}})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "permission_denied" || len(repository.users) != 0 {
		t.Fatalf("Create() error = %v, users=%v", err, repository.users)
	}
}

func TestCreateSMSUserHighRiskRoleRequiresPrivilegedPrincipal(t *testing.T) {
	for _, test := range []struct {
		name      string
		principal Principal
		wantError bool
	}{
		{name: "普通管理员", principal: Principal{}, wantError: true},
		{name: "高危角色", principal: Principal{HighRiskRole: true}},
		{name: "应急管理员", principal: Principal{EmergencyAdmin: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &memoryUsers{users: map[string]User{}, highRiskRole: true}
			service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
			service.newUserID = func() (string, error) { return "usr_sms", nil }
			service.newID = func() (string, error) { return "evt_test", nil }
			_, err := service.Create(context.Background(), test.principal, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000", RoleIDs: []string{"rol_high_risk"}})
			if test.wantError {
				var appError *apperror.Error
				if !errors.As(err, &appError) || appError.Code != "permission_denied" || len(repository.users) != 0 {
					t.Fatalf("Create() error = %v, users=%v", err, repository.users)
				}
				return
			}
			if err != nil || len(repository.users) != 1 {
				t.Fatalf("Create() error = %v, users=%v", err, repository.users)
			}
		})
	}
}

func TestCreateSMSUserRejectsRolePermissionsOutsidePrincipalScope(t *testing.T) {
	repository := &memoryUsers{
		users:           map[string]User{},
		rolePermissions: PermissionSet{System: []string{"audit.view"}},
	}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newUserID = func() (string, error) { return "usr_sms", nil }
	principal := Principal{SystemPermissions: []string{"users.manage", "roles.manage"}}

	_, err := service.Create(context.Background(), principal, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000", RoleIDs: []string{"rol_audit"}})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "permission_denied" || len(repository.users) != 0 {
		t.Fatalf("Create() error = %v, users=%v", err, repository.users)
	}
}

func TestCreateSMSUserRequiresRoleIDs(t *testing.T) {
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: &memoryUsers{users: map[string]User{}}, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	_, err := service.Create(context.Background(), Principal{}, RequestMeta{}, CreateUserRequest{DisplayName: "手机用户", Phone: "13800138000"})
	var appError *apperror.Error
	if !errors.As(err, &appError) || len(appError.Details) == 0 || appError.Details[0]["path"] != "/role_ids" {
		t.Fatalf("Create() error = %v", err)
	}
}

func TestUpdatePhoneNormalizesRevokesSessionsAndMasksAudit(t *testing.T) {
	oldPhone, oldMasked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: UserSummary{ID: "usr_sms", PhoneMasked: &oldMasked, AuthMethods: []AuthMethod{AuthMethodSMS}, Status: UserEnabled}, Phone: &oldPhone}}, revokedSessions: map[string]int{}}
	auditWriter := &recordingAudit{}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: auditWriter})
	service.newID = func() (string, error) { return "evt_test", nil }

	user, err := service.UpdatePhone(context.Background(), Principal{UserID: "usr_actor", DisplayName: "管理员", HighRiskRole: true}, RequestMeta{RequestID: "req", IP: "127.0.0.1"}, "usr_sms", UpdatePhoneRequest{Phone: "13900139000"})
	if err != nil {
		t.Fatalf("UpdatePhone() error = %v", err)
	}
	if user.Phone != nil || user.PhoneMasked == nil || *user.PhoneMasked != "139****9000" || *repository.users["usr_sms"].Phone != "+8613900139000" {
		t.Fatalf("换号结果 = %+v", user)
	}
	if repository.revokedSessions["usr_sms"] != 1 {
		t.Fatalf("撤销 session 次数 = %d", repository.revokedSessions["usr_sms"])
	}
	assertAuditMasksPhone(t, auditWriter.events, "13900139000")
	assertAuditMasksPhone(t, auditWriter.events, "13800138000")
}

func TestUpdatePhoneDoesNothingWhenNormalizedPhoneIsUnchanged(t *testing.T) {
	phone, masked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: UserSummary{ID: "usr_sms", PhoneMasked: &masked, AuthMethods: []AuthMethod{AuthMethodSMS}}, Phone: &phone}}, revokedSessions: map[string]int{}}
	auditWriter := &recordingAudit{}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: auditWriter})

	if _, err := service.UpdatePhone(context.Background(), Principal{HighRiskRole: true}, RequestMeta{}, "usr_sms", UpdatePhoneRequest{Phone: "13800138000"}); err != nil {
		t.Fatalf("UpdatePhone() error = %v", err)
	}
	if repository.revokedSessions["usr_sms"] != 0 || len(auditWriter.events) != 0 {
		t.Fatalf("相同号码仍产生副作用: revoked=%d audit=%d", repository.revokedSessions["usr_sms"], len(auditWriter.events))
	}
}

func TestUpdatePhoneSameNumberStillRequiresHighRiskPrincipal(t *testing.T) {
	phone, masked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: UserSummary{ID: "usr_sms", PhoneMasked: &masked}, Phone: &phone}}, revokedSessions: map[string]int{}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})

	_, err := service.UpdatePhone(context.Background(), Principal{}, RequestMeta{}, "usr_sms", UpdatePhoneRequest{Phone: "13800138000"})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "permission_denied" || repository.revokedSessions["usr_sms"] != 0 {
		t.Fatalf("UpdatePhone() error = %v, revoked=%d", err, repository.revokedSessions["usr_sms"])
	}
}

func TestUpdatePhoneRequiresHighRiskOrEmergencyPrincipal(t *testing.T) {
	for _, test := range []struct {
		name      string
		principal Principal
		target    UserSummary
		wantError bool
	}{
		{name: "普通全权限角色", principal: Principal{SystemPermissions: []string{"users.manage", "roles.view"}}, wantError: true},
		{name: "高危角色", principal: Principal{HighRiskRole: true}},
		{name: "高危角色修改高危用户", principal: Principal{HighRiskRole: true}, target: UserSummary{HighRiskRole: true}},
		{name: "应急管理员", principal: Principal{EmergencyAdmin: true}},
	} {
		t.Run(test.name, func(t *testing.T) {
			oldPhone, oldMasked := "+8613800138000", "138****8000"
			target := test.target
			target.ID = "usr_sms"
			target.PhoneMasked = &oldMasked
			repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: target, Phone: &oldPhone}}, revokedSessions: map[string]int{}}
			service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
			service.newID = func() (string, error) { return "evt_test", nil }
			_, err := service.UpdatePhone(context.Background(), test.principal, RequestMeta{}, "usr_sms", UpdatePhoneRequest{Phone: "13900139000"})
			if test.wantError {
				var appError *apperror.Error
				if !errors.As(err, &appError) || appError.Code != "permission_denied" || *repository.users["usr_sms"].Phone != oldPhone || repository.revokedSessions["usr_sms"] != 0 {
					t.Fatalf("UpdatePhone() error = %v, user=%+v, revoked=%d", err, repository.users["usr_sms"], repository.revokedSessions["usr_sms"])
				}
				return
			}
			if err != nil || *repository.users["usr_sms"].Phone != "+8613900139000" || repository.revokedSessions["usr_sms"] != 1 {
				t.Fatalf("UpdatePhone() error = %v, user=%+v, revoked=%d", err, repository.users["usr_sms"], repository.revokedSessions["usr_sms"])
			}
		})
	}
}

func TestUpdatePhoneRejectsNonSMSUser(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{"usr_local": {UserSummary: UserSummary{ID: "usr_local", AuthMethods: []AuthMethod{AuthMethodLocal}, Status: UserEnabled}}}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})

	_, err := service.UpdatePhone(context.Background(), Principal{}, RequestMeta{}, "usr_local", UpdatePhoneRequest{Phone: "13800138000"})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "sms_credential_required" {
		t.Fatalf("UpdatePhone() error = %v", err)
	}
}

func TestEmergencyAdminPhoneCannotBeChangedThroughManagementAPI(t *testing.T) {
	phone, masked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_emergency": {UserSummary: UserSummary{ID: "usr_emergency", EmergencyAdmin: true, PhoneMasked: &masked}, Phone: &phone}}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	_, err := service.UpdatePhone(context.Background(), Principal{EmergencyAdmin: true}, RequestMeta{}, "usr_emergency", UpdatePhoneRequest{Phone: "13900139000"})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "permission_denied" || *repository.users["usr_emergency"].Phone != phone {
		t.Fatalf("UpdatePhone() error = %v, user=%+v", err, repository.users["usr_emergency"])
	}
}

func TestHighRiskPrincipalCannotManageEmergencyAdmin(t *testing.T) {
	phone, masked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_emergency": {UserSummary: UserSummary{ID: "usr_emergency", EmergencyAdmin: true, PhoneMasked: &masked, Status: UserEnabled}, Phone: &phone}}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	principal := Principal{UserID: "usr_high", HighRiskRole: true}

	if _, err := service.SetStatus(context.Background(), principal, RequestMeta{}, "usr_emergency", UserDisabled); err == nil {
		t.Fatal("高危角色禁用了应急管理员")
	}
	if _, err := service.UpdatePhone(context.Background(), principal, RequestMeta{}, "usr_emergency", UpdatePhoneRequest{Phone: "13900139000"}); err == nil {
		t.Fatal("高危角色修改了应急管理员手机号")
	}
	if repository.users["usr_emergency"].Status != UserEnabled || *repository.users["usr_emergency"].Phone != phone {
		t.Fatalf("应急管理员被修改: %+v", repository.users["usr_emergency"])
	}
}

func TestUpdatePhoneRequiresUsersManage(t *testing.T) {
	phone, masked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: UserSummary{ID: "usr_sms", PhoneMasked: &masked}, Phone: &phone}}}
	authorizer := &recordingAuthorizer{denied: "users.manage"}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: authorizer, Audit: discardAudit{}})

	if _, err := service.UpdatePhone(context.Background(), Principal{}, RequestMeta{}, "usr_sms", UpdatePhoneRequest{Phone: "13900139000"}); err == nil {
		t.Fatal("缺少 users.manage 时 UpdatePhone() 应拒绝请求")
	}
	if len(authorizer.permissions) != 1 || authorizer.permissions[0] != "users.manage" || *repository.users["usr_sms"].Phone != phone {
		t.Fatalf("权限拒绝后状态错误: permissions=%v user=%+v", authorizer.permissions, repository.users["usr_sms"])
	}
}

func TestUpdatePhoneRequiresRolesView(t *testing.T) {
	phone := "+8613800138000"
	repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: UserSummary{ID: "usr_sms"}, Phone: &phone}}}
	authorizer := &recordingAuthorizer{denied: "roles.view"}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: authorizer, Audit: discardAudit{}})

	if _, err := service.UpdatePhone(context.Background(), Principal{}, RequestMeta{}, "usr_sms", UpdatePhoneRequest{Phone: "13900139000"}); err == nil {
		t.Fatal("缺少 roles.view 时 UpdatePhone() 应拒绝请求")
	}
	if len(authorizer.permissions) != 2 || authorizer.permissions[0] != "users.manage" || authorizer.permissions[1] != "roles.view" || *repository.users["usr_sms"].Phone != phone {
		t.Fatalf("权限检查或用户状态异常: permissions=%v user=%+v", authorizer.permissions, repository.users["usr_sms"])
	}
}

func TestUpdatePhoneReportsPhoneConflictWithoutRevokingSessions(t *testing.T) {
	phone, masked := "+8613800138000", "138****8000"
	repository := &memoryUsers{users: map[string]User{"usr_sms": {UserSummary: UserSummary{ID: "usr_sms", PhoneMasked: &masked}, Phone: &phone}}, revokedSessions: map[string]int{}, updatePhoneErr: errPhoneConflict}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})

	_, err := service.UpdatePhone(context.Background(), Principal{HighRiskRole: true}, RequestMeta{}, "usr_sms", UpdatePhoneRequest{Phone: "13900139000"})
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "phone_conflict" || repository.revokedSessions["usr_sms"] != 0 {
		t.Fatalf("UpdatePhone() error = %v, revoked=%d", err, repository.revokedSessions["usr_sms"])
	}
}

func TestNormalizeMainlandPhoneRejectsInvalidValues(t *testing.T) {
	for _, value := range []string{"", "12800138000", "+85213800138000", "1380013800"} {
		if _, _, err := normalizeMainlandPhone(value); err == nil {
			t.Fatalf("normalizeMainlandPhone(%q) 未拒绝非法号码", value)
		}
	}
}

func TestUserHTTPCreateAndUpdatePhone(t *testing.T) {
	repository := &memoryUsers{users: map[string]User{}, revokedSessions: map[string]int{}}
	service := NewUserService(UserDependencies{Transactor: &serialTransactor{}, Repository: repository, Authorizer: allowAuthorizer{}, Audit: discardAudit{}})
	service.newUserID = func() (string, error) { return "usr_sms", nil }
	service.newID = func() (string, error) { return "evt_test", nil }
	mux := http.NewServeMux()
	NewHandler(service, func(*http.Request) (Principal, error) {
		return Principal{UserID: "usr_actor", DisplayName: "管理员", HighRiskRole: true}, nil
	}).RegisterRoutes(mux)

	create := httptest.NewRequest(http.MethodPost, "/api/admin/v1/users", bytes.NewBufferString(`{"display_name":"手机用户","phone":"13800138000","role_ids":[]}`))
	create.RemoteAddr = "127.0.0.1:1234"
	created := httptest.NewRecorder()
	mux.ServeHTTP(created, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("POST /users status = %d, body = %s", created.Code, created.Body.String())
	}
	var createdUser User
	if err := json.Unmarshal(created.Body.Bytes(), &createdUser); err != nil {
		t.Fatal(err)
	}
	if createdUser.Phone != nil || *repository.users["usr_sms"].Phone != "+8613800138000" {
		t.Fatalf("POST /users response = %+v", createdUser)
	}

	update := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/users/usr_sms/phone", bytes.NewBufferString(`{"phone":"13900139000"}`))
	update.RemoteAddr = "127.0.0.1:1234"
	updated := httptest.NewRecorder()
	mux.ServeHTTP(updated, update)
	if updated.Code != http.StatusOK {
		t.Fatalf("PATCH /users/{id}/phone status = %d, body = %s", updated.Code, updated.Body.String())
	}
	var updatedUser User
	if err := json.Unmarshal(updated.Body.Bytes(), &updatedUser); err != nil {
		t.Fatal(err)
	}
	if updatedUser.Phone != nil || *repository.users["usr_sms"].Phone != "+8613900139000" || repository.revokedSessions["usr_sms"] != 1 {
		t.Fatalf("PATCH /users/{id}/phone response = %+v, revoked=%d", updatedUser, repository.revokedSessions["usr_sms"])
	}
}

func assertAuditMasksPhone(t *testing.T, events []audit.Event, forbidden string) {
	t.Helper()
	if len(events) != 1 {
		t.Fatalf("审计事件数 = %d", len(events))
	}
	encoded, err := json.Marshal(events[0].Changes)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), forbidden) {
		t.Fatalf("审计包含完整手机号: %s", encoded)
	}
}
