package schema

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/permission"
	"cms/internal/platform/database"
)

type ContentExistenceChecker interface {
	HasAnyContent(context.Context, string) (bool, error)
}

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type Service struct {
	db         database.Querier
	tx         TransactionRunner
	repository Repository
	authorizer permission.Authorizer
	audit      audit.Writer
	content    ContentExistenceChecker
	now        func() time.Time
	newID      func(string) (string, error)
}

var errFieldChanged = errors.New("字段在加锁前已变化")

type Dependencies struct {
	DB         database.Querier
	Transactor database.Transactor
	Repository Repository
	Authorizer permission.Authorizer
	Audit      audit.Writer
	Content    ContentExistenceChecker
}

func NewService(dependencies Dependencies) *Service {
	return &Service{db: dependencies.DB, tx: dependencies.Transactor, repository: dependencies.Repository, authorizer: dependencies.Authorizer, audit: dependencies.Audit, content: dependencies.Content, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: randomID}
}

func (s *Service) ListModels(ctx context.Context, principal identity.Principal, status *ResourceStatus) ([]ContentModelSummary, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsView); err != nil {
		return nil, err
	}
	return s.repository.ListModels(ctx, s.db, status)
}
func (s *Service) GetModel(ctx context.Context, principal identity.Principal, id string) (ContentModel, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsView); err != nil {
		return ContentModel{}, err
	}
	return s.repository.GetModel(ctx, s.db, id)
}
func (s *Service) GetField(ctx context.Context, principal identity.Principal, modelID, fieldID string) (ContentField, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsView); err != nil {
		return ContentField{}, err
	}
	if _, err := s.repository.GetModel(ctx, s.db, modelID); err != nil {
		return ContentField{}, err
	}
	return s.repository.GetField(ctx, s.db, modelID, fieldID)
}
func (s *Service) ListFields(ctx context.Context, principal identity.Principal, modelID string) ([]ContentField, error) {
	model, err := s.GetModel(ctx, principal, modelID)
	return model.Fields, err
}

func (s *Service) CreateModel(ctx context.Context, principal identity.Principal, meta RequestMeta, request CreateContentModelRequest) (ContentModel, error) {
	if err := ValidateModelCreate(request); err != nil {
		return ContentModel{}, err
	}
	id, err := s.newID("mdl_")
	if err != nil {
		return ContentModel{}, err
	}
	now := s.now()
	model := ContentModel{ContentModelSummary: ContentModelSummary{ID: id, Key: request.Key, DisplayName: request.DisplayName, Description: request.Description, Status: StatusActive, CreatedAt: now, UpdatedAt: now}, Fields: []ContentField{}}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsCreate); err != nil {
			return err
		}
		if err := s.repository.CreateModel(ctx, q, model.ContentModelSummary); err != nil {
			if errors.Is(err, ErrDuplicateKey) {
				return conflict("key_conflict", "模型 key 已存在")
			}
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_created", "content_model", model.ID, map[string]any{"key": model.Key})
	})
	return model, err
}

func (s *Service) UpdateModel(ctx context.Context, principal identity.Principal, meta RequestMeta, id string, request UpdateContentModelRequest) (ContentModel, error) {
	if err := ValidateModelPatch(request); err != nil {
		return ContentModel{}, err
	}
	var result ContentModel
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsUpdate); err != nil {
			return err
		}
		model, err := s.repository.LockModel(ctx, q, id)
		if err != nil {
			return err
		}
		if model.Status == StatusArchived {
			return conflict("resource_archived", "模型已归档")
		}
		changes := map[string]any{}
		if request.DisplayName != nil {
			changes["display_name"] = map[string]any{"from": model.DisplayName, "to": *request.DisplayName}
			model.DisplayName = *request.DisplayName
		}
		if request.Description != nil {
			changes["description"] = map[string]any{"from": model.Description, "to": *request.Description}
			model.Description = *request.Description
		}
		model.UpdatedAt = s.now()
		if err := s.repository.UpdateModel(ctx, q, model); err != nil {
			return err
		}
		if err := s.appendAudit(ctx, q, principal, meta, "model_updated", "content_model", id, changes); err != nil {
			return err
		}
		result, err = s.repository.GetModel(ctx, q, id)
		return err
	})
	return result, err
}

func (s *Service) ArchiveModel(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsArchive); err != nil {
			return err
		}
		model, err := s.repository.LockModel(ctx, q, id)
		if err != nil {
			return err
		}
		if model.Status == StatusArchived {
			return conflict("resource_archived", "模型已归档")
		}
		model.Status = StatusArchived
		model.UpdatedAt = s.now()
		if err := s.repository.UpdateModel(ctx, q, model); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_archived", "content_model", id, map[string]any{"status": map[string]any{"from": StatusActive, "to": StatusArchived}})
	})
}

func (s *Service) CreateField(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID string, input ContentFieldInput) (ContentField, error) {
	if err := ValidateField(&input); err != nil {
		return ContentField{}, err
	}
	var result ContentField
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsCreate); err != nil {
			return err
		}
		models, err := s.lockRelationModels(ctx, q, modelID, input)
		if err != nil {
			return err
		}
		model := models[modelID]
		if model.Status == StatusArchived {
			return conflict("resource_archived", "归档模型不能新增字段")
		}
		now := s.now()
		result, err = s.buildField(input, 0, now)
		if err != nil {
			return err
		}
		if err := s.repository.CreateFieldTree(ctx, q, modelID, &result, nil, 0); err != nil {
			if errors.Is(err, ErrDuplicateKey) {
				return conflict("key_conflict", "字段 key 已存在")
			}
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_field_created", "content_field", result.ID, map[string]any{"model_id": modelID, "key": result.Key})
	})
	return result, err
}

func (s *Service) UpdateField(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, fieldID string, patch ContentFieldPatch) (ContentField, error) {
	if patch.Empty() {
		var failures validationErrors
		failures.add("", "required", "至少提交一个可修改属性")
		return ContentField{}, failures.err()
	}
	var result ContentField
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
			if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsUpdate); err != nil {
				return err
			}
			field, err := s.repository.GetField(ctx, q, modelID, fieldID)
			if err != nil {
				return err
			}
			if field.Status == StatusArchived {
				return conflict("resource_archived", "归档字段不能修改")
			}
			fieldToInputSnapshot := fieldToInput(field)
			input := fieldToInputSnapshot
			typeChanged := patch.Type != nil && *patch.Type != field.Type
			if patch.Type != nil {
				var failures validationErrors
				if patch.DefaultValue == nil {
					failures.add("/default_value", "required", "修改类型时必须显式提交 default_value")
				}
				if patch.Constraints == nil {
					failures.add("/constraints", "required", "修改类型时必须显式提交 constraints")
				}
				if patch.Children == nil {
					failures.add("/children", "required", "修改类型时必须显式提交 children")
				}
				if err := failures.err(); err != nil {
					return err
				}
			}
			if patch.DisplayName != nil {
				input.DisplayName = *patch.DisplayName
			}
			if patch.Description != nil {
				input.Description = *patch.Description
			}
			if patch.Type != nil {
				input.Type = *patch.Type
			}
			if patch.Required != nil {
				input.Required = *patch.Required
			}
			if patch.DefaultValue != nil {
				input.DefaultValue = *patch.DefaultValue
			}
			if patch.Constraints != nil {
				input.Constraints = *patch.Constraints
			}
			if patch.Children != nil {
				input.Children = *patch.Children
			}
			if err := validateFieldAtDepth(&input, field.Depth); err != nil {
				return err
			}
			models, err := s.lockRelationModels(ctx, q, modelID, input)
			if err != nil {
				return err
			}
			if models[modelID].Status == StatusArchived {
				return conflict("resource_archived", "归档模型不能修改字段")
			}
			field, err = s.repository.LockField(ctx, q, modelID, fieldID)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(fieldToInput(field), fieldToInputSnapshot) {
				return errFieldChanged
			}
			if field.Status == StatusArchived {
				return conflict("resource_archived", "归档字段不能修改")
			}
			if patch.Constraints != nil {
				if err := validateEnumStability(field.Constraints, input.Constraints); err != nil {
					return err
				}
			}
			childrenTypeChanged := patch.Children != nil && childTypeChanged(field.Children, input.Children)
			if typeChanged || childrenTypeChanged {
				if s.content == nil {
					return fmt.Errorf("ContentExistenceChecker 未装配")
				}
				exists, err := s.content.HasAnyContent(ctx, modelID)
				if err != nil {
					return err
				}
				if exists {
					return conflict("field_type_locked", "模型已有内容，字段类型不可修改")
				}
			}
			projectionEnabled := !field.Constraints.Unique && input.Constraints.Unique || !field.Constraints.Filterable && input.Constraints.Filterable || !field.Constraints.Sortable && input.Constraints.Sortable
			if projectionEnabled {
				if s.content == nil {
					return fmt.Errorf("ContentExistenceChecker 未装配")
				}
				exists, err := s.content.HasAnyContent(ctx, modelID)
				if err != nil {
					return err
				}
				if exists {
					return conflict("field_projection_backfill_required", "模型已有内容，启用查询能力需要受控回填")
				}
			}
			now := s.now()
			field.DisplayName = input.DisplayName
			field.Description = input.Description
			field.Type = input.Type
			field.Required = input.Required
			field.DefaultValue = input.DefaultValue
			field.Constraints = input.Constraints
			field.UpdatedAt = now
			if err := s.repository.UpdateField(ctx, q, modelID, field); err != nil {
				return err
			}
			if patch.Children != nil {
				if _, err := s.reconcileChildren(ctx, q, modelID, field, input.Children, now); err != nil {
					if errors.Is(err, ErrDuplicateKey) {
						return conflict("key_conflict", "字段 key 已存在")
					}
					return err
				}
			}
			if err := s.appendAudit(ctx, q, principal, meta, "model_field_updated", "content_field", fieldID, map[string]any{"model_id": modelID}); err != nil {
				return err
			}
			result, err = s.repository.GetField(ctx, q, modelID, fieldID)
			return err
		})
		if !errors.Is(err, errFieldChanged) {
			break
		}
	}
	return result, err
}

func (s *Service) ArchiveField(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, fieldID string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsArchive); err != nil {
			return err
		}
		model, err := s.repository.LockModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		if model.Status == StatusArchived {
			return conflict("resource_archived", "模型已归档")
		}
		field, err := s.repository.GetField(ctx, q, modelID, fieldID)
		if err != nil {
			return err
		}
		if field.Status == StatusArchived {
			return conflict("resource_archived", "字段已归档")
		}
		if err := s.repository.ArchiveFieldTree(ctx, q, modelID, fieldID, s.now()); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_field_archived", "content_field", fieldID, map[string]any{"model_id": modelID})
	})
}

func (s *Service) reconcileChildren(ctx context.Context, q database.Querier, modelID string, parent ContentField, inputs []ContentFieldInput, now time.Time) ([]ContentField, error) {
	existing := map[string]ContentField{}
	for _, child := range parent.Children {
		existing[child.Key] = child
	}
	result := make([]ContentField, 0, len(inputs))
	for i, input := range inputs {
		if old, ok := existing[input.Key]; ok {
			if old.Status == StatusArchived {
				return nil, conflict("key_conflict", "归档字段 key 不可复用")
			}
			child := old
			if err := validateEnumStability(old.Constraints, input.Constraints); err != nil {
				return nil, err
			}
			child.DisplayName = input.DisplayName
			child.Description = input.Description
			child.Type = input.Type
			child.Required = input.Required
			child.DefaultValue = input.DefaultValue
			child.Constraints = input.Constraints
			child.UpdatedAt = now
			if err := s.repository.UpdateField(ctx, q, modelID, child); err != nil {
				return nil, err
			}
			if err := s.repository.UpdateFieldPosition(ctx, q, modelID, child.ID, i); err != nil {
				return nil, err
			}
			nested, err := s.reconcileChildren(ctx, q, modelID, child, input.Children, now)
			if err != nil {
				return nil, err
			}
			child.Children = nested
			result = append(result, child)
			delete(existing, input.Key)
		} else {
			child, err := s.buildField(input, parent.Depth+1, now)
			if err != nil {
				return nil, err
			}
			if err := s.repository.CreateFieldTree(ctx, q, modelID, &child, &parent.ID, i); err != nil {
				return nil, err
			}
			result = append(result, child)
		}
	}
	for _, old := range existing {
		if old.Status == StatusActive {
			if err := s.repository.ArchiveFieldTree(ctx, q, modelID, old.ID, now); err != nil {
				return nil, err
			}
		}
	}
	return result, nil
}

func (s *Service) buildField(input ContentFieldInput, depth int, now time.Time) (ContentField, error) {
	id, err := s.newID("fld_")
	if err != nil {
		return ContentField{}, err
	}
	field := ContentField{ID: id, Key: input.Key, DisplayName: input.DisplayName, Description: input.Description, Type: input.Type, Required: input.Required, DefaultValue: input.DefaultValue, Constraints: input.Constraints, Children: []ContentField{}, Status: StatusActive, CreatedAt: now, UpdatedAt: now, Depth: depth}
	for _, childInput := range input.Children {
		child, err := s.buildField(childInput, depth+1, now)
		if err != nil {
			return ContentField{}, err
		}
		field.Children = append(field.Children, child)
	}
	return field, nil
}
func (s *Service) lockRelationModels(ctx context.Context, q database.Querier, modelID string, input ContentFieldInput) (map[string]ContentModelSummary, error) {
	ids := map[string]bool{modelID: true}
	var collect func(ContentFieldInput)
	collect = func(field ContentFieldInput) {
		if field.Type == FieldTypeSingleRelation || field.Type == FieldTypeMultiRelation {
			ids[*field.Constraints.TargetModelID] = true
		}
		for _, child := range field.Children {
			collect(child)
		}
	}
	collect(input)
	ordered := make([]string, 0, len(ids))
	for id := range ids {
		ordered = append(ordered, id)
	}
	sort.Strings(ordered)
	models, err := s.repository.LockModels(ctx, q, ordered)
	if err != nil {
		return nil, err
	}
	if _, ok := models[modelID]; !ok {
		return nil, notFound("模型")
	}
	for _, id := range ordered {
		if id == modelID {
			continue
		}
		model, ok := models[id]
		if !ok || model.Status != StatusActive {
			return nil, conflict("target_model_invalid", "关联目标模型无效或已归档")
		}
	}
	return models, nil
}
func validateFieldAtDepth(input *ContentFieldInput, depth int) error {
	var failures validationErrors
	validateField(input, depth, "", map[string]bool{}, &failures)
	return failures.err()
}
func fieldToInput(field ContentField) ContentFieldInput {
	children := make([]ContentFieldInput, len(field.Children))
	for i := range field.Children {
		children[i] = fieldToInput(field.Children[i])
	}
	return ContentFieldInput{Key: field.Key, DisplayName: field.DisplayName, Description: field.Description, Type: field.Type, Required: field.Required, DefaultValue: field.DefaultValue, Constraints: field.Constraints, Children: children}
}

func childTypeChanged(existing []ContentField, inputs []ContentFieldInput) bool {
	byKey := make(map[string]ContentField, len(existing))
	for _, child := range existing {
		byKey[child.Key] = child
	}
	for _, input := range inputs {
		if child, ok := byKey[input.Key]; ok && (child.Type != input.Type || childTypeChanged(child.Children, input.Children)) {
			return true
		}
	}
	return false
}

func validateEnumStability(old, updated FieldConstraints) error {
	for _, option := range old.EnumOptions {
		if !enumContains(updated.EnumOptions, option.Value) {
			return conflict("enum_value_immutable", "已有枚举 value 不可修改或删除")
		}
	}
	return nil
}

type RequestMeta struct{ RequestID, IP, UserAgent string }

func (s *Service) appendAudit(ctx context.Context, q database.Querier, principal identity.Principal, meta RequestMeta, action, resourceType, resourceID string, changes map[string]any) error {
	eventID, err := s.newID("evt_")
	if err != nil {
		return err
	}
	actorID := principal.UserID
	return s.audit.Append(ctx, q, audit.Event{ID: eventID, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, Action: action, ResourceType: resourceType, ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}
func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成资源 ID: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
