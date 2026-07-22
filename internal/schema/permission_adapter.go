package schema

import (
	"context"

	"cms/internal/permission"
	"cms/internal/platform/database"
)

type PermissionModelAdapter struct {
	DB         database.Querier
	Repository Repository
}

func (a PermissionModelAdapter) ActiveModelIDs(ctx context.Context) ([]string, error) {
	return a.ActiveModelIDsWith(ctx, a.DB)
}

func (a PermissionModelAdapter) ActiveModelIDsWith(ctx context.Context, q database.Querier) ([]string, error) {
	status := StatusActive
	models, err := a.Repository.ListModels(ctx, q, &status)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(models))
	for index, model := range models {
		ids[index] = model.ID
	}
	return ids, nil
}

func (a PermissionModelAdapter) ValidateActiveModels(ctx context.Context, q database.Querier, ids []string) error {
	models, err := a.Repository.LockModels(ctx, q, ids)
	if err != nil {
		return err
	}
	for _, id := range ids {
		model, ok := models[id]
		if !ok || model.Status != StatusActive {
			return permission.ErrInvalidModels
		}
	}
	return nil
}
