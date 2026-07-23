package permission

import (
	"context"
	"database/sql"
	"errors"
	"sort"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

const (
	UsersView             = "users.view"
	UsersManage           = "users.manage"
	RolesView             = "roles.view"
	RolesManage           = "roles.manage"
	ModelsView            = "models.view"
	ModelsCreate          = "models.create"
	ModelsUpdate          = "models.update"
	ModelsArchive         = "models.archive"
	ConfigurationsView    = "configurations.view"
	ConfigurationsCreate  = "configurations.create"
	ConfigurationsUpdate  = "configurations.update"
	ConfigurationsArchive = "configurations.archive"
	ConfigView            = "config.view"
	ConfigCreate          = "config.create"
	ConfigUpdate          = "config.update"
	ConfigArchive         = "config.archive"
	ConfigSubmit          = "config.submit"
	ConfigReview          = "config.review"
	ConfigPublish         = "config.publish"
	ConfigUnpublish       = "config.unpublish"
	AuditView             = "audit.view"
)

type Authorizer interface {
	RequireSystemPermission(context.Context, identity.Principal, string) error
}

type ActiveModelProvider interface {
	ActiveModelIDs(context.Context) ([]string, error)
	ActiveModelIDsWith(context.Context, database.Querier) ([]string, error)
}

type ActiveConfigNamespaceProvider interface {
	ActiveConfigNamespaceIDs(context.Context) ([]string, error)
	ActiveConfigNamespaceIDsWith(context.Context, database.Querier) ([]string, error)
}

var allSystemPermissions = []string{
	UsersView, UsersManage, RolesView, RolesManage, ModelsView, ModelsCreate, ModelsUpdate, ModelsArchive,
	ConfigurationsView, ConfigurationsCreate, ConfigurationsUpdate, ConfigurationsArchive,
	"assets.view", "assets.upload", "assets.update", "assets.archive", "api_keys.view", "api_keys.create",
	"api_keys.revoke", AuditView,
}

var systemPermissionSet = makeSet(allSystemPermissions)
var modelPermissionSet = makeSet([]string{"content.view", "content.create", "content.update", "content.archive", "content.submit", "content.review", "content.publish", "content.unpublish"})
var allConfigNamespacePermissions = []string{ConfigView, ConfigCreate, ConfigUpdate, ConfigArchive, ConfigSubmit, ConfigReview, ConfigPublish, ConfigUnpublish}
var configNamespacePermissionSet = makeSet(allConfigNamespacePermissions)

type SQLProvider struct {
	DB               *sql.DB
	Models           ActiveModelProvider
	ConfigNamespaces ActiveConfigNamespaceProvider
}

func (p SQLProvider) Permissions(ctx context.Context, userID string) (identity.PermissionSet, error) {
	return p.permissions(ctx, p.DB, userID, false)
}

func (p SQLProvider) PermissionsWith(ctx context.Context, q database.Querier, userID string) (identity.PermissionSet, error) {
	return p.permissions(ctx, q, userID, true)
}

func (p SQLProvider) permissions(ctx context.Context, q database.Querier, userID string, lock bool) (identity.PermissionSet, error) {
	var enabled bool
	identityQuery := "SELECT enabled FROM users WHERE id=?"
	if lock {
		identityQuery += " FOR UPDATE"
	}
	err := q.QueryRowContext(ctx, identityQuery, userID).Scan(&enabled)
	if err == sql.ErrNoRows {
		return identity.PermissionSet{}, nil
	}
	if err != nil {
		return identity.PermissionSet{}, err
	}
	if !enabled {
		return identity.PermissionSet{}, nil
	}
	lockSuffix := ""
	if lock {
		lockSuffix = " FOR SHARE"
	}
	emergency := false
	emergencyQuery := "SELECT emergency_admin FROM local_credentials WHERE user_id=?"
	if lock {
		emergencyQuery += " FOR SHARE"
	}
	if err := q.QueryRowContext(ctx, emergencyQuery, userID).Scan(&emergency); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return identity.PermissionSet{}, err
	}
	roleRows, err := q.QueryContext(ctx, `SELECT r.id,r.kind FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=? ORDER BY r.id`+lockSuffix, userID)
	if err != nil {
		return identity.PermissionSet{}, err
	}
	roleIDs := []string{}
	highRisk := false
	for roleRows.Next() {
		var roleID, kind string
		if err := roleRows.Scan(&roleID, &kind); err != nil {
			roleRows.Close()
			return identity.PermissionSet{}, err
		}
		roleIDs = append(roleIDs, roleID)
		highRisk = highRisk || kind == string(RoleKindHighRisk)
	}
	if err := roleRows.Err(); err != nil {
		roleRows.Close()
		return identity.PermissionSet{}, err
	}
	if err := roleRows.Close(); err != nil {
		return identity.PermissionSet{}, err
	}
	if emergency || highRisk {
		if p.Models == nil {
			return identity.PermissionSet{}, &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "有效模型提供者未装配"}
		}
		var modelIDs []string
		var err error
		if lock {
			modelIDs, err = p.Models.ActiveModelIDsWith(ctx, q)
		} else {
			modelIDs, err = p.Models.ActiveModelIDs(ctx)
		}
		if err != nil {
			return identity.PermissionSet{}, err
		}
		var configNamespaceIDs []string
		if p.ConfigNamespaces != nil {
			if lock {
				configNamespaceIDs, err = p.ConfigNamespaces.ActiveConfigNamespaceIDsWith(ctx, q)
			} else {
				configNamespaceIDs, err = p.ConfigNamespaces.ActiveConfigNamespaceIDs(ctx)
			}
			if err != nil {
				return identity.PermissionSet{}, err
			}
		}
		system, models, configNamespaceGrants := emergencyPermissions(modelIDs, configNamespaceIDs)
		return identity.PermissionSet{System: system, Models: models, ConfigNamespacePermissions: configNamespaceGrants, EmergencyAdmin: emergency, HighRiskRole: highRisk}, nil
	}
	system := []string{}
	models := []identity.ModelPermissions{}
	configNamespaces := []identity.ConfigNamespacePermissions{}
	for _, roleID := range roleIDs {
		systemRows, err := q.QueryContext(ctx, `SELECT permission FROM role_system_permissions WHERE role_id=? ORDER BY permission`+lockSuffix, roleID)
		if err != nil {
			return identity.PermissionSet{}, err
		}
		for systemRows.Next() {
			var code string
			if err := systemRows.Scan(&code); err != nil {
				systemRows.Close()
				return identity.PermissionSet{}, err
			}
			if ValidSystemPermission(code) {
				system = append(system, code)
			}
		}
		if err := systemRows.Err(); err != nil {
			systemRows.Close()
			return identity.PermissionSet{}, err
		}
		if err := systemRows.Close(); err != nil {
			return identity.PermissionSet{}, err
		}
		modelRows, err := q.QueryContext(ctx, `SELECT model_id, permission FROM role_model_permissions WHERE role_id=? ORDER BY model_id, permission`+lockSuffix, roleID)
		if err != nil {
			return identity.PermissionSet{}, err
		}
		for modelRows.Next() {
			var modelID, code string
			if err := modelRows.Scan(&modelID, &code); err != nil {
				modelRows.Close()
				return identity.PermissionSet{}, err
			}
			if ValidModelPermission(code) {
				models = append(models, identity.ModelPermissions{ModelID: modelID, Permissions: []string{code}})
			}
		}
		if err := modelRows.Err(); err != nil {
			modelRows.Close()
			return identity.PermissionSet{}, err
		}
		if err := modelRows.Close(); err != nil {
			return identity.PermissionSet{}, err
		}
		configRows, err := q.QueryContext(ctx, `SELECT namespace_id, permission FROM role_config_namespace_permissions WHERE role_id=? ORDER BY namespace_id, permission`+lockSuffix, roleID)
		if err != nil {
			return identity.PermissionSet{}, err
		}
		for configRows.Next() {
			var namespace, code string
			if err := configRows.Scan(&namespace, &code); err != nil {
				configRows.Close()
				return identity.PermissionSet{}, err
			}
			if ValidConfigNamespacePermission(code) {
				configNamespaces = append(configNamespaces, identity.ConfigNamespacePermissions{ConfigNamespaceID: namespace, Permissions: []string{code}})
			}
		}
		if err := configRows.Err(); err != nil {
			configRows.Close()
			return identity.PermissionSet{}, err
		}
		if err := configRows.Close(); err != nil {
			return identity.PermissionSet{}, err
		}
	}
	return identity.PermissionSet{System: system, Models: models, ConfigNamespacePermissions: configNamespaces}, nil
}

func emergencyPermissions(modelIDs, configNamespaceIDs []string) ([]string, []identity.ModelPermissions, []identity.ConfigNamespacePermissions) {
	system := append([]string(nil), allSystemPermissions...)
	sort.Strings(system)
	modelIDs = append([]string(nil), modelIDs...)
	sort.Strings(modelIDs)
	codes := []string{"content.archive", "content.create", "content.publish", "content.review", "content.submit", "content.unpublish", "content.update", "content.view"}
	models := make([]identity.ModelPermissions, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		models = append(models, identity.ModelPermissions{ModelID: modelID, Permissions: append([]string(nil), codes...)})
	}
	configNamespaceIDs = append([]string(nil), configNamespaceIDs...)
	sort.Strings(configNamespaceIDs)
	configCodes := append([]string(nil), allConfigNamespacePermissions...)
	sort.Strings(configCodes)
	configs := make([]identity.ConfigNamespacePermissions, 0, len(configNamespaceIDs))
	for _, namespaceID := range configNamespaceIDs {
		configs = append(configs, identity.ConfigNamespacePermissions{ConfigNamespaceID: namespaceID, Permissions: append([]string(nil), configCodes...)})
	}
	return system, models, configs
}

type TransactionalPermissionProvider interface {
	PermissionsWith(context.Context, database.Querier, string) (identity.PermissionSet, error)
}

type PrincipalAuthorizer struct {
	Provider TransactionalPermissionProvider
}

func (PrincipalAuthorizer) RequireSystemPermission(_ context.Context, principal identity.Principal, required string) error {
	for _, granted := range principal.SystemPermissions {
		if granted == required {
			return nil
		}
	}
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}

func (a PrincipalAuthorizer) CurrentPrincipal(ctx context.Context, q database.Querier, principal identity.Principal) (identity.Principal, error) {
	if a.Provider == nil {
		return identity.Principal{}, &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "事务权限提供者未装配"}
	}
	permissions, err := a.Provider.PermissionsWith(ctx, q, principal.UserID)
	if err != nil {
		return identity.Principal{}, err
	}
	return identity.NewPrincipal(principal.UserID, principal.DisplayName, principal.Email, principal.AuthMethod, permissions), nil
}

func ValidSystemPermission(code string) bool { _, ok := systemPermissionSet[code]; return ok }
func ValidModelPermission(code string) bool  { _, ok := modelPermissionSet[code]; return ok }
func ValidConfigNamespacePermission(code string) bool {
	_, ok := configNamespacePermissionSet[code]
	return ok
}
func SystemPermissions() []string {
	result := append([]string(nil), allSystemPermissions...)
	sort.Strings(result)
	return result
}
func ConfigNamespacePermissions() []string {
	result := append([]string(nil), allConfigNamespacePermissions...)
	sort.Strings(result)
	return result
}
func makeSet(values []string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
