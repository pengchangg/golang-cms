package permission

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type Repository struct{}

func (r Repository) ListRoles(ctx context.Context, q database.Querier) ([]Role, error) {
	rows, err := q.QueryContext(ctx, "SELECT id, `key`, kind, display_name, description, created_at, updated_at FROM roles ORDER BY `key`, id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	roles := []Role{}
	for rows.Next() {
		var role Role
		if err := rows.Scan(&role.ID, &role.Key, &role.Kind, &role.DisplayName, &role.Description, &role.CreatedAt, &role.UpdatedAt); err != nil {
			return nil, err
		}
		roles = append(roles, role)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range roles {
		if err := r.loadGrants(ctx, q, &roles[i]); err != nil {
			return nil, err
		}
	}
	return roles, nil
}

func (r Repository) GetRole(ctx context.Context, q database.Querier, id string, lock bool) (Role, error) {
	query := "SELECT id, `key`, kind, display_name, description, created_at, updated_at FROM roles WHERE id=?"
	if lock {
		query += " FOR UPDATE"
	}
	var role Role
	err := q.QueryRowContext(ctx, query, id).Scan(&role.ID, &role.Key, &role.Kind, &role.DisplayName, &role.Description, &role.CreatedAt, &role.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Role{}, roleNotFound()
	}
	if err != nil {
		return Role{}, err
	}
	if err := r.loadGrants(ctx, q, &role, lock); err != nil {
		return Role{}, err
	}
	return role, nil
}

func (Repository) CreateRole(ctx context.Context, q database.Querier, role Role) error {
	_, err := q.ExecContext(ctx, "INSERT INTO roles (id, `key`, kind, display_name, description, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)", role.ID, role.Key, role.Kind, role.DisplayName, role.Description, role.CreatedAt, role.UpdatedAt)
	return err
}
func (Repository) UpdateRole(ctx context.Context, q database.Querier, role Role) error {
	_, err := q.ExecContext(ctx, "UPDATE roles SET display_name=?, description=?, updated_at=? WHERE id=?", role.DisplayName, role.Description, role.UpdatedAt, role.ID)
	return err
}
func (Repository) DeleteRole(ctx context.Context, q database.Querier, id string) error {
	_, err := q.ExecContext(ctx, "DELETE FROM roles WHERE id=?", id)
	return err
}

func (r Repository) LockRoleTransition(ctx context.Context, q database.Querier, currentIDs, requestedIDs []string) (identity.LockedRoleSelection, identity.LockedRoleSelection, error) {
	currentSet := make(map[string]struct{}, len(currentIDs))
	requestedSet := make(map[string]struct{}, len(requestedIDs))
	allSet := make(map[string]struct{}, len(currentIDs)+len(requestedIDs))
	for _, id := range currentIDs {
		currentSet[id] = struct{}{}
		allSet[id] = struct{}{}
	}
	for _, id := range requestedIDs {
		requestedSet[id] = struct{}{}
		allSet[id] = struct{}{}
	}
	allIDs := make([]string, 0, len(allSet))
	for id := range allSet {
		allIDs = append(allIDs, id)
	}
	sort.Strings(allIDs)
	current := identity.LockedRoleSelection{Permissions: identity.PermissionSet{System: []string{}, Models: []identity.ModelPermissions{}}}
	requested := identity.LockedRoleSelection{Permissions: identity.PermissionSet{System: []string{}, Models: []identity.ModelPermissions{}}}
	for _, id := range allIDs {
		role, err := r.GetRole(ctx, q, id, true)
		var appError *apperror.Error
		if errors.As(err, &appError) && appError.Kind == apperror.KindNotFound {
			continue
		}
		if err != nil {
			return identity.LockedRoleSelection{}, identity.LockedRoleSelection{}, err
		}
		if _, ok := currentSet[id]; ok {
			appendRoleSelection(&current, role)
		}
		if _, ok := requestedSet[id]; ok {
			appendRoleSelection(&requested, role)
		}
	}
	return current, requested, nil
}

func appendRoleSelection(selection *identity.LockedRoleSelection, role Role) {
	selection.Count++
	selection.HighRisk = selection.HighRisk || role.Kind == RoleKindHighRisk
	selection.Permissions.System = append(selection.Permissions.System, role.SystemPermissions...)
	selection.Permissions.Models = append(selection.Permissions.Models, role.ModelPermissions...)
}

func (Repository) IsRoleAssignedToUser(ctx context.Context, q database.Querier, roleID, userID string) (bool, error) {
	var assigned bool
	err := q.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM user_roles WHERE role_id=? AND user_id=?)", roleID, userID).Scan(&assigned)
	return assigned, err
}

func (Repository) ReplaceUserRoles(ctx context.Context, q database.Querier, userID string, roleIDs []string, now time.Time) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM user_roles WHERE user_id=?", userID); err != nil {
		return err
	}
	for _, roleID := range roleIDs {
		if _, err := q.ExecContext(ctx, "INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)", userID, roleID, now); err != nil {
			return err
		}
	}
	return nil
}

func (Repository) ReplaceSystemPermissions(ctx context.Context, q database.Querier, roleID string, values []string, now time.Time) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM role_system_permissions WHERE role_id=?", roleID); err != nil {
		return err
	}
	for _, value := range values {
		if _, err := q.ExecContext(ctx, "INSERT INTO role_system_permissions (role_id, permission, created_at) VALUES (?, ?, ?)", roleID, value, now); err != nil {
			return err
		}
	}
	return nil
}

func (Repository) ReplaceModelPermissions(ctx context.Context, q database.Querier, roleID string, values []identity.ModelPermissions, now time.Time) error {
	if _, err := q.ExecContext(ctx, "DELETE FROM role_model_permissions WHERE role_id=?", roleID); err != nil {
		return err
	}
	for _, value := range values {
		for _, code := range value.Permissions {
			if _, err := q.ExecContext(ctx, "INSERT INTO role_model_permissions (role_id, model_id, permission, created_at) VALUES (?, ?, ?, ?)", roleID, value.ModelID, code, now); err != nil {
				return err
			}
		}
	}
	return nil
}

func (Repository) TouchRole(ctx context.Context, q database.Querier, roleID string, now time.Time) error {
	_, err := q.ExecContext(ctx, "UPDATE roles SET updated_at=? WHERE id=?", now, roleID)
	return err
}

func (Repository) loadGrants(ctx context.Context, q database.Querier, role *Role, locked ...bool) error {
	lockSuffix := ""
	if len(locked) != 0 && locked[0] {
		lockSuffix = " FOR SHARE"
	}
	role.SystemPermissions = []string{}
	rows, err := q.QueryContext(ctx, "SELECT permission FROM role_system_permissions WHERE role_id=? ORDER BY permission"+lockSuffix, role.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var code string
		if err := rows.Scan(&code); err != nil {
			rows.Close()
			return err
		}
		role.SystemPermissions = append(role.SystemPermissions, code)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	role.ModelPermissions = []identity.ModelPermissions{}
	rows, err = q.QueryContext(ctx, "SELECT model_id, permission FROM role_model_permissions WHERE role_id=? ORDER BY model_id, permission"+lockSuffix, role.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var modelID, code string
		if err := rows.Scan(&modelID, &code); err != nil {
			return err
		}
		if len(role.ModelPermissions) == 0 || role.ModelPermissions[len(role.ModelPermissions)-1].ModelID != modelID {
			role.ModelPermissions = append(role.ModelPermissions, identity.ModelPermissions{ModelID: modelID, Permissions: []string{}})
		}
		role.ModelPermissions[len(role.ModelPermissions)-1].Permissions = append(role.ModelPermissions[len(role.ModelPermissions)-1].Permissions, code)
	}
	return rows.Err()
}

func roleNotFound() error {
	return &apperror.Error{Kind: apperror.KindNotFound, Code: "not_found", Message: "角色不存在"}
}
func sorted(values []string) []string {
	result := append([]string(nil), values...)
	sort.Strings(result)
	return result
}
