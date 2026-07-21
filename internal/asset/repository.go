package asset

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

type Cursor struct {
	CreatedAt time.Time
	ID        string
}

type Repository interface {
	Create(context.Context, database.Querier, Asset) error
	Get(context.Context, database.Querier, string) (Asset, error)
	Lock(context.Context, database.Querier, string) (Asset, error)
	Confirm(context.Context, database.Querier, string, string, time.Time) error
	Rename(context.Context, database.Querier, string, string) error
	Archive(context.Context, database.Querier, string, time.Time) error
	List(context.Context, database.Querier, ListQuery, int, *Cursor) ([]Asset, error)
}

type SQLRepository struct{}

func (SQLRepository) Create(ctx context.Context, q database.Querier, value Asset) error {
	_, err := q.ExecContext(ctx, `INSERT INTO assets(id,object_key,filename,mime_type,size,sha256,etag,status,created_by,created_at,upload_expires_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, value.ID, value.ObjectKey, value.Filename, value.MimeType, value.Size, value.SHA256, nil, value.Status, value.CreatedBy, value.CreatedAt, value.UploadUntil)
	if err != nil {
		return fmt.Errorf("创建素材: %w", err)
	}
	return nil
}

func (r SQLRepository) Get(ctx context.Context, q database.Querier, id string) (Asset, error) {
	return r.get(ctx, q, id, false)
}

func (r SQLRepository) Lock(ctx context.Context, q database.Querier, id string) (Asset, error) {
	return r.get(ctx, q, id, true)
}

func (SQLRepository) get(ctx context.Context, q database.Querier, id string, lock bool) (Asset, error) {
	query := `SELECT id,object_key,filename,mime_type,size,sha256,etag,status,created_by,created_at,confirmed_at,archived_at,upload_expires_at FROM assets WHERE id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	var value Asset
	var etag sql.NullString
	var confirmed, archived sql.NullTime
	err := q.QueryRowContext(ctx, query, id).Scan(&value.ID, &value.ObjectKey, &value.Filename, &value.MimeType, &value.Size, &value.SHA256, &etag, &value.Status, &value.CreatedBy, &value.CreatedAt, &confirmed, &archived, &value.UploadUntil)
	if errors.Is(err, sql.ErrNoRows) {
		return value, appError(apperror.KindNotFound, "resource_not_found", "素材不存在")
	}
	if err != nil {
		return value, fmt.Errorf("查询素材: %w", err)
	}
	normalizeAsset(&value, etag, confirmed, archived)
	return value, nil
}

func (SQLRepository) Confirm(ctx context.Context, q database.Querier, id, etag string, at time.Time) error {
	result, err := q.ExecContext(ctx, `UPDATE assets SET etag=?,status='available',confirmed_at=? WHERE id=? AND status='quarantined' AND upload_expires_at>?`, etag, at, id, at)
	if err != nil {
		return fmt.Errorf("确认素材: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return appError(apperror.KindConflict, "asset_upload_expired", "素材上传申请已过期")
	}
	return nil
}

func (SQLRepository) Rename(ctx context.Context, q database.Querier, id, filename string) error {
	_, err := q.ExecContext(ctx, `UPDATE assets SET filename=? WHERE id=?`, filename, id)
	return err
}

func (SQLRepository) Archive(ctx context.Context, q database.Querier, id string, at time.Time) error {
	result, err := q.ExecContext(ctx, `UPDATE assets SET status='archived',archived_at=? WHERE id=? AND status='available'`, at, id)
	if err != nil {
		return fmt.Errorf("归档素材: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return appError(apperror.KindConflict, "resource_archived", "素材已归档或不可归档")
	}
	return nil
}

func (SQLRepository) List(ctx context.Context, q database.Querier, input ListQuery, limit int, cursor *Cursor) ([]Asset, error) {
	query := `SELECT id,object_key,filename,mime_type,size,sha256,etag,status,created_by,created_at,confirmed_at,archived_at,upload_expires_at FROM assets WHERE 1=1`
	args := []any{}
	if input.Status != nil {
		query += ` AND status=?`
		args = append(args, *input.Status)
	}
	if input.MimeType != "" {
		query += ` AND mime_type=?`
		args = append(args, input.MimeType)
	}
	if cursor != nil {
		query += ` AND (created_at<? OR (created_at=? AND id<?))`
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
	}
	query += ` ORDER BY created_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("列出素材: %w", err)
	}
	defer rows.Close()
	items := []Asset{}
	for rows.Next() {
		var value Asset
		var etag sql.NullString
		var confirmed, archived sql.NullTime
		if err := rows.Scan(&value.ID, &value.ObjectKey, &value.Filename, &value.MimeType, &value.Size, &value.SHA256, &etag, &value.Status, &value.CreatedBy, &value.CreatedAt, &confirmed, &archived, &value.UploadUntil); err != nil {
			return nil, err
		}
		normalizeAsset(&value, etag, confirmed, archived)
		items = append(items, value)
	}
	return items, rows.Err()
}

func normalizeAsset(value *Asset, etag sql.NullString, confirmed, archived sql.NullTime) {
	decorateAsset(value)
	value.CreatedAt, value.UploadUntil = value.CreatedAt.UTC(), value.UploadUntil.UTC()
	if etag.Valid {
		value.ETag = &etag.String
	}
	if confirmed.Valid {
		t := confirmed.Time.UTC()
		value.ConfirmedAt = &t
	}
	if archived.Valid {
		t := archived.Time.UTC()
		value.ArchivedAt = &t
	}
}

func placeholders(count int) string { return strings.TrimSuffix(strings.Repeat("?,", count), ",") }
