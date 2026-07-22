package client

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type APIKeyCursor struct {
	CreatedAt time.Time
	ID        string
}

type Repository interface {
	List(context.Context, database.Querier, APIKeyStatus, int, *APIKeyCursor, time.Time) ([]APIKey, error)
	Get(context.Context, database.Querier, string, bool, time.Time) (APIKey, error)
	FindByPrefix(context.Context, database.Querier, string, bool, time.Time) (APIKey, error)
	ValidateActiveModels(context.Context, database.Querier, []string) error
	Create(context.Context, database.Querier, APIKey) error
	Revoke(context.Context, database.Querier, string, string, time.Time) error
	TouchLastUsed(context.Context, database.Querier, string, time.Time) error
}

type SQLRepository struct{}

func NewRepository() SQLRepository { return SQLRepository{} }

func (SQLRepository) List(ctx context.Context, q database.Querier, status APIKeyStatus, limit int, cursor *APIKeyCursor, now time.Time) ([]APIKey, error) {
	query := `SELECT id,name,prefix,expires_at,revoked_at,last_used_at,rotated_from_id,replaced_by_id,created_by,created_at FROM api_keys WHERE 1=1`
	args := []any{}
	switch status {
	case APIKeyActive:
		query += ` AND revoked_at IS NULL AND (expires_at IS NULL OR expires_at>?)`
		args = append(args, now)
	case APIKeyExpired:
		query += ` AND revoked_at IS NULL AND expires_at<=?`
		args = append(args, now)
	case APIKeyRevoked:
		query += ` AND revoked_at IS NOT NULL`
	}
	if cursor != nil {
		query += ` AND (created_at<? OR (created_at=? AND id<?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询 API Key: %w", err)
	}
	defer rows.Close()
	items := []APIKey{}
	for rows.Next() {
		item, err := scanAPIKey(rows, now, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	for i := range items {
		items[i].ModelIDs, err = modelIDs(ctx, q, items[i].ID)
		if err != nil {
			return nil, err
		}
	}
	return items, nil
}

func (SQLRepository) Get(ctx context.Context, q database.Querier, id string, lock bool, now time.Time) (APIKey, error) {
	query := `SELECT id,name,prefix,expires_at,revoked_at,last_used_at,rotated_from_id,replaced_by_id,created_by,created_at,salt,secret_hash FROM api_keys WHERE id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	item, err := scanAPIKey(q.QueryRowContext(ctx, query, id), now, true)
	if errors.Is(err, sql.ErrNoRows) {
		return item, appError(apperror.KindNotFound, "resource_not_found", "API Key 不存在")
	}
	if err != nil {
		return item, fmt.Errorf("查询 API Key: %w", err)
	}
	item.ModelIDs, err = modelIDs(ctx, q, id)
	return item, err
}

func (SQLRepository) FindByPrefix(ctx context.Context, q database.Querier, prefix string, lock bool, now time.Time) (APIKey, error) {
	query := `SELECT id,name,prefix,expires_at,revoked_at,last_used_at,rotated_from_id,replaced_by_id,created_by,created_at,salt,secret_hash FROM api_keys WHERE prefix=?`
	if lock {
		query += ` FOR UPDATE`
	}
	item, err := scanAPIKey(q.QueryRowContext(ctx, query, prefix), now, true)
	if err != nil {
		return item, err
	}
	item.ModelIDs, err = modelIDs(ctx, q, item.ID)
	return item, err
}

func (SQLRepository) ValidateActiveModels(ctx context.Context, q database.Querier, ids []string) error {
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i], args[i] = "?", id
	}
	var count int
	if err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM content_models WHERE status='active' AND id IN (`+strings.Join(marks, ",")+`)`, args...).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return appError(apperror.KindNotFound, "resource_not_found", "模型不存在")
	}
	return nil
}

func (SQLRepository) Create(ctx context.Context, q database.Querier, key APIKey) error {
	_, err := q.ExecContext(ctx, `INSERT INTO api_keys (id,name,prefix,salt,secret_hash,expires_at,rotated_from_id,created_by,created_at) VALUES (?,?,?,?,?,?,?,?,?)`, key.ID, key.Name, key.Prefix, key.Salt, key.Hash, key.ExpiresAt, key.RotatedFromID, key.CreatedBy, key.CreatedAt)
	if err != nil {
		return fmt.Errorf("创建 API Key: %w", err)
	}
	for _, modelID := range key.ModelIDs {
		if _, err := q.ExecContext(ctx, `INSERT INTO api_key_model_scopes (api_key_id,model_id) VALUES (?,?)`, key.ID, modelID); err != nil {
			return fmt.Errorf("创建 API Key 模型范围: %w", err)
		}
	}
	return nil
}

func (SQLRepository) Revoke(ctx context.Context, q database.Querier, oldID, replacementID string, now time.Time) error {
	var replacement any
	if replacementID != "" {
		replacement = replacementID
	}
	_, err := q.ExecContext(ctx, `UPDATE api_keys SET revoked_at=?,replaced_by_id=? WHERE id=?`, now, replacement, oldID)
	return err
}

func (SQLRepository) TouchLastUsed(ctx context.Context, q database.Querier, id string, now time.Time) error {
	_, err := q.ExecContext(ctx, `UPDATE api_keys SET last_used_at=? WHERE id=? AND (last_used_at IS NULL OR last_used_at<=?)`, now, id, now.Add(-5*time.Minute))
	return err
}

type scanner interface{ Scan(...any) error }

func scanAPIKey(row scanner, now time.Time, sensitive bool) (APIKey, error) {
	var item APIKey
	var expires, revoked, used sql.NullTime
	var rotated, replaced sql.NullString
	dest := []any{&item.ID, &item.Name, &item.Prefix, &expires, &revoked, &used, &rotated, &replaced, &item.CreatedBy, &item.CreatedAt}
	if sensitive {
		dest = append(dest, &item.Salt, &item.Hash)
	}
	if err := row.Scan(dest...); err != nil {
		return item, err
	}
	item.CreatedAt = item.CreatedAt.UTC()
	if expires.Valid {
		value := expires.Time.UTC()
		item.ExpiresAt = &value
	}
	if revoked.Valid {
		value := revoked.Time.UTC()
		item.RevokedAt = &value
	}
	if used.Valid {
		value := used.Time.UTC()
		item.LastUsedAt = &value
	}
	if rotated.Valid {
		item.RotatedFromID = &rotated.String
	}
	if replaced.Valid {
		item.ReplacedByID = &replaced.String
	}
	item.Status = APIKeyActive
	if item.ExpiresAt != nil && !now.Before(*item.ExpiresAt) {
		item.Status = APIKeyExpired
	}
	if item.RevokedAt != nil {
		item.Status = APIKeyRevoked
	}
	return item, nil
}

func modelIDs(ctx context.Context, q database.Querier, id string) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT model_id FROM api_key_model_scopes WHERE api_key_id=? ORDER BY model_id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []string{}
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}
