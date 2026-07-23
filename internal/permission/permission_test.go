package permission

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type fixedTransactionalPermissions struct{ permissions identity.PermissionSet }

func (p fixedTransactionalPermissions) PermissionsWith(context.Context, database.Querier, string) (identity.PermissionSet, error) {
	return p.permissions, nil
}

type fixedConfigNamespaceValidator struct{ err error }

func (v fixedConfigNamespaceValidator) ValidateActiveConfigNamespaces(context.Context, database.Querier, []string) error {
	return v.err
}

func TestPrincipalAuthorizerDefaultsToDeny(t *testing.T) {
	authorizer := PrincipalAuthorizer{}
	if err := authorizer.RequireSystemPermission(context.Background(), identity.Principal{}, ModelsView); err == nil {
		t.Fatal("RequireSystemPermission() expected an error")
	}
	if err := authorizer.RequireSystemPermission(context.Background(), identity.Principal{SystemPermissions: []string{ModelsView}}, ModelsView); err != nil {
		t.Fatalf("RequireSystemPermission() error = %v", err)
	}
}

func TestPrincipalAuthorizerRefreshesPrincipalInsideTransaction(t *testing.T) {
	authorizer := PrincipalAuthorizer{Provider: fixedTransactionalPermissions{permissions: identity.PermissionSet{System: []string{UsersView}}}}
	stale := identity.Principal{UserID: "usr_actor", HighRiskRole: true, SystemPermissions: []string{RolesManage}}
	refreshed, err := authorizer.CurrentPrincipal(context.Background(), nil, stale)
	if err != nil {
		t.Fatalf("CurrentPrincipal() error = %v", err)
	}
	if refreshed.HighRiskRole || len(refreshed.SystemPermissions) != 1 || refreshed.SystemPermissions[0] != UsersView {
		t.Fatalf("CurrentPrincipal() = %+v", refreshed)
	}
}

func TestReplacementValidationRejectsUnknownAndDuplicates(t *testing.T) {
	if _, invalid := invalidCodes([]string{ModelsView, ModelsView}, ValidSystemPermission); !invalid {
		t.Fatal("重复系统权限被接受")
	}
	if _, invalid := invalidCodes([]string{"unknown.permission"}, ValidSystemPermission); !invalid {
		t.Fatal("未知系统权限被接受")
	}
	if !duplicates([]string{"rol_1", "rol_1"}) {
		t.Fatal("重复角色 ID 被接受")
	}
}

func TestConfigNamespaceGrantValidationIsStrictAndSorted(t *testing.T) {
	grants, namespaceIDs, err := validateConfigNamespaceGrants([]identity.ConfigNamespacePermissions{
		{ConfigNamespaceID: "cns_b", Permissions: []string{ConfigUpdate, ConfigView}},
		{ConfigNamespaceID: "cns_a", Permissions: []string{ConfigPublish}},
	}, "/config_namespace_permissions")
	if err != nil {
		t.Fatalf("validateConfigNamespaceGrants() error = %v", err)
	}
	if !slices.Equal(namespaceIDs, []string{"cns_a", "cns_b"}) || grants[0].ConfigNamespaceID != "cns_a" || !slices.Equal(grants[1].Permissions, []string{ConfigUpdate, ConfigView}) {
		t.Fatalf("规范化结果 grants=%v namespaceIDs=%v", grants, namespaceIDs)
	}
	for _, invalid := range [][]identity.ConfigNamespacePermissions{
		{{ConfigNamespaceID: "cns_site", Permissions: []string{"content.view"}}},
		{{ConfigNamespaceID: "cns_site", Permissions: []string{"config.unknown"}}},
		{{ConfigNamespaceID: "cns_site", Permissions: []string{ConfigView, ConfigView}}},
		{{ConfigNamespaceID: "cns_site"}, {ConfigNamespaceID: "cns_site"}},
		{{ConfigNamespaceID: "", Permissions: []string{ConfigView}}},
	} {
		if _, _, err := validateConfigNamespaceGrants(invalid, "/config_namespace_permissions"); err == nil {
			t.Fatalf("非法授权被接受: %v", invalid)
		}
	}
}

func TestConfigNamespaceMustBeActive(t *testing.T) {
	service := Service{configNamespaces: fixedConfigNamespaceValidator{err: ErrInvalidConfigNamespaces}}
	err := service.validateActiveConfigNamespaces(context.Background(), nil, []string{"archived"}, "/config_namespace_permissions")
	var appError *apperror.Error
	if !errors.As(err, &appError) || appError.Code != "validation_failed" || appError.Kind != apperror.KindInvalidArgument {
		t.Fatalf("validateActiveConfigNamespaces() = %v", err)
	}
	service.configNamespaces = nil
	err = service.validateActiveConfigNamespaces(context.Background(), nil, []string{"active"}, "/config_namespace_permissions")
	if !errors.As(err, &appError) || appError.Kind != apperror.KindInternal {
		t.Fatalf("未装配 validator 时应拒绝非空授权，得到 %v", err)
	}
}

func TestAllDeclaredSystemPermissionsAreValid(t *testing.T) {
	for _, code := range SystemPermissions() {
		if !ValidSystemPermission(code) {
			t.Fatalf("系统权限 %q 未被识别", code)
		}
	}
}

func TestDeprecatedTransferPermissionsAreInvalid(t *testing.T) {
	for _, code := range []string{"transfers.execute", "transfers.download"} {
		if ValidSystemPermission(code) {
			t.Fatalf("废弃系统权限 %q 仍然有效", code)
		}
	}
}

func TestEmergencyPermissionsAlwaysGrantEverything(t *testing.T) {
	system, models, configs := emergencyPermissions([]string{"mdl_b", "mdl_a"}, []string{"cns_b", "cns_a"})
	if len(system) != len(allSystemPermissions) || !slices.IsSorted(system) {
		t.Fatalf("应急管理员系统权限 = %v", system)
	}
	for _, code := range system {
		if !ValidSystemPermission(code) {
			t.Fatalf("应急管理员包含未知系统权限 %q", code)
		}
	}
	if len(models) != 2 || models[0].ModelID != "mdl_a" || models[1].ModelID != "mdl_b" {
		t.Fatalf("应急管理员模型权限 = %v", models)
	}
	for _, model := range models {
		if len(model.Permissions) != len(modelPermissionSet) {
			t.Fatalf("模型 %s 权限 = %v", model.ModelID, model.Permissions)
		}
		for _, code := range model.Permissions {
			if !ValidModelPermission(code) {
				t.Fatalf("模型 %s 包含未知权限 %q", model.ModelID, code)
			}
		}
	}
	if len(configs) != 2 || configs[0].ConfigNamespaceID != "cns_a" || configs[1].ConfigNamespaceID != "cns_b" {
		t.Fatalf("应急管理员配置命名空间权限 = %v", configs)
	}
	for _, config := range configs {
		if len(config.Permissions) != len(configNamespacePermissionSet) {
			t.Fatalf("配置命名空间 %s 权限 = %v", config.ConfigNamespaceID, config.Permissions)
		}
		for _, code := range config.Permissions {
			if !ValidConfigNamespacePermission(code) {
				t.Fatalf("配置命名空间 %s 包含未知权限 %q", config.ConfigNamespaceID, code)
			}
		}
	}
}

func TestConfigurationPermissionCatalog(t *testing.T) {
	for _, code := range []string{ConfigurationsView, ConfigurationsCreate, ConfigurationsUpdate, ConfigurationsArchive} {
		if !ValidSystemPermission(code) {
			t.Fatalf("配置系统权限 %q 未被识别", code)
		}
	}
	for _, code := range ConfigNamespacePermissions() {
		if !ValidConfigNamespacePermission(code) {
			t.Fatalf("配置命名空间权限 %q 未被识别", code)
		}
	}
	if ValidConfigNamespacePermission("content.view") || ValidConfigNamespacePermission("config.unknown") {
		t.Fatal("非配置命名空间权限被识别")
	}
}

func TestSMSAuthMethodDoesNotChangeEmergencyPermissionSource(t *testing.T) {
	principal := identity.NewPrincipal("usr_sms", "手机用户", nil, identity.AuthMethodSMS, identity.PermissionSet{System: []string{UsersView}})
	if principal.AuthMethod != identity.AuthMethodSMS {
		t.Fatalf("auth_method = %q", principal.AuthMethod)
	}
	if len(principal.SystemPermissions) != 1 || principal.SystemPermissions[0] != UsersView {
		t.Fatalf("SMS 用户权限被提升: %v", principal.SystemPermissions)
	}
}

func TestHighRiskRoleChangesRequirePrivilegedPrincipal(t *testing.T) {
	if canManageHighRiskRole(identity.Principal{}) {
		t.Fatal("普通主体可以管理高危角色")
	}
	if !canManageHighRiskRole(identity.Principal{HighRiskRole: true}) {
		t.Fatal("高危角色主体不能传播高危角色")
	}
	if !canManageHighRiskRole(identity.Principal{EmergencyAdmin: true}) {
		t.Fatal("应急管理员不能管理高危角色")
	}
}

func TestBuiltinHighRiskRoleIsImmutable(t *testing.T) {
	var appError *apperror.Error
	err := builtinRoleImmutable()
	if !errors.As(err, &appError) || appError.Code != "builtin_role_immutable" || appError.Kind != apperror.KindConflict {
		t.Fatalf("builtinRoleImmutable() = %v", err)
	}
}

func TestAuthorizeRoleReplacementProtectsHighRiskAndEmergencyTargets(t *testing.T) {
	ordinary := identity.Principal{}
	highRisk := identity.Principal{HighRiskRole: true}
	emergency := identity.Principal{EmergencyAdmin: true}
	for _, test := range []struct {
		name      string
		principal identity.Principal
		target    identity.User
		current   identity.LockedRoleSelection
		requested identity.LockedRoleSelection
		wantError bool
	}{
		{name: "普通管理员授予高危角色", principal: ordinary, target: identity.User{}, requested: identity.LockedRoleSelection{HighRisk: true}, wantError: true},
		{name: "普通管理员保留高危角色", principal: ordinary, target: identity.User{UserSummary: identity.UserSummary{HighRiskRole: true}}, current: identity.LockedRoleSelection{HighRisk: true}, requested: identity.LockedRoleSelection{HighRisk: true}, wantError: true},
		{name: "普通管理员移除高危角色", principal: ordinary, target: identity.User{UserSummary: identity.UserSummary{HighRiskRole: true}}, current: identity.LockedRoleSelection{HighRisk: true}, wantError: true},
		{name: "高危主体传播高危角色", principal: highRisk, target: identity.User{}, requested: identity.LockedRoleSelection{HighRisk: true}},
		{name: "高危主体移除自己的高危角色", principal: highRisk, target: identity.User{UserSummary: identity.UserSummary{ID: "usr_high", HighRiskRole: true}}},
		{name: "高危主体管理应急管理员", principal: highRisk, target: identity.User{UserSummary: identity.UserSummary{EmergencyAdmin: true}}, wantError: true},
		{name: "应急管理员管理应急管理员", principal: emergency, target: identity.User{UserSummary: identity.UserSummary{EmergencyAdmin: true}}},
		{name: "普通管理员管理普通角色", principal: ordinary, target: identity.User{UserSummary: identity.UserSummary{ID: "usr_target"}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := authorizeRoleReplacement(test.principal, test.target, test.current, test.requested)
			if (err != nil) != test.wantError {
				t.Fatalf("authorizeRoleReplacement() error = %v, wantError=%v", err, test.wantError)
			}
		})
	}
}

func TestOrdinaryPrincipalCannotManageMorePrivilegedTarget(t *testing.T) {
	principal := identity.Principal{UserID: "usr_actor", SystemPermissions: []string{UsersView}}
	target := identity.User{UserSummary: identity.UserSummary{ID: "usr_target"}}
	current := identity.LockedRoleSelection{Permissions: identity.PermissionSet{System: []string{AuditView}}}
	requested := identity.LockedRoleSelection{Permissions: identity.PermissionSet{System: []string{UsersView}}}
	if err := authorizeRoleReplacement(principal, target, current, requested); err == nil {
		t.Fatal("普通管理员通过降权管理了权限高于自己的目标用户")
	}
}

func TestOrdinaryPrincipalCanOnlyDelegatePermissionSubset(t *testing.T) {
	principal := identity.Principal{
		UserID:                     "usr_actor",
		SystemPermissions:          []string{RolesManage, UsersView},
		ModelPermissions:           []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view", "content.update"}}},
		ConfigNamespacePermissions: []identity.ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{ConfigView, ConfigUpdate}}},
	}
	for _, test := range []struct {
		name      string
		assigned  bool
		current   identity.PermissionSet
		requested identity.PermissionSet
		want      bool
	}{
		{name: "系统和模型权限子集", current: identity.PermissionSet{System: []string{UsersView}}, requested: identity.PermissionSet{System: []string{UsersView}, Models: []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view"}}}}, want: true},
		{name: "自身角色", assigned: true, current: identity.PermissionSet{System: []string{UsersView}}, requested: identity.PermissionSet{System: []string{UsersView}}},
		{name: "当前角色越范围", current: identity.PermissionSet{System: []string{AuditView}}, requested: identity.PermissionSet{System: []string{UsersView}}},
		{name: "越范围系统权限", requested: identity.PermissionSet{System: []string{AuditView}}},
		{name: "越范围模型权限", requested: identity.PermissionSet{Models: []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.publish"}}}}},
		{name: "其他模型权限", requested: identity.PermissionSet{Models: []identity.ModelPermissions{{ModelID: "mdl_2", Permissions: []string{"content.view"}}}}},
		{name: "命名空间权限子集", requested: identity.PermissionSet{ConfigNamespacePermissions: []identity.ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{ConfigView}}}}, want: true},
		{name: "越范围命名空间权限", requested: identity.PermissionSet{ConfigNamespacePermissions: []identity.ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{ConfigPublish}}}}},
		{name: "其他命名空间权限", requested: identity.PermissionSet{ConfigNamespacePermissions: []identity.ConfigNamespacePermissions{{ConfigNamespaceID: "cns_other", Permissions: []string{ConfigView}}}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := canReplaceRoleGrants(principal, test.assigned, test.current, test.requested); got != test.want {
				t.Fatalf("canReplaceRoleGrants() = %v, want %v", got, test.want)
			}
		})
	}
	if !canReplaceRoleGrants(identity.Principal{HighRiskRole: true}, true, identity.PermissionSet{System: []string{AuditView}}, identity.PermissionSet{System: []string{AuditView}}) {
		t.Fatal("高危角色受普通权限子集限制")
	}
}

func TestSQLProviderLoadsOrdinaryRoleConfigNamespacePermissions(t *testing.T) {
	db := openPermissionTestDB(t)
	permissions, err := (SQLProvider{DB: db}).Permissions(context.Background(), "usr_1")
	if err != nil {
		t.Fatalf("Permissions() error = %v", err)
	}
	principal := identity.NewPrincipal("usr_1", "测试用户", nil, identity.AuthMethodSMS, permissions)
	if len(principal.ConfigNamespacePermissions) != 1 || principal.ConfigNamespacePermissions[0].ConfigNamespaceID != "cns_site" || !slices.Equal(principal.ConfigNamespacePermissions[0].Permissions, []string{ConfigUpdate, ConfigView}) {
		t.Fatalf("配置命名空间权限 = %v", principal.ConfigNamespacePermissions)
	}
}

var permissionTestDriverID atomic.Uint64

func openPermissionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	name := "permission_test_" + itoa(int(permissionTestDriverID.Add(1)))
	sql.Register(name, permissionTestDriver{})
	db, err := sql.Open(name, "")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

type permissionTestDriver struct{}

func (permissionTestDriver) Open(string) (driver.Conn, error) { return permissionTestConn{}, nil }

type permissionTestConn struct{}

func (permissionTestConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("不支持 Prepare")
}
func (permissionTestConn) Close() error              { return nil }
func (permissionTestConn) Begin() (driver.Tx, error) { return nil, errors.New("不支持事务") }
func (permissionTestConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "SELECT enabled FROM users"):
		return &permissionTestRows{columns: []string{"enabled"}, values: [][]driver.Value{{true}}}, nil
	case strings.Contains(query, "SELECT emergency_admin FROM local_credentials"):
		return &permissionTestRows{columns: []string{"emergency_admin"}}, nil
	case strings.Contains(query, "FROM user_roles"):
		return &permissionTestRows{columns: []string{"id", "kind"}, values: [][]driver.Value{{"rol_1", "custom"}}}, nil
	case strings.Contains(query, "role_system_permissions"):
		return &permissionTestRows{columns: []string{"permission"}}, nil
	case strings.Contains(query, "role_model_permissions"):
		return &permissionTestRows{columns: []string{"model_id", "permission"}}, nil
	case strings.Contains(query, "role_config_namespace_permissions"):
		return &permissionTestRows{columns: []string{"namespace_id", "permission"}, values: [][]driver.Value{{"cns_site", ConfigView}, {"cns_site", ConfigUpdate}, {"cns_site", "config.unknown"}}}, nil
	default:
		return nil, errors.New("未预期的查询: " + query)
	}
}

type permissionTestRows struct {
	columns []string
	values  [][]driver.Value
	index   int
}

func (r *permissionTestRows) Columns() []string { return r.columns }
func (r *permissionTestRows) Close() error      { return nil }
func (r *permissionTestRows) Next(dest []driver.Value) error {
	if r.index == len(r.values) {
		return io.EOF
	}
	copy(dest, r.values[r.index])
	r.index++
	return nil
}
