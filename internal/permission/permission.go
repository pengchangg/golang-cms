package permission

import (
	"context"
	"database/sql"
	"sort"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
)

const (
	UsersView     = "users.view"
	UsersManage   = "users.manage"
	RolesView     = "roles.view"
	RolesManage   = "roles.manage"
	ModelsView    = "models.view"
	ModelsCreate  = "models.create"
	ModelsUpdate  = "models.update"
	ModelsArchive = "models.archive"
	AuditView     = "audit.view"
)

type Authorizer interface {
	RequireSystemPermission(context.Context, identity.Principal, string) error
}

type ActiveModelProvider interface {
	ActiveModelIDs(context.Context) ([]string, error)
}

var allSystemPermissions = []string{
	UsersView, UsersManage, RolesView, RolesManage, ModelsView, ModelsCreate, ModelsUpdate, ModelsArchive,
	"assets.view", "assets.upload", "assets.update", "assets.archive", "api_keys.view", "api_keys.create",
	"api_keys.revoke", AuditView, "transfers.execute", "transfers.download",
}

var systemPermissionSet = makeSet(allSystemPermissions)
var modelPermissionSet = makeSet([]string{"content.view", "content.create", "content.update", "content.archive", "content.submit", "content.review", "content.publish", "content.unpublish"})

type SQLProvider struct {
	DB     *sql.DB
	Models ActiveModelProvider
}

func (p SQLProvider) Permissions(ctx context.Context, userID string) ([]string, []identity.ModelPermissions, error) {
	var enabled, emergency bool
	err := p.DB.QueryRowContext(ctx, `SELECT u.enabled, EXISTS(SELECT 1 FROM local_credentials lc
		WHERE lc.user_id=u.id AND lc.emergency_admin=TRUE) FROM users u WHERE u.id=?`, userID).Scan(&enabled, &emergency)
	if err == sql.ErrNoRows {
		return []string{}, []identity.ModelPermissions{}, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if !enabled {
		return []string{}, []identity.ModelPermissions{}, nil
	}
	if emergency {
		if p.Models == nil {
			return nil, nil, &apperror.Error{Kind: apperror.KindInternal, Code: "internal_error", Message: "有效模型提供者未装配"}
		}
		modelIDs, err := p.Models.ActiveModelIDs(ctx)
		if err != nil {
			return nil, nil, err
		}
		system, models := emergencyPermissions(modelIDs)
		return system, models, nil
	}
	systemRows, err := p.DB.QueryContext(ctx, `SELECT DISTINCT rsp.permission FROM user_roles ur
		JOIN role_system_permissions rsp ON rsp.role_id=ur.role_id WHERE ur.user_id=? ORDER BY rsp.permission`, userID)
	if err != nil {
		return nil, nil, err
	}
	system := []string{}
	for systemRows.Next() {
		var code string
		if err := systemRows.Scan(&code); err != nil {
			systemRows.Close()
			return nil, nil, err
		}
		if ValidSystemPermission(code) {
			system = append(system, code)
		}
	}
	if err := systemRows.Close(); err != nil {
		return nil, nil, err
	}
	modelRows, err := p.DB.QueryContext(ctx, `SELECT DISTINCT rmp.model_id, rmp.permission FROM user_roles ur
		JOIN role_model_permissions rmp ON rmp.role_id=ur.role_id WHERE ur.user_id=? ORDER BY rmp.model_id, rmp.permission`, userID)
	if err != nil {
		return nil, nil, err
	}
	defer modelRows.Close()
	models := []identity.ModelPermissions{}
	for modelRows.Next() {
		var modelID, code string
		if err := modelRows.Scan(&modelID, &code); err != nil {
			return nil, nil, err
		}
		if !ValidModelPermission(code) {
			continue
		}
		if len(models) == 0 || models[len(models)-1].ModelID != modelID {
			models = append(models, identity.ModelPermissions{ModelID: modelID, Permissions: []string{}})
		}
		models[len(models)-1].Permissions = append(models[len(models)-1].Permissions, code)
	}
	return system, models, modelRows.Err()
}

func emergencyPermissions(modelIDs []string) ([]string, []identity.ModelPermissions) {
	system := append([]string(nil), allSystemPermissions...)
	sort.Strings(system)
	modelIDs = append([]string(nil), modelIDs...)
	sort.Strings(modelIDs)
	codes := []string{"content.archive", "content.create", "content.publish", "content.review", "content.submit", "content.unpublish", "content.update", "content.view"}
	models := make([]identity.ModelPermissions, 0, len(modelIDs))
	for _, modelID := range modelIDs {
		models = append(models, identity.ModelPermissions{ModelID: modelID, Permissions: append([]string(nil), codes...)})
	}
	return system, models
}

type PrincipalAuthorizer struct{}

func (PrincipalAuthorizer) RequireSystemPermission(_ context.Context, principal identity.Principal, required string) error {
	for _, granted := range principal.SystemPermissions {
		if granted == required {
			return nil
		}
	}
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}

func ValidSystemPermission(code string) bool { _, ok := systemPermissionSet[code]; return ok }
func ValidModelPermission(code string) bool  { _, ok := modelPermissionSet[code]; return ok }
func SystemPermissions() []string {
	result := append([]string(nil), allSystemPermissions...)
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
