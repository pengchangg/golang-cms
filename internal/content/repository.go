package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cms/internal/platform/database"
	"github.com/go-sql-driver/mysql"
)

type UniqueValue struct {
	FieldID        string
	CanonicalValue []byte
}

type uniqueValueConflict struct{ FieldID string }

func (e *uniqueValueConflict) Error() string { return "唯一字段值已被占用" }

type EntryCursor struct {
	UpdatedAt time.Time
	ID        string
}

type Repository interface {
	HasAnyContent(context.Context, database.Querier, string) (bool, error)
	ListEntries(context.Context, database.Querier, string, EntryStatus, int, *EntryCursor) ([]EntrySummary, error)
	GetEntry(context.Context, database.Querier, string, string) (Entry, error)
	LockEntry(context.Context, database.Querier, string, string) (EntrySummary, error)
	CreateEntry(context.Context, database.Querier, EntrySummary) error
	UpdateEntry(context.Context, database.Querier, EntrySummary) error
	CreateRevision(context.Context, database.Querier, Revision) error
	SetDraftPointer(context.Context, database.Querier, string, string, string) error
	ReplaceUniqueValues(context.Context, database.Querier, string, string, []UniqueValue) error
	DeleteUniqueValues(context.Context, database.Querier, string, string) error
	ListRevisions(context.Context, database.Querier, string, string, int, *uint) ([]Revision, error)
	GetRevision(context.Context, database.Querier, string, string, string) (Revision, error)
}

type SQLRepository struct{}

func NewRepository() SQLRepository { return SQLRepository{} }

func (SQLRepository) HasAnyContent(ctx context.Context, q database.Querier, modelID string) (bool, error) {
	var exists bool
	if err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM content_entries WHERE model_id = ?)`, modelID).Scan(&exists); err != nil {
		return false, fmt.Errorf("查询模型内容存在性: %w", err)
	}
	return exists, nil
}

func (SQLRepository) ListEntries(ctx context.Context, q database.Querier, modelID string, status EntryStatus, limit int, cursor *EntryCursor) ([]EntrySummary, error) {
	query := `SELECT e.id, e.model_id, e.status, p.revision_id, e.created_by, e.created_at, e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id = e.id AND p.model_id = e.model_id WHERE e.model_id = ? AND e.status = ?`
	args := []any{modelID, status}
	if cursor != nil {
		query += ` AND (e.updated_at < ? OR (e.updated_at = ? AND e.id < ?))`
		args = append(args, cursor.UpdatedAt, cursor.UpdatedAt, cursor.ID)
	}
	query += ` ORDER BY e.updated_at DESC, e.id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询内容条目: %w", err)
	}
	defer rows.Close()
	items := []EntrySummary{}
	for rows.Next() {
		var item EntrySummary
		if err := rows.Scan(&item.ID, &item.ModelID, &item.Status, &item.CurrentDraftRevisionID, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取内容条目: %w", err)
		}
		item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r SQLRepository) GetEntry(ctx context.Context, q database.Querier, modelID, entryID string) (Entry, error) {
	var entry Entry
	err := q.QueryRowContext(ctx, `SELECT e.id, e.model_id, e.status, p.revision_id, e.created_by, e.created_at, e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id = e.id AND p.model_id = e.model_id WHERE e.id = ? AND e.model_id = ?`, entryID, modelID).Scan(&entry.ID, &entry.ModelID, &entry.Status, &entry.CurrentDraftRevisionID, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, notFound("内容条目")
	}
	if err != nil {
		return entry, fmt.Errorf("查询内容条目: %w", err)
	}
	entry.CreatedAt, entry.UpdatedAt = entry.CreatedAt.UTC(), entry.UpdatedAt.UTC()
	revision, err := r.GetRevision(ctx, q, modelID, entryID, entry.CurrentDraftRevisionID)
	entry.CurrentDraftRevision = revision
	return entry, err
}

func (SQLRepository) LockEntry(ctx context.Context, q database.Querier, modelID, entryID string) (EntrySummary, error) {
	var entry EntrySummary
	err := q.QueryRowContext(ctx, `SELECT e.id, e.model_id, e.status, p.revision_id, e.created_by, e.created_at, e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id = e.id AND p.model_id = e.model_id WHERE e.id = ? AND e.model_id = ? FOR UPDATE`, entryID, modelID).Scan(&entry.ID, &entry.ModelID, &entry.Status, &entry.CurrentDraftRevisionID, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, notFound("内容条目")
	}
	if err != nil {
		return entry, fmt.Errorf("锁定内容条目: %w", err)
	}
	entry.CreatedAt, entry.UpdatedAt = entry.CreatedAt.UTC(), entry.UpdatedAt.UTC()
	return entry, nil
}

func (SQLRepository) CreateEntry(ctx context.Context, q database.Querier, entry EntrySummary) error {
	_, err := q.ExecContext(ctx, `INSERT INTO content_entries (id, model_id, status, created_by, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?)`, entry.ID, entry.ModelID, entry.Status, entry.CreatedBy, entry.CreatedAt, entry.UpdatedAt)
	if err != nil {
		return fmt.Errorf("创建内容条目: %w", err)
	}
	return nil
}

func (SQLRepository) UpdateEntry(ctx context.Context, q database.Querier, entry EntrySummary) error {
	_, err := q.ExecContext(ctx, `UPDATE content_entries SET status = ?, updated_at = ? WHERE id = ? AND model_id = ?`, entry.Status, entry.UpdatedAt, entry.ID, entry.ModelID)
	if err != nil {
		return fmt.Errorf("更新内容条目: %w", err)
	}
	return nil
}

func (SQLRepository) CreateRevision(ctx context.Context, q database.Querier, revision Revision) error {
	_, err := q.ExecContext(ctx, `INSERT INTO content_revisions (id, entry_id, model_id, revision_number, content, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, revision.ID, revision.EntryID, revision.ModelID, revision.Number, []byte(revision.Content), revision.CreatedBy, revision.CreatedAt)
	if err != nil {
		return fmt.Errorf("创建内容 Revision: %w", err)
	}
	return nil
}

func (SQLRepository) SetDraftPointer(ctx context.Context, q database.Querier, modelID, entryID, revisionID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO content_draft_pointers (entry_id, model_id, revision_id) VALUES (?, ?, ?) ON DUPLICATE KEY UPDATE revision_id = VALUES(revision_id)`, entryID, modelID, revisionID)
	if err != nil {
		return fmt.Errorf("切换当前草稿指针: %w", err)
	}
	return nil
}

func (SQLRepository) ReplaceUniqueValues(ctx context.Context, q database.Querier, modelID, entryID string, values []UniqueValue) error {
	if _, err := q.ExecContext(ctx, `DELETE FROM content_unique_values WHERE model_id = ? AND entry_id = ?`, modelID, entryID); err != nil {
		return fmt.Errorf("释放内容唯一值: %w", err)
	}
	for _, value := range values {
		_, err := q.ExecContext(ctx, `INSERT INTO content_unique_values (model_id, field_id, canonical_value, entry_id) VALUES (?, ?, ?, ?)`, modelID, value.FieldID, value.CanonicalValue, entryID)
		var mysqlError *mysql.MySQLError
		if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
			return &uniqueValueConflict{FieldID: value.FieldID}
		}
		if err != nil {
			return fmt.Errorf("占用内容唯一值: %w", err)
		}
	}
	return nil
}

func (SQLRepository) DeleteUniqueValues(ctx context.Context, q database.Querier, modelID, entryID string) error {
	_, err := q.ExecContext(ctx, `DELETE FROM content_unique_values WHERE model_id = ? AND entry_id = ?`, modelID, entryID)
	if err != nil {
		return fmt.Errorf("释放内容唯一值: %w", err)
	}
	return nil
}

func (r SQLRepository) ListRevisions(ctx context.Context, q database.Querier, modelID, entryID string, limit int, before *uint) ([]Revision, error) {
	if _, err := r.GetEntrySummary(ctx, q, modelID, entryID); err != nil {
		return nil, err
	}
	query := `SELECT id, entry_id, model_id, revision_number, content, created_by, created_at FROM content_revisions WHERE entry_id = ? AND model_id = ?`
	args := []any{entryID, modelID}
	if before != nil {
		query += ` AND revision_number < ?`
		args = append(args, *before)
	}
	query += ` ORDER BY revision_number DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询内容 Revision: %w", err)
	}
	defer rows.Close()
	items := []Revision{}
	for rows.Next() {
		item, err := scanRevision(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (SQLRepository) GetRevision(ctx context.Context, q database.Querier, modelID, entryID, revisionID string) (Revision, error) {
	row := q.QueryRowContext(ctx, `SELECT id, entry_id, model_id, revision_number, content, created_by, created_at FROM content_revisions WHERE id = ? AND entry_id = ? AND model_id = ?`, revisionID, entryID, modelID)
	var revision Revision
	var content []byte
	err := row.Scan(&revision.ID, &revision.EntryID, &revision.ModelID, &revision.Number, &content, &revision.CreatedBy, &revision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, notFound("内容 Revision")
	}
	if err != nil {
		return revision, fmt.Errorf("查询内容 Revision: %w", err)
	}
	revision.Content = append(json.RawMessage(nil), content...)
	revision.CreatedAt = revision.CreatedAt.UTC()
	return revision, nil
}

func (SQLRepository) GetEntrySummary(ctx context.Context, q database.Querier, modelID, entryID string) (EntrySummary, error) {
	var entry EntrySummary
	err := q.QueryRowContext(ctx, `SELECT e.id, e.model_id, e.status, p.revision_id, e.created_by, e.created_at, e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id = e.id AND p.model_id = e.model_id WHERE e.id = ? AND e.model_id = ?`, entryID, modelID).Scan(&entry.ID, &entry.ModelID, &entry.Status, &entry.CurrentDraftRevisionID, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, notFound("内容条目")
	}
	return entry, err
}

type revisionScanner interface{ Scan(...any) error }

func scanRevision(row revisionScanner) (Revision, error) {
	var item Revision
	var content []byte
	if err := row.Scan(&item.ID, &item.EntryID, &item.ModelID, &item.Number, &content, &item.CreatedBy, &item.CreatedAt); err != nil {
		return item, fmt.Errorf("读取内容 Revision: %w", err)
	}
	item.Content = append(json.RawMessage(nil), content...)
	item.CreatedAt = item.CreatedAt.UTC()
	return item, nil
}
