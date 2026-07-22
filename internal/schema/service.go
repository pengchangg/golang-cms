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
	HasAnyContent(context.Context, database.Querier, string) (bool, error)
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
	model, err := s.repository.GetModel(ctx, s.db, id)
	if err == nil {
		model.Fields = normalizeFields(model.Fields)
	}
	return model, err
}
func (s *Service) GetField(ctx context.Context, principal identity.Principal, modelID, fieldID string) (ContentField, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsView); err != nil {
		return ContentField{}, err
	}
	if _, err := s.repository.GetModel(ctx, s.db, modelID); err != nil {
		return ContentField{}, err
	}
	field, err := s.repository.GetField(ctx, s.db, modelID, fieldID)
	if err == nil {
		field.Children = normalizeFields(field.Children)
	}
	return field, err
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
	if err == nil {
		result.Fields = normalizeFields(result.Fields)
	}
	return result, err
}

func (s *Service) ArchiveModel(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsArchive); err != nil {
			return err
		}
		sourceIDs, err := s.repository.InboundRelationModelIDs(ctx, q, id, false)
		if err != nil {
			return err
		}
		modelSet := map[string]bool{id: true}
		for _, sourceID := range sourceIDs {
			modelSet[sourceID] = true
		}
		modelIDs := make([]string, 0, len(modelSet))
		for modelID := range modelSet {
			modelIDs = append(modelIDs, modelID)
		}
		sort.Strings(modelIDs)
		models, err := s.repository.LockModels(ctx, q, modelIDs)
		if err != nil {
			return err
		}
		model, ok := models[id]
		if !ok {
			return notFound("模型")
		}
		if model.Status == StatusArchived {
			return conflict("resource_archived", "模型已归档")
		}
		inbound, err := s.repository.InboundRelationModelIDs(ctx, q, id, true)
		if err != nil {
			return err
		}
		if len(inbound) > 0 {
			return conflict("model_referenced", "模型仍被活动关联字段引用")
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
		models, err := s.lockRelationModels(ctx, q, modelID, input, nil)
		if err != nil {
			return err
		}
		model := models[modelID]
		if model.Status == StatusArchived {
			return conflict("resource_archived", "归档模型不能新增字段")
		}
		fields, err := s.repository.LockFieldTree(ctx, q, modelID)
		if err != nil {
			return err
		}
		position := int64(0)
		for _, field := range fields {
			if field.ParentID == nil && field.Status == StatusActive && field.Position >= position {
				position = field.Position + 1
			}
		}
		now := s.now()
		result, err = s.buildField(input, 0, now)
		if err != nil {
			return err
		}
		if err := s.repository.CreateFieldTree(ctx, q, modelID, &result, nil, int(position)); err != nil {
			if errors.Is(err, ErrDuplicateKey) {
				return conflict("key_conflict", "字段 key 已存在")
			}
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_field_created", "content_field", result.ID, map[string]any{"model_id": modelID, "key": result.Key})
	})
	return result, err
}

func (s *Service) UpdateFieldOrder(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID string, request UpdateFieldOrderRequest) error {
	if err := request.Validate(); err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsUpdate); err != nil {
			return err
		}
		model, err := s.repository.LockModel(ctx, q, modelID)
		if err != nil {
			return err
		}
		if model.Status == StatusArchived {
			return conflict("resource_archived", "归档模型不能修改字段顺序")
		}
		fields, err := s.repository.LockFieldTree(ctx, q, modelID)
		if err != nil {
			return err
		}
		if request.ParentID != nil {
			var parent *ContentField
			for _, field := range fields {
				if field.ID == *request.ParentID {
					item := field
					parent = &item
					break
				}
			}
			if parent == nil || parent.Status != StatusActive || !isChildContainer(*parent) {
				return conflict("field_order_conflict", "父字段必须是活动且可包含子字段的容器")
			}
		}
		current := make([]string, 0)
		for _, field := range fields {
			if field.Status == StatusActive && sameParent(field.ParentID, request.ParentID) {
				current = append(current, field.ID)
			}
		}
		if !reflect.DeepEqual(current, request.BaseFieldIDs) || !sameStringSet(current, request.FieldIDs) {
			return conflict("field_order_conflict", "字段顺序基线已变化或请求不是同级完整集合")
		}
		if err := s.repository.UpdateFieldOrder(ctx, q, modelID, request.ParentID, request.FieldIDs); err != nil {
			if errors.Is(err, ErrDuplicateKey) {
				return conflict("field_order_conflict", "字段顺序已变化")
			}
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_field_order_updated", "content_model", modelID, map[string]any{"parent_id": request.ParentID, "from": request.BaseFieldIDs, "to": request.FieldIDs})
	})
}

func (s *Service) CreateChildField(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, parentFieldID string, input ContentFieldInput) (ContentField, error) {
	if err := ValidateField(&input); err != nil {
		return ContentField{}, err
	}
	var result ContentField
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, permission.ModelsUpdate); err != nil {
			return err
		}
		models, err := s.lockRelationModels(ctx, q, modelID, input, nil)
		if err != nil {
			return err
		}
		if models[modelID].Status != StatusActive {
			return conflict("resource_archived", "归档模型不能新增子字段")
		}
		fields, err := s.repository.LockFieldTree(ctx, q, modelID)
		if err != nil {
			return err
		}
		var parent *ContentField
		position := int64(0)
		for _, field := range fields {
			if field.ID == parentFieldID {
				item := field
				parent = &item
			}
			if field.Status == StatusActive && field.ParentID != nil && *field.ParentID == parentFieldID && field.Position >= position {
				position = field.Position + 1
			}
		}
		if parent == nil {
			return notFound("父字段")
		}
		if parent.Status != StatusActive {
			return conflict("resource_archived", "归档父字段不能新增子字段")
		}
		if !isChildContainer(*parent) {
			return conflict("parent_field_invalid", "父字段必须是深度小于 2 的对象或重复组")
		}
		if err := validateFieldAtDepth(&input, parent.Depth+1); err != nil {
			return err
		}
		now := s.now()
		result, err = s.buildField(input, parent.Depth+1, now)
		if err != nil {
			return err
		}
		if err := s.repository.CreateFieldTree(ctx, q, modelID, &result, &parent.ID, int(position)); err != nil {
			if errors.Is(err, ErrDuplicateKey) {
				return conflict("key_conflict", "字段 key 已存在")
			}
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "model_field_created", "content_field", result.ID, map[string]any{"model_id": modelID, "parent_field_id": parent.ID, "key": result.Key})
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
			models, err := s.lockRelationModels(ctx, q, modelID, input, selfRelationPaths(fieldToInputSnapshot, modelID))
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
			if patch.Children != nil {
				if err := validateChildEnumStability(field.Children, input.Children); err != nil {
					return err
				}
			}
			childrenTypeChanged := patch.Children != nil && childTypeChanged(field.Children, input.Children)
			schemaTightened := fieldSchemaTightened(field, input)
			projectionEnabled := !field.Constraints.Unique && input.Constraints.Unique || !field.Constraints.Filterable && input.Constraints.Filterable || !field.Constraints.Sortable && input.Constraints.Sortable
			if typeChanged || childrenTypeChanged || schemaTightened || projectionEnabled {
				if s.content == nil {
					return fmt.Errorf("ContentExistenceChecker 未装配")
				}
				exists, err := s.content.HasAnyContent(ctx, q, modelID)
				if err != nil {
					return err
				}
				if exists {
					switch {
					case typeChanged || childrenTypeChanged:
						return conflict("field_type_locked", "模型已有内容，字段类型不可修改")
					case schemaTightened:
						return conflict("field_schema_migration_required", "模型已有内容，收紧字段约束前必须先迁移数据")
					default:
						return conflict("field_projection_backfill_required", "模型已有内容，启用查询能力需要受控回填")
					}
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
	if err == nil {
		result.Children = normalizeFields(result.Children)
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
	if err := s.repository.PrepareFieldOrder(ctx, q, modelID, &parent.ID); err != nil {
		return nil, err
	}
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

func normalizeFields(fields []ContentField) []ContentField {
	if fields == nil {
		return []ContentField{}
	}
	for i := range fields {
		fields[i].Children = normalizeFields(fields[i].Children)
	}
	return fields
}

func sameParent(left, right *string) bool {
	return left == nil && right == nil || left != nil && right != nil && *left == *right
}

func isChildContainer(field ContentField) bool {
	return (field.Type == FieldTypeObject || field.Type == FieldTypeRepeatableGroup) && field.Depth < 2
}

func sameStringSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	values := make(map[string]bool, len(left))
	for _, value := range left {
		values[value] = true
	}
	for _, value := range right {
		if !values[value] {
			return false
		}
	}
	return true
}

func (s *Service) lockRelationModels(ctx context.Context, q database.Querier, modelID string, input ContentFieldInput, existingSelfRelations map[string]bool) (map[string]ContentModelSummary, error) {
	ids := map[string]bool{modelID: true}
	var relationErr error
	var collect func(ContentFieldInput, string)
	collect = func(field ContentFieldInput, parentPath string) {
		path := parentPath + "/" + field.Key
		if field.Type == FieldTypeSingleRelation || field.Type == FieldTypeMultiRelation {
			if *field.Constraints.TargetModelID == modelID && !existingSelfRelations[path] {
				relationErr = conflict("target_model_self_relation", "关联目标不能是当前模型")
				return
			}
			ids[*field.Constraints.TargetModelID] = true
		}
		for _, child := range field.Children {
			if relationErr != nil {
				return
			}
			collect(child, path)
		}
	}
	collect(input, "")
	if relationErr != nil {
		return nil, relationErr
	}
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

func selfRelationPaths(input ContentFieldInput, modelID string) map[string]bool {
	paths := map[string]bool{}
	var collect func(ContentFieldInput, string)
	collect = func(field ContentFieldInput, parentPath string) {
		path := parentPath + "/" + field.Key
		if (field.Type == FieldTypeSingleRelation || field.Type == FieldTypeMultiRelation) && *field.Constraints.TargetModelID == modelID {
			paths[path] = true
		}
		for _, child := range field.Children {
			collect(child, path)
		}
	}
	collect(input, "")
	return paths
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

func fieldSchemaTightened(existing ContentField, updated ContentFieldInput) bool {
	if !existing.Required && updated.Required || lowerBoundTightened(existing.Constraints.MinLength, updated.Constraints.MinLength) || upperBoundTightened(existing.Constraints.MaxLength, updated.Constraints.MaxLength) || numericLowerBoundTightened(existing.Constraints.Minimum, updated.Constraints.Minimum) || numericUpperBoundTightened(existing.Constraints.Maximum, updated.Constraints.Maximum) {
		return true
	}
	if existing.Constraints.TargetModelID != nil && updated.Constraints.TargetModelID != nil && *existing.Constraints.TargetModelID != *updated.Constraints.TargetModelID {
		return true
	}
	updatedChildren := make(map[string]ContentFieldInput, len(updated.Children))
	for _, child := range updated.Children {
		updatedChildren[child.Key] = child
	}
	for _, child := range existing.Children {
		if child.Status != StatusActive {
			continue
		}
		updatedChild, ok := updatedChildren[child.Key]
		if !ok || fieldSchemaTightened(child, updatedChild) {
			return true
		}
		delete(updatedChildren, child.Key)
	}
	for _, child := range updatedChildren {
		if child.Required {
			return true
		}
	}
	return false
}

func lowerBoundTightened(old, updated *int) bool {
	return updated != nil && (old == nil && *updated > 0 || old != nil && *updated > *old)
}

func upperBoundTightened(old, updated *int) bool {
	return updated != nil && (old == nil || *updated < *old)
}

func numericLowerBoundTightened(old, updated *string) bool {
	if updated == nil {
		return false
	}
	if old == nil {
		return true
	}
	oldValue, oldOK := decimalRat(*old)
	updatedValue, updatedOK := decimalRat(*updated)
	return oldOK && updatedOK && updatedValue.Cmp(oldValue) > 0
}

func numericUpperBoundTightened(old, updated *string) bool {
	if updated == nil {
		return false
	}
	if old == nil {
		return true
	}
	oldValue, oldOK := decimalRat(*old)
	updatedValue, updatedOK := decimalRat(*updated)
	return oldOK && updatedOK && updatedValue.Cmp(oldValue) < 0
}

func validateEnumStability(old, updated FieldConstraints) error {
	for _, option := range old.EnumOptions {
		if !enumContains(updated.EnumOptions, option.Value) {
			return conflict("enum_value_immutable", "已有枚举 value 不可修改或删除")
		}
	}
	return nil
}

func validateChildEnumStability(existing []ContentField, updated []ContentFieldInput) error {
	updatedByKey := make(map[string]ContentFieldInput, len(updated))
	for _, child := range updated {
		updatedByKey[child.Key] = child
	}
	for _, child := range existing {
		if child.Status != StatusActive {
			continue
		}
		updatedChild, ok := updatedByKey[child.Key]
		if !ok {
			continue
		}
		if err := validateEnumStability(child.Constraints, updatedChild.Constraints); err != nil {
			return err
		}
		if err := validateChildEnumStability(child.Children, updatedChild.Children); err != nil {
			return err
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
	actorName := principal.DisplayName
	return s.audit.Append(ctx, q, audit.Event{ID: eventID, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, ActorDisplayName: &actorName, Action: action, ResourceType: resourceType, ResourceID: &resourceID, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}
func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("生成资源 ID: %w", err)
	}
	return prefix + hex.EncodeToString(value[:]), nil
}
