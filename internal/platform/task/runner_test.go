package task

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeJob struct {
	job             Job
	status          Status
	cancelRequested bool
	leaseLost       bool
	availableAt     time.Time
}

type fakeStore struct {
	mu        sync.Mutex
	jobs      []*fakeJob
	next      byte
	renewed   chan struct{}
	finishErr error
}

func (s *fakeStore) Claim(_ context.Context, _ string, _ time.Duration) (*Job, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for _, item := range s.jobs {
		if item.status != StatusQueued || item.availableAt.After(now) {
			continue
		}
		item.status = StatusRunning
		item.job.Attempt++
		s.next++
		item.job.LeaseToken[0] = s.next
		job := item.job
		return &job, nil
	}
	return nil, nil
}

func (s *fakeStore) Renew(_ context.Context, id string, token [32]byte, _ time.Duration) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	item := s.lookup(id, token)
	if item == nil || item.leaseLost {
		return false, ErrLeaseLost
	}
	if s.renewed != nil {
		select {
		case s.renewed <- struct{}{}:
		default:
		}
	}
	return item.cancelRequested, nil
}

func (s *fakeStore) UpdateProgress(_ context.Context, id string, token [32]byte, _ int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lookup(id, token) == nil {
		return ErrLeaseLost
	}
	return nil
}

func (s *fakeStore) Succeed(_ context.Context, id string, token [32]byte) error {
	return s.finish(id, token, StatusSucceeded, false)
}

func (s *fakeStore) Fail(_ context.Context, id string, token [32]byte, _, _ string, retryable bool) error {
	return s.finish(id, token, StatusFailed, retryable)
}

func (s *fakeStore) Cancel(_ context.Context, id string, token [32]byte) error {
	return s.finish(id, token, StatusCanceled, false)
}

func (s *fakeStore) finish(id string, token [32]byte, status Status, retryable bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.finishErr != nil {
		return s.finishErr
	}
	item := s.lookup(id, token)
	if item == nil || item.leaseLost {
		return ErrLeaseLost
	}
	if retryable && item.job.Attempt < item.job.MaxAttempts {
		item.status = StatusQueued
		return nil
	}
	item.status = status
	return nil
}

func TestRunnerReturnsTerminalPersistenceError(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "terminal-error", Type: "test", MaxAttempts: 3}, status: StatusQueued}}, finishErr: errors.New("database unavailable")}
	registry := NewRegistry()
	_ = registry.Register("test", func(context.Context, Job) error { return nil })
	err := testRunner(t, store, registry, 1).Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "持久化后台任务") {
		t.Fatalf("应返回终态持久化错误: %v", err)
	}
}

func TestRunnerPrefersHandlerSuccessAfterCancellationRequest(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "commit", Type: "test", MaxAttempts: 3}, status: StatusQueued, cancelRequested: true}}}
	registry := NewRegistry()
	_ = registry.Register("test", func(ctx context.Context, _ Job) error {
		<-ctx.Done()
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := testRunner(t, store, registry, 1).Run(ctx); err != nil {
		t.Fatal(err)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.jobs[0].status != StatusSucceeded {
		t.Fatalf("Handler 已成功时状态=%s，期望 succeeded", store.jobs[0].status)
	}
}

func TestRunnerTreatsAlreadyCommittedAsSuccess(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "committed", Type: "test", MaxAttempts: 3}, status: StatusQueued, cancelRequested: true}}}
	registry := NewRegistry()
	_ = registry.Register("test", func(context.Context, Job) error { return ErrAlreadyCommitted })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := testRunner(t, store, registry, 1).Run(ctx); err != nil {
		t.Fatal(err)
	}
	if store.jobs[0].status != StatusSucceeded {
		t.Fatalf("已提交任务状态=%s，期望 succeeded", store.jobs[0].status)
	}
}

func (s *fakeStore) lookup(id string, token [32]byte) *fakeJob {
	for _, item := range s.jobs {
		if item.job.ID == id && item.status == StatusRunning && item.job.LeaseToken == token {
			return item
		}
	}
	return nil
}

func testRunner(t *testing.T, store Store, registry *Registry, concurrency int) *Runner {
	t.Helper()
	runner, err := NewRunner(store, registry, RunnerConfig{
		Owner: "test", Concurrency: concurrency, PollInterval: time.Millisecond,
		LeaseDuration: 30 * time.Millisecond, RenewInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	return runner
}

func TestRunnerConcurrentWorkersExecuteJobOnce(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "one", Type: "test", MaxAttempts: 3}, status: StatusQueued}}}
	registry := NewRegistry()
	var calls atomic.Int32
	if err := registry.Register("test", func(context.Context, Job) error {
		calls.Add(1)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	runner := testRunner(t, store, registry, 8)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("Handler 执行次数 = %d，期望 1", calls.Load())
	}
}

func TestMultipleRunnersExecuteJobOnce(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "shared", Type: "test", MaxAttempts: 3}, status: StatusQueued}}}
	registry := NewRegistry()
	var calls atomic.Int32
	_ = registry.Register("test", func(context.Context, Job) error {
		calls.Add(1)
		time.Sleep(2 * time.Millisecond)
		return nil
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	runnerOne := testRunner(t, store, registry, 2)
	runnerTwo := testRunner(t, store, registry, 2)
	var runners sync.WaitGroup
	runners.Add(2)
	go func() { defer runners.Done(); _ = runnerOne.Run(ctx) }()
	go func() { defer runners.Done(); _ = runnerTwo.Run(ctx) }()
	runners.Wait()
	if calls.Load() != 1 {
		t.Fatalf("多 Runner Handler 执行次数 = %d，期望 1", calls.Load())
	}
}

func TestRunnerRetriesAtMostThreeAttempts(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "retry", Type: "test", MaxAttempts: 3}, status: StatusQueued}}}
	registry := NewRegistry()
	var calls atomic.Int32
	_ = registry.Register("test", func(context.Context, Job) error {
		calls.Add(1)
		return RetryableError("temporary", "临时失败")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = testRunner(t, store, registry, 3).Run(ctx)
	if calls.Load() != 3 {
		t.Fatalf("Handler 执行次数 = %d，期望 3", calls.Load())
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.jobs[0].status != StatusFailed || store.jobs[0].job.Attempt != 3 {
		t.Fatalf("最终任务 = %+v，状态 = %s", store.jobs[0].job, store.jobs[0].status)
	}
}

func TestRunnerCancelsHandlerAfterLeaseLoss(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "lost", Type: "test", MaxAttempts: 3}, status: StatusQueued, leaseLost: true}}}
	registry := NewRegistry()
	handlerCanceled := make(chan struct{})
	_ = registry.Register("test", func(ctx context.Context, _ Job) error {
		<-ctx.Done()
		close(handlerCanceled)
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if err := testRunner(t, store, registry, 1).Run(ctx); err != nil {
		t.Fatalf("失去租约后 Runner 错误 = %v", err)
	}
	select {
	case <-handlerCanceled:
	default:
		t.Fatal("失去租约后 Handler 未被取消")
	}
}

func TestRunnerHonorsCancellationRequest(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "cancel", Type: "test", MaxAttempts: 3}, status: StatusQueued, cancelRequested: true}}}
	registry := NewRegistry()
	_ = registry.Register("test", func(ctx context.Context, _ Job) error {
		<-ctx.Done()
		return ctx.Err()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_ = testRunner(t, store, registry, 1).Run(ctx)
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.jobs[0].status != StatusCanceled {
		t.Fatalf("任务状态 = %s，期望 canceled", store.jobs[0].status)
	}
}

func TestRunnerShutdownCancelsActiveHandler(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "active", Type: "test", MaxAttempts: 3}, status: StatusQueued}}}
	registry := NewRegistry()
	started := make(chan struct{})
	_ = registry.Register("test", func(ctx context.Context, _ Job) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	runner := testRunner(t, store, registry, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = runner.Run(ctx)
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("关闭后 Handler 未收到 context 或 Runner 未有界停止")
	}
}

func TestRunnerShutdownWaitsForHandlerSafePointAfterTimeout(t *testing.T) {
	store := &fakeStore{jobs: []*fakeJob{{job: Job{ID: "slow-shutdown", Type: "test", MaxAttempts: 3}, status: StatusQueued}}}
	registry := NewRegistry()
	started := make(chan struct{})
	safe := make(chan struct{})
	_ = registry.Register("test", func(ctx context.Context, _ Job) error {
		close(started)
		<-ctx.Done()
		<-safe
		return ctx.Err()
	})
	runner := testRunner(t, store, registry, 1)
	runner.config.ShutdownTimeout = 10 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-started
	cancel()
	time.Sleep(30 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Handler 到达安全点前 Runner 已返回: %v", err)
	default:
	}
	close(safe)
	select {
	case err := <-done:
		if !errors.Is(err, ErrShutdownTimeout) {
			t.Fatalf("Runner 错误 = %v，期望 ErrShutdownTimeout", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Handler 到达安全点后 Runner 未返回")
	}
}

func TestRunnerDoesNotStartClaimedJobAfterShutdown(t *testing.T) {
	claimed := make(chan struct{})
	release := make(chan struct{})
	store := &blockingClaimStore{fakeStore: fakeStore{jobs: []*fakeJob{{job: Job{ID: "claimed-during-shutdown", Type: "test", MaxAttempts: 3}, status: StatusQueued}}}, claimed: claimed, release: release}
	registry := NewRegistry()
	var calls atomic.Int32
	_ = registry.Register("test", func(context.Context, Job) error { calls.Add(1); return nil })
	runner := testRunner(t, store, registry, 1)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	<-claimed
	cancel()
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 {
		t.Fatalf("关闭期间领取的任务执行次数 = %d，期望 0", calls.Load())
	}
}

type blockingClaimStore struct {
	fakeStore
	claimed chan struct{}
	release chan struct{}
}

func (s *blockingClaimStore) Claim(ctx context.Context, owner string, lease time.Duration) (*Job, error) {
	close(s.claimed)
	<-s.release
	return s.fakeStore.Claim(ctx, owner, lease)
}

func TestNewRunnerValidatesLeaseIntervals(t *testing.T) {
	_, err := NewRunner(&fakeStore{}, NewRegistry(), RunnerConfig{Owner: "test", Concurrency: 1, PollInterval: time.Second, LeaseDuration: time.Second, RenewInterval: 600 * time.Millisecond})
	if err == nil {
		t.Fatal("租约周期不足两次续租时应报错")
	}
}

func TestNewRunnerDefaultsShutdownTimeoutWithinLease(t *testing.T) {
	runner := testRunner(t, &fakeStore{}, NewRegistry(), 1)
	if runner.config.ShutdownTimeout != runner.config.LeaseDuration {
		t.Fatalf("短租约的默认关闭超时 = %s，期望 %s", runner.config.ShutdownTimeout, runner.config.LeaseDuration)
	}
}

func TestDefaultRunnerConfigUsesF3LeaseIntervals(t *testing.T) {
	config := DefaultRunnerConfig("worker")
	if config.LeaseDuration != 60*time.Second || config.RenewInterval != 20*time.Second || config.ShutdownTimeout != 15*time.Second {
		t.Fatalf("默认租约/关闭配置 = %s/%s/%s", config.LeaseDuration, config.RenewInterval, config.ShutdownTimeout)
	}
}

func TestRegistryRejectsDuplicateType(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register("test", func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register("test", func(context.Context, Job) error { return nil }); err == nil {
		t.Fatal("重复注册任务类型应报错")
	}
}
