package content

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"time"

	"cms/internal/platform/database"
	"cms/internal/schema"
)

type SQLPublishedContentReader struct {
	db     database.Querier
	models schema.Repository
	assets PublishedAssetResolver
}

func NewPublishedContentReader(db database.Querier, models schema.Repository, assets PublishedAssetResolver) *SQLPublishedContentReader {
	return &SQLPublishedContentReader{db: db, models: models, assets: assets}
}

var _ PublishedContentReader = (*SQLPublishedContentReader)(nil)

func (r *SQLPublishedContentReader) ListPublishedModels(ctx context.Context, allowedModelIDs []string) ([]PublishedModel, error) {
	ids := normalizedScope(allowedModelIDs)
	if len(ids) == 0 {
		return []PublishedModel{}, nil
	}
	query, args := scopeQuery(`SELECT id FROM content_models WHERE status='active' AND id IN (`, ids, `) ORDER BY model_key ASC,id ASC`)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, publishedStorageError(ctx, "查询已发布模型", err)
	}
	defer rows.Close()
	items := []PublishedModel{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		model, err := r.models.GetModel(ctx, r.db, id)
		if err != nil {
			return nil, err
		}
		published, err := r.publishedModel(ctx, model)
		if err != nil {
			return nil, err
		}
		items = append(items, published)
	}
	return items, rows.Err()
}

func (r *SQLPublishedContentReader) GetPublishedModel(ctx context.Context, modelKey string, allowedModelIDs []string) (PublishedModel, error) {
	ids := normalizedScope(allowedModelIDs)
	if len(ids) == 0 {
		return PublishedModel{}, publishedNotFound("模型")
	}
	query, args := scopeQuery(`SELECT id FROM content_models WHERE status='active' AND model_key=? AND id IN (`, ids, `)`)
	args = append([]any{modelKey}, args...)
	var id string
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&id); errors.Is(err, sql.ErrNoRows) {
		return PublishedModel{}, publishedNotFound("模型")
	} else if err != nil {
		return PublishedModel{}, publishedStorageError(ctx, "查询已发布模型", err)
	}
	model, err := r.models.GetModel(ctx, r.db, id)
	if err != nil {
		return PublishedModel{}, err
	}
	return r.publishedModel(ctx, model)
}

func (r *SQLPublishedContentReader) publishedModel(ctx context.Context, model schema.ContentModel) (PublishedModel, error) {
	targetKeys := map[string]string{}
	var convert func([]schema.ContentField) ([]PublishedField, error)
	convert = func(fields []schema.ContentField) ([]PublishedField, error) {
		result := []PublishedField{}
		for _, field := range fields {
			if field.Status != schema.StatusActive {
				continue
			}
			constraints := PublishedFieldConstraints{MinLength: field.Constraints.MinLength, MaxLength: field.Constraints.MaxLength, Minimum: field.Constraints.Minimum, Maximum: field.Constraints.Maximum, EnumOptions: field.Constraints.EnumOptions, Unique: field.Constraints.Unique, Filterable: field.Constraints.Filterable, Sortable: field.Constraints.Sortable}
			if field.Constraints.TargetModelID != nil {
				key, ok := targetKeys[*field.Constraints.TargetModelID]
				if !ok {
					target, err := r.models.GetModel(ctx, r.db, *field.Constraints.TargetModelID)
					if err != nil {
						return nil, err
					}
					key = target.Key
					targetKeys[target.ID] = key
				}
				constraints.TargetModelKey = &key
			}
			children, err := convert(field.Children)
			if err != nil {
				return nil, err
			}
			result = append(result, PublishedField{ID: field.ID, Key: field.Key, DisplayName: field.DisplayName, Description: field.Description, Type: field.Type, Required: field.Required, Constraints: constraints, Children: children})
		}
		return result, nil
	}
	fields, err := convert(model.Fields)
	if err != nil {
		return PublishedModel{}, err
	}
	return PublishedModel{ID: model.ID, Key: model.Key, DisplayName: model.DisplayName, Description: model.Description, UpdatedAt: model.UpdatedAt, Fields: fields}, nil
}

func (r *SQLPublishedContentReader) GetPublishedEntry(ctx context.Context, modelKey, entryID string, allowedModelIDs []string, expand []string) (PublishedEntry, error) {
	publishedModel, err := r.GetPublishedModel(ctx, modelKey, allowedModelIDs)
	if err != nil {
		return PublishedEntry{}, err
	}
	model, err := r.models.GetModel(ctx, r.db, publishedModel.ID)
	if err != nil {
		return PublishedEntry{}, err
	}
	fields, err := validatePublishedQuery(model.Fields, PublishedQuery{Limit: 1, Expand: expand})
	if err != nil {
		return PublishedEntry{}, err
	}
	_ = fields
	entry, err := r.getPublishedEntry(ctx, publishedModel, entryID)
	if err != nil {
		return PublishedEntry{}, err
	}
	if entry.Content, err = activeRootContent(entry.Content, model.Fields); err != nil {
		return PublishedEntry{}, publishedStorageError(ctx, "裁剪已发布内容", err)
	}
	if err = r.expandEntries(ctx, []*PublishedEntry{&entry}, model.Fields, expand, normalizedScope(allowedModelIDs)); err != nil {
		return PublishedEntry{}, err
	}
	if err = r.resolvePublishedAssets(ctx, []*PublishedEntry{&entry}, map[string][]schema.ContentField{model.ID: model.Fields}); err != nil {
		return PublishedEntry{}, err
	}
	return entry, nil
}

func (r *SQLPublishedContentReader) getPublishedEntry(ctx context.Context, model PublishedModel, entryID string) (PublishedEntry, error) {
	row := r.db.QueryRowContext(ctx, `SELECT e.id,e.model_id,m.model_key,rv.id,rv.revision_number,rv.content,p.published_at,rv.created_at FROM content_published_pointers p JOIN content_entries e ON e.id=p.entry_id AND e.model_id=p.model_id AND e.status<>'archived' JOIN content_revisions rv ON rv.id=p.revision_id JOIN content_models m ON m.id=e.model_id AND m.status='active' WHERE e.id=? AND e.model_id=?`, entryID, model.ID)
	entry, err := scanPublishedEntry(row)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, publishedNotFound("内容")
	}
	if err != nil {
		return entry, publishedStorageError(ctx, "查询已发布内容", err)
	}
	return entry, nil
}

func (r *SQLPublishedContentReader) ListPublishedEntries(ctx context.Context, modelKey string, allowedModelIDs []string, query PublishedQuery) (PublishedEntryPage, error) {
	publishedModel, err := r.GetPublishedModel(ctx, modelKey, allowedModelIDs)
	if err != nil {
		return PublishedEntryPage{}, err
	}
	model, err := r.models.GetModel(ctx, r.db, publishedModel.ID)
	if err != nil {
		return PublishedEntryPage{}, err
	}
	fields, err := validatePublishedQuery(model.Fields, query)
	if err != nil {
		return PublishedEntryPage{}, err
	}
	if query.Limit == 0 {
		query.Limit = 20
	}
	binding, err := publishedQueryBinding(model.ID, allowedModelIDs, query)
	if err != nil {
		return PublishedEntryPage{}, err
	}
	cursor, err := decodePublishedCursor(query.Cursor, binding)
	if err != nil {
		return PublishedEntryPage{}, err
	}
	selectSQL := `SELECT e.id,e.model_id,m.model_key,rv.id,rv.revision_number,rv.content,p.published_at,rv.created_at`
	sqlQuery := ` FROM content_published_pointers p JOIN content_entries e ON e.id=p.entry_id AND e.model_id=p.model_id AND e.status<>'archived' JOIN content_revisions rv ON rv.id=p.revision_id JOIN content_models m ON m.id=e.model_id AND m.status='active'`
	joinArgs := []any{}
	whereArgs := []any{}
	orders := []publishedOrder{{Expression: "p.published_at", Descending: true}, {Expression: "e.id", Descending: true}}
	if len(query.Sort) > 0 {
		orders = []publishedOrder{}
		for i, item := range query.Sort {
			expression := ""
			if item.FieldKey == "published_at" {
				expression = "p.published_at"
			} else if item.FieldKey == "id" {
				expression = "e.id"
			} else {
				field := fields[item.FieldKey]
				alias := fmt.Sprintf("s%d", i)
				sqlQuery += ` LEFT JOIN content_field_values ` + alias + ` ON ` + alias + `.revision_id=rv.id AND ` + alias + `.field_id=?`
				joinArgs = append(joinArgs, field.ID)
				expression = alias + "." + projectionColumn(field.Type)
			}
			orders = append(orders, publishedOrder{Expression: expression, Descending: item.Descending})
		}
		if query.Sort[len(query.Sort)-1].FieldKey != "id" {
			orders = append(orders, publishedOrder{Expression: "e.id", Descending: query.Sort[len(query.Sort)-1].Descending})
		}
	}
	for _, order := range orders {
		selectSQL += `,` + order.Expression
	}
	where := []string{`e.model_id=?`}
	whereArgs = append(whereArgs, model.ID)
	for i, filter := range query.Filters {
		field := fields[filter.FieldKey]
		alias := fmt.Sprintf("f%d", i)
		sqlQuery += ` JOIN content_field_values ` + alias + ` ON ` + alias + `.revision_id=rv.id AND ` + alias + `.field_id=?`
		joinArgs = append(joinArgs, field.ID)
		clause, values, ok := projectionPredicate(alias, field, filter)
		if !ok {
			return PublishedEntryPage{}, invalidQuery()
		}
		where = append(where, clause)
		whereArgs = append(whereArgs, values...)
	}
	for i, filter := range query.RelationFilters {
		field := fields[filter.FieldKey]
		alias := fmt.Sprintf("r%d", i)
		sqlQuery += ` JOIN content_relations ` + alias + ` ON ` + alias + `.revision_id=rv.id AND ` + alias + `.field_id=? AND ` + alias + `.target_entry_id=?`
		joinArgs = append(joinArgs, field.ID, filter.EntryID)
	}
	if cursor != nil {
		clause, values, ok := publishedCursorPredicate(orders, cursor.Values)
		if !ok {
			return PublishedEntryPage{}, invalidCursor()
		}
		where = append(where, clause)
		whereArgs = append(whereArgs, values...)
	}
	directions := make([]string, len(orders))
	for i, order := range orders {
		direction := " ASC"
		if order.Descending {
			direction = " DESC"
		}
		directions[i] = order.Expression + direction
	}
	sqlQuery = selectSQL + sqlQuery + ` WHERE ` + strings.Join(where, " AND ") + ` ORDER BY ` + strings.Join(directions, ",") + ` LIMIT ?`
	args := append(joinArgs, whereArgs...)
	args = append(args, query.Limit+1)
	rows, err := r.db.QueryContext(ctx, sqlQuery, args...)
	if err != nil {
		return PublishedEntryPage{}, publishedStorageError(ctx, "查询已发布内容列表", err)
	}
	defer rows.Close()
	items := []PublishedEntry{}
	orderedValues := [][]*string{}
	for rows.Next() {
		item, values, err := scanPublishedEntryWithOrder(rows, len(orders))
		if err != nil {
			return PublishedEntryPage{}, err
		}
		if item.Content, err = activeRootContent(item.Content, model.Fields); err != nil {
			return PublishedEntryPage{}, publishedStorageError(ctx, "裁剪已发布内容", err)
		}
		items = append(items, item)
		orderedValues = append(orderedValues, values)
	}
	if err = rows.Err(); err != nil {
		return PublishedEntryPage{}, err
	}
	result := PublishedEntryPage{Items: items}
	if len(items) > query.Limit {
		result.Items = items[:query.Limit]
		value, _ := encodePublishedCursor(publishedCursor{Binding: binding, Values: orderedValues[query.Limit-1]})
		result.NextCursor = &value
	}
	pointers := make([]*PublishedEntry, len(result.Items))
	for i := range result.Items {
		pointers[i] = &result.Items[i]
	}
	if err = r.expandEntries(ctx, pointers, model.Fields, query.Expand, normalizedScope(allowedModelIDs)); err != nil {
		return PublishedEntryPage{}, err
	}
	if err = r.resolvePublishedAssets(ctx, pointers, map[string][]schema.ContentField{model.ID: model.Fields}); err != nil {
		return PublishedEntryPage{}, err
	}
	return result, nil
}

func (r *SQLPublishedContentReader) resolvePublishedAssets(ctx context.Context, entries []*PublishedEntry, fieldsByModel map[string][]schema.ContentField) error {
	type target struct {
		revisionID string
		modelID    string
		content    json.RawMessage
		assign     func(map[string]PublishedReferencedAsset)
	}
	targets := []target{}
	for _, entry := range entries {
		entry.ReferencedAssets = map[string]PublishedReferencedAsset{}
		targets = append(targets, target{entry.RevisionID, entry.ModelID, entry.Content, func(values map[string]PublishedReferencedAsset) { entry.ReferencedAssets = values }})
		for key, value := range entry.Expanded {
			switch expanded := value.(type) {
			case ExpandedEntry:
				item := expanded
				targets = append(targets, target{item.RevisionID, item.ModelID, item.Content, func(values map[string]PublishedReferencedAsset) {
					item.ReferencedAssets = values
					entry.Expanded[key] = item
				}})
			case []ExpandedEntry:
				items := append([]ExpandedEntry(nil), expanded...)
				for i := range items {
					index := i
					item := items[i]
					targets = append(targets, target{item.RevisionID, item.ModelID, item.Content, func(values map[string]PublishedReferencedAsset) {
						items[index].ReferencedAssets = values
						entry.Expanded[key] = items
					}})
				}
			}
		}
	}
	for _, item := range targets {
		item.assign(map[string]PublishedReferencedAsset{})
	}
	if r.assets == nil {
		return nil
	}
	for _, item := range targets {
		if _, ok := fieldsByModel[item.modelID]; ok {
			continue
		}
		model, err := r.models.GetModel(ctx, r.db, item.modelID)
		if err != nil {
			return err
		}
		fieldsByModel[item.modelID] = model.Fields
	}
	revisionIDs := make([]string, 0, len(targets))
	seen := map[string]struct{}{}
	for _, item := range targets {
		if _, ok := seen[item.revisionID]; !ok {
			seen[item.revisionID] = struct{}{}
			revisionIDs = append(revisionIDs, item.revisionID)
		}
	}
	resolved, err := r.assets.ResolvePublishedAssets(ctx, revisionIDs)
	if err != nil {
		return publishedStorageError(ctx, "批量查询发布引用素材", err)
	}
	for _, item := range targets {
		assetIDs, err := publishedAssetIDs(item.content, fieldsByModel[item.modelID])
		if err != nil {
			return publishedStorageError(ctx, "解析发布引用素材", err)
		}
		values := map[string]PublishedReferencedAsset{}
		for _, assetID := range assetIDs {
			if value, ok := resolved[item.revisionID][assetID]; ok {
				values[assetID] = value
			}
		}
		item.assign(values)
	}
	return nil
}

func publishedAssetIDs(content json.RawMessage, fields []schema.ContentField) ([]string, error) {
	var object map[string]any
	if err := json.Unmarshal(content, &object); err != nil {
		return nil, err
	}
	ids := []string{}
	var walk func(map[string]any, []schema.ContentField)
	walk = func(value map[string]any, fields []schema.ContentField) {
		for _, field := range fields {
			item, exists := value[field.Key]
			if !exists || item == nil || field.Status != schema.StatusActive {
				continue
			}
			switch field.Type {
			case schema.FieldTypeSingleMedia:
				if id, ok := item.(string); ok {
					ids = append(ids, id)
				}
			case schema.FieldTypeMultiMedia:
				if items, ok := item.([]any); ok {
					for _, value := range items {
						if id, ok := value.(string); ok {
							ids = append(ids, id)
						}
					}
				}
			case schema.FieldTypeObject:
				if child, ok := item.(map[string]any); ok {
					walk(child, field.Children)
				}
			case schema.FieldTypeRepeatableGroup:
				if groups, ok := item.([]any); ok {
					for _, group := range groups {
						if child, ok := group.(map[string]any); ok {
							walk(child, field.Children)
						}
					}
				}
			}
		}
	}
	walk(object, fields)
	return ids, nil
}

// ExplainPublishedEntries 返回代表性发布列表查询，便于在真实数据集上检查索引计划。
func ExplainPublishedEntries(modelID, fieldID string, limit int) (string, []any) {
	if limit < 1 || limit > 100 {
		limit = 20
	}
	return `EXPLAIN SELECT e.id FROM content_published_pointers p JOIN content_entries e ON e.id=p.entry_id AND e.model_id=p.model_id JOIN content_revisions rv ON rv.id=p.revision_id JOIN content_field_values fv ON fv.revision_id=rv.id AND fv.field_id=? WHERE e.model_id=? AND e.status<>'archived' AND fv.integer_value>=? ORDER BY p.published_at DESC,e.id DESC LIMIT ?`, []any{fieldID, modelID, 0, limit}
}

type publishedScanner interface{ Scan(...any) error }

func scanPublishedEntry(row publishedScanner) (PublishedEntry, error) {
	var item PublishedEntry
	var raw []byte
	if err := row.Scan(&item.ID, &item.ModelID, &item.ModelKey, &item.RevisionID, &item.RevisionNumber, &raw, &item.PublishedAt, &item.UpdatedAt); err != nil {
		return item, err
	}
	item.Content = append(json.RawMessage(nil), raw...)
	item.Expanded = map[string]any{}
	item.PublishedAt = item.PublishedAt.UTC()
	item.UpdatedAt = item.UpdatedAt.UTC()
	return item, nil
}

func scanPublishedEntryWithOrder(row publishedScanner, count int) (PublishedEntry, []*string, error) {
	var item PublishedEntry
	var raw []byte
	values := make([]sql.RawBytes, count)
	dest := []any{&item.ID, &item.ModelID, &item.ModelKey, &item.RevisionID, &item.RevisionNumber, &raw, &item.PublishedAt, &item.UpdatedAt}
	for i := range values {
		dest = append(dest, &values[i])
	}
	if err := row.Scan(dest...); err != nil {
		return item, nil, err
	}
	item.Content = append(json.RawMessage(nil), raw...)
	item.Expanded = map[string]any{}
	item.PublishedAt = item.PublishedAt.UTC()
	item.UpdatedAt = item.UpdatedAt.UTC()
	result := make([]*string, count)
	for i, value := range values {
		if value != nil {
			text := string(value)
			result[i] = &text
		}
	}
	return item, result, nil
}

func (r *SQLPublishedContentReader) expandEntries(ctx context.Context, entries []*PublishedEntry, fields []schema.ContentField, expand, scope []string) error {
	if len(entries) == 0 || len(expand) == 0 || len(scope) == 0 {
		return nil
	}
	byKey := map[string]schema.ContentField{}
	for _, field := range fields {
		byKey[field.Key] = field
	}
	revisionOwners := map[string]*PublishedEntry{}
	revisionIDs := []string{}
	for _, entry := range entries {
		revisionOwners[entry.RevisionID] = entry
		revisionIDs = append(revisionIDs, entry.RevisionID)
	}
	fieldIDs := []string{}
	fieldKeys := map[string]string{}
	modelFields := map[string][]schema.ContentField{}
	for _, key := range expand {
		field := byKey[key]
		fieldIDs = append(fieldIDs, field.ID)
		fieldKeys[field.ID] = key
		targetModelID := *field.Constraints.TargetModelID
		if _, ok := modelFields[targetModelID]; !ok {
			targetModel, err := r.models.GetModel(ctx, r.db, targetModelID)
			if err != nil {
				return err
			}
			modelFields[targetModelID] = targetModel.Fields
		}
	}
	query, args := inQuery(`SELECT rel.revision_id,rel.field_id,rel.position,e.id,e.model_id,m.model_key,rv.id,rv.revision_number,rv.content,p.published_at,rv.created_at FROM content_relations rel JOIN content_published_pointers p ON p.entry_id=rel.target_entry_id AND p.model_id=rel.target_model_id JOIN content_entries e ON e.id=p.entry_id AND e.status<>'archived' JOIN content_models m ON m.id=e.model_id AND m.status='active' JOIN content_revisions rv ON rv.id=p.revision_id WHERE rel.revision_id IN (`, revisionIDs, `) AND rel.field_id IN (`, fieldIDs, `) AND e.model_id IN (`, scope, `) ORDER BY rel.revision_id,rel.field_id,rel.position`)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return publishedStorageError(ctx, "批量展开已发布关联", err)
	}
	defer rows.Close()
	for rows.Next() {
		var sourceRevision, fieldID string
		var position int
		var target ExpandedEntry
		var revisionNumber uint
		var raw []byte
		if err := rows.Scan(&sourceRevision, &fieldID, &position, &target.ID, &target.ModelID, &target.ModelKey, &target.RevisionID, &revisionNumber, &raw, &target.PublishedAt, &target.UpdatedAt); err != nil {
			return err
		}
		targetFields, ok := modelFields[target.ModelID]
		if !ok {
			targetModel, modelErr := r.models.GetModel(ctx, r.db, target.ModelID)
			if modelErr != nil {
				return modelErr
			}
			targetFields = targetModel.Fields
			modelFields[target.ModelID] = targetFields
		}
		target.Content, err = activeRootContent(raw, targetFields)
		if err != nil {
			return publishedStorageError(ctx, "裁剪展开内容", err)
		}
		target.PublishedAt = target.PublishedAt.UTC()
		target.UpdatedAt = target.UpdatedAt.UTC()
		owner := revisionOwners[sourceRevision]
		key := fieldKeys[fieldID]
		field := byKey[key]
		if field.Type == schema.FieldTypeSingleRelation {
			owner.Expanded[key] = target
		} else {
			items, _ := owner.Expanded[key].([]ExpandedEntry)
			owner.Expanded[key] = append(items, target)
		}
	}
	return rows.Err()
}

func activeRootContent(content json.RawMessage, fields []schema.ContentField) (json.RawMessage, error) {
	var source map[string]json.RawMessage
	if err := json.Unmarshal(content, &source); err != nil {
		return nil, err
	}
	trimmed := make(map[string]json.RawMessage)
	for _, field := range fields {
		if field.Status == schema.StatusActive {
			if value, ok := source[field.Key]; ok {
				projected, err := activeFieldValue(value, field)
				if err != nil {
					return nil, err
				}
				trimmed[field.Key] = projected
			}
		}
	}
	return json.Marshal(trimmed)
}

func activeFieldValue(value json.RawMessage, field schema.ContentField) (json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return value, nil
	}
	switch field.Type {
	case schema.FieldTypeObject:
		return activeObjectValue(value, field.Children)
	case schema.FieldTypeRepeatableGroup:
		var groups []json.RawMessage
		if err := json.Unmarshal(value, &groups); err != nil {
			return nil, err
		}
		projected := make([]json.RawMessage, len(groups))
		for i, group := range groups {
			item, err := activeObjectValue(group, field.Children)
			if err != nil {
				return nil, err
			}
			projected[i] = item
		}
		return json.Marshal(projected)
	default:
		return value, nil
	}
}

func activeObjectValue(value json.RawMessage, fields []schema.ContentField) (json.RawMessage, error) {
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return value, nil
	}
	var source map[string]json.RawMessage
	if err := json.Unmarshal(value, &source); err != nil {
		return nil, err
	}
	projected := make(map[string]json.RawMessage)
	for _, field := range fields {
		if field.Status != schema.StatusActive {
			continue
		}
		item, ok := source[field.Key]
		if !ok {
			continue
		}
		value, err := activeFieldValue(item, field)
		if err != nil {
			return nil, err
		}
		projected[field.Key] = value
	}
	return json.Marshal(projected)
}

func validatePublishedQuery(fields []schema.ContentField, query PublishedQuery) (map[string]schema.ContentField, error) {
	if query.Limit < 0 || query.Limit > 100 || len(query.Filters) > 5 || len(query.RelationFilters) > 2 || len(query.Sort) > 3 || len(query.Expand) > 3 {
		return nil, invalidQuery()
	}
	byKey := map[string]schema.ContentField{}
	for _, field := range fields {
		if field.Status == schema.StatusActive {
			byKey[field.Key] = field
		}
	}
	for _, filter := range query.Filters {
		field, ok := byKey[filter.FieldKey]
		if !ok || !field.Constraints.Filterable || !validFilterOperator(field.Type, filter.Operator) {
			return nil, invalidQuery()
		}
		var value any
		decoder := json.NewDecoder(strings.NewReader(string(filter.Value)))
		decoder.UseNumber()
		if decoder.Decode(&value) != nil {
			return nil, invalidQuery()
		}
	}
	for _, filter := range query.RelationFilters {
		field, ok := byKey[filter.FieldKey]
		if !ok || (field.Type != schema.FieldTypeSingleRelation && field.Type != schema.FieldTypeMultiRelation) || filter.EntryID == "" {
			return nil, invalidQuery()
		}
	}
	seen := map[string]bool{}
	for _, key := range query.Expand {
		field, ok := byKey[key]
		if !ok || seen[key] || (field.Type != schema.FieldTypeSingleRelation && field.Type != schema.FieldTypeMultiRelation) || field.Constraints.TargetModelID == nil {
			return nil, invalidQuery()
		}
		seen[key] = true
	}
	seen = map[string]bool{}
	for _, item := range query.Sort {
		if seen[item.FieldKey] {
			return nil, invalidQuery()
		}
		seen[item.FieldKey] = true
		if item.FieldKey == "published_at" || item.FieldKey == "id" {
			continue
		}
		field, ok := byKey[item.FieldKey]
		if !ok || !field.Constraints.Sortable {
			return nil, invalidQuery()
		}
	}
	return byKey, nil
}
func validFilterOperator(t schema.FieldType, op string) bool {
	basic := op == "eq" || op == "ne" || op == "in"
	if basic {
		return true
	}
	numeric := t == schema.FieldTypeInteger || t == schema.FieldTypeDecimal || t == schema.FieldTypeDate || t == schema.FieldTypeDatetime
	return numeric && (op == "gt" || op == "gte" || op == "lt" || op == "lte")
}
func projectionPredicate(alias string, field schema.ContentField, filter PublishedFilter) (string, []any, bool) {
	column := projectionColumn(field.Type)
	if column == "" {
		return "", nil, false
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(filter.Value)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return "", nil, false
	}
	operator := map[string]string{"eq": "=", "ne": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[filter.Operator]
	if filter.Operator == "in" {
		items, ok := value.([]any)
		if !ok || len(items) < 1 || len(items) > 20 {
			return "", nil, false
		}
		args := make([]any, len(items))
		marks := make([]string, len(items))
		seen := map[string]bool{}
		for i, item := range items {
			encoded, _ := json.Marshal(item)
			if seen[string(encoded)] {
				return "", nil, false
			}
			seen[string(encoded)] = true
			argument, valid := projectionArgument(field.Type, item)
			if !valid {
				return "", nil, false
			}
			args[i] = argument
			marks[i] = "?"
		}
		return alias + "." + column + " IN (" + strings.Join(marks, ",") + ")", args, true
	}
	if operator == "" {
		return "", nil, false
	}
	argument, valid := projectionArgument(field.Type, value)
	if !valid {
		return "", nil, false
	}
	return alias + "." + column + operator + "?", []any{argument}, true
}
func projectionColumn(fieldType schema.FieldType) string {
	return map[schema.FieldType]string{schema.FieldTypeSingleLineText: "string_value", schema.FieldTypeMultiLineText: "string_value", schema.FieldTypeSingleSelect: "string_value", schema.FieldTypeInteger: "integer_value", schema.FieldTypeDecimal: "decimal_value", schema.FieldTypeBoolean: "boolean_value", schema.FieldTypeDate: "date_value", schema.FieldTypeDatetime: "datetime_value"}[fieldType]
}
func projectionArgument(t schema.FieldType, value any) (any, bool) {
	switch t {
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText, schema.FieldTypeSingleSelect:
		text, ok := value.(string)
		return text, ok
	case schema.FieldTypeInteger:
		number, ok := value.(json.Number)
		if !ok || !integerPattern.MatchString(number.String()) {
			return nil, false
		}
		integer, ok := new(big.Int).SetString(number.String(), 10)
		return number.String(), ok && integer.IsInt64()
	case schema.FieldTypeDecimal:
		text, ok := value.(string)
		if !ok || !decimalPattern.MatchString(text) {
			return nil, false
		}
		parts := strings.Split(strings.TrimPrefix(text, "-"), ".")
		return text, len(parts[0]) <= 35 && (len(parts) == 1 || len(parts[1]) <= 30)
	case schema.FieldTypeBoolean:
		boolean, ok := value.(bool)
		return boolean, ok
	case schema.FieldTypeDate:
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		parsed, err := time.Parse("2006-01-02", text)
		return text, err == nil && parsed.Format("2006-01-02") == text
	case schema.FieldTypeDatetime:
		text, ok := value.(string)
		if !ok {
			return nil, false
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil || parsed.Nanosecond()%1000 != 0 {
			return nil, false
		}
		return parsed.UTC(), true
	default:
		return nil, false
	}
}

type publishedCursor struct {
	Binding string    `json:"binding"`
	Values  []*string `json:"values"`
}
type publishedOrder struct {
	Expression string
	Descending bool
}

func publishedCursorPredicate(order []publishedOrder, values []*string) (string, []any, bool) {
	if len(order) != len(values) {
		return "", nil, false
	}
	parts := []string{}
	args := []any{}
	for i, item := range order {
		prefix := []string{}
		for j := 0; j < i; j++ {
			prefix = append(prefix, order[j].Expression+` <=> ?`)
			args = append(args, values[j])
		}
		comparison := ""
		if values[i] == nil {
			if item.Descending {
				continue
			}
			comparison = item.Expression + ` IS NOT NULL`
		} else if item.Descending {
			comparison = `(` + item.Expression + ` < ? OR ` + item.Expression + ` IS NULL)`
			args = append(args, *values[i])
		} else {
			comparison = item.Expression + ` > ?`
			args = append(args, *values[i])
		}
		if comparison != "" {
			prefix = append(prefix, comparison)
			parts = append(parts, "("+strings.Join(prefix, " AND ")+")")
		}
	}
	if len(parts) == 0 {
		return "", nil, false
	}
	return "(" + strings.Join(parts, " OR ") + ")", args, true
}

func publishedQueryBinding(modelID string, scope []string, query PublishedQuery) (string, error) {
	query.Cursor = ""
	normalized := normalizedScope(scope)
	data, err := json.Marshal(struct {
		Audience, Model string
		Scope           []string
		Query           PublishedQuery
	}{"published", modelID, normalized, query})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func encodePublishedCursor(cursor publishedCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func decodePublishedCursor(value, binding string) (*publishedCursor, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	var cursor publishedCursor
	if err != nil || json.Unmarshal(data, &cursor) != nil || cursor.Binding != binding || len(cursor.Values) == 0 {
		return nil, invalidCursor()
	}
	return &cursor, nil
}
func normalizedScope(ids []string) []string {
	seen := map[string]bool{}
	result := []string{}
	for _, id := range ids {
		if id != "" && !seen[id] {
			seen[id] = true
			result = append(result, id)
		}
	}
	sort.Strings(result)
	return result
}
func scopeQuery(prefix string, ids []string, suffix string) (string, []any) {
	marks := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		marks[i] = "?"
		args[i] = id
	}
	return prefix + strings.Join(marks, ",") + suffix, args
}
func inQuery(prefix string, first []string, middle string, second []string, middle2 string, third []string, suffix string) (string, []any) {
	q, a := scopeQuery(prefix, first, middle)
	q2, a2 := scopeQuery("", second, middle2)
	q3, a3 := scopeQuery("", third, suffix)
	return q + q2 + q3, append(append(a, a2...), a3...)
}

func publishedStorageError(ctx context.Context, operation string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
		return context.Canceled
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return context.DeadlineExceeded
	}
	return fmt.Errorf("%s: %w", operation, err)
}
