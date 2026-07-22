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
	mysql "github.com/go-sql-driver/mysql"
)

type Repository struct{}

var errPhoneConflict = errors.New("phone conflict")

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
			where = append(where, "EXISTS (SELECT 1 FROM sms_credentials sc2 WHERE sc2.user_id = u.id)")
		}
	}
	if filter.Query != "" {
		where = append(where, "(LOWER(u.display_name) LIKE ? ESCAPE '=' OR LOWER(COALESCE(u.email, '')) LIKE ? ESCAPE '=' OR EXISTS (SELECT 1 FROM sms_credentials sc3 WHERE sc3.user_id=u.id AND sc3.phone_e164 LIKE ? ESCAPE '='))")
		value := strings.ToLower(filter.Query)
		value = strings.NewReplacer("=", "==", "%", "=%", "_", "=_").Replace(value)
		value = "%" + value + "%"
		phoneValue := value
		if normalized, _, err := normalizeMainlandPhone(filter.Query); err == nil {
			phoneValue = "%" + normalized + "%"
		}
		args = append(args, value, value, phoneValue)
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
		EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.kind='high_risk'),
		EXISTS(SELECT 1 FROM sms_credentials sc WHERE sc.user_id=u.id),
		(SELECT sc.phone_masked FROM sms_credentials sc WHERE sc.user_id=u.id)
		FROM users u WHERE `+strings.Join(where, " AND ")+` ORDER BY u.updated_at DESC, u.id DESC LIMIT ?`, args...)
	if err != nil {
		return UserList{}, err
	}
	defer rows.Close()
	items := make([]UserSummary, 0, filter.Limit+1)
	for rows.Next() {
		var user UserSummary
		var enabled, local, sms bool
		var phoneMasked sql.NullString
		if err := rows.Scan(&user.ID, &user.DisplayName, &user.Email, &enabled, &user.CreatedAt, &user.UpdatedAt, &local, &user.EmergencyAdmin, &user.HighRiskRole, &sms, &phoneMasked); err != nil {
			return UserList{}, err
		}
		if phoneMasked.Valid {
			user.PhoneMasked = &phoneMasked.String
		}
		user.Status = UserDisabled
		if enabled {
			user.Status = UserEnabled
		}
		user.AuthMethods = []AuthMethod{}
		if local {
			user.AuthMethods = append(user.AuthMethods, AuthMethodLocal)
		}
		if sms {
			user.AuthMethods = append(user.AuthMethods, AuthMethodSMS)
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
		EXISTS(SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id WHERE ur.user_id=u.id AND r.kind='high_risk'),
		EXISTS(SELECT 1 FROM sms_credentials sc WHERE sc.user_id=u.id),
		(SELECT sc.phone_e164 FROM sms_credentials sc WHERE sc.user_id=u.id),
		(SELECT sc.phone_masked FROM sms_credentials sc WHERE sc.user_id=u.id) FROM users u WHERE u.id=?`
	if lock {
		query += " FOR UPDATE"
	}
	var user User
	var enabled, local, sms bool
	var phone, phoneMasked sql.NullString
	err := q.QueryRowContext(ctx, query, id).Scan(&user.ID, &user.DisplayName, &user.Email, &enabled, &user.CreatedAt, &user.UpdatedAt, &local, &user.EmergencyAdmin, &user.HighRiskRole, &sms, &phone, &phoneMasked)
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
	if phone.Valid {
		user.Phone = &phone.String
	}
	if phoneMasked.Valid {
		user.PhoneMasked = &phoneMasked.String
	}
	user.AuthMethods = []AuthMethod{}
	if local {
		user.AuthMethods = append(user.AuthMethods, AuthMethodLocal)
	}
	if sms {
		user.AuthMethods = append(user.AuthMethods, AuthMethodSMS)
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

func (Repository) LockRoleIDs(ctx context.Context, q database.Querier, ids []string) (LockedRoleSelection, error) {
	result := LockedRoleSelection{Permissions: PermissionSet{System: []string{}, Models: []ModelPermissions{}}}
	for _, id := range ids {
		var found string
		var kind string
		err := q.QueryRowContext(ctx, "SELECT id, kind FROM roles WHERE id=? FOR UPDATE", id).Scan(&found, &kind)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return LockedRoleSelection{}, err
		}
		result.Count++
		result.HighRisk = result.HighRisk || kind == "high_risk"
		systemRows, err := q.QueryContext(ctx, "SELECT permission FROM role_system_permissions WHERE role_id=? ORDER BY permission FOR SHARE", id)
		if err != nil {
			return LockedRoleSelection{}, err
		}
		for systemRows.Next() {
			var code string
			if err := systemRows.Scan(&code); err != nil {
				systemRows.Close()
				return LockedRoleSelection{}, err
			}
			result.Permissions.System = append(result.Permissions.System, code)
		}
		if err := systemRows.Err(); err != nil {
			systemRows.Close()
			return LockedRoleSelection{}, err
		}
		if err := systemRows.Close(); err != nil {
			return LockedRoleSelection{}, err
		}
		modelRows, err := q.QueryContext(ctx, "SELECT model_id, permission FROM role_model_permissions WHERE role_id=? ORDER BY model_id, permission FOR SHARE", id)
		if err != nil {
			return LockedRoleSelection{}, err
		}
		for modelRows.Next() {
			var modelID, code string
			if err := modelRows.Scan(&modelID, &code); err != nil {
				modelRows.Close()
				return LockedRoleSelection{}, err
			}
			result.Permissions.Models = append(result.Permissions.Models, ModelPermissions{ModelID: modelID, Permissions: []string{code}})
		}
		if err := modelRows.Err(); err != nil {
			modelRows.Close()
			return LockedRoleSelection{}, err
		}
		if err := modelRows.Close(); err != nil {
			return LockedRoleSelection{}, err
		}
	}
	return result, nil
}

func (Repository) CreateSMSUser(ctx context.Context, q database.Querier, user User, phoneE164, phoneMasked string, now time.Time) error {
	if _, err := q.ExecContext(ctx, "INSERT INTO users (id, display_name, email, enabled, created_at, updated_at) VALUES (?, ?, NULL, TRUE, ?, ?)", user.ID, user.DisplayName, now, now); err != nil {
		return err
	}
	if _, err := q.ExecContext(ctx, "INSERT INTO sms_credentials (user_id, phone_e164, phone_masked, created_at, updated_at) VALUES (?, ?, ?, ?, ?)", user.ID, phoneE164, phoneMasked, now, now); err != nil {
		if duplicateEntry(err) {
			return errPhoneConflict
		}
		return err
	}
	for _, roleID := range user.RoleIDs {
		if _, err := q.ExecContext(ctx, "INSERT INTO user_roles (user_id, role_id, created_at) VALUES (?, ?, ?)", user.ID, roleID, now); err != nil {
			return err
		}
	}
	return nil
}

func (Repository) UpdatePhone(ctx context.Context, q database.Querier, id, phoneE164, phoneMasked string, now time.Time) error {
	if _, err := q.ExecContext(ctx, "UPDATE sms_credentials SET phone_e164=?, phone_masked=?, updated_at=? WHERE user_id=?", phoneE164, phoneMasked, now, id); err != nil {
		if duplicateEntry(err) {
			return errPhoneConflict
		}
		return err
	}
	_, err := q.ExecContext(ctx, "UPDATE users SET updated_at=? WHERE id=?", now, id)
	return err
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

func duplicateEntry(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1062
}
