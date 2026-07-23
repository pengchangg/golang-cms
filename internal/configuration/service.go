package configuration

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/permission"
	"cms/internal/platform/database"
)

const (
	permissionView      = "config.view"
	permissionCreate    = "config.create"
	permissionUpdate    = "config.update"
	permissionArchive   = "config.archive"
	permissionSubmit    = "config.submit"
	permissionReview    = "config.review"
	permissionPublish   = "config.publish"
	permissionUnpublish = "config.unpublish"
)

type SystemAuthorizer interface {
	RequireSystemPermission(context.Context, identity.Principal, string) error
	CurrentPrincipal(context.Context, database.Querier, identity.Principal) (identity.Principal, error)
}

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type Dependencies struct {
	DB         database.Querier
	Transactor TransactionRunner
	Repository Repository
	Authorizer SystemAuthorizer
	Audit      audit.Writer
}

type Service struct {
	db         database.Querier
	tx         TransactionRunner
	repository Repository
	authorizer SystemAuthorizer
	audit      audit.Writer
	now        func() time.Time
	newID      func(string) (string, error)
}

func NewService(dependencies Dependencies) *Service {
	return &Service{db: dependencies.DB, tx: dependencies.Transactor, repository: dependencies.Repository, authorizer: dependencies.Authorizer, audit: dependencies.Audit, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: randomID}
}

func (s *Service) ListNamespaces(ctx context.Context, principal identity.Principal, status *ResourceStatus) ([]Namespace, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ConfigurationsView); err != nil {
		return nil, err
	}
	return s.repository.ListNamespaces(ctx, s.db, status)
}

func (s *Service) GetNamespace(ctx context.Context, principal identity.Principal, id string) (Namespace, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ConfigurationsView); err != nil {
		return Namespace{}, err
	}
	return s.repository.GetNamespace(ctx, s.db, id)
}

func (s *Service) CreateNamespace(ctx context.Context, principal identity.Principal, meta RequestMeta, input CreateNamespaceRequest) (Namespace, error) {
	if err := validateNamespaceCreate(input); err != nil {
		return Namespace{}, err
	}
	id, err := s.newID("cns_")
	if err != nil {
		return Namespace{}, err
	}
	now := s.now()
	item := Namespace{ID: id, Key: input.Key, DisplayName: input.DisplayName, Description: input.Description, Status: StatusActive, CreatedAt: now, UpdatedAt: now}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		current, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, current, permission.ConfigurationsCreate); err != nil {
			return err
		}
		if err := s.repository.CreateNamespace(ctx, q, item); errors.Is(err, ErrDuplicateKey) {
			return conflict("key_conflict", "namespace_key 已存在")
		} else if err != nil {
			return err
		}
		return s.appendAudit(ctx, q, current, meta, "configuration_namespace_created", "configuration_namespace", id, map[string]any{"namespace_key": input.Key})
	})
	return item, err
}

func (s *Service) UpdateNamespace(ctx context.Context, principal identity.Principal, meta RequestMeta, id string, patch NamespacePatch) (Namespace, error) {
	if err := validateNamespacePatch(patch); err != nil {
		return Namespace{}, err
	}
	var result Namespace
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		current, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, current, permission.ConfigurationsUpdate); err != nil {
			return err
		}
		item, err := s.repository.LockNamespace(ctx, q, id)
		if err != nil {
			return err
		}
		if item.Status == StatusArchived {
			return conflict("resource_archived", "配置 namespace 已归档")
		}
		changes := map[string]any{}
		if patch.DisplayName != nil {
			changes["display_name"] = map[string]any{"from": item.DisplayName, "to": *patch.DisplayName}
			item.DisplayName = *patch.DisplayName
		}
		if patch.Description != nil {
			changes["description"] = map[string]any{"from": item.Description, "to": *patch.Description}
			item.Description = *patch.Description
		}
		item.UpdatedAt = s.now()
		if err := s.repository.UpdateNamespace(ctx, q, item); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, current, meta, "configuration_namespace_updated", "configuration_namespace", id, changes); err != nil {
			return err
		}
		result = item
		return nil
	})
	return result, err
}

func (s *Service) ArchiveNamespace(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		current, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, current, permission.ConfigurationsArchive); err != nil {
			return err
		}
		item, err := s.repository.LockNamespace(ctx, q, id)
		if err != nil {
			return err
		}
		if item.Status == StatusArchived {
			return conflict("resource_archived", "配置 namespace 已归档")
		}
		active, err := s.repository.HasActiveItems(ctx, q, id)
		if err != nil {
			return err
		}
		if active {
			return conflict("namespace_not_empty", "归档 namespace 前必须先归档全部配置项")
		}
		published, err := s.repository.HasPublishedItems(ctx, q, id)
		if err != nil {
			return err
		}
		if published {
			return conflict("published_namespace_archive_forbidden", "已发布配置必须先下线")
		}
		item.Status, item.UpdatedAt = StatusArchived, s.now()
		if err := s.repository.UpdateNamespace(ctx, q, item); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, current, meta, "configuration_namespace_archived", "configuration_namespace", id, map[string]any{"status": map[string]any{"from": StatusActive, "to": StatusArchived}})
	})
}

func (s *Service) ListItems(ctx context.Context, principal identity.Principal, namespaceID string, status *ResourceStatus) ([]Item, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ConfigurationsView); err != nil {
		return nil, err
	}
	if _, err := s.repository.GetNamespace(ctx, s.db, namespaceID); err != nil {
		return nil, err
	}
	return s.repository.ListItems(ctx, s.db, namespaceID, status)
}

func (s *Service) GetItem(ctx context.Context, principal identity.Principal, namespaceID, itemID string) (Item, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ConfigurationsView); err != nil {
		return Item{}, err
	}
	if _, err := s.repository.GetNamespace(ctx, s.db, namespaceID); err != nil {
		return Item{}, err
	}
	return s.repository.GetItem(ctx, s.db, namespaceID, itemID)
}

func (s *Service) GetItemValue(ctx context.Context, principal identity.Principal, namespaceID, itemID string) (ItemValue, error) {
	namespace, err := s.repository.GetNamespace(ctx, s.db, namespaceID)
	if err != nil {
		return ItemValue{}, err
	}
	if err := requireNamespacePermission(principal, namespace.ID, permissionView); err != nil {
		return ItemValue{}, err
	}
	return s.repository.GetItemValue(ctx, s.db, namespaceID, itemID)
}

func (s *Service) ListRevisions(ctx context.Context, principal identity.Principal, namespaceID, itemID string, limit int, encodedCursor string) (RevisionList, error) {
	if err := requireNamespacePermission(principal, namespaceID, permissionView); err != nil {
		return RevisionList{}, err
	}
	before, err := decodeRevisionCursor(encodedCursor, namespaceID, itemID)
	if err != nil {
		return RevisionList{}, err
	}
	items, err := s.repository.ListRevisions(ctx, s.db, namespaceID, itemID, limit+1, before)
	if err != nil {
		return RevisionList{}, err
	}
	result := RevisionList{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		last := result.Items[len(result.Items)-1]
		value, err := encodeConfigurationCursor(configurationCursor{Kind: "revisions", NamespaceID: namespaceID, ItemID: itemID, Number: last.Number})
		if err != nil {
			return RevisionList{}, err
		}
		result.NextCursor = &value
	}
	return result, nil
}

func (s *Service) GetRevision(ctx context.Context, principal identity.Principal, namespaceID, itemID, revisionID string) (Revision, error) {
	if err := requireNamespacePermission(principal, namespaceID, permissionView); err != nil {
		return Revision{}, err
	}
	return s.repository.GetRevision(ctx, s.db, namespaceID, itemID, revisionID)
}

func (s *Service) ListWorkflowEvents(ctx context.Context, principal identity.Principal, namespaceID, itemID string, limit int, encodedCursor string) (WorkflowEventList, error) {
	if err := requireNamespacePermission(principal, namespaceID, permissionView); err != nil {
		return WorkflowEventList{}, err
	}
	cursor, err := decodeWorkflowEventCursor(encodedCursor, namespaceID, itemID)
	if err != nil {
		return WorkflowEventList{}, err
	}
	items, err := s.repository.ListWorkflowEvents(ctx, s.db, namespaceID, itemID, limit+1, cursor)
	if err != nil {
		return WorkflowEventList{}, err
	}
	result := WorkflowEventList{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		last := result.Items[len(result.Items)-1]
		value, err := encodeConfigurationCursor(configurationCursor{Kind: "workflow_events", NamespaceID: namespaceID, ItemID: itemID, OccurredAt: last.OccurredAt.Format(time.RFC3339Nano), ID: last.ID})
		if err != nil {
			return WorkflowEventList{}, err
		}
		result.NextCursor = &value
	}
	return result, nil
}

func (s *Service) CreateItem(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID string, input CreateItemRequest) (Item, error) {
	if err := validateItemCreate(&input); err != nil {
		return Item{}, err
	}
	id, err := s.newID("cit_")
	if err != nil {
		return Item{}, err
	}
	var result Item
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		current, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, current, permission.ConfigurationsCreate); err != nil {
			return err
		}
		namespace, err := s.repository.LockNamespace(ctx, q, namespaceID)
		if err != nil {
			return err
		}
		if namespace.Status == StatusArchived {
			return conflict("resource_archived", "归档 namespace 不能新增配置项")
		}
		count, err := s.repository.CountActiveItems(ctx, q, namespaceID)
		if err != nil {
			return err
		}
		if count >= 100 {
			return conflict("configuration_namespace_item_limit", "每个 namespace 最多包含 100 个活动配置项")
		}
		now := s.now()
		result = Item{ID: id, NamespaceID: namespaceID, Key: input.Key, DisplayName: input.DisplayName, Description: input.Description, ValueType: input.ValueType, Constraints: input.Constraints, Status: StatusActive, CreatedBy: current.UserID, CreatedAt: now, UpdatedAt: now}
		if err := s.repository.CreateItem(ctx, q, result); errors.Is(err, ErrDuplicateKey) {
			return conflict("key_conflict", "item_key 已存在")
		} else if err != nil {
			return err
		}
		return s.appendAudit(ctx, q, current, meta, "configuration_item_created", "configuration_item", id, map[string]any{"namespace_key": namespace.Key, "item_key": input.Key})
	})
	return result, err
}

func (s *Service) UpdateItem(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, patch ItemPatch) (Item, error) {
	var result Item
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		current, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, current, permission.ConfigurationsUpdate); err != nil {
			return err
		}
		namespace, err := s.repository.LockNamespace(ctx, q, namespaceID)
		if err != nil {
			return err
		}
		item, err := s.repository.LockItem(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if namespace.Status == StatusArchived || item.Status == StatusArchived {
			return conflict("resource_archived", "归档配置定义不可修改")
		}
		updated, err := validateItemPatch(patch, item)
		if err != nil {
			return err
		}
		value, err := s.repository.GetItemValue(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if updated.Constraints.TargetModelID != nil && item.Constraints.TargetModelID != nil && *updated.Constraints.TargetModelID != *item.Constraints.TargetModelID && value.CurrentDraftRevision.ID != "" {
			return conflict("item_relation_model_locked", "配置项已有 Revision 后不能修改关系目标模型")
		}
		if !constraintsEqual(updated.Constraints, item.Constraints) {
			for _, revision := range currentRevisions(value) {
				_, assetIDs, relationIDs, err := validateAndNormalizeValue(revision.Value, item.ValueType, updated.Constraints)
				if err != nil {
					return conflict("item_constraints_incompatible", "当前草稿或发布值不满足新约束")
				}
				if err := s.repository.ValidateAssetsAvailable(ctx, q, assetIDs, updated.Constraints); err != nil {
					return conflict("item_constraints_incompatible", "当前素材不满足新约束")
				}
				if err := s.repository.ValidateRelationTargetsActive(ctx, q, makeRelations(revision.ID, updated, relationIDs)); err != nil {
					return conflict("item_constraints_incompatible", "当前关系值不满足新约束")
				}
			}
		}
		updated.UpdatedAt = s.now()
		if err := s.repository.UpdateItem(ctx, q, updated); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, current, meta, "configuration_item_updated", "configuration_item", itemID, map[string]any{"namespace_key": namespace.Key}); err != nil {
			return err
		}
		result = updated
		return nil
	})
	return result, err
}

func currentRevisions(value ItemValue) []Revision {
	result := []Revision{}
	if value.CurrentDraftRevision.ID != "" {
		result = append(result, value.CurrentDraftRevision)
	}
	if value.CurrentPublishedRevision != nil && value.CurrentPublishedRevision.ID != value.CurrentDraftRevision.ID {
		result = append(result, *value.CurrentPublishedRevision)
	}
	return result
}

func (s *Service) ArchiveItem(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		current, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		if err := s.authorizer.RequireSystemPermission(ctx, current, permission.ConfigurationsArchive); err != nil {
			return err
		}
		namespace, err := s.repository.LockNamespace(ctx, q, namespaceID)
		if err != nil {
			return err
		}
		if err := requireNamespacePermission(current, namespace.ID, permissionArchive); err != nil {
			return err
		}
		item, err := s.repository.LockItem(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if item.Status == StatusArchived {
			return conflict("resource_archived", "配置项已归档")
		}
		value, err := s.repository.GetItemValue(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if value.CurrentPublishedRevisionID != nil {
			return conflict("published_item_archive_forbidden", "已发布配置必须先下线")
		}
		item.Status, item.UpdatedAt = StatusArchived, s.now()
		if err := s.repository.UpdateItem(ctx, q, item); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, current, meta, "configuration_item_archived", "configuration_item", itemID, map[string]any{"namespace_key": namespace.Key, "status": map[string]any{"from": StatusActive, "to": StatusArchived}})
	})
}

func (s *Service) CreateDraft(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, request CreateDraftRequest) (ItemValue, error) {
	return s.saveDraft(ctx, principal, meta, namespaceID, itemID, "", request.Value, true)
}

func (s *Service) UpdateDraft(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, request UpdateDraftRequest) (ItemValue, error) {
	if request.BaseRevisionID == "" {
		var failures validationErrors
		failures.add("/base_revision_id", "required", "base_revision_id 为必填项")
		return ItemValue{}, failures.err()
	}
	return s.saveDraft(ctx, principal, meta, namespaceID, itemID, request.BaseRevisionID, request.Value, false)
}

func (s *Service) saveDraft(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID, baseRevisionID string, raw json.RawMessage, create bool) (ItemValue, error) {
	revisionID, err := s.newID("crv_")
	if err != nil {
		return ItemValue{}, err
	}
	var result ItemValue
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		currentPrincipal, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		namespace, err := s.repository.LockNamespace(ctx, q, namespaceID)
		if err != nil {
			return err
		}
		permissionCode := permissionUpdate
		if create {
			permissionCode = permissionCreate
		}
		if err := requireNamespacePermission(currentPrincipal, namespace.ID, permissionCode); err != nil {
			return err
		}
		item, err := s.repository.LockItem(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if namespace.Status == StatusArchived || item.Status == StatusArchived {
			return conflict("resource_archived", "归档配置不可保存草稿")
		}
		normalized, assetIDs, relationIDs, err := validateAndNormalizeValue(raw, item.ValueType, item.Constraints)
		if err != nil {
			return err
		}
		current, err := s.repository.GetItemValue(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if create && current.CurrentDraftRevision.ID != "" {
			return conflict("draft_exists", "配置项已存在草稿")
		}
		if !create && current.CurrentDraftRevision.ID != baseRevisionID {
			return conflict("draft_revision_conflict", "草稿已被其他保存更新")
		}
		if !create && current.CurrentDraftRevision.WorkflowStatus == WorkflowPendingReview {
			return conflict("invalid_workflow_transition", "待审核 Revision 不可编辑")
		}
		if err := s.repository.ValidateAssetsAvailable(ctx, q, assetIDs, item.Constraints); err != nil {
			return err
		}
		relations := makeRelations(revisionID, item, relationIDs)
		if err := s.repository.ValidateRelationTargetsActive(ctx, q, relations); err != nil {
			return err
		}
		number := uint(1)
		if current.CurrentDraftRevision.ID != "" {
			number = current.CurrentDraftRevision.Number + 1
		}
		now := s.now()
		revision := Revision{ID: revisionID, ItemID: itemID, NamespaceID: namespaceID, Number: number, ValueType: item.ValueType, Constraints: item.Constraints, Value: normalized, WorkflowStatus: WorkflowDraft, CreatedBy: currentPrincipal.UserID, CreatedAt: now}
		if err := s.repository.CreateRevision(ctx, q, revision); err != nil {
			return err
		}
		if err := s.repository.InsertAssetReferences(ctx, q, makeAssetReferences(revisionID, item, assetIDs)); err != nil {
			return err
		}
		if err := s.repository.InsertRelations(ctx, q, relations); err != nil {
			return err
		}
		if err := s.repository.SetDraftPointer(ctx, q, namespaceID, itemID, revisionID); err != nil {
			return err
		}
		item.UpdatedAt = now
		if err := s.repository.UpdateItem(ctx, q, item); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, currentPrincipal, meta, "configuration_revision_created", "configuration_revision", revisionID, map[string]any{"namespace_key": namespace.Key, "item_id": itemID, "base_revision_id": baseRevisionID}); err != nil {
			return err
		}
		result = current
		result.Item = item
		result.CurrentDraftRevision = revision
		return nil
	})
	return result, err
}

func (s *Service) Submit(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, request RevisionConditionRequest) (ItemValue, error) {
	return s.workflow(ctx, principal, meta, namespaceID, itemID, request.RevisionID, "submitted", WorkflowDraft, WorkflowPendingReview, permissionSubmit, "", true)
}

func (s *Service) Approve(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, request RevisionConditionRequest) (ItemValue, error) {
	if request.RevisionID == "" {
		return ItemValue{}, requiredRevision()
	}
	return s.workflow(ctx, principal, meta, namespaceID, itemID, request.RevisionID, "approved", WorkflowPendingReview, WorkflowPublished, permissionPublish, "", false)
}

func (s *Service) Reject(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, request RejectRevisionRequest) (ItemValue, error) {
	reason := strings.TrimSpace(request.Reason)
	if length := utf8.RuneCountInString(reason); length < 1 || length > 1000 {
		var failures validationErrors
		failures.add("/reason", "out_of_range", "reason 去除首尾空白后长度必须为 1 至 1000")
		return ItemValue{}, failures.err()
	}
	return s.workflow(ctx, principal, meta, namespaceID, itemID, request.RevisionID, "rejected", WorkflowPendingReview, WorkflowRejected, permissionReview, reason, false)
}

func (s *Service) Unpublish(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID string, request RevisionConditionRequest) (ItemValue, error) {
	return s.workflow(ctx, principal, meta, namespaceID, itemID, request.RevisionID, "unpublished", WorkflowPublished, WorkflowUnpublished, permissionUnpublish, "", false)
}

func (s *Service) workflow(ctx context.Context, principal identity.Principal, meta RequestMeta, namespaceID, itemID, revisionID, eventType string, from, to WorkflowStatus, permissionCode, reason string, draftPointer bool) (ItemValue, error) {
	if revisionID == "" {
		return ItemValue{}, requiredRevision()
	}
	var result ItemValue
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		currentPrincipal, err := s.currentPrincipal(ctx, q, principal)
		if err != nil {
			return err
		}
		namespace, err := s.repository.LockNamespace(ctx, q, namespaceID)
		if err != nil {
			return err
		}
		if eventType == "approved" {
			if err := requireNamespacePermission(currentPrincipal, namespace.ID, permissionReview); err != nil {
				return err
			}
		}
		if err := requireNamespacePermission(currentPrincipal, namespace.ID, permissionCode); err != nil {
			return err
		}
		item, err := s.repository.LockItem(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		if namespace.Status == StatusArchived || item.Status == StatusArchived {
			return conflict("resource_archived", "归档配置不可执行工作流")
		}
		current, err := s.repository.GetItemValue(ctx, q, namespaceID, itemID)
		if err != nil {
			return err
		}
		expected := current.CurrentDraftRevision.ID
		if !draftPointer && eventType == "unpublished" {
			expected = ""
			if current.CurrentPublishedRevisionID != nil {
				expected = *current.CurrentPublishedRevisionID
			}
		}
		if expected != revisionID {
			return conflict("workflow_revision_conflict", "目标 Revision 已不是当前指针")
		}
		revision, err := s.repository.LockRevision(ctx, q, namespaceID, itemID, revisionID)
		if err != nil {
			return err
		}
		if revision.WorkflowStatus != from {
			return conflict("invalid_workflow_transition", "Revision 状态不允许该转换")
		}
		if eventType == "approved" {
			if err := s.repository.ValidateAssetsPublishable(ctx, q, revisionID, revision.Constraints); err != nil {
				return err
			}
			if err := s.repository.ValidateRelationTargetsPublished(ctx, q, revisionID); err != nil {
				return err
			}
		}
		now := s.now()
		var submitter *string
		var submittedAt *time.Time
		if eventType == "submitted" {
			submitter, submittedAt = &currentPrincipal.UserID, &now
		}
		changed, err := s.repository.TransitionRevision(ctx, q, revisionID, from, to, submitter, submittedAt)
		if err != nil {
			return err
		}
		if !changed {
			return conflict("invalid_workflow_transition", "Revision 状态不允许该转换")
		}
		if eventType == "approved" {
			if current.CurrentPublishedRevisionID != nil && *current.CurrentPublishedRevisionID != revisionID {
				changed, err := s.repository.TransitionRevision(ctx, q, *current.CurrentPublishedRevisionID, WorkflowPublished, WorkflowUnpublished, nil, nil)
				if err != nil {
					return err
				}
				if !changed {
					return conflict("workflow_revision_conflict", "当前发布 Revision 已变化")
				}
			}
			if err := s.repository.SetPublishedPointer(ctx, q, namespaceID, itemID, revisionID, now); err != nil {
				return err
			}
		}
		if eventType == "unpublished" {
			deleted, err := s.repository.DeletePublishedPointer(ctx, q, namespaceID, itemID, revisionID)
			if err != nil {
				return err
			}
			if !deleted {
				return conflict("workflow_revision_conflict", "目标 Revision 已不是当前发布指针")
			}
		}
		eventID, err := s.newID("cwe_")
		if err != nil {
			return err
		}
		var reasonPointer *string
		if reason != "" {
			reasonPointer = &reason
		}
		if err := s.repository.CreateWorkflowEvent(ctx, q, WorkflowEvent{ID: eventID, ItemID: itemID, NamespaceID: namespaceID, RevisionID: revisionID, Type: eventType, FromStatus: from, ToStatus: to, ActorID: currentPrincipal.UserID, Reason: reasonPointer, OccurredAt: now}); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, currentPrincipal, meta, "configuration_revision_"+eventType, "configuration_revision", revisionID, map[string]any{"namespace_key": namespace.Key, "item_id": itemID, "from": from, "to": to}); err != nil {
			return err
		}
		result = current
		if current.CurrentDraftRevision.ID == revisionID {
			result.CurrentDraftRevision = revision
			result.CurrentDraftRevision.WorkflowStatus = to
		}
		if eventType == "submitted" {
			result.CurrentDraftRevision.SubmittedBy, result.CurrentDraftRevision.SubmittedAt = submitter, submittedAt
		}
		if eventType == "approved" {
			result.CurrentPublishedRevisionID = &revisionID
			published := result.CurrentDraftRevision
			result.CurrentPublishedRevision = &published
		}
		if eventType == "unpublished" {
			result.CurrentPublishedRevisionID, result.CurrentPublishedRevision = nil, nil
		}
		return nil
	})
	return result, err
}

func (s *Service) currentPrincipal(ctx context.Context, q database.Querier, principal identity.Principal) (identity.Principal, error) {
	return s.authorizer.CurrentPrincipal(ctx, q, principal)
}

func requiredRevision() error {
	var failures validationErrors
	failures.add("/revision_id", "required", "revision_id 为必填项")
	return failures.err()
}

func requireNamespacePermission(principal identity.Principal, namespaceID, required string) error {
	for _, grant := range principal.ConfigNamespacePermissions {
		if grant.ConfigNamespaceID != namespaceID {
			continue
		}
		for _, code := range grant.Permissions {
			if code == required {
				return nil
			}
		}
	}
	return permissionDenied()
}

func constraintsEqual(left, right Constraints) bool {
	leftJSON, _ := json.Marshal(left)
	rightJSON, _ := json.Marshal(right)
	return string(leftJSON) == string(rightJSON)
}

func makeAssetReferences(revisionID string, item Item, ids []string) []AssetReference {
	result := make([]AssetReference, len(ids))
	for i, id := range ids {
		result[i] = AssetReference{RevisionID: revisionID, ItemID: item.ID, NamespaceID: item.NamespaceID, AssetID: id, Position: i}
	}
	return result
}

func makeRelations(revisionID string, item Item, ids []string) []Relation {
	result := make([]Relation, len(ids))
	targetModelID := ""
	if item.Constraints.TargetModelID != nil {
		targetModelID = *item.Constraints.TargetModelID
	}
	for i, id := range ids {
		result[i] = Relation{RevisionID: revisionID, ItemID: item.ID, NamespaceID: item.NamespaceID, TargetEntryID: id, TargetModelID: targetModelID, Position: i}
	}
	return result
}

func (s *Service) appendAudit(ctx context.Context, q database.Querier, principal identity.Principal, meta RequestMeta, action, resourceType, resourceID string, changes map[string]any) error {
	if s.audit == nil {
		return fmt.Errorf("审计写入器未装配")
	}
	id, err := s.newID("evt_")
	if err != nil {
		return err
	}
	actor, actorName := principal.UserID, principal.DisplayName
	return s.audit.Append(ctx, q, audit.Event{ID: id, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actor, ActorDisplayName: &actorName, Action: action, ResourceType: resourceType, ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}

func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成配置资源 ID: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

type configurationCursor struct {
	Kind        string `json:"kind"`
	NamespaceID string `json:"namespace_id"`
	ItemID      string `json:"item_id"`
	Number      uint   `json:"revision_number,omitempty"`
	OccurredAt  string `json:"occurred_at,omitempty"`
	ID          string `json:"id,omitempty"`
}

func encodeConfigurationCursor(cursor configurationCursor) (string, error) {
	data, err := json.Marshal(cursor)
	if err != nil {
		return "", fmt.Errorf("编码配置分页游标: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func decodeConfigurationCursor(value string) (configurationCursor, error) {
	var cursor configurationCursor
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil || json.Unmarshal(data, &cursor) != nil {
		return cursor, invalidCursor()
	}
	return cursor, nil
}

func decodeRevisionCursor(value, namespaceID, itemID string) (*uint, error) {
	if value == "" {
		return nil, nil
	}
	cursor, err := decodeConfigurationCursor(value)
	if err != nil || cursor.Kind != "revisions" || cursor.NamespaceID != namespaceID || cursor.ItemID != itemID || cursor.Number == 0 {
		return nil, invalidCursor()
	}
	return &cursor.Number, nil
}

func decodeWorkflowEventCursor(value, namespaceID, itemID string) (*WorkflowEventCursor, error) {
	if value == "" {
		return nil, nil
	}
	cursor, err := decodeConfigurationCursor(value)
	if err != nil || cursor.Kind != "workflow_events" || cursor.NamespaceID != namespaceID || cursor.ItemID != itemID || cursor.ID == "" {
		return nil, invalidCursor()
	}
	occurredAt, err := time.Parse(time.RFC3339Nano, cursor.OccurredAt)
	if err != nil {
		return nil, invalidCursor()
	}
	return &WorkflowEventCursor{OccurredAt: occurredAt.UTC(), ID: cursor.ID}, nil
}
