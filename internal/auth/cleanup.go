package auth

import (
	"context"
	"log/slog"
	"time"
)

const (
	cleanupBatchSize    = 500
	cleanupMaxBatches   = 10
	cleanupInterval     = 15 * time.Minute
	cleanupQueryTimeout = 5 * time.Second
	challengeRetention  = time.Hour
	sessionRetention    = 24 * time.Hour
)

type CleanupStore interface {
	Cleanup(context.Context, string, time.Time, int) (int64, error)
}

type Cleaner struct {
	store  CleanupStore
	now    func() time.Time
	logger *slog.Logger
}

func NewCleaner(store CleanupStore, logger *slog.Logger) *Cleaner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Cleaner{store: store, now: time.Now, logger: logger}
}

func (c *Cleaner) Run(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.runOnce(ctx)
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			c.runOnce(ctx)
		}
	}
}

func (c *Cleaner) runOnce(ctx context.Context) {
	now := c.now().UTC()
	categories := []struct {
		name   string
		cutoff time.Time
	}{
		{"captcha", now.Add(-challengeRetention)},
		{"sms", now.Add(-challengeRetention)},
		{"rate_limit", now},
		{"session_revoked", now.Add(-sessionRetention)},
		{"session_absolute", now.Add(-sessionRetention)},
		{"session_idle", now.Add(-sessionRetention)},
	}
	active := make([]bool, len(categories))
	for i := range active {
		active[i] = true
	}
	for batch := 0; batch < cleanupMaxBatches; batch++ {
		for i, category := range categories {
			if !active[i] || ctx.Err() != nil {
				continue
			}
			queryCtx, cancel := context.WithTimeout(ctx, cleanupQueryTimeout)
			count, err := c.store.Cleanup(queryCtx, category.name, category.cutoff, cleanupBatchSize)
			cancel()
			if err != nil {
				active[i] = false
				if ctx.Err() == nil {
					c.logger.Error("认证状态清理失败", "component", "auth_cleanup", "category", category.name, "error", err)
				}
				continue
			}
			if count < cleanupBatchSize {
				active[i] = false
			}
		}
		if ctx.Err() != nil {
			return
		}
	}
}
