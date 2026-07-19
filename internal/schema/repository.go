package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"cms/internal/platform/database"
	"github.com/go-sql-driver/mysql"
)

var ErrDuplicateKey = errors.New("稳定标识已存在")

type Repository interface {
	ListModels(context.Context, database.Querier, *ResourceStatus) ([]ContentModelSummary, error)
	GetModel(context.Context, database.Querier, string) (ContentModel, error)
	LockModel(context.Context, database.Querier, string) (ContentModelSummary, error)
	LockModels(context.Context, database.Querier, []string) (map[string]ContentModelSummary, error)
	CreateModel(context.Context, database.Querier, ContentModelSummary) error
	UpdateModel(context.Context, database.Querier, ContentModelSummary) error
	GetField(context.Context, database.Querier, string, string) (ContentField, error)
	LockField(context.Context, database.Querier, string, string) (ContentField, error)
	CreateFieldTree(context.Context, database.Querier, string, *ContentField, *string, int) error
	UpdateField(context.Context, database.Querier, string, ContentField) error
	UpdateFieldPosition(context.Context, database.Querier, string, string, int) error
	ArchiveFieldTree(context.Context, database.Querier, string, string, time.Time) error
}

type SQLRepository struct{}

func NewRepository() SQLRepository { return SQLRepository{} }

func (SQLRepository) ListModels(ctx context.Context, q database.Querier, status *ResourceStatus) ([]ContentModelSummary, error) {
	query := `SELECT id, model_key, display_name, description, status, created_at, updated_at FROM content_models`
	args := []any{}
	if status != nil {
		query += ` WHERE status = ?`
		args = append(args, *status)
	}
	query += ` ORDER BY model_key`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询模型: %w", err)
	}
	defer rows.Close()
	items := []ContentModelSummary{}
	for rows.Next() {
		var item ContentModelSummary
		if err := rows.Scan(&item.ID, &item.Key, &item.DisplayName, &item.Description, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取模型: %w", err)
		}
		item.CreatedAt = item.CreatedAt.UTC()
		item.UpdatedAt = item.UpdatedAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r SQLRepository) GetModel(ctx context.Context, q database.Querier, id string) (ContentModel, error) {
	var model ContentModel
	err := q.QueryRowContext(ctx, `SELECT id, model_key, display_name, description, status, created_at, updated_at FROM content_models WHERE id = ?`, id).Scan(&model.ID, &model.Key, &model.DisplayName, &model.Description, &model.Status, &model.CreatedAt, &model.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model, notFound("模型")
	}
	if err != nil {
		return model, fmt.Errorf("查询模型: %w", err)
	}
	model.CreatedAt = model.CreatedAt.UTC()
	model.UpdatedAt = model.UpdatedAt.UTC()
	fields, err := r.listFields(ctx, q, id, false)
	if err != nil {
		return model, err
	}
	model.Fields = fields
	return model, nil
}

func (SQLRepository) LockModel(ctx context.Context, q database.Querier, id string) (ContentModelSummary, error) {
	var model ContentModelSummary
	err := q.QueryRowContext(ctx, `SELECT id, model_key, display_name, description, status, created_at, updated_at FROM content_models WHERE id = ? FOR UPDATE`, id).Scan(&model.ID, &model.Key, &model.DisplayName, &model.Description, &model.Status, &model.CreatedAt, &model.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return model, notFound("模型")
	}
	if err != nil {
		return model, fmt.Errorf("锁定模型: %w", err)
	}
	return model, nil
}

func (SQLRepository) LockModels(ctx context.Context, q database.Querier, ids []string) (map[string]ContentModelSummary, error) {
	ids = append([]string(nil), ids...)
	sort.Strings(ids)
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := `SELECT id, model_key, display_name, description, status, created_at, updated_at FROM content_models WHERE id IN (` + strings.Join(placeholders, ",") + `) ORDER BY id FOR UPDATE`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("锁定模型: %w", err)
	}
	defer rows.Close()
	models := make(map[string]ContentModelSummary, len(ids))
	for rows.Next() {
		var model ContentModelSummary
		if err := rows.Scan(&model.ID, &model.Key, &model.DisplayName, &model.Description, &model.Status, &model.CreatedAt, &model.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取锁定模型: %w", err)
		}
		models[model.ID] = model
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("读取锁定模型: %w", err)
	}
	return models, nil
}

func (SQLRepository) CreateModel(ctx context.Context, q database.Querier, model ContentModelSummary) error {
	_, err := q.ExecContext(ctx, `INSERT INTO content_models (id, model_key, display_name, description, status, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?)`, model.ID, model.Key, model.DisplayName, model.Description, model.Status, model.CreatedAt, model.UpdatedAt)
	return translateWriteError("创建模型", err)
}

func (SQLRepository) UpdateModel(ctx context.Context, q database.Querier, model ContentModelSummary) error {
	_, err := q.ExecContext(ctx, `UPDATE content_models SET display_name = ?, description = ?, status = ?, updated_at = ? WHERE id = ?`, model.DisplayName, model.Description, model.Status, model.UpdatedAt, model.ID)
	return translateWriteError("更新模型", err)
}

func (r SQLRepository) GetField(ctx context.Context, q database.Querier, modelID, fieldID string) (ContentField, error) {
	return r.getField(ctx, q, modelID, fieldID, false)
}

func (r SQLRepository) LockField(ctx context.Context, q database.Querier, modelID, fieldID string) (ContentField, error) {
	return r.getField(ctx, q, modelID, fieldID, true)
}

func (r SQLRepository) getField(ctx context.Context, q database.Querier, modelID, fieldID string, lock bool) (ContentField, error) {
	fields, err := r.listFields(ctx, q, modelID, lock)
	if err != nil {
		return ContentField{}, err
	}
	var walk func([]ContentField) (ContentField, bool)
	walk = func(items []ContentField) (ContentField, bool) {
		for _, item := range items {
			if item.ID == fieldID {
				return item, true
			}
			if found, ok := walk(item.Children); ok {
				return found, true
			}
		}
		return ContentField{}, false
	}
	if field, ok := walk(fields); ok {
		return field, nil
	}
	return ContentField{}, notFound("字段")
}

func (r SQLRepository) listFields(ctx context.Context, q database.Querier, modelID string, lock bool) ([]ContentField, error) {
	query := `SELECT id, parent_id, field_key, display_name, description, field_type, is_required, default_value, constraints, status, depth, created_at, updated_at FROM content_fields WHERE model_id = ? ORDER BY depth, position, field_key`
	if lock {
		query += ` FOR UPDATE`
	}
	rows, err := q.QueryContext(ctx, query, modelID)
	if err != nil {
		return nil, fmt.Errorf("查询字段: %w", err)
	}
	defer rows.Close()
	type row struct {
		field  ContentField
		parent sql.NullString
	}
	all := []row{}
	for rows.Next() {
		var item row
		var defaultValue, constraints []byte
		if err := rows.Scan(&item.field.ID, &item.parent, &item.field.Key, &item.field.DisplayName, &item.field.Description, &item.field.Type, &item.field.Required, &defaultValue, &constraints, &item.field.Status, &item.field.Depth, &item.field.CreatedAt, &item.field.UpdatedAt); err != nil {
			return nil, fmt.Errorf("读取字段: %w", err)
		}
		item.field.DefaultValue = append(json.RawMessage(nil), defaultValue...)
		item.field.Children = []ContentField{}
		item.field.CreatedAt = item.field.CreatedAt.UTC()
		item.field.UpdatedAt = item.field.UpdatedAt.UTC()
		if err := json.Unmarshal(constraints, &item.field.Constraints); err != nil {
			return nil, fmt.Errorf("读取字段约束: %w", err)
		}
		all = append(all, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	byParent := map[string][]ContentField{}
	for i := len(all) - 1; i >= 0; i-- {
		item := all[i]
		item.field.Children = byParent[item.field.ID]
		key := ""
		if item.parent.Valid {
			key = item.parent.String
		}
		byParent[key] = append([]ContentField{item.field}, byParent[key]...)
	}
	return byParent[""], nil
}

func (r SQLRepository) CreateFieldTree(ctx context.Context, q database.Querier, modelID string, field *ContentField, parentID *string, position int) error {
	constraints, _ := json.Marshal(field.Constraints)
	_, err := q.ExecContext(ctx, `INSERT INTO content_fields (id, model_id, parent_id, field_key, display_name, description, field_type, is_required, default_value, constraints, status, depth, position, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, field.ID, modelID, parentID, field.Key, field.DisplayName, field.Description, field.Type, field.Required, []byte(field.DefaultValue), constraints, field.Status, field.Depth, position, field.CreatedAt, field.UpdatedAt)
	if err := translateWriteError("创建字段", err); err != nil {
		return err
	}
	for i := range field.Children {
		if err := r.CreateFieldTree(ctx, q, modelID, &field.Children[i], &field.ID, i); err != nil {
			return err
		}
	}
	return nil
}

func (SQLRepository) UpdateField(ctx context.Context, q database.Querier, modelID string, field ContentField) error {
	constraints, _ := json.Marshal(field.Constraints)
	_, err := q.ExecContext(ctx, `UPDATE content_fields SET display_name = ?, description = ?, field_type = ?, is_required = ?, default_value = ?, constraints = ?, status = ?, updated_at = ? WHERE id = ? AND model_id = ?`, field.DisplayName, field.Description, field.Type, field.Required, []byte(field.DefaultValue), constraints, field.Status, field.UpdatedAt, field.ID, modelID)
	return translateWriteError("更新字段", err)
}

func (SQLRepository) UpdateFieldPosition(ctx context.Context, q database.Querier, modelID, fieldID string, position int) error {
	_, err := q.ExecContext(ctx, `UPDATE content_fields SET position = ? WHERE id = ? AND model_id = ?`, position, fieldID, modelID)
	return translateWriteError("更新字段顺序", err)
}

func (SQLRepository) ArchiveFieldTree(ctx context.Context, q database.Querier, modelID, fieldID string, now time.Time) error {
	_, err := q.ExecContext(ctx, `WITH RECURSIVE descendants AS (SELECT id FROM content_fields WHERE id = ? AND model_id = ? UNION ALL SELECT f.id FROM content_fields f JOIN descendants d ON f.parent_id = d.id) UPDATE content_fields SET status = 'archived', updated_at = ? WHERE id IN (SELECT id FROM descendants)`, fieldID, modelID, now)
	return translateWriteError("归档字段", err)
}

func translateWriteError(operation string, err error) error {
	if err == nil {
		return nil
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return ErrDuplicateKey
	}
	return fmt.Errorf("%s: %w", operation, err)
}
