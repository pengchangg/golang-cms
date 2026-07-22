package permission

import (
	"context"
	"errors"
	"slices"
	"testing"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type fixedTransactionalPermissions struct{ permissions identity.PermissionSet }

func (p fixedTransactionalPermissions) PermissionsWith(context.Context, database.Querier, string) (identity.PermissionSet, error) {
	return p.permissions, nil
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
	system, models := emergencyPermissions([]string{"mdl_b", "mdl_a"})
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
		UserID:            "usr_actor",
		SystemPermissions: []string{RolesManage, UsersView},
		ModelPermissions:  []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view", "content.update"}}},
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
