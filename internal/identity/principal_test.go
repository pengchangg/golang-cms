package identity

import "testing"

func TestNewPrincipalNormalizesPermissions(t *testing.T) {
	principal := NewPrincipal("u", "name", nil, AuthMethodLocal, []string{"models.view", "audit.view", "transfers.execute", "transfers.download", "unknown"}, []ModelPermissions{{ModelID: "b", Permissions: []string{"unknown"}}, {ModelID: "a", Permissions: []string{"content.update"}}, {ModelID: "a", Permissions: []string{"content.view", "content.update"}}})
	if len(principal.SystemPermissions) != 2 || principal.SystemPermissions[0] != "audit.view" || principal.SystemPermissions[1] != "models.view" {
		t.Fatalf("system permissions = %v", principal.SystemPermissions)
	}
	if len(principal.ModelPermissions) != 1 || principal.ModelPermissions[0].ModelID != "a" || len(principal.ModelPermissions[0].Permissions) != 2 {
		t.Fatalf("model permissions = %v", principal.ModelPermissions)
	}
}
