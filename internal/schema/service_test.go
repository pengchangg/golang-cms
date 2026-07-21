package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

func TestCreateModelAuthorizationWriteAndAuditShareTransaction(t *testing.T) {
	state := &transactionState{}
	repository := &memoryRepository{}
	service := testService(repository, state)
	result, err := service.CreateModel(context.Background(), testPrincipal(), RequestMeta{RequestID: "req_1", IP: "127.0.0.1", UserAgent: "test"}, CreateContentModelRequest{Key: "articles", DisplayName: "Articles"})
	if err != nil {
		t.Fatalf("CreateModel() error = %v", err)
	}
	if result.ID != "mdl_1" || len(repository.models) != 1 || state.commits != 1 {
		t.Fatalf("result/state = %#v/%#v", result, state)
	}
	if !state.authorizedInTx || !state.auditInTx {
		t.Fatalf("authorization/audit transaction = %#v", state)
	}
}

func TestCreateModelRollsBackWhenAuditFails(t *testing.T) {
	state := &transactionState{auditErr: errors.New("audit unavailable")}
	repository := &memoryRepository{}
	service := testService(repository, state)
	_, err := service.CreateModel(context.Background(), testPrincipal(), RequestMeta{}, CreateContentModelRequest{Key: "articles", DisplayName: "Articles"})
	if err == nil || state.rollbacks != 1 || state.commits != 0 {
		t.Fatalf("err/state = %v/%#v", err, state)
	}
}

func TestCreateModelPermissionDeniedBeforeWriteAndAudit(t *testing.T) {
	state := &transactionState{authorizeErr: &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}}
	repository := &memoryRepository{}
	service := testService(repository, state)
	_, err := service.CreateModel(context.Background(), testPrincipal(), RequestMeta{}, CreateContentModelRequest{Key: "articles", DisplayName: "Articles"})
	assertErrorCode(t, err, "permission_denied")
	if len(repository.models) != 0 || state.auditInTx || state.rollbacks != 1 {
		t.Fatalf("repository/state = %#v/%#v", repository.models, state)
	}
}

func TestUpdateFieldTypeIsLockedAfterContent(t *testing.T) {
	state := &transactionState{}
	repository := seededRepository()
	service := testService(repository, state)
	service.content = contentChecker{exists: true}
	newType := FieldTypeInteger
	defaultValue := json.RawMessage(`null`)
	constraints := FieldConstraints{}
	children := []ContentFieldInput{}
	_, err := service.UpdateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", "fld_1", ContentFieldPatch{Type: &newType, DefaultValue: &defaultValue, Constraints: &constraints, Children: &children})
	assertErrorCode(t, err, "field_type_locked")
	if repository.fields["fld_1"].Type != FieldTypeSingleLineText || state.rollbacks != 1 {
		t.Fatalf("field/state = %#v/%#v", repository.fields["fld_1"], state)
	}
}

func TestUpdateFieldTypeRequiresAffectedProperties(t *testing.T) {
	service := testService(seededRepository(), &transactionState{})
	newType := FieldTypeInteger
	_, err := service.UpdateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", "fld_1", ContentFieldPatch{Type: &newType})
	details := validationDetails(t, err)
	if len(details) != 3 {
		t.Fatalf("details = %#v", details)
	}
}

func TestArchivedResourcesCannotBeChangedOrArchivedTwice(t *testing.T) {
	state := &transactionState{}
	repository := seededRepository()
	model := repository.models["mdl_1"]
	model.Status = StatusArchived
	repository.models["mdl_1"] = model
	service := testService(repository, state)
	err := service.ArchiveModel(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1")
	assertErrorCode(t, err, "resource_archived")
	_, err = service.CreateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", fieldInput(FieldTypeBoolean, `null`, FieldConstraints{}))
	assertErrorCode(t, err, "resource_archived")
}

func TestArchiveFieldArchivesChildren(t *testing.T) {
	state := &transactionState{}
	repository := seededRepository()
	child := repository.fields["fld_1"]
	child.Children = []ContentField{{ID: "fld_child", Key: "nested", Status: StatusActive}}
	repository.fields["fld_1"] = child
	repository.fields["fld_child"] = child.Children[0]
	service := testService(repository, state)
	if err := service.ArchiveField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", "fld_1"); err != nil {
		t.Fatal(err)
	}
	if repository.fields["fld_1"].Status != StatusArchived || repository.fields["fld_child"].Status != StatusArchived {
		t.Fatalf("fields = %#v", repository.fields)
	}
}

func TestEnumValuesCannotBeRemoved(t *testing.T) {
	state := &transactionState{}
	repository := seededRepository()
	field := repository.fields["fld_1"]
	field.Type = FieldTypeSingleSelect
	field.Constraints.EnumOptions = []EnumOption{{Value: "stable", Label: "Old"}}
	repository.fields["fld_1"] = field
	service := testService(repository, state)
	constraints := FieldConstraints{EnumOptions: []EnumOption{{Value: "replacement", Label: "New"}}}
	_, err := service.UpdateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", "fld_1", ContentFieldPatch{Constraints: &constraints})
	assertErrorCode(t, err, "enum_value_immutable")
}

func TestCreateFieldLocksRelationModelsInStableOrder(t *testing.T) {
	repository := seededRepository()
	repository.models["mdl_a"] = ContentModelSummary{ID: "mdl_a", Status: StatusActive}
	repository.models["mdl_z"] = ContentModelSummary{ID: "mdl_z", Status: StatusActive}
	targetA, targetZ := "mdl_a", "mdl_z"
	input := fieldInput(FieldTypeObject, `null`, FieldConstraints{},
		ContentFieldInput{Key: "relation_z", DisplayName: "Z", Type: FieldTypeSingleRelation, DefaultValue: json.RawMessage(`null`), Constraints: FieldConstraints{TargetModelID: &targetZ}, Children: []ContentFieldInput{}},
		ContentFieldInput{Key: "relation_a", DisplayName: "A", Type: FieldTypeSingleRelation, DefaultValue: json.RawMessage(`null`), Constraints: FieldConstraints{TargetModelID: &targetA}, Children: []ContentFieldInput{}},
	)
	if _, err := testService(repository, &transactionState{}).CreateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", input); err != nil {
		t.Fatal(err)
	}
	want := []string{"mdl_1", "mdl_a", "mdl_z"}
	if !reflect.DeepEqual(repository.lockedIDs, want) {
		t.Fatalf("locked IDs = %#v, want %#v", repository.lockedIDs, want)
	}
}

func TestRelationLockInfrastructureErrorIsNotConflict(t *testing.T) {
	infrastructureErr := errors.New("database unavailable")
	repository := seededRepository()
	repository.lockModelsErr = infrastructureErr
	target := "mdl_target"
	_, err := testService(repository, &transactionState{}).CreateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", fieldInput(FieldTypeSingleRelation, `null`, FieldConstraints{TargetModelID: &target}))
	if !errors.Is(err, infrastructureErr) {
		t.Fatalf("error = %v, want infrastructure error", err)
	}
	var appErr *apperror.Error
	if errors.As(err, &appErr) && appErr.Kind == apperror.KindConflict {
		t.Fatalf("infrastructure error was converted to conflict: %v", err)
	}
}

func TestCreateFieldRejectsSelfRelation(t *testing.T) {
	target := "mdl_1"
	_, err := testService(seededRepository(), &transactionState{}).CreateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", fieldInput(FieldTypeSingleRelation, `null`, FieldConstraints{TargetModelID: &target}))
	assertErrorCode(t, err, "target_model_self_relation")
}

func TestUpdateFieldCanPreserveExistingSelfRelation(t *testing.T) {
	repository := seededRepository()
	target := "mdl_1"
	field := repository.fields["fld_1"]
	field.Type = FieldTypeSingleRelation
	field.Constraints.TargetModelID = &target
	repository.fields[field.ID] = field
	displayName := "保留的历史自关联"
	result, err := testService(repository, &transactionState{}).UpdateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", field.ID, ContentFieldPatch{DisplayName: &displayName})
	if err != nil {
		t.Fatal(err)
	}
	if result.DisplayName != displayName {
		t.Fatalf("display name = %q", result.DisplayName)
	}
}

func TestUpdateFieldCanPreserveExistingSelfRelationWithConstraintsPatch(t *testing.T) {
	repository := seededRepository()
	target := "mdl_1"
	field := repository.fields["fld_1"]
	field.Type = FieldTypeSingleRelation
	field.Constraints.TargetModelID = &target
	repository.fields[field.ID] = field
	constraints := field.Constraints
	if _, err := testService(repository, &transactionState{}).UpdateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", field.ID, ContentFieldPatch{Constraints: &constraints}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateFieldResponseIncludesArchivedChildren(t *testing.T) {
	repository := seededRepository()
	parent := repository.fields["fld_1"]
	parent.Type = FieldTypeObject
	parent.Children = []ContentField{{ID: "fld_child", Key: "nested", DisplayName: "Nested", Type: FieldTypeSingleLineText, DefaultValue: json.RawMessage(`null`), Children: []ContentField{}, Status: StatusActive, Depth: 1}}
	repository.fields[parent.ID] = parent
	repository.fields["fld_child"] = parent.Children[0]
	children := []ContentFieldInput{{Key: "replacement", DisplayName: "Replacement", Type: FieldTypeSingleLineText, DefaultValue: json.RawMessage(`null`), Constraints: FieldConstraints{}, Children: []ContentFieldInput{}}}
	service := testService(&rereadRepository{repository}, &transactionState{})
	service.newID = func(string) (string, error) { return "fld_new", nil }
	result, err := service.UpdateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", "fld_1", ContentFieldPatch{Children: &children})
	if err != nil {
		t.Fatal(err)
	}
	foundArchived := false
	for _, child := range result.Children {
		foundArchived = foundArchived || child.Key == "nested" && child.Status == StatusArchived
	}
	if !foundArchived {
		t.Fatalf("response children = %#v", result.Children)
	}
}

func TestUpdateFieldOrderUsesCompleteSiblingSetAndBaseline(t *testing.T) {
	repository := seededRepository()
	repository.fields["fld_2"] = ContentField{ID: "fld_2", Key: "summary", Status: StatusActive}
	repository.positions["fld_1"] = 0
	repository.positions["fld_2"] = 1
	state := &transactionState{}
	service := testService(repository, state)
	request := UpdateFieldOrderRequest{ParentID: nil, BaseFieldIDs: []string{"fld_1", "fld_2"}, FieldIDs: []string{"fld_2", "fld_1"}, parentSet: true}
	if err := service.UpdateFieldOrder(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", request); err != nil {
		t.Fatal(err)
	}
	if repository.positions["fld_2"] != 0 || repository.positions["fld_1"] != 1 || state.commits != 1 || !state.auditInTx {
		t.Fatalf("positions/state = %#v/%#v", repository.positions, state)
	}

	request.BaseFieldIDs = []string{"fld_1", "fld_2"}
	err := service.UpdateFieldOrder(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", request)
	assertErrorCode(t, err, "field_order_conflict")
	if repository.positions["fld_2"] != 0 || repository.positions["fld_1"] != 1 {
		t.Fatalf("conflict changed positions = %#v", repository.positions)
	}
}

func TestUpdateFieldOrderRejectsCrossParentAndArchivedFields(t *testing.T) {
	repository := seededRepository()
	parentID := "fld_parent"
	repository.fields[parentID] = ContentField{ID: parentID, Key: "group", Status: StatusActive}
	repository.fields["fld_child"] = ContentField{ID: "fld_child", Key: "child", Status: StatusActive, Depth: 1}
	repository.parents["fld_child"] = &parentID
	repository.fields["fld_archived"] = ContentField{ID: "fld_archived", Key: "old", Status: StatusArchived}
	service := testService(repository, &transactionState{})

	for _, fieldIDs := range [][]string{{"fld_1", "fld_child"}, {"fld_1", "fld_archived"}} {
		err := service.UpdateFieldOrder(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", UpdateFieldOrderRequest{BaseFieldIDs: []string{"fld_1"}, FieldIDs: fieldIDs, parentSet: true})
		assertErrorCode(t, err, "field_order_conflict")
	}
}

func TestCreateRootFieldAppendsAfterPositionGap(t *testing.T) {
	repository := seededRepository()
	repository.positions["fld_1"] = 3
	service := testService(repository, &transactionState{})
	service.newID = func(string) (string, error) { return "fld_new", nil }
	if _, err := service.CreateField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", fieldInput(FieldTypeBoolean, `null`, FieldConstraints{})); err != nil {
		t.Fatal(err)
	}
	if repository.positions["fld_new"] != 4 {
		t.Fatalf("new field position = %d", repository.positions["fld_new"])
	}
}

func TestCreateChildFieldAppendsAndLocksRelationModelsInStableOrder(t *testing.T) {
	repository := seededRepository()
	parent := repository.fields["fld_1"]
	parent.Type = FieldTypeObject
	repository.fields[parent.ID] = parent
	repository.models["mdl_a"] = ContentModelSummary{ID: "mdl_a", Status: StatusActive}
	repository.models["mdl_z"] = ContentModelSummary{ID: "mdl_z", Status: StatusActive}
	parentID := parent.ID
	repository.fields["fld_existing"] = ContentField{ID: "fld_existing", Key: "existing", Status: StatusActive, Depth: 1}
	repository.parents["fld_existing"] = &parentID
	repository.positions["fld_existing"] = 4
	targetA, targetZ := "mdl_a", "mdl_z"
	input := fieldInput(FieldTypeObject, `null`, FieldConstraints{},
		ContentFieldInput{Key: "relation_z", DisplayName: "Z", Type: FieldTypeSingleRelation, DefaultValue: json.RawMessage(`null`), Constraints: FieldConstraints{TargetModelID: &targetZ}, Children: []ContentFieldInput{}},
		ContentFieldInput{Key: "relation_a", DisplayName: "A", Type: FieldTypeSingleRelation, DefaultValue: json.RawMessage(`null`), Constraints: FieldConstraints{TargetModelID: &targetA}, Children: []ContentFieldInput{}},
	)
	state := &transactionState{}
	service := testService(repository, state)
	nextID := 0
	service.newID = func(string) (string, error) {
		nextID++
		return fmt.Sprintf("fld_new_%d", nextID), nil
	}
	result, err := service.CreateChildField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", parent.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if result.ID != "fld_new_1" || repository.positions[result.ID] != 5 || repository.parents[result.ID] == nil || *repository.parents[result.ID] != parent.ID {
		t.Fatalf("result/position/parent = %#v/%d/%#v", result, repository.positions[result.ID], repository.parents[result.ID])
	}
	if want := []string{"mdl_1", "mdl_a", "mdl_z"}; !reflect.DeepEqual(repository.lockedIDs, want) {
		t.Fatalf("locked IDs = %#v, want %#v", repository.lockedIDs, want)
	}
	if state.requiredPermission != "models.update" || !state.auditInTx || state.commits != 1 {
		t.Fatalf("transaction state = %#v", state)
	}
}

func TestCreateChildFieldReturnsKeyConflict(t *testing.T) {
	repository := seededRepository()
	parent := repository.fields["fld_1"]
	parent.Type = FieldTypeObject
	repository.fields[parent.ID] = parent
	_, err := testService(repository, &transactionState{}).CreateChildField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", parent.ID, ContentFieldInput{Key: parent.Key, DisplayName: "Duplicate", Type: FieldTypeBoolean, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{}})
	assertErrorCode(t, err, "key_conflict")
}

func TestCreateChildFieldRejectsInvalidParentAndDepth(t *testing.T) {
	for _, test := range []struct {
		name   string
		status ResourceStatus
		type_  FieldType
		depth  int
		code   string
	}{
		{name: "archived", status: StatusArchived, type_: FieldTypeObject, code: "resource_archived"},
		{name: "leaf", status: StatusActive, type_: FieldTypeSingleLineText, code: "parent_field_invalid"},
		{name: "depth", status: StatusActive, type_: FieldTypeObject, depth: 2, code: "parent_field_invalid"},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := seededRepository()
			parent := repository.fields["fld_1"]
			parent.Status, parent.Type, parent.Depth = test.status, test.type_, test.depth
			repository.fields[parent.ID] = parent
			_, err := testService(repository, &transactionState{}).CreateChildField(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", parent.ID, fieldInput(FieldTypeBoolean, `null`, FieldConstraints{}))
			assertErrorCode(t, err, test.code)
		})
	}
}

func TestUpdateFieldOrderRejectsNonContainerOrMaxDepthParent(t *testing.T) {
	for _, parent := range []ContentField{
		{ID: "fld_parent", Key: "leaf", Type: FieldTypeSingleLineText, Status: StatusActive},
		{ID: "fld_parent", Key: "nested", Type: FieldTypeObject, Status: StatusActive, Depth: 2},
	} {
		repository := seededRepository()
		repository.fields[parent.ID] = parent
		parentID := parent.ID
		err := testService(repository, &transactionState{}).UpdateFieldOrder(context.Background(), testPrincipal(), RequestMeta{}, "mdl_1", UpdateFieldOrderRequest{ParentID: &parentID, BaseFieldIDs: []string{}, FieldIDs: []string{}, parentSet: true})
		assertErrorCode(t, err, "field_order_conflict")
	}
}

type transactionState struct {
	active, authorizedInTx, auditInTx bool
	commits, rollbacks                int
	auditErr                          error
	authorizeErr                      error
	requiredPermission                string
}
type fakeTransactor struct{ state *transactionState }

func (f fakeTransactor) WithinTx(_ context.Context, _ *sql.TxOptions, fn func(database.Querier) error) error {
	f.state.active = true
	err := fn(fakeQuerier{})
	f.state.active = false
	if err != nil {
		f.state.rollbacks++
		return err
	}
	f.state.commits++
	return nil
}

type fakeQuerier struct{}

func (fakeQuerier) ExecContext(context.Context, string, ...any) (sql.Result, error) { return nil, nil }
func (fakeQuerier) QueryContext(context.Context, string, ...any) (*sql.Rows, error) { return nil, nil }
func (fakeQuerier) QueryRowContext(context.Context, string, ...any) *sql.Row        { return &sql.Row{} }

type fakeAuthorizer struct{ state *transactionState }

func (f fakeAuthorizer) RequireSystemPermission(_ context.Context, _ identity.Principal, required string) error {
	f.state.authorizedInTx = f.state.active
	f.state.requiredPermission = required
	if f.state.authorizeErr != nil {
		return f.state.authorizeErr
	}
	if !f.state.active {
		return errors.New("authorization outside transaction")
	}
	return nil
}

type fakeAudit struct{ state *transactionState }

func (f fakeAudit) Append(context.Context, database.Querier, audit.Event) error {
	f.state.auditInTx = f.state.active
	return f.state.auditErr
}

type contentChecker struct {
	exists bool
	err    error
}

func (c contentChecker) HasAnyContent(context.Context, string) (bool, error) { return c.exists, c.err }

type memoryRepository struct {
	models        map[string]ContentModelSummary
	fields        map[string]ContentField
	lockedIDs     []string
	lockModelsErr error
	parents       map[string]*string
	positions     map[string]int
}

func (m *memoryRepository) ensure() {
	if m.models == nil {
		m.models = map[string]ContentModelSummary{}
	}
	if m.fields == nil {
		m.fields = map[string]ContentField{}
	}
	if m.parents == nil {
		m.parents = map[string]*string{}
	}
	if m.positions == nil {
		m.positions = map[string]int{}
	}
}
func (m *memoryRepository) ListModels(context.Context, database.Querier, *ResourceStatus) ([]ContentModelSummary, error) {
	return nil, nil
}
func (m *memoryRepository) GetModel(_ context.Context, _ database.Querier, id string) (ContentModel, error) {
	m.ensure()
	model, ok := m.models[id]
	if !ok {
		return ContentModel{}, notFound("模型")
	}
	fields := []ContentField{}
	for _, field := range m.fields {
		if field.Depth == 0 {
			fields = append(fields, field)
		}
	}
	return ContentModel{ContentModelSummary: model, Fields: fields}, nil
}
func (m *memoryRepository) LockModel(ctx context.Context, q database.Querier, id string) (ContentModelSummary, error) {
	model, err := m.GetModel(ctx, q, id)
	return model.ContentModelSummary, err
}
func (m *memoryRepository) LockModels(_ context.Context, _ database.Querier, ids []string) (map[string]ContentModelSummary, error) {
	if m.lockModelsErr != nil {
		return nil, m.lockModelsErr
	}
	m.ensure()
	m.lockedIDs = append([]string(nil), ids...)
	models := make(map[string]ContentModelSummary, len(ids))
	for _, id := range ids {
		if model, ok := m.models[id]; ok {
			models[id] = model
		}
	}
	return models, nil
}
func (m *memoryRepository) CreateModel(_ context.Context, _ database.Querier, model ContentModelSummary) error {
	m.ensure()
	for _, existing := range m.models {
		if existing.Key == model.Key {
			return ErrDuplicateKey
		}
	}
	m.models[model.ID] = model
	return nil
}
func (m *memoryRepository) UpdateModel(_ context.Context, _ database.Querier, model ContentModelSummary) error {
	m.ensure()
	m.models[model.ID] = model
	return nil
}
func (m *memoryRepository) GetField(_ context.Context, _ database.Querier, _, fieldID string) (ContentField, error) {
	m.ensure()
	field, ok := m.fields[fieldID]
	if !ok {
		return ContentField{}, notFound("字段")
	}
	return field, nil
}
func (m *memoryRepository) LockFieldTree(_ context.Context, _ database.Querier, _ string) ([]ContentField, error) {
	m.ensure()
	result := make([]ContentField, 0, len(m.fields))
	for id, field := range m.fields {
		field.ParentID = m.parents[id]
		field.Position = int64(m.positions[id])
		result = append(result, field)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Depth != result[j].Depth {
			return result[i].Depth < result[j].Depth
		}
		if result[i].Position != result[j].Position {
			return result[i].Position < result[j].Position
		}
		return result[i].ID < result[j].ID
	})
	return result, nil
}
func (m *memoryRepository) CreateFieldTree(_ context.Context, _ database.Querier, _ string, field *ContentField, parentID *string, position int) error {
	m.ensure()
	for _, existing := range m.fields {
		if existing.Key == field.Key {
			return ErrDuplicateKey
		}
	}
	m.fields[field.ID] = *field
	m.parents[field.ID] = parentID
	m.positions[field.ID] = position
	return nil
}
func (m *memoryRepository) UpdateField(_ context.Context, _ database.Querier, _ string, field ContentField) error {
	m.ensure()
	m.fields[field.ID] = field
	return nil
}
func (m *memoryRepository) PrepareFieldOrder(_ context.Context, _ database.Querier, _ string, parentID *string) error {
	for id, field := range m.fields {
		if field.Status == StatusActive && sameParent(m.parents[id], parentID) {
			m.positions[id] = -m.positions[id] - 1
		}
	}
	return nil
}
func (m *memoryRepository) UpdateFieldPosition(_ context.Context, _ database.Querier, _ string, fieldID string, position int) error {
	m.positions[fieldID] = position
	return nil
}
func (m *memoryRepository) UpdateFieldOrder(ctx context.Context, q database.Querier, modelID string, parentID *string, fieldIDs []string) error {
	if err := m.PrepareFieldOrder(ctx, q, modelID, parentID); err != nil {
		return err
	}
	for position, id := range fieldIDs {
		m.positions[id] = position
	}
	return nil
}
func (m *memoryRepository) ArchiveFieldTree(_ context.Context, _ database.Querier, _, fieldID string, now time.Time) error {
	var archive func(string)
	archive = func(id string) {
		field := m.fields[id]
		field.Status = StatusArchived
		field.UpdatedAt = now
		m.fields[id] = field
		for _, child := range field.Children {
			archive(child.ID)
		}
	}
	archive(fieldID)
	return nil
}

func testService(repository Repository, state *transactionState) *Service {
	sequence := map[string]int{}
	return &Service{db: fakeQuerier{}, tx: fakeTransactor{state}, repository: repository, authorizer: fakeAuthorizer{state}, audit: fakeAudit{state}, content: contentChecker{}, now: func() time.Time { return time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC) }, newID: func(prefix string) (string, error) { sequence[prefix]++; return prefix + "1", nil }}
}
func seededRepository() *memoryRepository {
	repository := &memoryRepository{}
	repository.ensure()
	repository.models["mdl_1"] = ContentModelSummary{ID: "mdl_1", Key: "articles", DisplayName: "Articles", Status: StatusActive}
	repository.fields["fld_1"] = ContentField{ID: "fld_1", Key: "title", DisplayName: "Title", Type: FieldTypeSingleLineText, DefaultValue: json.RawMessage(`null`), Children: []ContentField{}, Status: StatusActive}
	return repository
}
func testPrincipal() identity.Principal { return identity.Principal{UserID: "usr_1"} }
func assertErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != code {
		t.Fatalf("error = %#v, want code %s", err, code)
	}
}
