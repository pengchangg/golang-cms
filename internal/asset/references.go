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

func (SQLReferenceManager) ValidateAvailable(ctx context.Context, q database.Querier, ids []string) error {
	ids = uniqueSorted(ids)
	for _, id := range ids {
		var status Status
		err := q.QueryRowContext(ctx, `SELECT status FROM assets WHERE id=? FOR UPDATE`, id).Scan(&status)
		if errors.Is(err, sql.ErrNoRows) || err == nil && status != StatusAvailable {
			return appError(apperror.KindConflict, "asset_not_available", "素材不存在或不可用")
		}
		if err != nil {
			return fmt.Errorf("校验素材可用状态: %w", err)
		}
	}
	return nil
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
