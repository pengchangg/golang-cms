package asset

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type SQLReferenceManager struct{}

func (SQLReferenceManager) ValidateAvailable(ctx context.Context, q database.Querier, references []Reference, baseRevisionID string) error {
	kinds := map[string]map[string]bool{}
	counts := map[string]int{}
	for _, reference := range references {
		counts[reference.AssetID]++
		if kinds[reference.AssetID] == nil {
			kinds[reference.AssetID] = map[string]bool{}
		}
		if reference.Kind != "" {
			kinds[reference.AssetID][reference.Kind] = true
		}
	}
	ids := make([]string, 0, len(kinds))
	for id := range kinds {
		ids = append(ids, id)
	}
	ids = uniqueSorted(ids)
	for _, id := range ids {
		var status Status
		var mimeType string
		err := q.QueryRowContext(ctx, `SELECT status,mime_type FROM assets WHERE id=? FOR UPDATE`, id).Scan(&status, &mimeType)
		if errors.Is(err, sql.ErrNoRows) {
			return appError(apperror.KindConflict, "asset_not_available", "素材不存在或不可用")
		}
		if err != nil {
			return fmt.Errorf("校验素材可用状态: %w", err)
		}
		if status == StatusArchived && baseRevisionID != "" {
			var inherited int
			err = q.QueryRowContext(ctx, `SELECT COUNT(*) FROM asset_references WHERE revision_id=? AND asset_id=?`, baseRevisionID, id).Scan(&inherited)
			if err != nil {
				return fmt.Errorf("校验归档素材继承关系: %w", err)
			}
			if !canInheritArchivedReferences(inherited, counts[id]) {
				return appError(apperror.KindConflict, "asset_not_available", "归档素材只能继承 base Revision 中已有的引用数量")
			}
		} else if status != StatusAvailable {
			return appError(apperror.KindConflict, "asset_not_available", "素材不存在或不可用")
		}
		for kind := range kinds[id] {
			if !validRichTextMedia(kind, mimeType) {
				return appError(apperror.KindConflict, "asset_media_type_invalid", "素材类型不适用于富文本媒体节点")
			}
		}
	}
	return nil
}

func canInheritArchivedReferences(inherited, requested int) bool {
	return requested <= inherited
}

func validRichTextMedia(kind, mimeType string) bool {
	switch kind {
	case "image":
		return mimeType == "image/jpeg" || mimeType == "image/png" || mimeType == "image/gif" || mimeType == "image/webp" || mimeType == "image/avif"
	case "audio":
		return PreviewKindFor(mimeType) == PreviewAudio
	case "video":
		return PreviewKindFor(mimeType) == PreviewVideo
	default:
		return false
	}
}

func (SQLReferenceManager) InsertRevisionReferences(ctx context.Context, q database.Querier, values []Reference) error {
	for _, value := range values {
		_, err := q.ExecContext(ctx, `INSERT INTO asset_references(revision_id,entry_id,model_id,field_id,asset_id,json_pointer,position) VALUES(?,?,?,?,?,?,?)`, value.RevisionID, value.EntryID, value.ModelID, value.FieldID, value.AssetID, value.JSONPointer, value.Position)
		if err != nil {
			return fmt.Errorf("创建 Revision 素材引用: %w", err)
		}
	}
	return nil
}

func (SQLReferenceManager) ValidatePublishableRevision(ctx context.Context, q database.Querier, revisionID string) error {
	rows, err := q.QueryContext(ctx, `SELECT a.id,a.status FROM asset_references r JOIN assets a ON a.id=r.asset_id WHERE r.revision_id=? ORDER BY a.id ASC FOR UPDATE`, revisionID)
	if err != nil {
		return fmt.Errorf("锁定待发布素材: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		var status Status
		if err := rows.Scan(&id, &status); err != nil {
			return err
		}
		if status != StatusAvailable {
			return appError(apperror.KindConflict, "asset_not_publishable", "Revision 包含不可发布素材")
		}
	}
	return rows.Err()
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			seen[value] = struct{}{}
		}
	}
	for value := range seen {
		result = append(result, value)
	}
	sort.Strings(result)
	return result
}
