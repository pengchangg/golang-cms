package identity

import (
	"context"
	"sort"
)

type AuthMethod string

const (
	AuthMethodOIDC  AuthMethod = "oidc"
	AuthMethodLocal AuthMethod = "local"
)

type ModelPermissions struct {
	ModelID     string   `json:"model_id"`
	Permissions []string `json:"permissions"`
}

type Principal struct {
	UserID            string             `json:"user_id"`
	DisplayName       string             `json:"display_name"`
	Email             *string            `json:"email"`
	AuthMethod        AuthMethod         `json:"auth_method"`
	SystemPermissions []string           `json:"system_permissions"`
	ModelPermissions  []ModelPermissions `json:"model_permissions"`
}

type PermissionProvider interface {
	Permissions(context.Context, string) ([]string, []ModelPermissions, error)
}

var systemPermissionCodes = map[string]struct{}{
	"users.view": {}, "users.manage": {}, "roles.view": {}, "roles.manage": {},
	"models.view": {}, "models.create": {}, "models.update": {}, "models.archive": {},
	"assets.view": {}, "assets.upload": {}, "assets.update": {}, "assets.archive": {},
	"api_keys.view": {}, "api_keys.create": {}, "api_keys.revoke": {}, "audit.view": {},
}

var modelPermissionCodes = map[string]struct{}{
	"content.view": {}, "content.create": {}, "content.update": {}, "content.archive": {},
	"content.submit": {}, "content.review": {}, "content.publish": {}, "content.unpublish": {},
}

// NewPrincipal 规范化外部权限提供者返回的权限并集，不在身份模块内伪造 RBAC。
func NewPrincipal(userID, displayName string, email *string, method AuthMethod, system []string, models []ModelPermissions) Principal {
	return Principal{
		UserID:            userID,
		DisplayName:       displayName,
		Email:             email,
		AuthMethod:        method,
		SystemPermissions: validUniqueSorted(system, systemPermissionCodes),
		ModelPermissions:  normalizeModels(models),
	}
}

func validUniqueSorted(values []string, allowed map[string]struct{}) []string {
	seen := make(map[string]struct{}, len(values))
	result := make([]string, 0, len(values))
	for _, value := range values {
		if _, valid := allowed[value]; !valid {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func normalizeModels(values []ModelPermissions) []ModelPermissions {
	merged := make(map[string][]string, len(values))
	for _, value := range values {
		if value.ModelID != "" {
			merged[value.ModelID] = append(merged[value.ModelID], value.Permissions...)
		}
	}
	ids := make([]string, 0, len(merged))
	for id := range merged {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	result := make([]ModelPermissions, 0, len(ids))
	for _, id := range ids {
		permissions := validUniqueSorted(merged[id], modelPermissionCodes)
		if len(permissions) != 0 {
			result = append(result, ModelPermissions{ModelID: id, Permissions: permissions})
		}
	}
	return result
}
