package identity

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type Repository struct{}

type userCursor struct {
	UpdatedAt time.Time   `json:"updated_at"`
	ID        string      `json:"id"`
	Status    *UserStatus `json:"status,omitempty"`
	Method    *AuthMethod `json:"auth_method,omitempty"`
	Query     string      `json:"query,omitempty"`
}

func (Repository) List(ctx context.Context, q database.Querier, filter UserFilter) (UserList, error) {
	args := []any{}
	where := []string{"1=1"}
	if filter.Status != nil {
		where = append(where, "u.enabled = ?")
		args = append(args, *filter.Status == UserEnabled)
	}
	if filter.AuthMethod != nil {
		if *filter.AuthMethod == AuthMethodLocal {
			where = append(where, "EXISTS (SELECT 1 FROM local_credentials lc2 WHERE lc2.user_id = u.id)")
		} else {
			where = append(where, "EXISTS (SELECT 1 FROM oidc_identities oi2 WHERE oi2.user_id = u.id)")
		}
	}
	if filter.Query != "" {
		where = append(where, "(LOWER(u.display_name) LIKE ? ESCAPE '=' OR LOWER(COALESCE(u.email, '')) LIKE ? ESCAPE '=')")
		value := strings.ToLower(filter.Query)
		value = strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(value)
		value = "%" + value + "%"
		args = append(args, value, value)
	}
	if filter.Cursor != "" {
		cursor, err := decodeUserCursor(filter.Cursor)
		if err != nil || !sameUserFilter(cursor, filter) {
			return UserList{}, invalidCursor()
		}
		where = append(where, "(u.updated_at < ? OR (u.updated_at = ? AND u.id < ?))")
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.ID)
	}
	args = append(args, filter.Limit+1)
	rows, err := q.QueryContext(ctx, `SELECT u.id, u.display_name, u.email, u.enabled, u.created_at, u.updated_at,
		EXISTS(SELECT 1 FROM local_credentials lc WHERE lc.user_id=u.id),
		EXISTS(SELECT 1 FROM local_credentials lc WHERE lc.user_id=u.id AND lc.emergency_admin=TRUE),
		EXISTS(SELECT 1 FROM oidc_identities oi WHERE oi.user_id=u.id)
		FROM users u WHERE `+strings.Join(where, " AND ")+` ORDER BY u.updated_at DESC, u.id DESC LIMIT ?`, args...)
	if err != nil {
		return UserList{}, err
	}
	defer rows.Close()
	items := make([]UserSummary, 0, filter.Limit+1)
	for rows.Next() {
		var user UserSummary
		var enabled, local, oidc bool
		if err := rows.Scan(&user.ID, &user.DisplayName, &user.Email, &enabled, &user.CreatedAt, &user.UpdatedAt, &local, &user.EmergencyAdmin, &oidc); err != nil {
			return UserList{}, err
		}
		user.Status = UserDisabled
		if enabled {
			user.Status = UserEnabled
		}
		user.AuthMethods = []AuthMethod{}
		if local {
			user.AuthMethods = append(user.AuthMethods, AuthMethodLocal)
		}
		if oidc {
			user.AuthMethods = append(user.AuthMethods, AuthMethodOIDC)
		}
		items = append(items, user)
	}
	if err := rows.Err(); err != nil {
		return UserList{}, err
	}
	var next *string
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		last := items[len(items)-1]
		encoded, err := encodeUserCursor(userCursor{UpdatedAt: last.UpdatedAt, ID: last.ID, Status: filter.Status, Method: filter.AuthMethod, Query: filter.Query})
		if err != nil {
			return UserList{}, err
		}
		next = &encoded
	}
	return UserList{Items: items, NextCursor: next}, nil
}

func (r Repository) Get(ctx context.Context, q database.Querier, id string, lock bool) (User, error) {
	query := `SELECT u.id, u.display_name, u.email, u.enabled, u.created_at, u.updated_at,
		EXISTS(SELECT 1 FROM local_credentials lc WHERE lc.user_id=u.id),
		EXISTS(SELECT 1 FROM local_credentials lc WHERE lc.user_id=u.id AND lc.emergency_admin=TRUE),
		EXISTS(SELECT 1 FROM oidc_identities oi WHERE oi.user_id=u.id) FROM users u WHERE u.id=?`
	if lock {
		query += " FOR UPDATE"
	}
	var user User
	var enabled, local, oidc bool
	err := q.QueryRowContext(ctx, query, id).Scan(&user.ID, &user.DisplayName, &user.Email, &enabled, &user.CreatedAt, &user.UpdatedAt, &local, &user.EmergencyAdmin, &oidc)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, notFound("用户不存在")
	}
	if err != nil {
		return User{}, err
	}
	user.Status = UserDisabled
	if enabled {
		user.Status = UserEnabled
	}
	user.AuthMethods = []AuthMethod{}
	if local {
		user.AuthMethods = append(user.AuthMethods, AuthMethodLocal)
	}
	if oidc {
		user.AuthMethods = append(user.AuthMethods, AuthMethodOIDC)
	}
	rows, err := q.QueryContext(ctx, "SELECT role_id FROM user_roles WHERE user_id=? ORDER BY role_id", id)
	if err != nil {
		return User{}, err
	}
	defer rows.Close()
	user.RoleIDs = []string{}
	for rows.Next() {
		var roleID string
		if err := rows.Scan(&roleID); err != nil {
			return User{}, err
		}
		user.RoleIDs = append(user.RoleIDs, roleID)
	}
	return user, rows.Err()
}

func (Repository) SetStatus(ctx context.Context, q database.Querier, id string, status UserStatus, now time.Time) error {
	_, err := q.ExecContext(ctx, "UPDATE users SET enabled=?, updated_at=? WHERE id=?", status == UserEnabled, now, id)
	return err
}

func (Repository) RevokeSessions(ctx context.Context, q database.Querier, id string, now time.Time) error {
	_, err := q.ExecContext(ctx, "UPDATE sessions SET revoked_at=? WHERE user_id=? AND revoked_at IS NULL", now, id)
	return err
}

func (Repository) LockEnabledEmergencyAdmins(ctx context.Context, q database.Querier) (int, error) {
	rows, err := q.QueryContext(ctx, `SELECT u.id FROM users u JOIN local_credentials lc ON lc.user_id=u.id
		WHERE u.enabled=TRUE AND lc.emergency_admin=TRUE ORDER BY u.id FOR UPDATE`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
	}
	return count, rows.Err()
}

func encodeUserCursor(cursor userCursor) (string, error) {
	value, err := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(value), err
}
func decodeUserCursor(value string) (userCursor, error) {
	var cursor userCursor
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		err = json.Unmarshal(raw, &cursor)
	}
	if cursor.ID == "" || cursor.UpdatedAt.IsZero() {
		err = fmt.Errorf("invalid cursor")
	}
	return cursor, err
}
func sameUserFilter(cursor userCursor, filter UserFilter) bool {
	return equalStatus(cursor.Status, filter.Status) && equalMethod(cursor.Method, filter.AuthMethod) && cursor.Query == filter.Query
}
func equalStatus(a, b *UserStatus) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func equalMethod(a, b *AuthMethod) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}
func invalidCursor() error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "invalid_cursor", Message: "游标无效"}
}
func notFound(message string) error {
	return &apperror.Error{Kind: apperror.KindNotFound, Code: "not_found", Message: message}
}
