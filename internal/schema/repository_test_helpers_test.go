package schema

import (
	"context"

	"cms/internal/platform/database"
)

type rereadRepository struct{ *memoryRepository }

func (m *memoryRepository) LockField(ctx context.Context, q database.Querier, modelID, fieldID string) (ContentField, error) {
	return m.GetField(ctx, q, modelID, fieldID)
}

func (r *rereadRepository) GetField(ctx context.Context, q database.Querier, modelID, fieldID string) (ContentField, error) {
	field, err := r.memoryRepository.GetField(ctx, q, modelID, fieldID)
	if err != nil {
		return ContentField{}, err
	}
	var refresh func(ContentField) ContentField
	refresh = func(item ContentField) ContentField {
		if current, ok := r.fields[item.ID]; ok {
			item.Status = current.Status
			item.UpdatedAt = current.UpdatedAt
		}
		for i := range item.Children {
			item.Children[i] = refresh(item.Children[i])
		}
		return item
	}
	return refresh(field), nil
}

func (r *rereadRepository) LockField(ctx context.Context, q database.Querier, modelID, fieldID string) (ContentField, error) {
	return r.GetField(ctx, q, modelID, fieldID)
}
