package configuration

import (
	"context"
	"strings"

	"cms/internal/permission"
	"cms/internal/platform/database"
)

type ActiveConfigNamespaceProvider struct {
	db         database.Querier
	repository Repository
}

func NewActiveConfigNamespaceProvider(db database.Querier, repository Repository) ActiveConfigNamespaceProvider {
	return ActiveConfigNamespaceProvider{db: db, repository: repository}
}

func (p ActiveConfigNamespaceProvider) ActiveConfigNamespaceIDs(ctx context.Context) ([]string, error) {
	return p.repository.ActiveConfigNamespaceIDs(ctx, p.db)
}

func (p ActiveConfigNamespaceProvider) ActiveConfigNamespaceIDsWith(ctx context.Context, q database.Querier) ([]string, error) {
	return p.repository.ActiveConfigNamespaceIDs(ctx, q)
}

func (p ActiveConfigNamespaceProvider) ValidateActiveConfigNamespaces(ctx context.Context, q database.Querier, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	query := `SELECT COUNT(*) FROM config_namespaces WHERE status='active' AND id IN (` + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + `)`
	var count int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&count); err != nil {
		return err
	}
	if count != len(ids) {
		return permission.ErrInvalidConfigNamespaces
	}
	return nil
}
