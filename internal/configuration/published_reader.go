package configuration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"cms/internal/content"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type PublishedReader interface {
	GetPublishedNamespace(context.Context, string, []string, []string) (PublishedNamespace, error)
	GetPublishedItem(context.Context, string, string, []string, []string) (PublishedItem, error)
}

type SQLPublishedReader struct {
	db      database.Querier
	tx      TransactionRunner
	content interface {
		GetPublishedEntryWith(context.Context, database.Querier, string, string, []string, []string) (content.PublishedEntry, error)
	}
}

func NewPublishedReader(db database.Querier, tx TransactionRunner, publishedContent interface {
	GetPublishedEntryWith(context.Context, database.Querier, string, string, []string, []string) (content.PublishedEntry, error)
}) *SQLPublishedReader {
	return &SQLPublishedReader{db: db, tx: tx, content: publishedContent}
}

func (r *SQLPublishedReader) GetPublishedNamespace(ctx context.Context, namespaceKey string, namespaceScope, modelScope []string) (PublishedNamespace, error) {
	var result PublishedNamespace
	err := r.tx.WithinTx(ctx, &sql.TxOptions{ReadOnly: true}, func(q database.Querier) error {
		value, err := r.getPublishedNamespace(ctx, q, namespaceKey, namespaceScope, modelScope)
		result = value
		return err
	})
	return result, err
}

func (r *SQLPublishedReader) getPublishedNamespace(ctx context.Context, q database.Querier, namespaceKey string, namespaceScope, modelScope []string) (PublishedNamespace, error) {
	namespaceID, err := r.activeNamespaceID(ctx, q, namespaceKey)
	if err != nil {
		return PublishedNamespace{}, err
	}
	if !scopeContains(namespaceScope, namespaceID) {
		return PublishedNamespace{}, publishedNotFound()
	}
	rows, err := q.QueryContext(ctx, `SELECT i.item_key,i.value_type,rv.id,rv.revision_number,rv.value,p.published_at FROM config_items i JOIN config_published_pointers p ON p.item_id=i.id AND p.namespace_id=i.namespace_id JOIN config_revisions rv ON rv.id=p.revision_id WHERE i.namespace_id=? AND i.status='active' ORDER BY i.item_key,i.id`, namespaceID)
	if err != nil {
		return PublishedNamespace{}, fmt.Errorf("查询已发布配置: %w", err)
	}
	result := PublishedNamespace{}
	items := []PublishedItem{}
	for rows.Next() {
		item, err := scanPublishedItem(rows)
		if err != nil {
			rows.Close()
			return PublishedNamespace{}, err
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return PublishedNamespace{}, err
	}
	if err := rows.Close(); err != nil {
		return PublishedNamespace{}, err
	}
	for i := range items {
		if err := r.expandValue(ctx, q, &items[i], modelScope); err != nil {
			return PublishedNamespace{}, err
		}
		result[items[i].Key] = items[i].Value
	}
	return result, nil
}

func (r *SQLPublishedReader) GetPublishedItem(ctx context.Context, namespaceKey, itemKey string, namespaceScope, modelScope []string) (PublishedItem, error) {
	var result PublishedItem
	err := r.tx.WithinTx(ctx, &sql.TxOptions{ReadOnly: true}, func(q database.Querier) error {
		value, err := r.getPublishedItem(ctx, q, namespaceKey, itemKey, namespaceScope, modelScope)
		result = value
		return err
	})
	return result, err
}

func (r *SQLPublishedReader) getPublishedItem(ctx context.Context, q database.Querier, namespaceKey, itemKey string, namespaceScope, modelScope []string) (PublishedItem, error) {
	namespaceID, err := r.activeNamespaceID(ctx, q, namespaceKey)
	if err != nil {
		return PublishedItem{}, err
	}
	if !scopeContains(namespaceScope, namespaceID) {
		return PublishedItem{}, publishedNotFound()
	}
	row := q.QueryRowContext(ctx, `SELECT i.item_key,i.value_type,rv.id,rv.revision_number,rv.value,p.published_at FROM config_items i JOIN config_published_pointers p ON p.item_id=i.id AND p.namespace_id=i.namespace_id JOIN config_revisions rv ON rv.id=p.revision_id WHERE i.namespace_id=? AND i.item_key=? AND i.status='active'`, namespaceID, itemKey)
	item, err := scanPublishedItem(row)
	if errors.Is(err, sql.ErrNoRows) {
		return PublishedItem{}, publishedNotFound()
	}
	if err != nil {
		return PublishedItem{}, fmt.Errorf("查询已发布配置项: %w", err)
	}
	if err := r.expandValue(ctx, q, &item, modelScope); err != nil {
		return PublishedItem{}, err
	}
	return item, nil
}

func (r *SQLPublishedReader) activeNamespaceID(ctx context.Context, q database.Querier, key string) (string, error) {
	var id string
	err := q.QueryRowContext(ctx, `SELECT id FROM config_namespaces WHERE namespace_key=? AND status='active'`, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", publishedNotFound()
	}
	if err != nil {
		return "", fmt.Errorf("查询已发布配置 namespace: %w", err)
	}
	return id, nil
}

type publishedItemScanner interface{ Scan(...any) error }

func scanPublishedItem(row publishedItemScanner) (PublishedItem, error) {
	var item PublishedItem
	var raw []byte
	if err := row.Scan(&item.Key, &item.ValueType, &item.RevisionID, &item.RevisionNumber, &raw, &item.PublishedAt); err != nil {
		return item, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&item.Value); err != nil {
		return item, fmt.Errorf("解析已发布配置值: %w", err)
	}
	item.PublishedAt = item.PublishedAt.UTC()
	return item, nil
}

func (r *SQLPublishedReader) expandValue(ctx context.Context, q database.Querier, item *PublishedItem, modelScope []string) error {
	if item.Value == nil {
		return nil
	}
	expected, err := valueIdentifierCount(item.Value, item.ValueType)
	if err != nil {
		return fmt.Errorf("解析已发布配置投影基线: %w", err)
	}
	switch item.ValueType {
	case TypeSingleAsset, TypeMultiAsset:
		assets, err := r.publishedAssets(ctx, q, item.RevisionID)
		if err != nil {
			return err
		}
		if len(assets) != expected {
			return publishedNotFound()
		}
		if item.ValueType == TypeSingleAsset {
			item.Value = assets[0]
		} else {
			item.Value = assets
		}
	case TypeSingleRelation, TypeMultiRelation:
		relations, err := r.publishedRelations(ctx, q, item.RevisionID, modelScope)
		if err != nil {
			return err
		}
		if len(relations) != expected {
			return publishedNotFound()
		}
		if item.ValueType == TypeSingleRelation {
			item.Value = relations[0]
		} else {
			item.Value = relations
		}
	}
	return nil
}

func valueIdentifierCount(value any, valueType ValueType) (int, error) {
	switch valueType {
	case TypeSingleAsset, TypeSingleRelation:
		if _, ok := value.(string); !ok {
			return 0, errors.New("单值投影不是 ID")
		}
		return 1, nil
	case TypeMultiAsset, TypeMultiRelation:
		values, ok := value.([]any)
		if !ok {
			return 0, errors.New("多值投影不是 ID 数组")
		}
		return len(values), nil
	default:
		return 0, nil
	}
}

func (r *SQLPublishedReader) publishedAssets(ctx context.Context, q database.Querier, revisionID string) ([]PublishedAsset, error) {
	rows, err := q.QueryContext(ctx, `SELECT a.id,a.object_key,a.filename,a.mime_type,a.size,a.sha256,a.etag FROM config_asset_references r JOIN assets a ON a.id=r.asset_id AND a.status IN ('available','archived') WHERE r.revision_id=? ORDER BY r.position`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("查询已发布配置素材: %w", err)
	}
	defer rows.Close()
	items := []PublishedAsset{}
	for rows.Next() {
		var item PublishedAsset
		if err := rows.Scan(&item.ID, &item.ObjectKey, &item.Filename, &item.MimeType, &item.Size, &item.SHA256, &item.ETag); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (r *SQLPublishedReader) publishedRelations(ctx context.Context, q database.Querier, revisionID string, modelScope []string) ([]content.PublishedEntry, error) {
	rows, err := q.QueryContext(ctx, `SELECT r.target_entry_id,r.target_model_id,m.model_key FROM config_content_relations r JOIN content_models m ON m.id=r.target_model_id AND m.status='active' JOIN content_entries e ON e.id=r.target_entry_id AND e.model_id=r.target_model_id AND e.status<>'archived' JOIN content_published_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id WHERE r.revision_id=? ORDER BY r.position`, revisionID)
	if err != nil {
		return nil, fmt.Errorf("查询已发布配置关系: %w", err)
	}
	type target struct{ entryID, modelID, modelKey string }
	targets := []target{}
	for rows.Next() {
		var item target
		if err := rows.Scan(&item.entryID, &item.modelID, &item.modelKey); err != nil {
			rows.Close()
			return nil, err
		}
		targets = append(targets, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if r.content == nil && len(targets) > 0 {
		return nil, fmt.Errorf("已发布内容读取器未装配")
	}
	items := make([]content.PublishedEntry, 0, len(targets))
	for _, target := range targets {
		if !scopeContains(modelScope, target.modelID) {
			return nil, publishedNotFound()
		}
		entry, err := r.content.GetPublishedEntryWith(ctx, q, target.modelKey, target.entryID, modelScope, nil)
		if err != nil {
			var applicationError *apperror.Error
			if errors.As(err, &applicationError) && applicationError.Kind == apperror.KindNotFound {
				return nil, publishedNotFound()
			}
			return nil, err
		}
		items = append(items, entry)
	}
	return items, nil
}

func scopeContains(scope []string, value string) bool {
	index := sort.SearchStrings(scope, value)
	if index < len(scope) && scope[index] == value {
		return true
	}
	for _, item := range scope {
		if item == value {
			return true
		}
	}
	return false
}
