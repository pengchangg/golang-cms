package permission

import (
	"time"

	"cms/internal/identity"
)

type RoleKind string

const (
	RoleKindCustom   RoleKind = "custom"
	RoleKindHighRisk RoleKind = "high_risk"
)

type Role struct {
	ID                         string                                `json:"id"`
	Key                        string                                `json:"key"`
	Kind                       RoleKind                              `json:"kind"`
	DisplayName                string                                `json:"display_name"`
	Description                string                                `json:"description"`
	SystemPermissions          []string                              `json:"system_permissions"`
	ModelPermissions           []identity.ModelPermissions           `json:"model_permissions"`
	ConfigNamespacePermissions []identity.ConfigNamespacePermissions `json:"config_namespace_permissions"`
	CreatedAt                  time.Time                             `json:"created_at"`
	UpdatedAt                  time.Time                             `json:"updated_at"`
}

type CreateRoleRequest struct {
	Key                        string                                `json:"key"`
	DisplayName                string                                `json:"display_name"`
	Description                string                                `json:"description"`
	ConfigNamespacePermissions []identity.ConfigNamespacePermissions `json:"config_namespace_permissions"`
}
type UpdateRoleRequest struct {
	DisplayName                *string                                `json:"display_name"`
	Description                *string                                `json:"description"`
	ConfigNamespacePermissions *[]identity.ConfigNamespacePermissions `json:"config_namespace_permissions"`
}
type ReplaceUserRolesRequest struct {
	RoleIDs []string `json:"role_ids"`
}
type ReplaceSystemPermissionsRequest struct {
	Permissions []string `json:"permissions"`
}
type ReplaceModelPermissionsRequest struct {
	Grants []identity.ModelPermissions `json:"grants"`
}
type RequestMeta struct{ RequestID, IP, UserAgent string }
