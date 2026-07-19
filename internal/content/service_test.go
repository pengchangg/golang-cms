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
	entries    map[string]EntrySummary
	revisions  map[string][]Revision
	unique     map[string]map[string]string
	created    int
	updated    int
	lockEvents *[]string
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{entries: map[string]EntrySummary{}, revisions: map[string][]Revision{}, unique: map[string]map[string]string{}}
}

func (r *memoryRepository) HasAnyContent(_ context.Context, _ database.Querier, modelID string) (bool, error) {
	for _, entry := range r.entries {
		if entry.ModelID == modelID {
			return true, nil
		}
	}
	return false, nil
}
func (r *memoryRepository) ListEntries(_ context.Context, _ database.Querier, modelID string, status EntryStatus, limit int, cursor *EntryCursor) ([]EntrySummary, error) {
	items := []EntrySummary{}
	for _, item := range r.entries {
		if item.ModelID == modelID && item.Status == status && (cursor == nil || item.UpdatedAt.Before(cursor.UpdatedAt) || item.UpdatedAt.Equal(cursor.UpdatedAt) && item.ID < cursor.ID) {
			items = append(items, item)
		}
	}
	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}
func (r *memoryRepository) GetEntry(_ context.Context, _ database.Querier, modelID, entryID string) (Entry, error) {
	entry, ok := r.entries[entryID]
	if !ok || entry.ModelID != modelID {
		return Entry{}, notFound("内容条目")
	}
	revisions := r.revisions[entryID]
	for _, revision := range revisions {
		if revision.ID == entry.CurrentDraftRevisionID {
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
	model      schema.ContentModel
	lockEvents *[]string
}

func (m memoryModels) GetModel(context.Context, database.Querier, string) (schema.ContentModel, error) {
	return m.model, nil
}
func (m memoryModels) LockModel(context.Context, database.Querier, string) (schema.ContentModelSummary, error) {
	if m.lockEvents != nil {
		*m.lockEvents = append(*m.lockEvents, "model")
	}
	return m.model.ContentModelSummary, nil
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
	principal := identity.NewPrincipal("usr_1", "用户", nil, identity.AuthMethodOIDC, []string{"models.create"}, nil)
	_, err := service.CreateEntry(context.Background(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"内容"}`)})
	assertApplicationCode(t, err, "permission_denied")
}

func TestCursorBindsListAndFilters(t *testing.T) {
	entryCursor, err := encodeCursor(cursorEnvelope{Kind: "entries", ModelID: "mdl_1", Status: "draft", UpdatedAt: "2026-07-18T00:00:00Z", ID: "ent_1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeEntryCursor(entryCursor, "mdl_1", StatusArchived); err == nil {
		t.Fatal("条目游标不应跨筛选条件使用")
	}
	revisionCursor, _ := encodeCursor(cursorEnvelope{Kind: "revisions", ModelID: "mdl_1", EntryID: "ent_1", Number: 2})
	if _, err := decodeRevisionCursor(revisionCursor, "mdl_1", "ent_2"); err == nil {
		t.Fatal("Revision 游标不应跨条目使用")
	}
}

func testService() (*Service, *memoryRepository, *directTx, *auditRecorder) {
	repository, tx, audits := newMemoryRepository(), &directTx{}, &auditRecorder{}
	model := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", Status: schema.StatusActive}, Fields: []schema.ContentField{{ID: "fld_title", Key: "title", Type: schema.FieldTypeSingleLineText, Required: true, DefaultValue: json.RawMessage("null"), Status: schema.StatusActive}}}
	service := NewService(Dependencies{Repository: repository, Transactor: tx, ModelRepository: memoryModels{model: model}, Audit: audits})
	ids := []string{"ent_1", "rev_1", "evt_1", "rev_2", "evt_2", "evt_3"}
	service.newID = func(string) (string, error) { id := ids[0]; ids = ids[1:]; return id, nil }
	service.now = func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) }
	return service, repository, tx, audits
}

func contentPrincipal(permissions ...string) identity.Principal {
	return identity.NewPrincipal("usr_1", "用户", nil, identity.AuthMethodOIDC, nil, []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: permissions}})
}

func testMeta() RequestMeta { return RequestMeta{RequestID: "req_1", IP: "127.0.0.1"} }

func assertApplicationCode(t *testing.T, err error, code string) {
	t.Helper()
	var applicationError *apperror.Error
	if !errors.As(err, &applicationError) || applicationError.Code != code {
		t.Fatalf("期望错误码 %s，得到 %v", code, err)
	}
}
