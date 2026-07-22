package identity

import "testing"

func TestNewPrincipalNormalizesPermissions(t *testing.T) {
	principal := NewPrincipal("u", "name", nil, AuthMethodLocal, PermissionSet{System: []string{"models.view", "audit.view", "transfers.execute", "transfers.download", "unknown"}, Models: []ModelPermissions{{ModelID: "b", Permissions: []string{"unknown"}}, {ModelID: "a", Permissions: []string{"content.update"}}, {ModelID: "a", Permissions: []string{"content.view", "content.update"}}}, HighRiskRole: true})
	if len(principal.SystemPermissions) != 2 || principal.SystemPermissions[0] != "audit.view" || principal.SystemPermissions[1] != "models.view" {
		t.Fatalf("system permissions = %v", principal.SystemPermissions)
	}
	if len(principal.ModelPermissions) != 1 || principal.ModelPermissions[0].ModelID != "a" || len(principal.ModelPermissions[0].Permissions) != 2 {
		t.Fatalf("model permissions = %v", principal.ModelPermissions)
	}
	if !principal.HighRiskRole || principal.EmergencyAdmin {
		t.Fatalf("主体等级错误: %+v", principal)
	}
}

func TestPrincipalCanDelegateOnlyPermissionSubset(t *testing.T) {
	principal := Principal{
		SystemPermissions: []string{"users.view"},
		ModelPermissions:  []ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view", "content.update"}}},
	}
	if !principal.CanDelegate(PermissionSet{System: []string{"users.view"}, Models: []ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.view"}}}}) {
		t.Fatal("合法权限子集被拒绝")
	}
	if principal.CanDelegate(PermissionSet{System: []string{"audit.view"}}) {
		t.Fatal("越范围系统权限被允许")
	}
	if principal.CanDelegate(PermissionSet{Models: []ModelPermissions{{ModelID: "mdl_2", Permissions: []string{"content.view"}}}}) {
		t.Fatal("越范围模型权限被允许")
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
