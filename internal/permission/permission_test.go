package permission

import (
	"context"
	"slices"
	"testing"

	"cms/internal/identity"
)

func TestPrincipalAuthorizerDefaultsToDeny(t *testing.T) {
	authorizer := PrincipalAuthorizer{}
	if err := authorizer.RequireSystemPermission(context.Background(), identity.Principal{}, ModelsView); err == nil {
		t.Fatal("RequireSystemPermission() expected an error")
	}
	if err := authorizer.RequireSystemPermission(context.Background(), identity.Principal{SystemPermissions: []string{ModelsView}}, ModelsView); err != nil {
		t.Fatalf("RequireSystemPermission() error = %v", err)
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
