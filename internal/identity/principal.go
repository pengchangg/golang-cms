package identity

import (
	"context"
	"sort"
)

type AuthMethod string

const (
	AuthMethodLocal AuthMethod = "local"
	AuthMethodSMS   AuthMethod = "sms"
)

func ValidAuthMethod(method AuthMethod) bool {
	return method == AuthMethodLocal || method == AuthMethodSMS
}

type ModelPermissions struct {
	ModelID     string   `json:"model_id"`
	Permissions []string `json:"permissions"`
}

type ConfigNamespacePermissions struct {
	ConfigNamespaceID string   `json:"config_namespace_id"`
	Permissions       []string `json:"permissions"`
}

type PermissionSet struct {
	System                     []string
	Models                     []ModelPermissions
	ConfigNamespacePermissions []ConfigNamespacePermissions
	EmergencyAdmin             bool
	HighRiskRole               bool
}

type LockedRoleSelection struct {
	Count       int
	HighRisk    bool
	Permissions PermissionSet
}

type Principal struct {
	UserID                     string                       `json:"user_id"`
	DisplayName                string                       `json:"display_name"`
	Email                      *string                      `json:"email"`
	AuthMethod                 AuthMethod                   `json:"auth_method"`
	EmergencyAdmin             bool                         `json:"is_emergency_admin"`
	HighRiskRole               bool                         `json:"has_high_risk_role"`
	SystemPermissions          []string                     `json:"system_permissions"`
	ModelPermissions           []ModelPermissions           `json:"model_permissions"`
	ConfigNamespacePermissions []ConfigNamespacePermissions `json:"config_namespace_permissions"`
}

type PermissionProvider interface {
	Permissions(context.Context, string) (PermissionSet, error)
}

func (p Principal) CanManageSecurityTier() bool {
	return p.EmergencyAdmin || p.HighRiskRole
}

func (p Principal) CanDelegate(permissions PermissionSet) bool {
	if p.CanManageSecurityTier() {
		return true
	}
	system := stringSet(p.SystemPermissions)
	for _, code := range permissions.System {
		if _, ok := system[code]; !ok {
			return false
		}
	}
	models := make(map[string]map[string]struct{}, len(p.ModelPermissions))
	for _, grant := range p.ModelPermissions {
		models[grant.ModelID] = stringSet(grant.Permissions)
	}
	for _, grant := range permissions.Models {
		allowed := models[grant.ModelID]
		for _, code := range grant.Permissions {
			if _, ok := allowed[code]; !ok {
				return false
			}
		}
	}
	configNamespaces := make(map[string]map[string]struct{}, len(p.ConfigNamespacePermissions))
	for _, grant := range p.ConfigNamespacePermissions {
		configNamespaces[grant.ConfigNamespaceID] = stringSet(grant.Permissions)
	}
	for _, grant := range permissions.ConfigNamespacePermissions {
		allowed := configNamespaces[grant.ConfigNamespaceID]
		for _, code := range grant.Permissions {
			if _, ok := allowed[code]; !ok {
				return false
			}
		}
	}
	return true
}

func stringSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

var systemPermissionCodes = map[string]struct{}{
	"users.view": {}, "users.manage": {}, "roles.view": {}, "roles.manage": {},
	"models.view": {}, "models.create": {}, "models.update": {}, "models.archive": {},
	"configurations.view": {}, "configurations.create": {}, "configurations.update": {}, "configurations.archive": {},
	"assets.view": {}, "assets.upload": {}, "assets.update": {}, "assets.archive": {},
	"api_keys.view": {}, "api_keys.create": {}, "api_keys.revoke": {}, "audit.view": {},
}

var modelPermissionCodes = map[string]struct{}{
	"content.view": {}, "content.create": {}, "content.update": {}, "content.archive": {},
	"content.submit": {}, "content.review": {}, "content.publish": {}, "content.unpublish": {},
}

var configNamespacePermissionCodes = map[string]struct{}{
	"config.view": {}, "config.create": {}, "config.update": {}, "config.archive": {},
	"config.submit": {}, "config.review": {}, "config.publish": {}, "config.unpublish": {},
}

// NewPrincipal 规范化外部权限提供者返回的权限并集，不在身份模块内伪造 RBAC。
func NewPrincipal(userID, displayName string, email *string, method AuthMethod, permissions PermissionSet) Principal {
	return Principal{
		UserID:                     userID,
		DisplayName:                displayName,
		Email:                      email,
		AuthMethod:                 method,
		EmergencyAdmin:             permissions.EmergencyAdmin,
		HighRiskRole:               permissions.HighRiskRole,
		SystemPermissions:          validUniqueSorted(permissions.System, systemPermissionCodes),
		ModelPermissions:           normalizeModels(permissions.Models),
		ConfigNamespacePermissions: normalizeConfigNamespaces(permissions.ConfigNamespacePermissions),
	}
}

func normalizeConfigNamespaces(values []ConfigNamespacePermissions) []ConfigNamespacePermissions {
	merged := make(map[string][]string, len(values))
	for _, value := range values {
		if value.ConfigNamespaceID != "" {
			merged[value.ConfigNamespaceID] = append(merged[value.ConfigNamespaceID], value.Permissions...)
		}
	}
	namespaces := make([]string, 0, len(merged))
	for namespace := range merged {
		namespaces = append(namespaces, namespace)
	}
	sort.Strings(namespaces)
	result := make([]ConfigNamespacePermissions, 0, len(namespaces))
	for _, namespace := range namespaces {
		permissions := validUniqueSorted(merged[namespace], configNamespacePermissionCodes)
		if len(permissions) != 0 {
			result = append(result, ConfigNamespacePermissions{ConfigNamespaceID: namespace, Permissions: permissions})
		}
	}
	return result
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
