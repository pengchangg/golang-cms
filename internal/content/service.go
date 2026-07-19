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
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

const (
	permissionView    = "content.view"
	permissionCreate  = "content.create"
	permissionUpdate  = "content.update"
	permissionArchive = "content.archive"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type ModelRepository interface {
	GetModel(context.Context, database.Querier, string) (schema.ContentModel, error)
	LockModel(context.Context, database.Querier, string) (schema.ContentModelSummary, error)
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

func (s *Service) ListEntries(ctx context.Context, principal identity.Principal, modelID string, status EntryStatus, limit int, encodedCursor string) (EntryList, error) {
	if err := requireModelPermission(principal, modelID, permissionView); err != nil {
		return EntryList{}, err
	}
	if _, err := s.models.GetModel(ctx, s.db, modelID); err != nil {
		return EntryList{}, err
	}
	cursor, err := decodeEntryCursor(encodedCursor, modelID, status)
	if err != nil {
		return EntryList{}, err
	}
	items, err := s.repository.ListEntries(ctx, s.db, modelID, status, limit+1, cursor)
	if err != nil {
		return EntryList{}, err
	}
	result := EntryList{Items: items, NextCursor: nil}
	if len(items) >= limit {
		if len(items) > limit {
			result.Items = items[:limit]
		}
		last := result.Items[len(result.Items)-1]
		value, err := encodeCursor(cursorEnvelope{Kind: "entries", ModelID: modelID, Status: string(status), UpdatedAt: last.UpdatedAt.Format(time.RFC3339Nano), ID: last.ID})
		if err != nil {
			return EntryList{}, err
		}
		result.NextCursor = &value
	}
	return result, nil
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
		model, err := s.models.LockModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		if model.Status == schema.StatusArchived {
			return conflict("resource_archived", "归档模型不能创建内容")
		}
		fullModel, err := s.models.GetModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		content, err := validateContent(request.Content, fullModel.Fields)
		if err != nil {
			return err
		}
		now := s.now()
		revision := Revision{ID: revisionID, EntryID: entryID, ModelID: modelID, Number: 1, Content: content, CreatedBy: principal.UserID, CreatedAt: now}
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
		result = Entry{EntrySummary: entry, CurrentDraftRevision: revision}
		return nil
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
		modelSummary, err := s.models.LockModel(ctx, q, modelID)
		if err != nil {
			return err
		}
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
		content, err := validateContent(request.Content, model.Fields)
		if err != nil {
			return err
		}
		current, err := s.repository.GetRevision(ctx, q, modelID, entryID, entry.CurrentDraftRevisionID)
		if err != nil {
			return err
		}
		now := s.now()
		revision := Revision{ID: revisionID, EntryID: entryID, ModelID: modelID, Number: current.Number + 1, Content: content, CreatedBy: principal.UserID, CreatedAt: now}
		if err := s.repository.CreateRevision(ctx, q, revision); err != nil {
			return err
		}
		values, err := uniqueValues(content, model.Fields)
		if err != nil {
			return err
		}
		if err := s.replaceUniqueValues(ctx, q, modelID, entryID, model.Fields, values); err != nil {
			return err
		}
		if err := s.repository.SetDraftPointer(ctx, q, modelID, entryID, revisionID); err != nil {
			return err
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
	Kind      string `json:"kind"`
	ModelID   string `json:"model_id"`
	EntryID   string `json:"entry_id,omitempty"`
	Status    string `json:"status,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
	ID        string `json:"id,omitempty"`
	Number    uint   `json:"number,omitempty"`
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

func decodeEntryCursor(value, modelID string, status EntryStatus) (*EntryCursor, error) {
	if value == "" {
		return nil, nil
	}
	cursor, err := decodeCursor(value)
	if err != nil || cursor.Kind != "entries" || cursor.ModelID != modelID || cursor.Status != string(status) || cursor.ID == "" {
		return nil, invalidCursor()
	}
	updatedAt, err := time.Parse(time.RFC3339Nano, cursor.UpdatedAt)
	if err != nil {
		return nil, invalidCursor()
	}
	return &EntryCursor{UpdatedAt: updatedAt.UTC(), ID: cursor.ID}, nil
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

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成资源 ID: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
