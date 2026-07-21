package integration

import (
	"context"

	"cms/internal/asset"
	"cms/internal/content"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

type MediaReferenceManager struct{ Manager asset.ReferenceManager }

func (a MediaReferenceManager) ValidateAvailable(ctx context.Context, q database.Querier, values []content.MediaReference) error {
	ids := make([]string, len(values))
	for i, value := range values {
		ids[i] = value.AssetID
	}
	return a.Manager.ValidateAvailable(ctx, q, ids)
}

func (a MediaReferenceManager) InsertRevisionReferences(ctx context.Context, q database.Querier, values []content.MediaReference) error {
	references := make([]asset.Reference, len(values))
	for i, value := range values {
		references[i] = asset.Reference{RevisionID: value.RevisionID, EntryID: value.EntryID, ModelID: value.ModelID, FieldID: value.FieldID, AssetID: value.AssetID, JSONPointer: value.JSONPointer, Position: value.Position}
	}
	return a.Manager.InsertRevisionReferences(ctx, q, references)
}

func (a MediaReferenceManager) ValidatePublishableRevision(ctx context.Context, q database.Querier, id string) error {
	return a.Manager.ValidatePublishableRevision(ctx, q, id)
}

type ModelReader struct {
	DB         database.Querier
	Repository schema.SQLRepository
}

func (r ModelReader) GetModel(ctx context.Context, q database.Querier, id string) (schema.ContentModel, error) {
	if q == nil {
		q = r.DB
	}
	return r.Repository.GetModel(ctx, q, id)
}
