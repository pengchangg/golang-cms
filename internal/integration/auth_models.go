package integration

import (
	"context"
	"strings"

	"cms/internal/auth"
	"cms/internal/platform/database"
)

type AuthModelSummaryProvider struct {
	DB database.Querier
}

func (p AuthModelSummaryProvider) ActiveModelSummaries(ctx context.Context, ids []string) ([]auth.SessionModelSummary, error) {
	if len(ids) == 0 {
		return []auth.SessionModelSummary{}, nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i], args[i] = "?", id
	}
	rows, err := p.DB.QueryContext(ctx, `SELECT id, model_key, display_name FROM content_models WHERE status='active' AND id IN (`+strings.Join(placeholders, ",")+`) ORDER BY model_key, id`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []auth.SessionModelSummary{}
	for rows.Next() {
		var model auth.SessionModelSummary
		if err := rows.Scan(&model.ID, &model.Key, &model.DisplayName); err != nil {
			return nil, err
		}
		result = append(result, model)
	}
	return result, rows.Err()
}
