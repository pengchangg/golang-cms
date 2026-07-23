package configuration

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

var ErrDuplicateKey = errors.New("配置 key 已存在")

type Repository interface {
	ListNamespaces(context.Context, database.Querier, *ResourceStatus) ([]Namespace, error)
	GetNamespace(context.Context, database.Querier, string) (Namespace, error)
	GetNamespaceByKey(context.Context, database.Querier, string) (Namespace, error)
	LockNamespace(context.Context, database.Querier, string) (Namespace, error)
	CreateNamespace(context.Context, database.Querier, Namespace) error
	UpdateNamespace(context.Context, database.Querier, Namespace) error
	ActiveConfigNamespaceIDs(context.Context, database.Querier) ([]string, error)
	HasActiveItems(context.Context, database.Querier, string) (bool, error)
	CountActiveItems(context.Context, database.Querier, string) (int, error)
	ListItems(context.Context, database.Querier, string, *ResourceStatus) ([]Item, error)
	GetItem(context.Context, database.Querier, string, string) (Item, error)
	LockItem(context.Context, database.Querier, string, string) (Item, error)
	CreateItem(context.Context, database.Querier, Item) error
	UpdateItem(context.Context, database.Querier, Item) error
	HasPublishedItems(context.Context, database.Querier, string) (bool, error)
	GetItemValue(context.Context, database.Querier, string, string) (ItemValue, error)
	ListRevisions(context.Context, database.Querier, string, string, int, *uint) ([]Revision, error)
	GetRevision(context.Context, database.Querier, string, string, string) (Revision, error)
	CreateRevision(context.Context, database.Querier, Revision) error
	SetDraftPointer(context.Context, database.Querier, string, string, string) error
	InsertAssetReferences(context.Context, database.Querier, []AssetReference) error
	InsertRelations(context.Context, database.Querier, []Relation) error
	ValidateAssetsAvailable(context.Context, database.Querier, []string, Constraints) error
	ValidateAssetsPublishable(context.Context, database.Querier, string, Constraints) error
	ValidateRelationTargetsActive(context.Context, database.Querier, []Relation) error
	ValidateRelationTargetsPublished(context.Context, database.Querier, string) error
	LockRevision(context.Context, database.Querier, string, string, string) (Revision, error)
	TransitionRevision(context.Context, database.Querier, string, WorkflowStatus, WorkflowStatus, *string, *time.Time) (bool, error)
	SetPublishedPointer(context.Context, database.Querier, string, string, string, time.Time) error
	DeletePublishedPointer(context.Context, database.Querier, string, string, string) (bool, error)
	CreateWorkflowEvent(context.Context, database.Querier, WorkflowEvent) error
	ListWorkflowEvents(context.Context, database.Querier, string, string, int, *WorkflowEventCursor) ([]WorkflowEvent, error)
}

type SQLRepository struct{}

func NewRepository() SQLRepository { return SQLRepository{} }

func (SQLRepository) ListNamespaces(ctx context.Context, q database.Querier, status *ResourceStatus) ([]Namespace, error) {
	query := `SELECT id,namespace_key,display_name,description,status,created_at,updated_at FROM config_namespaces`
	args := []any{}
	if status != nil {
		query += ` WHERE status=?`
		args = append(args, *status)
	}
	query += ` ORDER BY namespace_key,id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询配置 namespace: %w", err)
	}
	defer rows.Close()
	items := []Namespace{}
	for rows.Next() {
		var item Namespace
		if err := rows.Scan(&item.ID, &item.Key, &item.DisplayName, &item.Description, &item.Status, &item.CreatedAt, &item.UpdatedAt); err != nil {
			return nil, err
		}
		item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func (SQLRepository) GetNamespace(ctx context.Context, q database.Querier, id string) (Namespace, error) {
	return getNamespace(ctx, q, `SELECT id,namespace_key,display_name,description,status,created_at,updated_at FROM config_namespaces WHERE id=?`, id)
}

func (SQLRepository) GetNamespaceByKey(ctx context.Context, q database.Querier, key string) (Namespace, error) {
	return getNamespace(ctx, q, `SELECT id,namespace_key,display_name,description,status,created_at,updated_at FROM config_namespaces WHERE namespace_key=?`, key)
}

func (SQLRepository) LockNamespace(ctx context.Context, q database.Querier, id string) (Namespace, error) {
	return getNamespace(ctx, q, `SELECT id,namespace_key,display_name,description,status,created_at,updated_at FROM config_namespaces WHERE id=? FOR UPDATE`, id)
}

func getNamespace(ctx context.Context, q database.Querier, query string, argument any) (Namespace, error) {
	var item Namespace
	err := q.QueryRowContext(ctx, query, argument).Scan(&item.ID, &item.Key, &item.DisplayName, &item.Description, &item.Status, &item.CreatedAt, &item.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return item, notFound("配置 namespace")
	}
	if err != nil {
		return item, fmt.Errorf("查询配置 namespace: %w", err)
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}

func (SQLRepository) CreateNamespace(ctx context.Context, q database.Querier, item Namespace) error {
	_, err := q.ExecContext(ctx, `INSERT INTO config_namespaces(id,namespace_key,display_name,description,status,created_at,updated_at) VALUES(?,?,?,?,?,?,?)`, item.ID, item.Key, item.DisplayName, item.Description, item.Status, item.CreatedAt, item.UpdatedAt)
	return duplicateOrWrap(err, "创建配置 namespace")
}

func (SQLRepository) UpdateNamespace(ctx context.Context, q database.Querier, item Namespace) error {
	_, err := q.ExecContext(ctx, `UPDATE config_namespaces SET display_name=?,description=?,status=?,updated_at=? WHERE id=?`, item.DisplayName, item.Description, item.Status, item.UpdatedAt, item.ID)
	return duplicateOrWrap(err, "更新配置 namespace")
}

func (SQLRepository) ActiveConfigNamespaceIDs(ctx context.Context, q database.Querier) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT id FROM config_namespaces WHERE status='active' ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("查询活动配置 namespace: %w", err)
	}
	defer rows.Close()
	items := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		items = append(items, id)
	}
	return items, rows.Err()
}

func (SQLRepository) HasActiveItems(ctx context.Context, q database.Querier, namespaceID string) (bool, error) {
	var value int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM config_items WHERE namespace_id=? AND status='active' LIMIT 1 FOR SHARE`, namespaceID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (SQLRepository) CountActiveItems(ctx context.Context, q database.Querier, namespaceID string) (int, error) {
	var count int
	err := q.QueryRowContext(ctx, `SELECT COUNT(*) FROM config_items WHERE namespace_id=? AND status='active'`, namespaceID).Scan(&count)
	return count, err
}

func (SQLRepository) ListItems(ctx context.Context, q database.Querier, namespaceID string, status *ResourceStatus) ([]Item, error) {
	query := `SELECT id,namespace_id,item_key,display_name,description,value_type,constraints,status,created_by,created_at,updated_at FROM config_items WHERE namespace_id=?`
	args := []any{namespaceID}
	if status != nil {
		query += ` AND status=?`
		args = append(args, *status)
	}
	query += ` ORDER BY item_key,id`
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询配置项: %w", err)
	}
	defer rows.Close()
	items := []Item{}
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (SQLRepository) GetItem(ctx context.Context, q database.Querier, namespaceID, itemID string) (Item, error) {
	return getItem(ctx, q, `SELECT id,namespace_id,item_key,display_name,description,value_type,constraints,status,created_by,created_at,updated_at FROM config_items WHERE id=? AND namespace_id=?`, itemID, namespaceID)
}

func (SQLRepository) LockItem(ctx context.Context, q database.Querier, namespaceID, itemID string) (Item, error) {
	return getItem(ctx, q, `SELECT id,namespace_id,item_key,display_name,description,value_type,constraints,status,created_by,created_at,updated_at FROM config_items WHERE id=? AND namespace_id=? FOR UPDATE`, itemID, namespaceID)
}

func getItem(ctx context.Context, q database.Querier, query, itemID, namespaceID string) (Item, error) {
	item, err := scanItem(q.QueryRowContext(ctx, query, itemID, namespaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return item, notFound("配置项")
	}
	if err != nil {
		return item, fmt.Errorf("查询配置项: %w", err)
	}
	return item, nil
}

type rowScanner interface{ Scan(...any) error }

func scanItem(row rowScanner) (Item, error) {
	var item Item
	var constraints []byte
	if err := row.Scan(&item.ID, &item.NamespaceID, &item.Key, &item.DisplayName, &item.Description, &item.ValueType, &constraints, &item.Status, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt); err != nil {
		return item, err
	}
	if err := json.Unmarshal(constraints, &item.Constraints); err != nil {
		return item, fmt.Errorf("解析配置项约束: %w", err)
	}
	item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
	return item, nil
}

func (SQLRepository) CreateItem(ctx context.Context, q database.Querier, item Item) error {
	constraints, err := json.Marshal(item.Constraints)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO config_items(id,namespace_id,item_key,display_name,description,value_type,constraints,status,created_by,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, item.ID, item.NamespaceID, item.Key, item.DisplayName, item.Description, item.ValueType, constraints, item.Status, item.CreatedBy, item.CreatedAt, item.UpdatedAt)
	return duplicateOrWrap(err, "创建配置项")
}

func (SQLRepository) UpdateItem(ctx context.Context, q database.Querier, item Item) error {
	constraints, err := json.Marshal(item.Constraints)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `UPDATE config_items SET display_name=?,description=?,value_type=?,constraints=?,status=?,updated_at=? WHERE id=? AND namespace_id=?`, item.DisplayName, item.Description, item.ValueType, constraints, item.Status, item.UpdatedAt, item.ID, item.NamespaceID)
	return duplicateOrWrap(err, "更新配置项")
}

func (SQLRepository) HasPublishedItems(ctx context.Context, q database.Querier, namespaceID string) (bool, error) {
	var value int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM config_published_pointers WHERE namespace_id=? LIMIT 1 FOR SHARE`, namespaceID).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func (r SQLRepository) GetItemValue(ctx context.Context, q database.Querier, namespaceID, itemID string) (ItemValue, error) {
	item, err := r.GetItem(ctx, q, namespaceID, itemID)
	if err != nil {
		return ItemValue{}, err
	}
	var draftID string
	var published sql.NullString
	err = q.QueryRowContext(ctx, `SELECT d.revision_id,p.revision_id FROM config_draft_pointers d LEFT JOIN config_published_pointers p ON p.item_id=d.item_id WHERE d.item_id=? AND d.namespace_id=?`, itemID, namespaceID).Scan(&draftID, &published)
	if errors.Is(err, sql.ErrNoRows) {
		return ItemValue{Item: item}, nil
	}
	if err != nil {
		return ItemValue{}, fmt.Errorf("查询配置指针: %w", err)
	}
	draft, err := r.getRevision(ctx, q, namespaceID, itemID, draftID, false)
	if err != nil {
		return ItemValue{}, err
	}
	result := ItemValue{Item: item, CurrentDraftRevision: draft}
	if published.Valid {
		result.CurrentPublishedRevisionID = &published.String
		value, err := r.getRevision(ctx, q, namespaceID, itemID, published.String, false)
		if err != nil {
			return ItemValue{}, err
		}
		result.CurrentPublishedRevision = &value
	}
	return result, nil
}

func (SQLRepository) CreateRevision(ctx context.Context, q database.Querier, revision Revision) error {
	constraints, err := json.Marshal(revision.Constraints)
	if err != nil {
		return err
	}
	_, err = q.ExecContext(ctx, `INSERT INTO config_revisions(id,item_id,namespace_id,revision_number,value_type,constraints,value,workflow_status,created_by,created_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, revision.ID, revision.ItemID, revision.NamespaceID, revision.Number, revision.ValueType, constraints, []byte(revision.Value), WorkflowDraft, revision.CreatedBy, revision.CreatedAt)
	return duplicateOrWrap(err, "创建配置 Revision")
}

func (SQLRepository) SetDraftPointer(ctx context.Context, q database.Querier, namespaceID, itemID, revisionID string) error {
	_, err := q.ExecContext(ctx, `INSERT INTO config_draft_pointers(item_id,namespace_id,revision_id) VALUES(?,?,?) ON DUPLICATE KEY UPDATE revision_id=VALUES(revision_id)`, itemID, namespaceID, revisionID)
	return err
}

func (SQLRepository) InsertAssetReferences(ctx context.Context, q database.Querier, values []AssetReference) error {
	for _, value := range values {
		if _, err := q.ExecContext(ctx, `INSERT INTO config_asset_references(revision_id,item_id,namespace_id,asset_id,position) VALUES(?,?,?,?,?)`, value.RevisionID, value.ItemID, value.NamespaceID, value.AssetID, value.Position); err != nil {
			return fmt.Errorf("创建配置素材投影: %w", err)
		}
	}
	return nil
}

func (SQLRepository) InsertRelations(ctx context.Context, q database.Querier, values []Relation) error {
	for _, value := range values {
		if _, err := q.ExecContext(ctx, `INSERT INTO config_content_relations(revision_id,item_id,namespace_id,target_entry_id,target_model_id,position) VALUES(?,?,?,?,?,?)`, value.RevisionID, value.ItemID, value.NamespaceID, value.TargetEntryID, value.TargetModelID, value.Position); err != nil {
			return fmt.Errorf("创建配置关系投影: %w", err)
		}
	}
	return nil
}

func (SQLRepository) ValidateAssetsAvailable(ctx context.Context, q database.Querier, ids []string, constraints Constraints) error {
	ids = uniqueSorted(ids)
	for _, id := range ids {
		var status, mimeType string
		var size int64
		err := q.QueryRowContext(ctx, `SELECT status,mime_type,size FROM assets WHERE id=? FOR UPDATE`, id).Scan(&status, &mimeType, &size)
		if errors.Is(err, sql.ErrNoRows) || err == nil && status != "available" {
			return conflict("asset_not_available", "素材不存在或不可用")
		}
		if err != nil {
			return fmt.Errorf("校验配置素材: %w", err)
		}
		if !assetMatchesConstraints(mimeType, size, constraints) {
			return conflict("asset_constraint_mismatch", "素材 MIME 类型或大小不满足配置约束")
		}
	}
	return nil
}

func (SQLRepository) ValidateAssetsPublishable(ctx context.Context, q database.Querier, revisionID string, constraints Constraints) error {
	rows, err := q.QueryContext(ctx, `SELECT a.id,a.status,a.mime_type,a.size FROM config_asset_references r JOIN assets a ON a.id=r.asset_id WHERE r.revision_id=? ORDER BY a.id FOR UPDATE`, revisionID)
	if err != nil {
		return fmt.Errorf("锁定配置素材: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status, mimeType string
		var size int64
		if err := rows.Scan(&id, &status, &mimeType, &size); err != nil {
			return err
		}
		if status != "available" {
			return conflict("asset_not_publishable", "Revision 包含不可发布素材")
		}
		if !assetMatchesConstraints(mimeType, size, constraints) {
			return conflict("asset_constraint_mismatch", "Revision 包含不满足约束的素材")
		}
	}
	return rows.Err()
}

func assetMatchesConstraints(mimeType string, size int64, constraints Constraints) bool {
	if len(constraints.AllowedMimeTypes) > 0 {
		index := sort.SearchStrings(constraints.AllowedMimeTypes, mimeType)
		if index == len(constraints.AllowedMimeTypes) || constraints.AllowedMimeTypes[index] != mimeType {
			return false
		}
	}
	return constraints.MaxSize == nil || size <= *constraints.MaxSize
}

func (SQLRepository) ValidateRelationTargetsActive(ctx context.Context, q database.Querier, values []Relation) error {
	for _, value := range orderedRelations(values) {
		var modelStatus, entryStatus string
		err := q.QueryRowContext(ctx, `SELECT m.status,e.status FROM content_models m JOIN content_entries e ON e.model_id=m.id WHERE m.id=? AND e.id=? FOR UPDATE`, value.TargetModelID, value.TargetEntryID).Scan(&modelStatus, &entryStatus)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (modelStatus != "active" || entryStatus == "archived") {
			return conflict("relation_target_invalid", "关系目标模型或内容不存在或已归档")
		}
		if err != nil {
			return fmt.Errorf("校验配置关系目标: %w", err)
		}
	}
	return nil
}

func (SQLRepository) ValidateRelationTargetsPublished(ctx context.Context, q database.Querier, revisionID string) error {
	rows, err := q.QueryContext(ctx, `SELECT r.target_model_id,r.target_entry_id,m.status,e.status,p.revision_id FROM config_content_relations r JOIN content_models m ON m.id=r.target_model_id JOIN content_entries e ON e.id=r.target_entry_id AND e.model_id=r.target_model_id LEFT JOIN content_published_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id WHERE r.revision_id=? ORDER BY r.target_model_id,r.target_entry_id FOR UPDATE`, revisionID)
	if err != nil {
		return fmt.Errorf("锁定配置关系目标: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var modelID, entryID, modelStatus, entryStatus string
		var published sql.NullString
		if err := rows.Scan(&modelID, &entryID, &modelStatus, &entryStatus, &published); err != nil {
			return err
		}
		if modelStatus != "active" || entryStatus == "archived" || !published.Valid {
			return conflict("relation_target_not_published", "Revision 包含未发布或不可用的关系目标")
		}
	}
	return rows.Err()
}

func (r SQLRepository) LockRevision(ctx context.Context, q database.Querier, namespaceID, itemID, revisionID string) (Revision, error) {
	return r.getRevision(ctx, q, namespaceID, itemID, revisionID, true)
}

func (r SQLRepository) GetRevision(ctx context.Context, q database.Querier, namespaceID, itemID, revisionID string) (Revision, error) {
	return r.getRevision(ctx, q, namespaceID, itemID, revisionID, false)
}

func (r SQLRepository) ListRevisions(ctx context.Context, q database.Querier, namespaceID, itemID string, limit int, before *uint) ([]Revision, error) {
	if _, err := r.GetItem(ctx, q, namespaceID, itemID); err != nil {
		return nil, err
	}
	query := `SELECT id,item_id,namespace_id,revision_number,value_type,constraints,value,workflow_status,created_by,submitted_by,submitted_at,created_at FROM config_revisions WHERE item_id=? AND namespace_id=?`
	args := []any{itemID, namespaceID}
	if before != nil {
		query += ` AND revision_number<?`
		args = append(args, *before)
	}
	query += ` ORDER BY revision_number DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询配置 Revision: %w", err)
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

func (SQLRepository) getRevision(ctx context.Context, q database.Querier, namespaceID, itemID, revisionID string, lock bool) (Revision, error) {
	query := `SELECT id,item_id,namespace_id,revision_number,value_type,constraints,value,workflow_status,created_by,submitted_by,submitted_at,created_at FROM config_revisions WHERE id=? AND item_id=? AND namespace_id=?`
	if lock {
		query += ` FOR UPDATE`
	}
	item, err := scanRevision(q.QueryRowContext(ctx, query, revisionID, itemID, namespaceID))
	if errors.Is(err, sql.ErrNoRows) {
		return item, notFound("配置 Revision")
	}
	if err != nil {
		return item, fmt.Errorf("查询配置 Revision: %w", err)
	}
	return item, nil
}

func scanRevision(row rowScanner) (Revision, error) {
	var item Revision
	var constraints, value []byte
	var submittedBy sql.NullString
	var submittedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.ItemID, &item.NamespaceID, &item.Number, &item.ValueType, &constraints, &value, &item.WorkflowStatus, &item.CreatedBy, &submittedBy, &submittedAt, &item.CreatedAt); err != nil {
		return item, err
	}
	if err := json.Unmarshal(constraints, &item.Constraints); err != nil {
		return item, err
	}
	item.Value = append(json.RawMessage(nil), value...)
	if submittedBy.Valid {
		item.SubmittedBy = &submittedBy.String
	}
	if submittedAt.Valid {
		value := submittedAt.Time.UTC()
		item.SubmittedAt = &value
	}
	item.CreatedAt = item.CreatedAt.UTC()
	return item, nil
}

func (SQLRepository) TransitionRevision(ctx context.Context, q database.Querier, revisionID string, from, to WorkflowStatus, submitter *string, submittedAt *time.Time) (bool, error) {
	result, err := q.ExecContext(ctx, `UPDATE config_revisions SET workflow_status=?,submitted_by=COALESCE(submitted_by,?),submitted_at=COALESCE(submitted_at,?) WHERE id=? AND workflow_status=?`, to, submitter, submittedAt, revisionID, from)
	if err != nil {
		return false, fmt.Errorf("转换配置 Revision 工作流: %w", err)
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (SQLRepository) SetPublishedPointer(ctx context.Context, q database.Querier, namespaceID, itemID, revisionID string, at time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO config_published_pointers(item_id,namespace_id,revision_id,published_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE revision_id=VALUES(revision_id),published_at=VALUES(published_at)`, itemID, namespaceID, revisionID, at)
	return err
}

func (SQLRepository) DeletePublishedPointer(ctx context.Context, q database.Querier, namespaceID, itemID, revisionID string) (bool, error) {
	result, err := q.ExecContext(ctx, `DELETE FROM config_published_pointers WHERE item_id=? AND namespace_id=? AND revision_id=?`, itemID, namespaceID, revisionID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count == 1, err
}

func (SQLRepository) CreateWorkflowEvent(ctx context.Context, q database.Querier, event WorkflowEvent) error {
	_, err := q.ExecContext(ctx, `INSERT INTO config_workflow_events(id,item_id,namespace_id,revision_id,event_type,from_status,to_status,actor_id,reason,occurred_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.ID, event.ItemID, event.NamespaceID, event.RevisionID, event.Type, event.FromStatus, event.ToStatus, event.ActorID, event.Reason, event.OccurredAt)
	return err
}

func (r SQLRepository) ListWorkflowEvents(ctx context.Context, q database.Querier, namespaceID, itemID string, limit int, cursor *WorkflowEventCursor) ([]WorkflowEvent, error) {
	if _, err := r.GetItem(ctx, q, namespaceID, itemID); err != nil {
		return nil, err
	}
	query := `SELECT id,item_id,namespace_id,revision_id,event_type,from_status,to_status,actor_id,reason,occurred_at FROM config_workflow_events WHERE item_id=? AND namespace_id=?`
	args := []any{itemID, namespaceID}
	if cursor != nil {
		query += ` AND (occurred_at<? OR (occurred_at=? AND id<?))`
		args = append(args, cursor.OccurredAt, cursor.OccurredAt, cursor.ID)
	}
	query += ` ORDER BY occurred_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("查询配置工作流事件: %w", err)
	}
	defer rows.Close()
	items := []WorkflowEvent{}
	for rows.Next() {
		var item WorkflowEvent
		var reason sql.NullString
		if err := rows.Scan(&item.ID, &item.ItemID, &item.NamespaceID, &item.RevisionID, &item.Type, &item.FromStatus, &item.ToStatus, &item.ActorID, &reason, &item.OccurredAt); err != nil {
			return nil, err
		}
		if reason.Valid {
			item.Reason = &reason.String
		}
		item.OccurredAt = item.OccurredAt.UTC()
		items = append(items, item)
	}
	return items, rows.Err()
}

func duplicateOrWrap(err error, operation string) error {
	if err == nil {
		return nil
	}
	var mysqlError *mysql.MySQLError
	if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
		return ErrDuplicateKey
	}
	return fmt.Errorf("%s: %w", operation, err)
}

func uniqueSorted(values []string) []string {
	seen := map[string]bool{}
	for _, value := range values {
		if value != "" {
			seen[value] = true
		}
	}
	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}

func orderedRelations(values []Relation) []Relation {
	result := append([]Relation(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		left := result[i].TargetModelID + "\x00" + result[i].TargetEntryID
		right := result[j].TargetModelID + "\x00" + result[j].TargetEntryID
		return strings.Compare(left, right) < 0
	})
	unique := result[:0]
	for _, value := range result {
		if len(unique) == 0 || unique[len(unique)-1].TargetModelID != value.TargetModelID || unique[len(unique)-1].TargetEntryID != value.TargetEntryID {
			unique = append(unique, value)
		}
	}
	return unique
}
