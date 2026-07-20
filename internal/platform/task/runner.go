package task

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
)

type RunnerConfig struct {
	Owner           string
	Concurrency     int
	PollInterval    time.Duration
	LeaseDuration   time.Duration
	RenewInterval   time.Duration
	ShutdownTimeout time.Duration
}

func DefaultRunnerConfig(owner string) RunnerConfig {
	return RunnerConfig{Owner: owner, Concurrency: 1, PollInterval: time.Second, LeaseDuration: 60 * time.Second, RenewInterval: 20 * time.Second, ShutdownTimeout: 15 * time.Second}
}

type Runner struct {
	store    Store
	registry *Registry
	config   RunnerConfig
}

func NewRunner(store Store, registry *Registry, config RunnerConfig) (*Runner, error) {
	if store == nil || registry == nil {
		return nil, errors.New("任务 Store 和 Registry 不能为空")
	}
	if config.Owner == "" || len(config.Owner) > 128 || config.Concurrency < 1 || config.PollInterval <= 0 || config.LeaseDuration <= 0 || config.RenewInterval <= 0 {
		return nil, errors.New("任务 Runner 配置无效")
	}
	if config.RenewInterval > config.LeaseDuration/2 {
		return nil, errors.New("一个租约周期必须至少容纳两次续租")
	}
	if config.ShutdownTimeout < 0 {
		return nil, errors.New("任务 Runner 关闭超时配置无效")
	}
	if config.ShutdownTimeout == 0 {
		config.ShutdownTimeout = min(15*time.Second, config.LeaseDuration)
	}
	return &Runner{store: store, registry: registry, config: config}, nil
}

// Run 在 ctx 取消后停止领取，并等待已领取任务到达 Handler 的安全退出点。
func (r *Runner) Run(ctx context.Context) error {
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var workers sync.WaitGroup
	errorsFound := make(chan error, r.config.Concurrency)
	workers.Add(r.config.Concurrency)
	for range r.config.Concurrency {
		go func() {
			defer workers.Done()
			if err := r.worker(runCtx); err != nil {
				select {
				case errorsFound <- err:
				default:
				}
				stop()
			}
		}()
	}
	workers.Wait()
	select {
	case err := <-errorsFound:
		return err
	default:
		return nil
	}
}

func (r *Runner) worker(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		job, err := r.store.Claim(ctx, r.config.Owner, r.config.LeaseDuration)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("领取后台任务: %w", err)
		}
		if job == nil {
			if !wait(ctx, r.config.PollInterval) {
				return nil
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		if err := r.execute(ctx, job); err != nil {
			return err
		}
	}
}

func (r *Runner) execute(parent context.Context, job *Job) error {
	handler, ok := r.registry.Handler(job.Type)
	if !ok {
		return r.finish(job, func(ctx context.Context) error {
			return r.store.Fail(ctx, job.ID, job.LeaseToken, "handler_not_registered", "任务类型未注册 Handler", false)
		})
	}

	handlerCtx, cancel := context.WithCancel(parent)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- handler(handlerCtx, *job) }()
	ticker := time.NewTicker(r.config.RenewInterval)
	defer ticker.Stop()
	renew := ticker.C
	var shutdown <-chan time.Time
	var shutdownTimer *time.Timer
	parentDone := parent.Done()
	stopping := false
	cancelRequested := false
	var shutdownErr error
	var executionErr error
	executionStopped := false
	defer func() {
		if shutdownTimer != nil {
			shutdownTimer.Stop()
		}
	}()

	for {
		select {
		case err := <-result:
			if executionStopped {
				return executionErr
			}
			if shutdownErr != nil {
				return shutdownErr
			}
			if err == nil || errors.Is(err, ErrAlreadyCommitted) {
				return r.finish(job, func(ctx context.Context) error { return r.store.Succeed(ctx, job.ID, job.LeaseToken) })
			}
			if cancelRequested {
				return r.finish(job, func(ctx context.Context) error { return r.store.Cancel(ctx, job.ID, job.LeaseToken) })
			}
			if (stopping || parent.Err() != nil) && errors.Is(err, context.Canceled) {
				return nil
			}
			var handlerError *HandlerError
			if errors.As(err, &handlerError) {
				return r.finish(job, func(ctx context.Context) error {
					return r.store.Fail(ctx, job.ID, job.LeaseToken, handlerError.Code, handlerError.Message, handlerError.Retryable)
				})
			}
			return r.finish(job, func(ctx context.Context) error {
				return r.store.Fail(ctx, job.ID, job.LeaseToken, "job_failed", "任务执行失败", false)
			})
		case <-parentDone:
			if !stopping {
				stopping = true
				parentDone = nil
				cancel()
				ticker.Stop()
				renew = nil
				shutdownTimer = time.NewTimer(r.config.ShutdownTimeout)
				shutdown = shutdownTimer.C
			}
		case <-shutdown:
			// task 层无法强杀 Handler；记录超时后仍等待其离开可能访问数据库的安全区。
			shutdown = nil
			shutdownErr = fmt.Errorf("%w: Handler %s 超过 %s 未返回", ErrShutdownTimeout, job.ID, r.config.ShutdownTimeout)
		case <-renew:
			opCtx, opCancel := context.WithTimeout(context.Background(), r.config.RenewInterval)
			requested, err := r.store.Renew(opCtx, job.ID, job.LeaseToken, r.config.LeaseDuration)
			opCancel()
			if err != nil {
				cancel()
				ticker.Stop()
				renew = nil
				if errors.Is(err, ErrLeaseLost) {
					executionStopped = true
					continue
				}
				executionErr = fmt.Errorf("续租后台任务 %s: %w", job.ID, err)
				executionStopped = true
				continue
			}
			if requested {
				cancelRequested = true
				cancel()
			}
		}
	}
}

func (r *Runner) finish(job *Job, mutation func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), r.config.RenewInterval)
	defer cancel()
	err := mutation(ctx)
	if errors.Is(err, ErrLeaseLost) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("持久化后台任务 %s 终态: %w", job.ID, err)
	}
	return nil
}

func wait(ctx context.Context, duration time.Duration) bool {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
