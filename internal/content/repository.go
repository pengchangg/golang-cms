package content

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
	"cms/internal/schema"
	"github.com/go-sql-driver/mysql"
)

type UniqueValue struct {
	FieldID        string
	CanonicalValue []byte
}

type uniqueValueConflict struct{ FieldID string }

func (e *uniqueValueConflict) Error() string { return "唯一字段值已被占用" }

type EntryCursor struct {
	Values []*string
}

type Repository interface {
	HasAnyContent(context.Context, database.Querier, string) (bool, error)
	ListEntries(context.Context, database.Querier, string, AdminEntryQuery, map[string]schema.ContentField, int, *EntryCursor) ([]EntrySummary, [][]*string, error)
	CountEntries(context.Context, database.Querier, string, AdminEntryQuery, map[string]schema.ContentField) (int, bool, error)
	ExpandEntries(context.Context, database.Querier, []EntrySummary, []schema.ContentField, []string) error
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
	CreateFieldValues(context.Context, database.Querier, []FieldValue) error
	CreateRelations(context.Context, database.Querier, []Relation) error
	ValidateRelationTargets(context.Context, database.Querier, []Relation) error
	GetWorkflowEntry(context.Context, database.Querier, string, string) (Entry, error)
	LockRevision(context.Context, database.Querier, string, string, string) (Revision, error)
	TransitionRevision(context.Context, database.Querier, string, WorkflowStatus, WorkflowStatus, *string, *time.Time) (bool, error)
	SetPublishedPointer(context.Context, database.Querier, string, string, string, time.Time) error
	DeletePublishedPointer(context.Context, database.Querier, string, string, string) (bool, error)
	CreateWorkflowEvent(context.Context, database.Querier, string, WorkflowEvent) error
	ListWorkflowEvents(context.Context, database.Querier, string, string, int, *WorkflowEventCursor) ([]WorkflowEvent, error)
}

type WorkflowEventCursor struct {
	OccurredAt time.Time
	ID         string
}

type SQLRepository struct{}

func NewRepository() SQLRepository { return SQLRepository{} }

func (SQLRepository) HasAnyContent(ctx context.Context, q database.Querier, modelID string) (bool, error) {
	var found int
	err := q.QueryRowContext(ctx, `SELECT 1 FROM content_entries WHERE model_id = ? LIMIT 1 FOR SHARE`, modelID).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("查询模型内容存在性: %w", err)
	}
	return true, nil
}

func (SQLRepository) ListEntries(ctx context.Context, q database.Querier, modelID string, input AdminEntryQuery, fields map[string]schema.ContentField, limit int, cursor *EntryCursor) ([]EntrySummary, [][]*string, error) {
	from, joinArgs, where, whereArgs, orders := adminEntriesSQL(modelID, input, fields)
	selectSQL := `SELECT e.id,e.model_id,e.status,p.revision_id,rv.content,rv.workflow_status,pp.revision_id,e.created_by,e.created_at,e.updated_at`
	for _, order := range orders {
		selectSQL += `,` + order.Expression
	}
	if cursor != nil {
		clause, values, ok := publishedCursorPredicate(orders, cursor.Values)
		if !ok {
			return nil, nil, invalidCursor()
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
	query := selectSQL + from + ` WHERE ` + strings.Join(where, ` AND `) + ` ORDER BY ` + strings.Join(directions, `,`) + ` LIMIT ?`
	args := append(append(joinArgs, whereArgs...), limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, nil, fmt.Errorf("查询内容条目: %w", err)
	}
	defer rows.Close()
	items := []EntrySummary{}
	orderedValues := [][]*string{}
	for rows.Next() {
		var item EntrySummary
		var published sql.NullString
		values := make([]sql.RawBytes, len(orders))
		dest := []any{&item.ID, &item.ModelID, &item.Status, &item.CurrentDraftRevisionID, &item.CurrentDraftContent, &item.WorkflowStatus, &published, &item.CreatedBy, &item.CreatedAt, &item.UpdatedAt}
		for i := range values {
			dest = append(dest, &values[i])
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, nil, fmt.Errorf("读取内容条目: %w", err)
		}
		if published.Valid {
			item.CurrentPublishedRevisionID = &published.String
		}
		item.CreatedAt, item.UpdatedAt = item.CreatedAt.UTC(), item.UpdatedAt.UTC()
		items = append(items, item)
		orderValues := make([]*string, len(values))
		for i, value := range values {
			if value != nil {
				text := string(value)
				orderValues[i] = &text
			}
		}
		orderedValues = append(orderedValues, orderValues)
	}
	return items, orderedValues, rows.Err()
}

func (SQLRepository) CountEntries(ctx context.Context, q database.Querier, modelID string, input AdminEntryQuery, fields map[string]schema.ContentField) (int, bool, error) {
	input.Sort = nil
	from, joinArgs, where, whereArgs, _ := adminEntriesSQL(modelID, input, fields)
	query := `SELECT COUNT(*) FROM (SELECT e.id` + from + ` WHERE ` + strings.Join(where, ` AND `) + ` LIMIT 10001) matched`
	var total int
	if err := q.QueryRowContext(ctx, query, append(joinArgs, whereArgs...)...).Scan(&total); err != nil {
		return 0, false, fmt.Errorf("统计内容条目: %w", err)
	}
	if total > 10000 {
		return 10000, true, nil
	}
	return total, false, nil
}

func adminEntriesSQL(modelID string, input AdminEntryQuery, fields map[string]schema.ContentField) (string, []any, []string, []any, []publishedOrder) {
	from := ` FROM content_entries e JOIN content_draft_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id JOIN content_revisions rv ON rv.id=p.revision_id LEFT JOIN content_published_pointers pp ON pp.entry_id=e.id`
	joinArgs, whereArgs := []any{}, []any{modelID, input.Status}
	where := []string{`e.model_id=?`, `e.status=?`}
	if input.WorkflowStatus != nil {
		where = append(where, `rv.workflow_status=?`)
		whereArgs = append(whereArgs, *input.WorkflowStatus)
	}
	for i, filter := range input.Filters {
		field, alias := fields[filter.FieldKey], fmt.Sprintf("f%d", i)
		from += ` JOIN content_field_values ` + alias + ` ON ` + alias + `.revision_id=rv.id AND ` + alias + `.field_id=?`
		joinArgs = append(joinArgs, field.ID)
		clause, values, _ := projectionPredicate(alias, field, filter)
		where, whereArgs = append(where, clause), append(whereArgs, values...)
	}
	for i, filter := range input.RelationFilters {
		field, alias := fields[filter.FieldKey], fmt.Sprintf("r%d", i)
		from += ` JOIN content_relations ` + alias + ` ON ` + alias + `.revision_id=rv.id AND ` + alias + `.field_id=? AND ` + alias + `.target_entry_id=?`
		joinArgs = append(joinArgs, field.ID, filter.EntryID)
	}
	orders := []publishedOrder{{Expression: "e.updated_at", Descending: true}, {Expression: "e.id", Descending: true}}
	if len(input.Sort) > 0 {
		orders = nil
		for i, item := range input.Sort {
			expression := map[string]string{"updated_at": "e.updated_at", "id": "e.id"}[item.FieldKey]
			if expression == "" {
				field, alias := fields[item.FieldKey], fmt.Sprintf("s%d", i)
				from += ` LEFT JOIN content_field_values ` + alias + ` ON ` + alias + `.revision_id=rv.id AND ` + alias + `.field_id=?`
				joinArgs = append(joinArgs, field.ID)
				expression = alias + `.` + projectionColumn(field.Type)
			}
			orders = append(orders, publishedOrder{Expression: expression, Descending: item.Descending})
		}
		if input.Sort[len(input.Sort)-1].FieldKey != "id" {
			orders = append(orders, publishedOrder{Expression: "e.id", Descending: input.Sort[len(input.Sort)-1].Descending})
		}
	}
	return from, joinArgs, where, whereArgs, orders
}

func (SQLRepository) ExpandEntries(ctx context.Context, q database.Querier, items []EntrySummary, fields []schema.ContentField, expand []string) error {
	if len(items) == 0 || len(expand) == 0 {
		return nil
	}
	byKey, byID, revisionIDs, fieldIDs := map[string]schema.ContentField{}, map[string]schema.ContentField{}, []string{}, []string{}
	owners := map[string]*EntrySummary{}
	for i := range items {
		items[i].Expanded = map[string]any{}
		owners[items[i].CurrentDraftRevisionID] = &items[i]
		revisionIDs = append(revisionIDs, items[i].CurrentDraftRevisionID)
	}
	for _, field := range fields {
		byKey[field.Key] = field
	}
	for _, key := range expand {
		field := byKey[key]
		byID[field.ID] = field
		fieldIDs = append(fieldIDs, field.ID)
	}
	query, args := scopeQuery(`SELECT rel.revision_id,rel.field_id,e.id,e.model_id,e.status,p.revision_id,rv.content,rv.workflow_status,pp.revision_id,e.created_by,e.created_at,e.updated_at FROM content_relations rel JOIN content_entries e ON e.id=rel.target_entry_id AND e.model_id=rel.target_model_id JOIN content_draft_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id JOIN content_revisions rv ON rv.id=p.revision_id LEFT JOIN content_published_pointers pp ON pp.entry_id=e.id WHERE rel.revision_id IN (`, revisionIDs, `) AND rel.field_id IN (`)
	query2, args2 := scopeQuery("", fieldIDs, `) ORDER BY rel.revision_id,rel.field_id,rel.position`)
	query += query2
	args = append(args, args2...)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("展开内容关联: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var revisionID, fieldID string
		var target EntrySummary
		var published sql.NullString
		if err := rows.Scan(&revisionID, &fieldID, &target.ID, &target.ModelID, &target.Status, &target.CurrentDraftRevisionID, &target.CurrentDraftContent, &target.WorkflowStatus, &published, &target.CreatedBy, &target.CreatedAt, &target.UpdatedAt); err != nil {
			return err
		}
		if published.Valid {
			target.CurrentPublishedRevisionID = &published.String
		}
		target.CreatedAt, target.UpdatedAt = target.CreatedAt.UTC(), target.UpdatedAt.UTC()
		owner, field := owners[revisionID], byID[fieldID]
		if field.Type == schema.FieldTypeSingleRelation {
			owner.Expanded[field.Key] = target
		} else {
			values, _ := owner.Expanded[field.Key].([]EntrySummary)
			owner.Expanded[field.Key] = append(values, target)
		}
	}
	return rows.Err()
}

func (r SQLRepository) GetEntry(ctx context.Context, q database.Querier, modelID, entryID string) (Entry, error) {
	var entry Entry
	var published sql.NullString
	err := q.QueryRowContext(ctx, `SELECT e.id,e.model_id,e.status,p.revision_id,rv.workflow_status,pp.revision_id,e.created_by,e.created_at,e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id JOIN content_revisions rv ON rv.id=p.revision_id LEFT JOIN content_published_pointers pp ON pp.entry_id=e.id WHERE e.id=? AND e.model_id=?`, entryID, modelID).Scan(&entry.ID, &entry.ModelID, &entry.Status, &entry.CurrentDraftRevisionID, &entry.WorkflowStatus, &published, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, notFound("内容条目")
	}
	if err != nil {
		return entry, fmt.Errorf("查询内容条目: %w", err)
	}
	entry.CreatedAt, entry.UpdatedAt = entry.CreatedAt.UTC(), entry.UpdatedAt.UTC()
	if published.Valid {
		entry.CurrentPublishedRevisionID = &published.String
	}
	revision, err := r.GetRevision(ctx, q, modelID, entryID, entry.CurrentDraftRevisionID)
	entry.CurrentDraftRevision = revision
	entry.CurrentDraftContent = revision.Content
	if err == nil && published.Valid {
		value, e := r.GetRevision(ctx, q, modelID, entryID, published.String)
		if e != nil {
			return entry, e
		}
		entry.CurrentPublishedRevision = &value
	}
	return entry, err
}

func (SQLRepository) LockEntry(ctx context.Context, q database.Querier, modelID, entryID string) (EntrySummary, error) {
	var entry EntrySummary
	var published sql.NullString
	err := q.QueryRowContext(ctx, `SELECT e.id,e.model_id,e.status,p.revision_id,rv.workflow_status,pp.revision_id,e.created_by,e.created_at,e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id JOIN content_revisions rv ON rv.id=p.revision_id LEFT JOIN content_published_pointers pp ON pp.entry_id=e.id WHERE e.id=? AND e.model_id=? FOR UPDATE`, entryID, modelID).Scan(&entry.ID, &entry.ModelID, &entry.Status, &entry.CurrentDraftRevisionID, &entry.WorkflowStatus, &published, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, notFound("内容条目")
	}
	if err != nil {
		return entry, fmt.Errorf("锁定内容条目: %w", err)
	}
	entry.CreatedAt, entry.UpdatedAt = entry.CreatedAt.UTC(), entry.UpdatedAt.UTC()
	if published.Valid {
		entry.CurrentPublishedRevisionID = &published.String
	}
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
	_, err := q.ExecContext(ctx, `INSERT INTO content_revisions (id, entry_id, model_id, revision_number, content, workflow_status, created_by, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, revision.ID, revision.EntryID, revision.ModelID, revision.Number, []byte(revision.Content), WorkflowDraft, revision.CreatedBy, revision.CreatedAt)
	if err != nil {
		return fmt.Errorf("创建内容 Revision: %w", err)
	}
	return nil
}

func (SQLRepository) CreateFieldValues(ctx context.Context, q database.Querier, values []FieldValue) error {
	for _, v := range values {
		if _, err := q.ExecContext(ctx, `INSERT INTO content_field_values (revision_id,entry_id,model_id,field_id,value_type,string_value,integer_value,decimal_value,boolean_value,date_value,datetime_value) VALUES (?,?,?,?,?,?,?,?,?,?,?)`, v.RevisionID, v.EntryID, v.ModelID, v.FieldID, v.ValueType, v.StringValue, v.IntegerValue, v.DecimalValue, v.BooleanValue, v.DateValue, v.DatetimeValue); err != nil {
			return fmt.Errorf("创建 Revision 字段投影: %w", err)
		}
	}
	return nil
}
func (SQLRepository) CreateRelations(ctx context.Context, q database.Querier, values []Relation) error {
	for _, v := range values {
		if _, err := q.ExecContext(ctx, `INSERT INTO content_relations (revision_id,entry_id,model_id,field_id,target_entry_id,target_model_id,position) VALUES (?,?,?,?,?,?,?)`, v.RevisionID, v.EntryID, v.ModelID, v.FieldID, v.TargetEntryID, v.TargetModelID, v.Position); err != nil {
			return fmt.Errorf("创建 Revision 关联: %w", err)
		}
	}
	return nil
}
func (SQLRepository) ValidateRelationTargets(ctx context.Context, q database.Querier, values []Relation) error {
	for _, v := range orderedRelationTargets(values) {
		var status EntryStatus
		if err := q.QueryRowContext(ctx, `SELECT status FROM content_entries WHERE id=? AND model_id=? FOR UPDATE`, v.TargetEntryID, v.TargetModelID).Scan(&status); errors.Is(err, sql.ErrNoRows) {
			var failures validationErrors
			failures.add("/content", "invalid_relation", "关联目标不存在或已归档")
			return failures.err()
		} else if err != nil {
			return fmt.Errorf("校验关联目标: %w", err)
		}
		if status == StatusArchived {
			var failures validationErrors
			failures.add("/content", "invalid_relation", "关联目标不存在或已归档")
			return failures.err()
		}
	}
	return nil
}

func orderedRelationTargets(values []Relation) []Relation {
	targets := append([]Relation(nil), values...)
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].TargetModelID == targets[j].TargetModelID {
			return targets[i].TargetEntryID < targets[j].TargetEntryID
		}
		return targets[i].TargetModelID < targets[j].TargetModelID
	})
	result := targets[:0]
	for _, target := range targets {
		if len(result) == 0 || result[len(result)-1].TargetModelID != target.TargetModelID || result[len(result)-1].TargetEntryID != target.TargetEntryID {
			result = append(result, target)
		}
	}
	return result
}

func (r SQLRepository) GetWorkflowEntry(ctx context.Context, q database.Querier, modelID, entryID string) (Entry, error) {
	entry, err := r.GetEntry(ctx, q, modelID, entryID)
	if err != nil {
		return entry, err
	}
	var published sql.NullString
	if err = q.QueryRowContext(ctx, `SELECT p.revision_id FROM content_entries e LEFT JOIN content_published_pointers p ON p.entry_id=e.id WHERE e.id=? AND e.model_id=? FOR UPDATE`, entryID, modelID).Scan(&published); err != nil {
		return entry, err
	}
	entry.WorkflowStatus = entry.CurrentDraftRevision.WorkflowStatus
	if published.Valid {
		entry.CurrentPublishedRevisionID = &published.String
		revision, e := r.GetRevision(ctx, q, modelID, entryID, published.String)
		if e != nil {
			return entry, e
		}
		entry.CurrentPublishedRevision = &revision
	}
	return entry, nil
}
func (SQLRepository) LockRevision(ctx context.Context, q database.Querier, modelID, entryID, revisionID string) (Revision, error) {
	return getRevisionRow(ctx, q, `SELECT id,entry_id,model_id,revision_number,content,workflow_status,created_by,submitted_by,submitted_at,created_at FROM content_revisions WHERE id=? AND entry_id=? AND model_id=? FOR UPDATE`, revisionID, entryID, modelID)
}
func (SQLRepository) TransitionRevision(ctx context.Context, q database.Querier, id string, from, to WorkflowStatus, submitter *string, submittedAt *time.Time) (bool, error) {
	result, err := q.ExecContext(ctx, `UPDATE content_revisions SET workflow_status=?,submitted_by=COALESCE(submitted_by,?),submitted_at=COALESCE(submitted_at,?) WHERE id=? AND workflow_status=?`, to, submitter, submittedAt, id, from)
	if err != nil {
		return false, fmt.Errorf("转换 Revision 工作流: %w", err)
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (SQLRepository) SetPublishedPointer(ctx context.Context, q database.Querier, modelID, entryID, revisionID string, at time.Time) error {
	_, err := q.ExecContext(ctx, `INSERT INTO content_published_pointers(entry_id,model_id,revision_id,published_at) VALUES(?,?,?,?) ON DUPLICATE KEY UPDATE revision_id=VALUES(revision_id),published_at=VALUES(published_at)`, entryID, modelID, revisionID, at)
	return err
}
func (SQLRepository) DeletePublishedPointer(ctx context.Context, q database.Querier, modelID, entryID, revisionID string) (bool, error) {
	result, err := q.ExecContext(ctx, `DELETE FROM content_published_pointers WHERE entry_id=? AND model_id=? AND revision_id=?`, entryID, modelID, revisionID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (SQLRepository) CreateWorkflowEvent(ctx context.Context, q database.Querier, modelID string, event WorkflowEvent) error {
	_, err := q.ExecContext(ctx, `INSERT INTO content_workflow_events(id,entry_id,model_id,revision_id,event_type,from_status,to_status,actor_id,reason,occurred_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, event.ID, event.EntryID, modelID, event.RevisionID, event.Type, event.FromStatus, event.ToStatus, event.ActorID, event.Reason, event.OccurredAt)
	return err
}
func (SQLRepository) ListWorkflowEvents(ctx context.Context, q database.Querier, modelID, entryID string, limit int, cursor *WorkflowEventCursor) ([]WorkflowEvent, error) {
	query := `SELECT id,entry_id,revision_id,event_type,from_status,to_status,actor_id,reason,occurred_at FROM content_workflow_events WHERE model_id=? AND entry_id=?`
	args := []any{modelID, entryID}
	if cursor != nil {
		query += ` AND (occurred_at<? OR (occurred_at=? AND id<?))`
		args = append(args, cursor.OccurredAt, cursor.OccurredAt, cursor.ID)
	}
	query += ` ORDER BY occurred_at DESC,id DESC LIMIT ?`
	args = append(args, limit)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []WorkflowEvent{}
	for rows.Next() {
		var item WorkflowEvent
		var reason sql.NullString
		if err := rows.Scan(&item.ID, &item.EntryID, &item.RevisionID, &item.Type, &item.FromStatus, &item.ToStatus, &item.ActorID, &reason, &item.OccurredAt); err != nil {
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
	query := `SELECT id,entry_id,model_id,revision_number,content,workflow_status,created_by,submitted_by,submitted_at,created_at FROM content_revisions WHERE entry_id = ? AND model_id = ?`
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
	return getRevisionRow(ctx, q, `SELECT id,entry_id,model_id,revision_number,content,workflow_status,created_by,submitted_by,submitted_at,created_at FROM content_revisions WHERE id=? AND entry_id=? AND model_id=?`, revisionID, entryID, modelID)
}

func getRevisionRow(ctx context.Context, q database.Querier, query string, args ...any) (Revision, error) {
	row := q.QueryRowContext(ctx, query, args...)
	var revision Revision
	var content []byte
	var submittedBy sql.NullString
	var submittedAt sql.NullTime
	err := row.Scan(&revision.ID, &revision.EntryID, &revision.ModelID, &revision.Number, &content, &revision.WorkflowStatus, &revision.CreatedBy, &submittedBy, &submittedAt, &revision.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return revision, notFound("内容 Revision")
	}
	if err != nil {
		return revision, fmt.Errorf("查询内容 Revision: %w", err)
	}
	revision.Content = append(json.RawMessage(nil), content...)
	if submittedBy.Valid {
		revision.SubmittedBy = &submittedBy.String
	}
	if submittedAt.Valid {
		value := submittedAt.Time.UTC()
		revision.SubmittedAt = &value
	}
	revision.CreatedAt = revision.CreatedAt.UTC()
	return revision, nil
}

func (SQLRepository) GetEntrySummary(ctx context.Context, q database.Querier, modelID, entryID string) (EntrySummary, error) {
	var entry EntrySummary
	var published sql.NullString
	err := q.QueryRowContext(ctx, `SELECT e.id,e.model_id,e.status,p.revision_id,rv.workflow_status,pp.revision_id,e.created_by,e.created_at,e.updated_at FROM content_entries e JOIN content_draft_pointers p ON p.entry_id=e.id AND p.model_id=e.model_id JOIN content_revisions rv ON rv.id=p.revision_id LEFT JOIN content_published_pointers pp ON pp.entry_id=e.id WHERE e.id=? AND e.model_id=?`, entryID, modelID).Scan(&entry.ID, &entry.ModelID, &entry.Status, &entry.CurrentDraftRevisionID, &entry.WorkflowStatus, &published, &entry.CreatedBy, &entry.CreatedAt, &entry.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return entry, notFound("内容条目")
	}
	if published.Valid {
		entry.CurrentPublishedRevisionID = &published.String
	}
	return entry, err
}

type revisionScanner interface{ Scan(...any) error }

func scanRevision(row revisionScanner) (Revision, error) {
	var item Revision
	var content []byte
	var submittedBy sql.NullString
	var submittedAt sql.NullTime
	if err := row.Scan(&item.ID, &item.EntryID, &item.ModelID, &item.Number, &content, &item.WorkflowStatus, &item.CreatedBy, &submittedBy, &submittedAt, &item.CreatedAt); err != nil {
		return item, fmt.Errorf("读取内容 Revision: %w", err)
	}
	item.Content = append(json.RawMessage(nil), content...)
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
