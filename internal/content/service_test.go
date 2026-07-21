package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

type memoryRepository struct {
	entries     map[string]EntrySummary
	revisions   map[string][]Revision
	unique      map[string]map[string]string
	created     int
	updated     int
	lockEvents  *[]string
	published   map[string]string
	events      []WorkflowEvent
	fieldValues []FieldValue
	relations   []Relation
	listCalls   int
	expandCalls int
}

type mediaRecorder struct{ events *[]string }

func (m mediaRecorder) ValidateAvailable(context.Context, database.Querier, []MediaReference) error {
	*m.events = append(*m.events, "asset")
	return nil
}
func (m mediaRecorder) InsertRevisionReferences(context.Context, database.Querier, []MediaReference) error {
	*m.events = append(*m.events, "insert_asset_reference")
	return nil
}
func (m mediaRecorder) ValidatePublishableRevision(context.Context, database.Querier, string) error {
	return nil
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{entries: map[string]EntrySummary{}, revisions: map[string][]Revision{}, unique: map[string]map[string]string{}, published: map[string]string{}}
}
func (r *memoryRepository) CreateFieldValues(_ context.Context, _ database.Querier, values []FieldValue) error {
	r.fieldValues = append(r.fieldValues, values...)
	return nil
}
func (r *memoryRepository) CreateRelations(_ context.Context, _ database.Querier, values []Relation) error {
	r.relations = append(r.relations, values...)
	return nil
}
func (r *memoryRepository) ValidateRelationTargets(_ context.Context, _ database.Querier, values []Relation) error {
	if r.lockEvents != nil && len(values) > 0 {
		*r.lockEvents = append(*r.lockEvents, "target")
	}
	for _, value := range values {
		entry, ok := r.entries[value.TargetEntryID]
		if !ok || entry.ModelID != value.TargetModelID || entry.Status == StatusArchived {
			return errors.New("关联目标无效")
		}
	}
	return nil
}
func (r *memoryRepository) GetWorkflowEntry(ctx context.Context, q database.Querier, modelID, entryID string) (Entry, error) {
	entry, err := r.GetEntry(ctx, q, modelID, entryID)
	if err != nil {
		return entry, err
	}
	entry.WorkflowStatus = entry.CurrentDraftRevision.WorkflowStatus
	if id := r.published[entryID]; id != "" {
		entry.CurrentPublishedRevisionID = &id
		revision, err := r.GetRevision(ctx, q, modelID, entryID, id)
		if err != nil {
			return entry, err
		}
		entry.CurrentPublishedRevision = &revision
	}
	return entry, nil
}
func (r *memoryRepository) LockRevision(ctx context.Context, q database.Querier, modelID, entryID, revisionID string) (Revision, error) {
	return r.GetRevision(ctx, q, modelID, entryID, revisionID)
}
func (r *memoryRepository) TransitionRevision(_ context.Context, _ database.Querier, id string, from, to WorkflowStatus, submitter *string, submittedAt *time.Time) (bool, error) {
	for entryID, revisions := range r.revisions {
		for i := range revisions {
			if revisions[i].ID == id {
				if revisions[i].WorkflowStatus != from {
					return false, nil
				}
				revisions[i].WorkflowStatus = to
				if revisions[i].SubmittedBy == nil {
					revisions[i].SubmittedBy = submitter
					revisions[i].SubmittedAt = submittedAt
				}
				r.revisions[entryID] = revisions
				return true, nil
			}
		}
	}
	return false, nil
}
func (r *memoryRepository) SetPublishedPointer(_ context.Context, _ database.Querier, _, entryID, revisionID string, _ time.Time) error {
	r.published[entryID] = revisionID
	return nil
}
func (r *memoryRepository) DeletePublishedPointer(_ context.Context, _ database.Querier, _, entryID, revisionID string) (bool, error) {
	if r.published[entryID] != revisionID {
		return false, nil
	}
	delete(r.published, entryID)
	return true, nil
}
func (r *memoryRepository) CreateWorkflowEvent(_ context.Context, _ database.Querier, _ string, event WorkflowEvent) error {
	r.events = append(r.events, event)
	return nil
}
func (r *memoryRepository) ListWorkflowEvents(_ context.Context, _ database.Querier, _, entryID string, limit int, cursor *WorkflowEventCursor) ([]WorkflowEvent, error) {
	items := []WorkflowEvent{}
	for i := len(r.events) - 1; i >= 0; i-- {
		event := r.events[i]
		if event.EntryID == entryID && (cursor == nil || event.OccurredAt.Before(cursor.OccurredAt) || event.OccurredAt.Equal(cursor.OccurredAt) && event.ID < cursor.ID) {
			items = append(items, event)
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

func (r *memoryRepository) HasAnyContent(_ context.Context, _ database.Querier, modelID string) (bool, error) {
	for _, entry := range r.entries {
		if entry.ModelID == modelID {
			return true, nil
		}
	}
	return false, nil
}
func (r *memoryRepository) ListEntries(_ context.Context, _ database.Querier, modelID string, query AdminEntryQuery, _ map[string]schema.ContentField, limit int, _ *EntryCursor) ([]EntrySummary, [][]*string, error) {
	r.listCalls++
	items := []EntrySummary{}
	for _, item := range r.entries {
		if item.ModelID == modelID && item.Status == query.Status && (query.WorkflowStatus == nil || item.WorkflowStatus == *query.WorkflowStatus) {
			for _, revision := range r.revisions[item.ID] {
				if revision.ID == item.CurrentDraftRevisionID {
					item.CurrentDraftContent = revision.Content
					break
				}
			}
			items = append(items, item)
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	values := make([][]*string, len(items))
	for i, item := range items {
		values[i] = []*string{pointerString(item.UpdatedAt.Format(time.RFC3339Nano)), pointerString(item.ID)}
	}
	return items, values, nil
}
func (r *memoryRepository) CountEntries(_ context.Context, _ database.Querier, modelID string, query AdminEntryQuery, _ map[string]schema.ContentField) (int, bool, error) {
	count := 0
	for _, item := range r.entries {
		if item.ModelID == modelID && item.Status == query.Status && (query.WorkflowStatus == nil || item.WorkflowStatus == *query.WorkflowStatus) {
			count++
		}
	}
	if count > 10000 {
		return 10000, true, nil
	}
	return count, false, nil
}
func (r *memoryRepository) ExpandEntries(context.Context, database.Querier, []EntrySummary, []schema.ContentField, []string) error {
	r.expandCalls++
	return nil
}
func (r *memoryRepository) GetEntry(_ context.Context, _ database.Querier, modelID, entryID string) (Entry, error) {
	entry, ok := r.entries[entryID]
	if !ok || entry.ModelID != modelID {
		return Entry{}, notFound("内容条目")
	}
	revisions := r.revisions[entryID]
	for _, revision := range revisions {
		if revision.ID == entry.CurrentDraftRevisionID {
			entry.CurrentDraftContent = revision.Content
			return Entry{EntrySummary: entry, CurrentDraftRevision: revision}, nil
		}
	}
	return Entry{}, errors.New("当前 Revision 不存在")
}
func (r *memoryRepository) LockEntry(ctx context.Context, q database.Querier, modelID, entryID string) (EntrySummary, error) {
	if r.lockEvents != nil {
		*r.lockEvents = append(*r.lockEvents, "entry")
	}
	entry, err := r.GetEntry(ctx, q, modelID, entryID)
	return entry.EntrySummary, err
}
func (r *memoryRepository) CreateEntry(_ context.Context, _ database.Querier, entry EntrySummary) error {
	r.entries[entry.ID], r.created = entry, r.created+1
	return nil
}
func (r *memoryRepository) UpdateEntry(_ context.Context, _ database.Querier, entry EntrySummary) error {
	r.entries[entry.ID], r.updated = entry, r.updated+1
	return nil
}
func (r *memoryRepository) CreateRevision(_ context.Context, _ database.Querier, revision Revision) error {
	r.revisions[revision.EntryID] = append(r.revisions[revision.EntryID], revision)
	return nil
}
func (r *memoryRepository) SetDraftPointer(_ context.Context, _ database.Querier, modelID, entryID, revisionID string) error {
	entry := r.entries[entryID]
	if entry.ModelID != modelID {
		return notFound("内容条目")
	}
	entry.CurrentDraftRevisionID = revisionID
	r.entries[entryID] = entry
	return nil
}
func (r *memoryRepository) ReplaceUniqueValues(_ context.Context, _ database.Querier, modelID, entryID string, values []UniqueValue) error {
	next := map[string]string{}
	for _, value := range values {
		key := modelID + "\x00" + value.FieldID + "\x00" + string(value.CanonicalValue)
		if owner := r.unique[key]["owner"]; owner != "" && owner != entryID {
			return &uniqueValueConflict{FieldID: value.FieldID}
		}
		next[key] = entryID
	}
	for key, holder := range r.unique {
		if holder["owner"] == entryID {
			delete(r.unique, key)
		}
	}
	for key, owner := range next {
		r.unique[key] = map[string]string{"owner": owner}
	}
	return nil
}
func (r *memoryRepository) DeleteUniqueValues(_ context.Context, _ database.Querier, _, entryID string) error {
	for key, holder := range r.unique {
		if holder["owner"] == entryID {
			delete(r.unique, key)
		}
	}
	return nil
}
func (r *memoryRepository) ListRevisions(_ context.Context, _ database.Querier, modelID, entryID string, limit int, before *uint) ([]Revision, error) {
	items := []Revision{}
	for i := len(r.revisions[entryID]) - 1; i >= 0; i-- {
		item := r.revisions[entryID][i]
		if item.ModelID == modelID && (before == nil || item.Number < *before) {
			items = append(items, item)
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
func (r *memoryRepository) GetRevision(_ context.Context, _ database.Querier, modelID, entryID, revisionID string) (Revision, error) {
	for _, item := range r.revisions[entryID] {
		if item.ModelID == modelID && item.ID == revisionID {
			return item, nil
		}
	}
	return Revision{}, notFound("内容 Revision")
}

type memoryModels struct {
	model          schema.ContentModel
	models         map[string]schema.ContentModel
	lockEvents     *[]string
	lockedModelIDs *[]string
}

type memoryAssetResolver struct {
	calls int
	items map[string]map[string]ReferencedAsset
}

func (r *memoryAssetResolver) ResolveReferencedAssets(_ context.Context, _ []string) (map[string]map[string]ReferencedAsset, error) {
	r.calls++
	return r.items, nil
}

func (m memoryModels) GetModel(_ context.Context, _ database.Querier, id string) (schema.ContentModel, error) {
	if m.models != nil {
		model, ok := m.models[id]
		if !ok {
			return schema.ContentModel{}, notFound("模型")
		}
		return model, nil
	}
	return m.model, nil
}
func (m memoryModels) LockModel(context.Context, database.Querier, string) (schema.ContentModelSummary, error) {
	if m.lockEvents != nil {
		*m.lockEvents = append(*m.lockEvents, "model")
	}
	return m.model.ContentModelSummary, nil
}
func (m memoryModels) LockModels(_ context.Context, _ database.Querier, ids []string) (map[string]schema.ContentModelSummary, error) {
	if m.lockEvents != nil {
		*m.lockEvents = append(*m.lockEvents, "model")
	}
	if m.lockedModelIDs != nil {
		*m.lockedModelIDs = append((*m.lockedModelIDs)[:0], ids...)
	}
	result := make(map[string]schema.ContentModelSummary, len(ids))
	for _, id := range ids {
		model, err := m.GetModel(context.Background(), nil, id)
		if err != nil {
			return nil, err
		}
		result[id] = model.ContentModelSummary
	}
	return result, nil
}

type directTx struct{ calls int }

func (t *directTx) WithinTx(_ context.Context, _ *sql.TxOptions, fn func(database.Querier) error) error {
	t.calls++
	return fn(nil)
}

type auditRecorder struct{ events []audit.Event }

func (a *auditRecorder) Append(_ context.Context, q database.Querier, event audit.Event) error {
	if q != nil {
		return errors.New("测试事务执行器应为 nil")
	}
	a.events = append(a.events, event)
	return nil
}

func TestCreateUpdateArchiveEntryCreatesImmutableRevisionsAndAudits(t *testing.T) {
	service, repository, tx, audits := testService()
	principal := contentPrincipal(permissionCreate, permissionUpdate, permissionArchive, permissionView)
	created, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"第一版"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if created.CurrentDraftRevision.Number != 1 || len(repository.revisions[created.ID]) != 1 || tx.calls != 1 || audits.events[0].Action != "content_entry_created" {
		t.Fatalf("创建未原子产生首个 Revision 和审计: %#v", created)
	}
	updated, err := service.UpdateEntry(context.Background(), principal, testMeta(), "mdl_1", created.ID, UpdateEntryRequest{BaseRevisionID: created.CurrentDraftRevisionID, Content: json.RawMessage(`{"title":"第二版"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if updated.CurrentDraftRevision.Number != 2 || len(repository.revisions[created.ID]) != 2 || string(repository.revisions[created.ID][0].Content) != `{"title":"第一版"}` || audits.events[1].Action != "content_revision_created" {
		t.Fatalf("更新破坏了 Revision 不可变语义: %#v", repository.revisions[created.ID])
	}
	if audits.events[1].ResourceType != "content_revision" || *audits.events[1].ResourceID != updated.CurrentDraftRevisionID {
		t.Fatal("Revision 审计资源类型或 ID 不正确")
	}
	if err := service.ArchiveEntry(context.Background(), principal, testMeta(), "mdl_1", created.ID); err != nil {
		t.Fatal(err)
	}
	if repository.entries[created.ID].Status != StatusArchived || len(repository.revisions[created.ID]) != 2 || audits.events[2].Action != "content_entry_archived" {
		t.Fatal("归档不应创建或删除 Revision")
	}
}

func TestUpdateConflictAndValidationDoNotCreateRevision(t *testing.T) {
	service, repository, _, _ := testService()
	principal := contentPrincipal(permissionCreate, permissionUpdate)
	created, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"第一版"}`)})
	if err != nil {
		t.Fatal(err)
	}
	err = func() error {
		_, err := service.UpdateEntry(context.Background(), principal, testMeta(), "mdl_1", created.ID, UpdateEntryRequest{BaseRevisionID: "rev_stale", Content: json.RawMessage(`{"title":"第二版"}`)})
		return err
	}()
	assertApplicationCode(t, err, "draft_revision_conflict")
	if len(repository.revisions[created.ID]) != 1 {
		t.Fatal("并发冲突产生了 Revision")
	}
	_, err = service.UpdateEntry(context.Background(), principal, testMeta(), "mdl_1", created.ID, UpdateEntryRequest{BaseRevisionID: created.CurrentDraftRevisionID, Content: json.RawMessage(`{"unknown":true}`)})
	assertApplicationCode(t, err, "validation_failed")
	if len(repository.revisions[created.ID]) != 1 {
		t.Fatal("校验失败产生了 Revision")
	}
}

func TestUpdateLocksModelBeforeEntry(t *testing.T) {
	service, repository, _, _ := testService()
	principal := contentPrincipal(permissionCreate, permissionUpdate)
	created, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"第一版"}`)})
	if err != nil {
		t.Fatal(err)
	}
	events := []string{}
	repository.lockEvents = &events
	models := service.models.(memoryModels)
	models.lockEvents = &events
	service.models = models
	if _, err := service.UpdateEntry(context.Background(), principal, testMeta(), "mdl_1", created.ID, UpdateEntryRequest{BaseRevisionID: created.CurrentDraftRevisionID, Content: json.RawMessage(`{"title":"第二版"}`)}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "model,entry" {
		t.Fatalf("锁顺序必须统一为 model、entry，实际为 %v", events)
	}
}

func TestImportDraftsWritesAllRowsAndCompletionInOneTransaction(t *testing.T) {
	service, repository, _, _ := testService()
	principal := contentPrincipal(permissionCreate)
	completed := false
	err := service.ImportDrafts(context.Background(), principal, testMeta(), "mdl_1", func(yield func(ImportDraft) error) error {
		for _, title := range []string{"第一条", "第二条"} {
			if err := yield(ImportDraft{Content: json.RawMessage(fmt.Sprintf(`{"title":%q}`, title))}); err != nil {
				return err
			}
		}
		return nil
	}, func(database.Querier) error { completed = true; return nil })
	if err != nil {
		t.Fatal(err)
	}
	if len(repository.entries) != 2 || !completed {
		t.Fatalf("批量写入或完成标记缺失: entries=%d completed=%v", len(repository.entries), completed)
	}
}

func TestImportDraftsRepeatsSourceWithoutRetainingRowBuffers(t *testing.T) {
	service, repository, _, _ := testService()
	passes := 0
	source := func(yield func(ImportDraft) error) error {
		passes++
		buffer := make([]byte, 0, 64)
		for _, title := range []string{"第一条", "第二条"} {
			buffer = append(buffer[:0], fmt.Sprintf(`{"title":%q}`, title)...)
			if err := yield(ImportDraft{Content: json.RawMessage(buffer)}); err != nil {
				return err
			}
		}
		return nil
	}
	if err := service.ImportDrafts(context.Background(), contentPrincipal(permissionCreate), testMeta(), "mdl_1", source, nil); err != nil {
		t.Fatal(err)
	}
	if passes != 2 {
		t.Fatalf("DraftSource 遍历次数=%d，期望 2", passes)
	}
	titles := map[string]bool{}
	for _, revisions := range repository.revisions {
		var value struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal(revisions[0].Content, &value); err != nil {
			t.Fatal(err)
		}
		titles[value.Title] = true
	}
	if !titles["第一条"] || !titles["第二条"] {
		t.Fatalf("导入保留了可复用行缓冲: %v", titles)
	}
}

func TestUniqueValueConflictAndArchiveRelease(t *testing.T) {
	repository, tx, audits := newMemoryRepository(), &directTx{}, &auditRecorder{}
	model := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{{ID: "fld_title", Key: "title", Type: schema.FieldTypeSingleLineText, DefaultValue: json.RawMessage("null"), Constraints: schema.FieldConstraints{Unique: true}, Status: schema.StatusActive}}}
	service := NewService(Dependencies{Repository: repository, Transactor: tx, ModelRepository: memoryModels{model: model}, Audit: audits})
	sequence := 0
	service.newID = func(prefix string) (string, error) {
		sequence++
		return fmt.Sprintf("%s%d", prefix, sequence), nil
	}
	principal := contentPrincipal(permissionCreate, permissionArchive)
	first, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"相同"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"相同"}`)})
	assertApplicationCode(t, err, "validation_failed")
	if err := service.ArchiveEntry(context.Background(), principal, testMeta(), "mdl_1", first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"相同"}`)}); err != nil {
		t.Fatalf("归档释放后应可重新占用唯一值: %v", err)
	}
}

func TestPermissionDefaultsToDenyAndSystemPermissionCannotSubstitute(t *testing.T) {
	service, _, _, _ := testService()
	principal := identity.NewPrincipal("usr_1", "用户", nil, identity.AuthMethodSMS, []string{"models.create"}, nil)
	_, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"内容"}`)})
	assertApplicationCode(t, err, "permission_denied")
}

func TestWorkflowPublishEditAndUnpublish(t *testing.T) {
	service, repository, _, _ := testService()
	creator := contentPrincipal(permissionCreate, permissionUpdate, permissionSubmit)
	created, err := service.CreateEntry(context.Background(), creator, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"线上版本"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(context.Background(), creator, testMeta(), "mdl_1", created.ID, RevisionConditionRequest{RevisionID: created.CurrentDraftRevisionID}); err != nil {
		t.Fatal(err)
	}
	reviewer := identity.NewPrincipal("usr_2", "审核人", nil, identity.AuthMethodSMS, nil, []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{permissionReview, permissionPublish, permissionUnpublish}}})
	published, err := service.Approve(context.Background(), reviewer, testMeta(), "mdl_1", created.ID, RevisionConditionRequest{RevisionID: created.CurrentDraftRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if published.CurrentPublishedRevisionID == nil || *published.CurrentPublishedRevisionID != created.CurrentDraftRevisionID || published.CurrentPublishedRevision.WorkflowStatus != WorkflowPublished {
		t.Fatalf("发布指针错误: %#v", published)
	}
	edited, err := service.UpdateEntry(context.Background(), creator, testMeta(), "mdl_1", created.ID, UpdateEntryRequest{BaseRevisionID: created.CurrentDraftRevisionID, Content: json.RawMessage(`{"title":"下一版"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if repository.published[created.ID] != created.CurrentDraftRevisionID || edited.CurrentDraftRevisionID == created.CurrentDraftRevisionID {
		t.Fatal("编辑已发布内容提前移动发布指针")
	}
	if _, err = service.Unpublish(context.Background(), reviewer, testMeta(), "mdl_1", created.ID, RevisionConditionRequest{RevisionID: created.CurrentDraftRevisionID}); err != nil {
		t.Fatal(err)
	}
	if repository.published[created.ID] != "" {
		t.Fatal("下线后发布指针未删除")
	}
}

func TestWorkflowAllowsSubmitterToReviewWithPermissions(t *testing.T) {
	service, repository, _, _ := testService()
	creator := contentPrincipal(permissionCreate, permissionSubmit, permissionReview, permissionPublish)
	created, err := service.CreateEntry(context.Background(), creator, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"内容"}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Submit(context.Background(), creator, testMeta(), "mdl_1", created.ID, RevisionConditionRequest{RevisionID: created.CurrentDraftRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	reviewerOnly := contentPrincipal(permissionReview)
	_, err = service.Approve(context.Background(), reviewerOnly, testMeta(), "mdl_1", created.ID, RevisionConditionRequest{RevisionID: created.CurrentDraftRevisionID})
	assertApplicationCode(t, err, "permission_denied")
	if repository.revisions[created.ID][0].WorkflowStatus != WorkflowPendingReview || len(repository.events) != 1 {
		t.Fatal("缺少发布权限时改变了状态或事件")
	}
	published, err := service.Approve(context.Background(), creator, testMeta(), "mdl_1", created.ID, RevisionConditionRequest{RevisionID: created.CurrentDraftRevisionID})
	if err != nil {
		t.Fatal(err)
	}
	if published.WorkflowStatus != WorkflowPublished || len(repository.events) != 2 || repository.events[1].ActorID != creator.UserID {
		t.Fatalf("提交人审核发布结果错误: %#v/%#v", published, repository.events)
	}

	rejectedEntry, err := service.CreateEntry(context.Background(), creator, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"待修改"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.Submit(context.Background(), creator, testMeta(), "mdl_1", rejectedEntry.ID, RevisionConditionRequest{RevisionID: rejectedEntry.CurrentDraftRevisionID}); err != nil {
		t.Fatal(err)
	}
	_, err = service.Reject(context.Background(), creator, testMeta(), "mdl_1", rejectedEntry.ID, RejectRevisionRequest{RevisionID: "rev_stale", Reason: "原因"})
	assertApplicationCode(t, err, "workflow_revision_conflict")
	_, err = service.Reject(context.Background(), creator, testMeta(), "mdl_1", rejectedEntry.ID, RejectRevisionRequest{RevisionID: rejectedEntry.CurrentDraftRevisionID, Reason: "  "})
	assertApplicationCode(t, err, "validation_failed")
	result, err := service.Reject(context.Background(), creator, testMeta(), "mdl_1", rejectedEntry.ID, RejectRevisionRequest{RevisionID: rejectedEntry.CurrentDraftRevisionID, Reason: "  需要修改  "})
	if err != nil {
		t.Fatal(err)
	}
	lastEvent := repository.events[len(repository.events)-1]
	if result.WorkflowStatus != WorkflowRejected || lastEvent.ActorID != creator.UserID || lastEvent.Reason == nil || *lastEvent.Reason != "需要修改" {
		t.Fatal("驳回状态或理由错误")
	}
}

func TestRevisionDerivativesProjectAndPreserveRelationOrder(t *testing.T) {
	targetModel := "mdl_target"
	fields := []schema.ContentField{{ID: "fld_score", Key: "score", Type: schema.FieldTypeInteger, Constraints: schema.FieldConstraints{Filterable: true}, Status: schema.StatusActive}, {ID: "fld_related", Key: "related", Type: schema.FieldTypeMultiRelation, Constraints: schema.FieldConstraints{TargetModelID: &targetModel}, Status: schema.StatusActive}}
	revision := Revision{ID: "rev_1", EntryID: "ent_1", ModelID: "mdl_1"}
	content, err := validateContent(json.RawMessage(`{"score":42,"related":["ent_2","ent_3"]}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	values, relations, err := revisionDerivatives(content, revision, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(values) != 1 || values[0].IntegerValue == nil || *values[0].IntegerValue != 42 || len(relations) != 2 || relations[1].Position != 1 {
		t.Fatalf("派生投影或关联错误: %#v %#v", values, relations)
	}
}

func BenchmarkProjectionPredicate(b *testing.B) {
	field := schema.ContentField{Type: schema.FieldTypeInteger}
	filter := PublishedFilter{Operator: "gte", Value: json.RawMessage(`42`)}
	for b.Loop() {
		projectionPredicate("f0", field, filter)
	}
}

func TestPublishedQueryValidationAndCursorBinding(t *testing.T) {
	fields := []schema.ContentField{{ID: "fld_score", Key: "score", Type: schema.FieldTypeInteger, Constraints: schema.FieldConstraints{Filterable: true, Sortable: true}, Status: schema.StatusActive}}
	if _, err := validatePublishedQuery(fields, PublishedQuery{Filters: []PublishedFilter{{FieldKey: "score", Operator: "eq", Value: json.RawMessage(`"42"`)}}}); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := projectionPredicate("f0", fields[0], PublishedFilter{Operator: "eq", Value: json.RawMessage(`"42"`)}); ok {
		t.Fatal("整数过滤不应接受字符串")
	}
	query := PublishedQuery{Limit: 20, Sort: []PublishedSort{{FieldKey: "score"}}}
	binding, _ := publishedQueryBinding("mdl_1", []string{"mdl_1"}, query)
	value, _ := encodePublishedCursor(publishedCursor{Binding: binding, Values: []*string{pointerString("42"), pointerString("ent_1")}})
	if _, err := decodePublishedCursor(value, binding); err != nil {
		t.Fatal(err)
	}
	other, _ := publishedQueryBinding("mdl_1", []string{"mdl_2"}, query)
	if _, err := decodePublishedCursor(value, other); err == nil {
		t.Fatal("游标不应跨模型授权范围复用")
	}
}

func pointerString(value string) *string { return &value }

func TestRepresentativeExplainUsesProjectionAndKeysetPlan(t *testing.T) {
	query, args := ExplainPublishedEntries("mdl_1", "fld_score", 20)
	if !strings.HasPrefix(query, "EXPLAIN ") || strings.Contains(query, "JSON_EXTRACT") || strings.Contains(query, " OFFSET ") || !strings.Contains(query, "content_field_values") || len(args) != 4 {
		t.Fatalf("代表性查询计划辅助不符合投影键集查询要求: %s", query)
	}
}

func TestCursorBindsListAndFilters(t *testing.T) {
	binding, _ := adminEntryQueryBinding("mdl_1", AdminEntryQuery{Status: StatusDraft, Limit: 20})
	entryCursor, err := encodeCursor(cursorEnvelope{Kind: "entries", Binding: binding, Values: []*string{pointerString("2026-07-18T00:00:00Z"), pointerString("ent_1")}})
	if err != nil {
		t.Fatal(err)
	}
	otherBinding, _ := adminEntryQueryBinding("mdl_1", AdminEntryQuery{Status: StatusArchived, Limit: 20})
	if _, err := decodeEntryCursor(entryCursor, otherBinding); err == nil {
		t.Fatal("条目游标不应跨筛选条件使用")
	}
	revisionCursor, _ := encodeCursor(cursorEnvelope{Kind: "revisions", ModelID: "mdl_1", EntryID: "ent_1", Number: 2})
	if _, err := decodeRevisionCursor(revisionCursor, "mdl_1", "ent_2"); err == nil {
		t.Fatal("Revision 游标不应跨条目使用")
	}
}

func TestAdminEntryQueryValidationAndCursorBindsAllConditions(t *testing.T) {
	target := "mdl_related"
	fields := []schema.ContentField{
		{ID: "fld_score", Key: "score", Type: schema.FieldTypeInteger, Constraints: schema.FieldConstraints{Filterable: true, Sortable: true}, Status: schema.StatusActive},
		{ID: "fld_related", Key: "related", Type: schema.FieldTypeMultiRelation, Constraints: schema.FieldConstraints{TargetModelID: &target}, Status: schema.StatusActive},
	}
	query := AdminEntryQuery{Status: StatusDraft, WorkflowStatus: workflowStatusPointer(WorkflowPendingReview), Limit: 20, Filters: []PublishedFilter{{FieldKey: "score", Operator: "gte", Value: json.RawMessage(`42`)}}, RelationFilters: []PublishedRelationFilter{{FieldKey: "related", EntryID: "ent_target"}}, Sort: []PublishedSort{{FieldKey: "score", Descending: true}}, Expand: []string{"related"}, IncludeTotal: true}
	if _, err := validateAdminEntryQuery(fields, query); err != nil {
		t.Fatal(err)
	}
	binding, _ := adminEntryQueryBinding("mdl_1", query)
	cursor, _ := encodeCursor(cursorEnvelope{Kind: "entries", Binding: binding, Values: []*string{pointerString("42"), pointerString("ent_1")}})
	changed := query
	changed.IncludeTotal = false
	otherBinding, _ := adminEntryQueryBinding("mdl_1", changed)
	if _, err := decodeEntryCursor(cursor, otherBinding); err == nil {
		t.Fatal("游标不应在 include_total 变化后复用")
	}
	changed = query
	changed.Expand = nil
	otherBinding, _ = adminEntryQueryBinding("mdl_1", changed)
	if _, err := decodeEntryCursor(cursor, otherBinding); err == nil {
		t.Fatal("游标不应在 expand 变化后复用")
	}
	if _, err := validateAdminEntryQuery(fields, AdminEntryQuery{Status: StatusDraft, Limit: 20, Filters: []PublishedFilter{{FieldKey: "score", Operator: "eq", Value: json.RawMessage(`"42"`)}}}); err == nil {
		t.Fatal("整数过滤不应接受字符串")
	}
	if _, err := validateAdminEntryQuery(fields, AdminEntryQuery{Status: StatusDraft, Limit: 20, Sort: []PublishedSort{{FieldKey: "related"}}}); err == nil {
		t.Fatal("未声明 sortable 的字段不应排序")
	}
}

func TestListEntriesRequiresViewPermissionForEveryExpandedTarget(t *testing.T) {
	targetModelID := "mdl_target"
	source := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{{ID: "fld_related", Key: "related", Type: schema.FieldTypeSingleRelation, Constraints: schema.FieldConstraints{TargetModelID: &targetModelID}, Status: schema.StatusActive}}}
	target := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: targetModelID, Status: schema.StatusActive}}
	repository := newMemoryRepository()
	service := NewService(Dependencies{Repository: repository, ModelRepository: memoryModels{models: map[string]schema.ContentModel{"mdl_1": source, targetModelID: target}}})
	principal := contentPrincipal(permissionView)

	_, err := service.ListEntries(context.Background(), principal, "mdl_1", AdminEntryQuery{Status: StatusDraft, Limit: 20, Expand: []string{"related"}})
	assertApplicationCode(t, err, "permission_denied")
	if repository.listCalls != 0 || repository.expandCalls != 0 {
		t.Fatalf("目标模型授权失败前不应查询或展开条目: list=%d expand=%d", repository.listCalls, repository.expandCalls)
	}

	principal = identity.NewPrincipal("usr_1", "用户", nil, identity.AuthMethodSMS, nil, []identity.ModelPermissions{
		{ModelID: "mdl_1", Permissions: []string{permissionView}},
		{ModelID: targetModelID, Permissions: []string{permissionView}},
	})
	if _, err = service.ListEntries(context.Background(), principal, "mdl_1", AdminEntryQuery{Status: StatusDraft, Limit: 20, Expand: []string{"related"}}); err != nil {
		t.Fatal(err)
	}
	if repository.listCalls != 1 || repository.expandCalls != 1 {
		t.Fatalf("授权后应正常查询并展开: list=%d expand=%d", repository.listCalls, repository.expandCalls)
	}
}

func TestListEntriesReturnsDraftContentAndActiveRootFieldDefinitions(t *testing.T) {
	service, _, _, _ := testService()
	created, err := service.CreateEntry(context.Background(), contentPrincipal(permissionCreate), testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"列表标题"}`)})
	if err != nil {
		t.Fatal(err)
	}
	models := service.models.(memoryModels)
	models.model.Fields = append(models.model.Fields,
		schema.ContentField{ID: "fld_kind", Key: "kind", DisplayName: "类型", Type: schema.FieldTypeSingleSelect, Constraints: schema.FieldConstraints{EnumOptions: []schema.EnumOption{{Value: "news", Label: "新闻"}}, Filterable: true, Sortable: true}, Status: schema.StatusActive},
		schema.ContentField{ID: "fld_group", Key: "group", DisplayName: "分组", Type: schema.FieldTypeObject, Status: schema.StatusActive, Children: []schema.ContentField{{ID: "fld_child", Key: "child", DisplayName: "子字段", Type: schema.FieldTypeSingleLineText, Status: schema.StatusActive}, {ID: "fld_old_child", Key: "old_child", Status: schema.StatusArchived}}},
		schema.ContentField{ID: "fld_old", Key: "old", DisplayName: "旧字段", Type: schema.FieldTypeSingleLineText, Status: schema.StatusArchived},
	)
	service.models = models
	resolver := &memoryAssetResolver{items: map[string]map[string]ReferencedAsset{created.CurrentDraftRevisionID: {"ast_1": {ID: "ast_1", Filename: "a.png", MimeType: "image/png", Size: 10, Status: "available", PreviewKind: "image"}}}}
	service.assets = resolver

	result, err := service.ListEntries(context.Background(), contentPrincipal(permissionView), "mdl_1", AdminEntryQuery{Status: StatusDraft, Limit: 20})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 1 || result.Items[0].ID != created.ID || string(result.Items[0].CurrentDraftContent) != `{"title":"列表标题"}` {
		t.Fatalf("列表未返回当前草稿内容: %#v", result.Items)
	}
	if len(result.Fields) != 3 || result.Fields[0].Key != "title" || result.Fields[1].Key != "kind" || len(result.Fields[1].Constraints.EnumOptions) != 1 || !result.Fields[1].Constraints.Filterable || !result.Fields[1].Constraints.Sortable || len(result.Fields[2].Children) != 1 || result.Fields[2].Children[0].Key != "child" {
		t.Fatalf("列表字段定义未按活动根字段裁剪: %#v", result.Fields)
	}
	if resolver.calls != 1 || result.Items[0].ReferencedAssets["ast_1"].PreviewKind != "image" {
		t.Fatalf("列表素材引用未一次批量解析: calls=%d items=%#v", resolver.calls, result.Items[0].ReferencedAssets)
	}
}

func TestPublishedContentUsesOnlyActiveRootFields(t *testing.T) {
	fields := []schema.ContentField{
		{Key: "title", Status: schema.StatusActive},
		{Key: "old", Status: schema.StatusArchived},
		{Key: "group", Status: schema.StatusActive, Children: []schema.ContentField{{Key: "old_child", Status: schema.StatusArchived}}},
	}
	content, err := activeRootContent(json.RawMessage(`{"title":"可见","old":"归档","group":{"old_child":"仍保留"},"unknown":true}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != `{"group":{"old_child":"仍保留"},"title":"可见"}` {
		t.Fatalf("发布 content 未按 active 根字段裁剪: %s", content)
	}
}

func TestExpandedEntryIsNonRecursiveAndOmitsRevisionNumber(t *testing.T) {
	value := PublishedEntry{Expanded: map[string]any{"related": ExpandedEntry{ID: "ent_2", ModelID: "mdl_2", ModelKey: "target", RevisionID: "rev_2", Content: json.RawMessage(`{}`)}}}
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(encoded, &document); err != nil {
		t.Fatal(err)
	}
	target := document["expanded"].(map[string]any)["related"].(map[string]any)
	if _, ok := target["revision_number"]; ok {
		t.Fatal("ExpandedEntry 不应包含 revision_number")
	}
	if _, ok := target["expanded"]; ok {
		t.Fatal("ExpandedEntry 不应递归包含 expanded")
	}
}

func TestRelationTargetsHaveStableModelEntryLockOrder(t *testing.T) {
	values := []Relation{
		{TargetModelID: "mdl_b", TargetEntryID: "ent_2"},
		{TargetModelID: "mdl_a", TargetEntryID: "ent_3"},
		{TargetModelID: "mdl_a", TargetEntryID: "ent_1"},
		{TargetModelID: "mdl_a", TargetEntryID: "ent_1"},
	}
	targets := orderedRelationTargets(values)
	got := make([]string, len(targets))
	for i, target := range targets {
		got[i] = target.TargetModelID + "/" + target.TargetEntryID
	}
	if strings.Join(got, ",") != "mdl_a/ent_1,mdl_a/ent_3,mdl_b/ent_2" {
		t.Fatalf("关联目标锁顺序不稳定: %v", got)
	}
}

func TestCreateLocksSourceAndRelationModelsBeforeValidatingTargets(t *testing.T) {
	targetModelID := "mdl_target"
	source := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{{ID: "fld_related", Key: "related", Type: schema.FieldTypeSingleRelation, Constraints: schema.FieldConstraints{TargetModelID: &targetModelID}, Status: schema.StatusActive}}}
	target := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: targetModelID, Status: schema.StatusActive}}
	repository, lockedIDs := newMemoryRepository(), []string{}
	repository.entries["ent_target"] = EntrySummary{ID: "ent_target", ModelID: targetModelID, Status: StatusDraft}
	service := NewService(Dependencies{Repository: repository, Transactor: &directTx{}, ModelRepository: memoryModels{models: map[string]schema.ContentModel{"mdl_1": source, targetModelID: target}, lockedModelIDs: &lockedIDs}, Audit: &auditRecorder{}})
	service.newID = func(prefix string) (string, error) { return prefix + "1", nil }

	if _, err := service.CreateEntry(context.Background(), contentPrincipal(permissionCreate), testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"related":"ent_target"}`)}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(lockedIDs, ",") != "mdl_1,mdl_target" {
		t.Fatalf("必须先统一锁住源和目标模型，实际为 %v", lockedIDs)
	}
}

func TestCreateLocksRelationTargetsBeforeAssets(t *testing.T) {
	targetModelID := "mdl_target"
	events := []string{}
	model := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{
		{ID: "fld_related", Key: "related", Type: schema.FieldTypeSingleRelation, Constraints: schema.FieldConstraints{TargetModelID: &targetModelID}, Status: schema.StatusActive},
		{ID: "fld_image", Key: "image", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive},
	}}
	repository := newMemoryRepository()
	repository.lockEvents = &events
	repository.entries["ent_target"] = EntrySummary{ID: "ent_target", ModelID: targetModelID, Status: StatusDraft}
	service := NewService(Dependencies{Repository: repository, Transactor: &directTx{}, ModelRepository: memoryModels{models: map[string]schema.ContentModel{"mdl_1": model, targetModelID: {ContentModelSummary: schema.ContentModelSummary{ID: targetModelID, Status: schema.StatusActive}}}}, Media: mediaRecorder{events: &events}, Audit: &auditRecorder{}})
	service.newID = func(prefix string) (string, error) { return prefix + "1", nil }

	if _, err := service.CreateEntry(context.Background(), contentPrincipal(permissionCreate), testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"related":"ent_target","image":"ast_1"}`)}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "target,asset,insert_asset_reference" {
		t.Fatalf("锁顺序应为关系目标、素材，实际为 %v", events)
	}
}

func TestUpdateLocksSourceEntryThenRelationTargetsThenAssets(t *testing.T) {
	targetModelID := "mdl_target"
	events := []string{}
	model := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{
		{ID: "fld_related", Key: "related", Type: schema.FieldTypeSingleRelation, Constraints: schema.FieldConstraints{TargetModelID: &targetModelID}, Status: schema.StatusActive},
		{ID: "fld_image", Key: "image", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive},
	}}
	repository := newMemoryRepository()
	repository.lockEvents = &events
	repository.entries["ent_source"] = EntrySummary{ID: "ent_source", ModelID: "mdl_1", Status: StatusDraft, CurrentDraftRevisionID: "rev_base"}
	repository.entries["ent_target"] = EntrySummary{ID: "ent_target", ModelID: targetModelID, Status: StatusDraft}
	repository.revisions["ent_source"] = []Revision{{ID: "rev_base", EntryID: "ent_source", ModelID: "mdl_1", Number: 1, Content: json.RawMessage(`{}`), WorkflowStatus: WorkflowDraft}}
	service := NewService(Dependencies{Repository: repository, Transactor: &directTx{}, ModelRepository: memoryModels{models: map[string]schema.ContentModel{"mdl_1": model, targetModelID: {ContentModelSummary: schema.ContentModelSummary{ID: targetModelID, Status: schema.StatusActive}}}}, Media: mediaRecorder{events: &events}, Audit: &auditRecorder{}})
	service.newID = func(prefix string) (string, error) { return prefix + "next", nil }

	_, err := service.UpdateEntry(context.Background(), contentPrincipal(permissionUpdate), testMeta(), "mdl_1", "ent_source", UpdateEntryRequest{BaseRevisionID: "rev_base", Content: json.RawMessage(`{"related":"ent_target","image":"ast_1"}`)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(events[:3], ",") != "entry,target,asset" {
		t.Fatalf("更新锁顺序应为源条目、关系目标、素材，实际为 %v", events)
	}
}

func TestImportPrechecksAllRelationTargetsBeforeAnyAsset(t *testing.T) {
	targetModelID := "mdl_target"
	events := []string{}
	model := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{
		{ID: "fld_related", Key: "related", Type: schema.FieldTypeSingleRelation, Constraints: schema.FieldConstraints{TargetModelID: &targetModelID}, Status: schema.StatusActive},
		{ID: "fld_image", Key: "image", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive},
	}}
	repository := newMemoryRepository()
	repository.lockEvents = &events
	repository.entries["ent_a"] = EntrySummary{ID: "ent_a", ModelID: targetModelID, Status: StatusDraft}
	repository.entries["ent_b"] = EntrySummary{ID: "ent_b", ModelID: targetModelID, Status: StatusDraft}
	service := NewService(Dependencies{Repository: repository, Transactor: &directTx{}, ModelRepository: memoryModels{models: map[string]schema.ContentModel{"mdl_1": model, targetModelID: {ContentModelSummary: schema.ContentModelSummary{ID: targetModelID, Status: schema.StatusActive}}}}, Media: mediaRecorder{events: &events}, Audit: &auditRecorder{}})
	service.newID = func(prefix string) (string, error) {
		return fmt.Sprintf("%s%d", prefix, len(repository.revisions)+len(repository.entries)), nil
	}

	err := service.ImportDrafts(context.Background(), contentPrincipal(permissionCreate), testMeta(), "mdl_1", func(yield func(ImportDraft) error) error {
		if err := yield(ImportDraft{Content: json.RawMessage(`{"related":"ent_b","image":"ast_b"}`)}); err != nil {
			return err
		}
		return yield(ImportDraft{Content: json.RawMessage(`{"related":"ent_a","image":"ast_a"}`)})
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 || events[0] != "target" || events[1] != "asset" {
		t.Fatalf("批量保存必须先锁全部关系目标再锁素材，实际为 %v", events)
	}
}

func TestAdminEntriesSQLUsesDraftPointerProjectionRelationsAndWorkflow(t *testing.T) {
	fields := map[string]schema.ContentField{
		"score":   {ID: "fld_score", Key: "score", Type: schema.FieldTypeInteger},
		"related": {ID: "fld_related", Key: "related", Type: schema.FieldTypeMultiRelation},
	}
	query := AdminEntryQuery{Status: StatusDraft, WorkflowStatus: workflowStatusPointer(WorkflowRejected), Filters: []PublishedFilter{{FieldKey: "score", Operator: "gte", Value: json.RawMessage(`10`)}}, RelationFilters: []PublishedRelationFilter{{FieldKey: "related", EntryID: "ent_target"}}, Sort: []PublishedSort{{FieldKey: "score", Descending: true}}}
	from, joinArgs, where, whereArgs, orders := adminEntriesSQL("mdl_1", query, fields)
	joined := from + strings.Join(where, " ")
	for _, required := range []string{"content_draft_pointers", "content_revisions rv ON rv.id=p.revision_id", "content_field_values", "content_relations", "rv.workflow_status=?"} {
		if !strings.Contains(joined, required) {
			t.Fatalf("管理查询缺少 %s: %s", required, joined)
		}
	}
	if strings.Contains(joined, "JSON_EXTRACT") || len(joinArgs) != 4 || len(whereArgs) != 4 || len(orders) != 2 || orders[1].Expression != "e.id" || !orders[1].Descending {
		t.Fatalf("管理查询参数或稳定排序错误: %#v %#v %#v", joinArgs, whereArgs, orders)
	}
}

func TestIncludeTotalCapsAtTenThousandAsEstimate(t *testing.T) {
	repository := newMemoryRepository()
	for i := 0; i < 10001; i++ {
		id := fmt.Sprintf("ent_%05d", i)
		repository.entries[id] = EntrySummary{ID: id, ModelID: "mdl_1", Status: StatusDraft}
	}
	total, estimate, err := repository.CountEntries(context.Background(), nil, "mdl_1", AdminEntryQuery{Status: StatusDraft}, nil)
	if err != nil || total != 10000 || !estimate {
		t.Fatalf("计数上限错误: total=%d estimate=%v err=%v", total, estimate, err)
	}
}

func workflowStatusPointer(value WorkflowStatus) *WorkflowStatus { return &value }

func testService() (*Service, *memoryRepository, *directTx, *auditRecorder) {
	repository, tx, audits := newMemoryRepository(), &directTx{}, &auditRecorder{}
	model := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{{ID: "fld_title", Key: "title", Type: schema.FieldTypeSingleLineText, Required: true, DefaultValue: json.RawMessage("null"), Status: schema.StatusActive}}}
	service := NewService(Dependencies{Repository: repository, Transactor: tx, ModelRepository: memoryModels{model: model}, Audit: audits})
	counters := map[string]int{}
	service.newID = func(prefix string) (string, error) {
		counters[prefix]++
		return fmt.Sprintf("%s%d", prefix, counters[prefix]), nil
	}
	service.now = func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) }
	return service, repository, tx, audits
}

func contentPrincipal(permissions ...string) identity.Principal {
	return identity.NewPrincipal("usr_1", "用户", nil, identity.AuthMethodSMS, nil, []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: permissions}})
}

func testMeta() RequestMeta { return RequestMeta{RequestID: "req_1", IP: "127.0.0.1"} }

func assertApplicationCode(t *testing.T, err error, code string) {
	t.Helper()
	var applicationError *apperror.Error
	if !errors.As(err, &applicationError) || applicationError.Code != code {
		t.Fatalf("期望错误码 %s，得到 %v", code, err)
	}
}
