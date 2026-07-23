package integration

import (
	"context"
	"fmt"
	"strings"

	"cms/internal/asset"
	"cms/internal/content"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

type ReferencedAssetResolver struct{ DB database.Querier }

func (r ReferencedAssetResolver) ResolveReferencedAssets(ctx context.Context, revisionIDs []string) (map[string]map[string]content.ReferencedAsset, error) {
	result := make(map[string]map[string]content.ReferencedAsset, len(revisionIDs))
	if len(revisionIDs) == 0 {
		return result, nil
	}
	query := `SELECT ar.revision_id,a.id,a.filename,a.mime_type,a.size,a.status FROM asset_references ar JOIN assets a ON a.id=ar.asset_id WHERE ar.revision_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(revisionIDs)), ",") + `) ORDER BY ar.revision_id,a.id`
	args := make([]any, len(revisionIDs))
	for i, id := range revisionIDs {
		args[i] = id
		result[id] = map[string]content.ReferencedAsset{}
	}
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询草稿引用素材: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var revisionID string
		var value content.ReferencedAsset
		if err := rows.Scan(&revisionID, &value.ID, &value.Filename, &value.MimeType, &value.Size, &value.Status); err != nil {
			return nil, err
		}
		value.PreviewKind = string(asset.PreviewKindFor(value.MimeType))
		result[revisionID][value.ID] = value
	}
	return result, rows.Err()
}

func (r ReferencedAssetResolver) ResolvePublishedAssets(ctx context.Context, revisionIDs []string) (map[string]map[string]content.PublishedReferencedAsset, error) {
	result := make(map[string]map[string]content.PublishedReferencedAsset, len(revisionIDs))
	if len(revisionIDs) == 0 {
		return result, nil
	}
	query := `SELECT ar.revision_id,a.id,a.object_key,a.filename,a.mime_type,a.size,a.sha256,a.etag FROM asset_references ar JOIN assets a ON a.id=ar.asset_id WHERE ar.revision_id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(revisionIDs)), ",") + `) AND a.status IN ('available','archived') ORDER BY ar.revision_id,a.id`
	args := make([]any, len(revisionIDs))
	for i, id := range revisionIDs {
		args[i] = id
		result[id] = map[string]content.PublishedReferencedAsset{}
	}
	rows, err := r.DB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("批量查询发布引用素材: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var revisionID string
		var value content.PublishedReferencedAsset
		if err := rows.Scan(&revisionID, &value.ID, &value.ObjectKey, &value.Filename, &value.MimeType, &value.Size, &value.SHA256, &value.ETag); err != nil {
			return nil, err
		}
		result[revisionID][value.ID] = value
	}
	return result, rows.Err()
}

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
