package identity

import "testing"

func TestNewPrincipalNormalizesPermissions(t *testing.T) {
	principal := NewPrincipal("u", "name", nil, AuthMethodLocal, PermissionSet{System: []string{"models.view", "configurations.view", "audit.view", "transfers.execute", "transfers.download", "unknown"}, Models: []ModelPermissions{{ModelID: "b", Permissions: []string{"unknown"}}, {ModelID: "a", Permissions: []string{"content.update"}}, {ModelID: "a", Permissions: []string{"content.view", "content.update"}}}, ConfigNamespacePermissions: []ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{"config.update"}}, {ConfigNamespaceID: "cns_invalid", Permissions: []string{"unknown"}}, {ConfigNamespaceID: "cns_site", Permissions: []string{"config.view", "config.update"}}}, HighRiskRole: true})
	if len(principal.SystemPermissions) != 3 || principal.SystemPermissions[0] != "audit.view" || principal.SystemPermissions[1] != "configurations.view" || principal.SystemPermissions[2] != "models.view" {
		t.Fatalf("system permissions = %v", principal.SystemPermissions)
	}
	if len(principal.ModelPermissions) != 1 || principal.ModelPermissions[0].ModelID != "a" || len(principal.ModelPermissions[0].Permissions) != 2 {
		t.Fatalf("model permissions = %v", principal.ModelPermissions)
	}
	if len(principal.ConfigNamespacePermissions) != 1 || principal.ConfigNamespacePermissions[0].ConfigNamespaceID != "cns_site" || len(principal.ConfigNamespacePermissions[0].Permissions) != 2 {
		t.Fatalf("config namespace permissions = %v", principal.ConfigNamespacePermissions)
	}
	if !principal.HighRiskRole || principal.EmergencyAdmin {
		t.Fatalf("主体等级错误: %+v", principal)
	}
}

func TestPrincipalCanDelegateOnlyPermissionSubset(t *testing.T) {
	principal := Principal{
		SystemPermissions:          []string{"users.view"},
		ModelPermissions:           []ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view", "content.update"}}},
		ConfigNamespacePermissions: []ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{"config.view", "config.update"}}},
	}
	if !principal.CanDelegate(PermissionSet{System: []string{"users.view"}, Models: []ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view"}}}, ConfigNamespacePermissions: []ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{"config.view"}}}}) {
		t.Fatal("合法权限子集被拒绝")
	}
	if principal.CanDelegate(PermissionSet{System: []string{"audit.view"}}) {
		t.Fatal("越范围系统权限被允许")
	}
	if principal.CanDelegate(PermissionSet{Models: []ModelPermissions{{ModelID: "mdl_2", Permissions: []string{"content.view"}}}}) {
		t.Fatal("越范围模型权限被允许")
	}
	if principal.CanDelegate(PermissionSet{ConfigNamespacePermissions: []ConfigNamespacePermissions{{ConfigNamespaceID: "cns_other", Permissions: []string{"config.view"}}}}) {
		t.Fatal("越范围配置命名空间权限被允许")
	}
	if principal.CanDelegate(PermissionSet{ConfigNamespacePermissions: []ConfigNamespacePermissions{{ConfigNamespaceID: "cns_site", Permissions: []string{"config.publish"}}}}) {
		t.Fatal("越范围配置权限被允许")
	}
	if !(&Principal{HighRiskRole: true}).CanDelegate(PermissionSet{System: []string{"audit.view"}}) {
		t.Fatal("高危主体错误地受权限子集限制")
	}
}

func TestValidAuthMethodOnlyAcceptsLocalAndSMS(t *testing.T) {
	if !ValidAuthMethod(AuthMethodLocal) || !ValidAuthMethod(AuthMethodSMS) {
		t.Fatal("当前认证方式被拒绝")
	}
	if ValidAuthMethod(AuthMethod("oidc")) || ValidAuthMethod(AuthMethod("unknown")) {
		t.Fatal("历史或未知认证方式被接受")
	}
}
