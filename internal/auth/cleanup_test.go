package auth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type cleanupCall struct {
	category string
	cutoff   time.Time
	limit    int
}
type fakeCleanupStore struct {
	calls  []cleanupCall
	counts map[string][]int64
	fail   string
}

func (s *fakeCleanupStore) Cleanup(_ context.Context, category string, cutoff time.Time, limit int) (int64, error) {
	s.calls = append(s.calls, cleanupCall{category, cutoff, limit})
	if category == s.fail {
		return 0, errors.New("failed")
	}
	values := s.counts[category]
	if len(values) == 0 {
		return 0, nil
	}
	s.counts[category] = values[1:]
	return values[0], nil
}

func TestCleanerUsesCutoffsBatchesAndContinuesAfterError(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := &fakeCleanupStore{counts: map[string][]int64{"captcha": {cleanupBatchSize, 1}}, fail: "sms"}
	cleaner := NewCleaner(store, nil)
	cleaner.now = func() time.Time { return now }
	cleaner.runOnce(context.Background())
	if len(store.calls) < 7 {
		t.Fatalf("calls=%d", len(store.calls))
	}
	if store.calls[0].category != "captcha" || !store.calls[0].cutoff.Equal(now.Add(-challengeRetention)) || store.calls[0].limit != cleanupBatchSize {
		t.Fatalf("call=%+v", store.calls[0])
	}
	seenRate := false
	for _, call := range store.calls {
		if call.category == "rate_limit" && call.cutoff.Equal(now) {
			seenRate = true
		}
	}
	if !seenRate {
		t.Fatal("一个类别失败不应阻止后续类别")
	}
}

func TestCleanerRunStopsOnCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := NewCleaner(&fakeCleanupStore{counts: map[string][]int64{}}, nil).Run(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestCleanerFairlyLimitsEveryCategoryToTenBatches(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	store := &fakeCleanupStore{counts: map[string][]int64{}}
	for _, category := range []string{"captcha", "sms", "rate_limit", "session_revoked", "session_absolute", "session_idle"} {
		store.counts[category] = make([]int64, cleanupMaxBatches)
		for i := range store.counts[category] {
			store.counts[category][i] = cleanupBatchSize
		}
	}
	cleaner := NewCleaner(store, nil)
	cleaner.now = func() time.Time { return now }
	cleaner.runOnce(context.Background())
	wantOrder := []string{"captcha", "sms", "rate_limit", "session_revoked", "session_absolute", "session_idle"}
	if len(store.calls) != len(wantOrder)*cleanupMaxBatches {
		t.Fatalf("calls=%d", len(store.calls))
	}
	for i, call := range store.calls {
		if call.category != wantOrder[i%len(wantOrder)] {
			t.Fatalf("call[%d]=%s", i, call.category)
		}
		wantCutoff := now
		if call.category == "captcha" || call.category == "sms" {
			wantCutoff = now.Add(-challengeRetention)
		}
		if strings.HasPrefix(call.category, "session_") {
			wantCutoff = now.Add(-sessionRetention)
		}
		if !call.cutoff.Equal(wantCutoff) || call.limit != cleanupBatchSize {
			t.Fatalf("call=%+v", call)
		}
	}
}
