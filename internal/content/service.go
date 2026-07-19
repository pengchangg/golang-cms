package content

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

const (
	permissionView      = "content.view"
	permissionCreate    = "content.create"
	permissionUpdate    = "content.update"
	permissionArchive   = "content.archive"
	permissionSubmit    = "content.submit"
	permissionReview    = "content.review"
	permissionPublish   = "content.publish"
	permissionUnpublish = "content.unpublish"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type ModelRepository interface {
	GetModel(context.Context, database.Querier, string) (schema.ContentModel, error)
	LockModel(context.Context, database.Querier, string) (schema.ContentModelSummary, error)
	LockModels(context.Context, database.Querier, []string) (map[string]schema.ContentModelSummary, error)
}

type Dependencies struct {
	DB              database.Querier
	Transactor      TransactionRunner
	Repository      Repository
	ModelRepository ModelRepository
	Audit           audit.Writer
}

type Service struct {
	db         database.Querier
	tx         TransactionRunner
	repository Repository
	models     ModelRepository
	audit      audit.Writer
	now        func() time.Time
	newID      func(string) (string, error)
}

var _ schema.ContentExistenceChecker = (*Service)(nil)

type RequestMeta struct{ RequestID, IP, UserAgent string }

func NewService(dependencies Dependencies) *Service {
	return &Service{db: dependencies.DB, tx: dependencies.Transactor, repository: dependencies.Repository, models: dependencies.ModelRepository, audit: dependencies.Audit, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: randomID}
}

func (s *Service) HasAnyContent(ctx context.Context, modelID string) (bool, error) {
	return s.repository.HasAnyContent(ctx, s.db, modelID)
}

func (s *Service) ListEntries(ctx context.Context, principal identity.Principal, modelID string, query AdminEntryQuery) (EntryList, error) {
	if err := requireModelPermission(principal, modelID, permissionView); err != nil {
		return EntryList{}, err
	}
	model, err := s.models.GetModel(ctx, s.db, modelID)
	if err != nil {
		return EntryList{}, err
	}
	fields, err := validateAdminEntryQuery(model.Fields, query)
	if err != nil {
		return EntryList{}, err
	}
	seenTargets := map[string]bool{}
	for _, key := range query.Expand {
		targetModelID := *fields[key].Constraints.TargetModelID
		if !seenTargets[targetModelID] {
			if err := requireModelPermission(principal, targetModelID, permissionView); err != nil {
				return EntryList{}, err
			}
			seenTargets[targetModelID] = true
		}
	}
	binding, err := adminEntryQueryBinding(modelID, query)
	if err != nil {
		return EntryList{}, err
	}
	cursor, err := decodeEntryCursor(query.Cursor, binding)
	if err != nil {
		return EntryList{}, err
	}
	items, orderedValues, err := s.repository.ListEntries(ctx, s.db, modelID, query, fields, query.Limit+1, cursor)
	if err != nil {
		return EntryList{}, err
	}
	result := EntryList{Items: items, NextCursor: nil}
	if len(items) > query.Limit {
		result.Items = items[:query.Limit]
		if len(orderedValues) >= query.Limit {
			value, err := encodeCursor(cursorEnvelope{Kind: "entries", Binding: binding, Values: orderedValues[query.Limit-1]})
			if err != nil {
				return EntryList{}, err
			}
			result.NextCursor = &value
		}
	}
	if err = s.repository.ExpandEntries(ctx, s.db, result.Items, model.Fields, query.Expand); err != nil {
		return EntryList{}, err
	}
	if query.IncludeTotal {
		total, estimate, err := s.repository.CountEntries(ctx, s.db, modelID, query, fields)
		if err != nil {
			return EntryList{}, err
		}
		result.Total = &total
		result.TotalIsEstimate = &estimate
	}
	return result, nil
}

func validateAdminEntryQuery(fields []schema.ContentField, query AdminEntryQuery) (map[string]schema.ContentField, error) {
	if query.Limit < 1 || query.Limit > 100 || len(query.Filters) > 5 || len(query.RelationFilters) > 2 || len(query.Sort) > 3 || len(query.Expand) > 3 {
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
		if _, _, ok = projectionPredicate("f", field, filter); !ok {
			return nil, invalidQuery()
		}
	}
	seen := map[string]bool{}
	for _, filter := range query.RelationFilters {
		field, ok := byKey[filter.FieldKey]
		if !ok || seen[filter.FieldKey] || filter.EntryID == "" || (field.Type != schema.FieldTypeSingleRelation && field.Type != schema.FieldTypeMultiRelation) {
			return nil, invalidQuery()
		}
		seen[filter.FieldKey] = true
	}
	seen = map[string]bool{}
	for _, item := range query.Sort {
		if seen[item.FieldKey] {
			return nil, invalidQuery()
		}
		seen[item.FieldKey] = true
		if item.FieldKey == "updated_at" || item.FieldKey == "id" {
			continue
		}
		field, ok := byKey[item.FieldKey]
		if !ok || !field.Constraints.Sortable {
			return nil, invalidQuery()
		}
	}
	seen = map[string]bool{}
	for _, key := range query.Expand {
		field, ok := byKey[key]
		if !ok || seen[key] || (field.Type != schema.FieldTypeSingleRelation && field.Type != schema.FieldTypeMultiRelation) || field.Constraints.TargetModelID == nil {
			return nil, invalidQuery()
		}
		seen[key] = true
	}
	return byKey, nil
}

func adminEntryQueryBinding(modelID string, query AdminEntryQuery) (string, error) {
	query.Cursor = ""
	data, err := json.Marshal(struct {
		Audience, Model string
		Query           AdminEntryQuery
	}{"admin", modelID, query})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Service) GetEntry(ctx context.Context, principal identity.Principal, modelID, entryID string) (Entry, error) {
	if err := requireModelPermission(principal, modelID, permissionView); err != nil {
		return Entry{}, err
	}
	return s.repository.GetEntry(ctx, s.db, modelID, entryID)
}

func (s *Service) CreateEntry(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID string, request CreateEntryRequest) (Entry, error) {
	entryID, err := s.newID("ent_")
	if err != nil {
		return Entry{}, err
	}
	revisionID, err := s.newID("rev_")
	if err != nil {
		return Entry{}, err
	}
	var result Entry
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := requireModelPermission(principal, modelID, permissionCreate); err != nil {
			return err
		}
		unlockedModel, err := s.models.GetModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		content, err := validateContent(request.Content, unlockedModel.Fields)
		if err != nil {
			return err
		}
		revision := Revision{ID: revisionID, EntryID: entryID, ModelID: modelID, Number: 1, Content: content, WorkflowStatus: WorkflowDraft, CreatedBy: principal.UserID}
		_, relations, err := revisionDerivatives(content, revision, unlockedModel.Fields)
		if err != nil {
			return err
		}
		lockedModels, err := s.lockRelationModels(ctx, q, modelID, relations)
		if err != nil {
			return err
		}
		model := lockedModels[modelID]
		if model.Status == schema.StatusArchived {
			return conflict("resource_archived", "归档模型不能创建内容")
		}
		fullModel, err := s.models.GetModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		content, err = validateContent(request.Content, fullModel.Fields)
		if err != nil {
			return err
		}
		if err = ensureRelationModelsLocked(content, revision, fullModel.Fields, lockedModels); err != nil {
			return err
		}
		now := s.now()
		revision = Revision{ID: revisionID, EntryID: entryID, ModelID: modelID, Number: 1, Content: content, WorkflowStatus: WorkflowDraft, CreatedBy: principal.UserID, CreatedAt: now}
		entry := EntrySummary{ID: entryID, ModelID: modelID, Status: StatusDraft, CurrentDraftRevisionID: revisionID, CreatedBy: principal.UserID, CreatedAt: now, UpdatedAt: now}
		if err := s.repository.CreateEntry(ctx, q, entry); err != nil {
			return err
		}
		if err := s.repository.CreateRevision(ctx, q, revision); err != nil {
			return err
		}
		if err := s.repository.SetDraftPointer(ctx, q, modelID, entryID, revisionID); err != nil {
			return err
		}
		if err := s.writeRevisionDerivatives(ctx, q, revision, fullModel.Fields); err != nil {
			return err
		}
		values, err := uniqueValues(content, fullModel.Fields)
		if err != nil {
			return err
		}
		if err := s.replaceUniqueValues(ctx, q, modelID, entryID, fullModel.Fields, values); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "content_entry_created", "content_entry", entryID, map[string]any{"model_id": modelID, "revision_id": revisionID}); err != nil {
			return err
		}
		result, err = s.repository.GetEntry(ctx, q, modelID, entryID)
		return err
	})
	return result, err
}

func (s *Service) UpdateEntry(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID string, request UpdateEntryRequest) (Entry, error) {
	if request.BaseRevisionID == "" {
		var failures validationErrors
		failures.add("/base_revision_id", "required", "base_revision_id 为必填项")
		return Entry{}, failures.err()
	}
	revisionID, err := s.newID("rev_")
	if err != nil {
		return Entry{}, err
	}
	var result Entry
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := requireModelPermission(principal, modelID, permissionUpdate); err != nil {
			return err
		}
		unlockedModel, err := s.models.GetModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		content, err := validateContent(request.Content, unlockedModel.Fields)
		if err != nil {
			return err
		}
		candidate := Revision{ID: revisionID, EntryID: entryID, ModelID: modelID, Content: content}
		_, relations, err := revisionDerivatives(content, candidate, unlockedModel.Fields)
		if err != nil {
			return err
		}
		lockedModels, err := s.lockRelationModels(ctx, q, modelID, relations)
		if err != nil {
			return err
		}
		modelSummary := lockedModels[modelID]
		if modelSummary.Status == schema.StatusArchived {
			return conflict("resource_archived", "归档模型不能修改内容")
		}
		entry, err := s.repository.LockEntry(ctx, q, modelID, entryID)
		if err != nil {
			return err
		}
		if entry.Status == StatusArchived {
			return conflict("resource_archived", "归档内容不可修改")
		}
		if entry.CurrentDraftRevisionID != request.BaseRevisionID {
			return conflict("draft_revision_conflict", "草稿已被其他保存更新")
		}
		model, err := s.models.GetModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		content, err = validateContent(request.Content, model.Fields)
		if err != nil {
			return err
		}
		if err = ensureRelationModelsLocked(content, candidate, model.Fields, lockedModels); err != nil {
			return err
		}
		current, err := s.repository.GetRevision(ctx, q, modelID, entryID, entry.CurrentDraftRevisionID)
		if err != nil {
			return err
		}
		now := s.now()
		if current.WorkflowStatus == WorkflowPendingReview {
			return conflict("invalid_workflow_transition", "待审核 Revision 不可编辑")
		}
		revision := Revision{ID: revisionID, EntryID: entryID, ModelID: modelID, Number: current.Number + 1, Content: content, WorkflowStatus: WorkflowDraft, CreatedBy: principal.UserID, CreatedAt: now}
		if err := s.repository.CreateRevision(ctx, q, revision); err != nil {
			return err
		}
		if err := s.writeRevisionDerivatives(ctx, q, revision, model.Fields); err != nil {
			return err
		}
		if err := s.repository.SetDraftPointer(ctx, q, modelID, entryID, revisionID); err != nil {
			return err
		}
		if _, ok := s.repository.(F2Repository); ok {
			if err := s.rebuildUniqueValues(ctx, q, modelID, entryID, model.Fields); err != nil {
				return err
			}
		} else {
			values, err := uniqueValues(content, model.Fields)
			if err != nil {
				return err
			}
			if err = s.replaceUniqueValues(ctx, q, modelID, entryID, model.Fields, values); err != nil {
				return err
			}
		}
		entry.CurrentDraftRevisionID, entry.UpdatedAt = revisionID, now
		if err := s.repository.UpdateEntry(ctx, q, entry); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "content_revision_created", "content_revision", revisionID, map[string]any{"entry_id": entryID, "model_id": modelID, "base_revision_id": request.BaseRevisionID}); err != nil {
			return err
		}
		result = Entry{EntrySummary: entry, CurrentDraftRevision: revision}
		return nil
	})
	return result, err
}

func (s *Service) lockRelationModels(ctx context.Context, q database.Querier, sourceModelID string, relations []Relation) (map[string]schema.ContentModelSummary, error) {
	unique := map[string]bool{sourceModelID: true}
	for _, relation := range relations {
		unique[relation.TargetModelID] = true
	}
	ids := make([]string, 0, len(unique))
	for id := range unique {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	models, err := s.models.LockModels(ctx, q, ids)
	if err != nil {
		return nil, err
	}
	if len(models) != len(ids) {
		return nil, notFound("模型")
	}
	return models, nil
}

func ensureRelationModelsLocked(content json.RawMessage, revision Revision, fields []schema.ContentField, locked map[string]schema.ContentModelSummary) error {
	_, relations, err := revisionDerivatives(content, revision, fields)
	if err != nil {
		return err
	}
	for _, relation := range relations {
		if _, ok := locked[relation.TargetModelID]; !ok {
			return conflict("model_schema_conflict", "关联目标模型已变化，请重试")
		}
	}
	return nil
}

func (s *Service) ArchiveEntry(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := requireModelPermission(principal, modelID, permissionArchive); err != nil {
			return err
		}
		if _, err := s.models.LockModel(ctx, q, modelID); err != nil {
			return err
		}
		entry, err := s.repository.LockEntry(ctx, q, modelID, entryID)
		if err != nil {
			return err
		}
		if entry.Status == StatusArchived {
			return conflict("resource_archived", "内容条目已归档")
		}
		if f2, ok := s.repository.(F2Repository); ok {
			workflowEntry, err := f2.GetWorkflowEntry(ctx, q, modelID, entryID)
			if err != nil {
				return err
			}
			if workflowEntry.CurrentPublishedRevisionID != nil {
				return conflict("published_entry_archive_forbidden", "已发布内容必须先下线")
			}
		}
		entry.Status, entry.UpdatedAt = StatusArchived, s.now()
		if err := s.repository.UpdateEntry(ctx, q, entry); err != nil {
			return err
		}
		if err := s.repository.DeleteUniqueValues(ctx, q, modelID, entryID); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "content_entry_archived", "content_entry", entryID, map[string]any{"model_id": modelID, "status": map[string]any{"from": StatusDraft, "to": StatusArchived}})
	})
}

func (s *Service) writeRevisionDerivatives(ctx context.Context, q database.Querier, revision Revision, fields []schema.ContentField) error {
	f2, ok := s.repository.(F2Repository)
	if !ok {
		return nil
	}
	values, relations, err := revisionDerivatives(revision.Content, revision, fields)
	if err != nil {
		return err
	}
	if err = f2.ValidateRelationTargets(ctx, q, relations); err != nil {
		return err
	}
	if err = f2.CreateFieldValues(ctx, q, values); err != nil {
		return err
	}
	return f2.CreateRelations(ctx, q, relations)
}

func (s *Service) rebuildUniqueValues(ctx context.Context, q database.Querier, modelID, entryID string, fields []schema.ContentField) error {
	f2 := s.repository.(F2Repository)
	entry, err := f2.GetWorkflowEntry(ctx, q, modelID, entryID)
	if err != nil {
		return err
	}
	all := []UniqueValue{}
	seen := map[string]bool{}
	for _, revision := range []Revision{entry.CurrentDraftRevision} {
		values, err := uniqueValues(revision.Content, fields)
		if err != nil {
			return err
		}
		for _, value := range values {
			key := value.FieldID + "\x00" + string(value.CanonicalValue)
			if !seen[key] {
				seen[key] = true
				all = append(all, value)
			}
		}
	}
	if entry.CurrentPublishedRevision != nil {
		values, err := uniqueValues(entry.CurrentPublishedRevision.Content, fields)
		if err != nil {
			return err
		}
		for _, value := range values {
			key := value.FieldID + "\x00" + string(value.CanonicalValue)
			if !seen[key] {
				seen[key] = true
				all = append(all, value)
			}
		}
	}
	return s.replaceUniqueValues(ctx, q, modelID, entryID, fields, all)
}

func (s *Service) Submit(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID string, request RevisionConditionRequest) (Entry, error) {
	return s.workflow(ctx, principal, meta, modelID, entryID, request.RevisionID, "submitted", WorkflowDraft, WorkflowPendingReview, permissionSubmit, "", true)
}
func (s *Service) Approve(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID string, request RevisionConditionRequest) (Entry, error) {
	if err := requireModelPermission(principal, modelID, permissionReview); err != nil {
		return Entry{}, err
	}
	return s.workflow(ctx, principal, meta, modelID, entryID, request.RevisionID, "approved", WorkflowPendingReview, WorkflowPublished, permissionPublish, "", false)
}
func (s *Service) Reject(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID string, request RejectRevisionRequest) (Entry, error) {
	reason := strings.TrimSpace(request.Reason)
	if n := utf8.RuneCountInString(reason); n < 1 || n > 1000 {
		var failures validationErrors
		failures.add("/reason", "out_of_range", "reason 去除首尾空白后长度必须为 1 至 1000")
		return Entry{}, failures.err()
	}
	return s.workflow(ctx, principal, meta, modelID, entryID, request.RevisionID, "rejected", WorkflowPendingReview, WorkflowRejected, permissionReview, reason, false)
}
func (s *Service) Unpublish(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID string, request RevisionConditionRequest) (Entry, error) {
	return s.workflow(ctx, principal, meta, modelID, entryID, request.RevisionID, "unpublished", WorkflowPublished, WorkflowUnpublished, permissionUnpublish, "", false)
}

func (s *Service) workflow(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID, revisionID, eventType string, from, to WorkflowStatus, permission, reason string, draftPointer bool) (Entry, error) {
	if revisionID == "" {
		var failures validationErrors
		failures.add("/revision_id", "required", "revision_id 为必填项")
		return Entry{}, failures.err()
	}
	f2, ok := s.repository.(F2Repository)
	if !ok {
		return Entry{}, fmt.Errorf("F2Repository 未装配")
	}
	var result Entry
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := requireModelPermission(principal, modelID, permission); err != nil {
			return err
		}
		if _, err := s.models.LockModel(ctx, q, modelID); err != nil {
			return err
		}
		entry, err := s.repository.LockEntry(ctx, q, modelID, entryID)
		if err != nil {
			return err
		}
		current, err := f2.GetWorkflowEntry(ctx, q, modelID, entryID)
		if err != nil {
			return err
		}
		expected := entry.CurrentDraftRevisionID
		if !draftPointer {
			if eventType == "unpublished" {
				if current.CurrentPublishedRevisionID == nil {
					expected = ""
				} else {
					expected = *current.CurrentPublishedRevisionID
				}
			}
		}
		if expected != revisionID {
			return conflict("workflow_revision_conflict", "目标 Revision 已不是当前指针")
		}
		revision, err := f2.LockRevision(ctx, q, modelID, entryID, revisionID)
		if err != nil {
			return err
		}
		if revision.WorkflowStatus != from {
			return conflict("invalid_workflow_transition", "Revision 状态不允许该转换")
		}
		if !draftPointer && eventType != "unpublished" && revision.SubmittedBy != nil && *revision.SubmittedBy == principal.UserID {
			return conflict("self_review_forbidden", "提交人不能审核自己的 Revision")
		}
		now := s.now()
		var submitter *string
		var submittedAt *time.Time
		if eventType == "submitted" {
			submitter = &principal.UserID
			submittedAt = &now
		}
		changed, err := f2.TransitionRevision(ctx, q, revisionID, from, to, submitter, submittedAt)
		if err != nil {
			return err
		}
		if !changed {
			return conflict("invalid_workflow_transition", "Revision 状态不允许该转换")
		}
		if eventType == "approved" {
			if current.CurrentPublishedRevisionID != nil && *current.CurrentPublishedRevisionID != revisionID {
				var changed bool
				if changed, err = f2.TransitionRevision(ctx, q, *current.CurrentPublishedRevisionID, WorkflowPublished, WorkflowUnpublished, nil, nil); err != nil {
					return err
				}
				if !changed {
					return conflict("workflow_revision_conflict", "当前发布 Revision 已变化")
				}
			}
			if err = f2.SetPublishedPointer(ctx, q, modelID, entryID, revisionID, now); err != nil {
				return err
			}
		}
		if eventType == "unpublished" {
			deleted, err := f2.DeletePublishedPointer(ctx, q, modelID, entryID, revisionID)
			if err != nil {
				return err
			}
			if !deleted {
				return conflict("workflow_revision_conflict", "目标 Revision 已不是当前发布指针")
			}
		}
		eventID, err := s.newID("wfe_")
		if err != nil {
			return err
		}
		var reasonPointer *string
		if reason != "" {
			reasonPointer = &reason
		}
		if err = f2.CreateWorkflowEvent(ctx, q, modelID, WorkflowEvent{ID: eventID, EntryID: entryID, RevisionID: revisionID, Type: eventType, FromStatus: from, ToStatus: to, ActorID: principal.UserID, Reason: reasonPointer, OccurredAt: now}); err != nil {
			return err
		}
		if eventType == "approved" || eventType == "unpublished" {
			model, modelErr := s.models.GetModel(ctx, q, modelID)
			if modelErr != nil {
				return modelErr
			}
			if err = s.rebuildUniqueValues(ctx, q, modelID, entryID, model.Fields); err != nil {
				return err
			}
		}
		if err = s.appendAudit(ctx, q, principal, meta, "content_revision_"+eventType, "content_revision", revisionID, map[string]any{"entry_id": entryID, "model_id": modelID, "from": from, "to": to}); err != nil {
			return err
		}
		result, err = f2.GetWorkflowEntry(ctx, q, modelID, entryID)
		return err
	})
	return result, err
}

func (s *Service) ListWorkflowEvents(ctx context.Context, principal identity.Principal, modelID, entryID string, limit int, encoded string) (WorkflowEventList, error) {
	if err := requireModelPermission(principal, modelID, permissionView); err != nil {
		return WorkflowEventList{}, err
	}
	f2, ok := s.repository.(F2Repository)
	if !ok {
		return WorkflowEventList{}, fmt.Errorf("F2Repository 未装配")
	}
	if _, err := s.repository.GetEntry(ctx, s.db, modelID, entryID); err != nil {
		return WorkflowEventList{}, err
	}
	cursor, err := decodeWorkflowEventCursor(encoded, modelID, entryID)
	if err != nil {
		return WorkflowEventList{}, err
	}
	items, err := f2.ListWorkflowEvents(ctx, s.db, modelID, entryID, limit+1, cursor)
	if err != nil {
		return WorkflowEventList{}, err
	}
	result := WorkflowEventList{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		last := result.Items[limit-1]
		value, _ := encodeCursor(cursorEnvelope{Kind: "workflow_events", ModelID: modelID, EntryID: entryID, UpdatedAt: last.OccurredAt.Format(time.RFC3339Nano), ID: last.ID})
		result.NextCursor = &value
	}
	return result, nil
}

func (s *Service) replaceUniqueValues(ctx context.Context, q database.Querier, modelID, entryID string, fields []schema.ContentField, values []UniqueValue) error {
	err := s.repository.ReplaceUniqueValues(ctx, q, modelID, entryID, values)
	var conflictError *uniqueValueConflict
	if !errors.As(err, &conflictError) {
		return err
	}
	path := "/content"
	for _, field := range fields {
		if field.ID == conflictError.FieldID {
			path += "/" + escapePointer(field.Key)
			break
		}
	}
	var failures validationErrors
	failures.add(path, "not_unique", "字段值已被其他内容占用")
	return failures.err()
}

func (s *Service) ListRevisions(ctx context.Context, principal identity.Principal, modelID, entryID string, limit int, encodedCursor string) (RevisionList, error) {
	if err := requireModelPermission(principal, modelID, permissionView); err != nil {
		return RevisionList{}, err
	}
	before, err := decodeRevisionCursor(encodedCursor, modelID, entryID)
	if err != nil {
		return RevisionList{}, err
	}
	items, err := s.repository.ListRevisions(ctx, s.db, modelID, entryID, limit+1, before)
	if err != nil {
		return RevisionList{}, err
	}
	result := RevisionList{Items: items}
	if len(items) >= limit {
		if len(items) > limit {
			result.Items = items[:limit]
		}
		last := result.Items[len(result.Items)-1]
		value, err := encodeCursor(cursorEnvelope{Kind: "revisions", ModelID: modelID, EntryID: entryID, Number: last.Number})
		if err != nil {
			return RevisionList{}, err
		}
		result.NextCursor = &value
	}
	return result, nil
}

func (s *Service) GetRevision(ctx context.Context, principal identity.Principal, modelID, entryID, revisionID string) (Revision, error) {
	if err := requireModelPermission(principal, modelID, permissionView); err != nil {
		return Revision{}, err
	}
	if _, err := s.repository.GetEntry(ctx, s.db, modelID, entryID); err != nil {
		return Revision{}, err
	}
	return s.repository.GetRevision(ctx, s.db, modelID, entryID, revisionID)
}

func requireModelPermission(principal identity.Principal, modelID, required string) error {
	for _, item := range principal.ModelPermissions {
		if item.ModelID != modelID {
			continue
		}
		for _, granted := range item.Permissions {
			if granted == required {
				return nil
			}
		}
	}
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}

func (s *Service) appendAudit(ctx context.Context, q database.Querier, principal identity.Principal, meta RequestMeta, action, resourceType, resourceID string, changes map[string]any) error {
	id, err := s.newID("evt_")
	if err != nil {
		return err
	}
	actorID := principal.UserID
	return s.audit.Append(ctx, q, audit.Event{ID: id, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, Action: action, ResourceType: resourceType, ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}

type cursorEnvelope struct {
	Kind      string    `json:"kind"`
	ModelID   string    `json:"model_id"`
	EntryID   string    `json:"entry_id,omitempty"`
	Status    string    `json:"status,omitempty"`
	UpdatedAt string    `json:"updated_at,omitempty"`
	ID        string    `json:"id,omitempty"`
	Number    uint      `json:"number,omitempty"`
	Binding   string    `json:"binding,omitempty"`
	Values    []*string `json:"values,omitempty"`
}

func encodeCursor(cursor cursorEnvelope) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("编码分页游标: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeCursor(value string) (cursorEnvelope, error) {
	var cursor cursorEnvelope
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(data, &cursor) != nil {
		return cursor, invalidCursor()
	}
	return cursor, nil
}

func decodeEntryCursor(value, binding string) (*EntryCursor, error) {
	if value == "" {
		return nil, nil
	}
	cursor, err := decodeCursor(value)
	if err != nil || cursor.Kind != "entries" || cursor.Binding != binding || len(cursor.Values) == 0 {
		return nil, invalidCursor()
	}
	return &EntryCursor{Values: cursor.Values}, nil
}

func decodeRevisionCursor(value, modelID, entryID string) (*uint, error) {
	if value == "" {
		return nil, nil
	}
	cursor, err := decodeCursor(value)
	if err != nil || cursor.Kind != "revisions" || cursor.ModelID != modelID || cursor.EntryID != entryID || cursor.Number == 0 {
		return nil, invalidCursor()
	}
	return &cursor.Number, nil
}

func decodeWorkflowEventCursor(value, modelID, entryID string) (*WorkflowEventCursor, error) {
	if value == "" {
		return nil, nil
	}
	cursor, err := decodeCursor(value)
	if err != nil || cursor.Kind != "workflow_events" || cursor.ModelID != modelID || cursor.EntryID != entryID || cursor.ID == "" {
		return nil, invalidCursor()
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
	if err != nil {
		return nil, invalidCursor()
	}
	return &WorkflowEventCursor{OccurredAt: occurredAt.UTC(), ID: cursor.ID}, nil
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成资源 ID: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
