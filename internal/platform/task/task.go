package task

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

type Status string

const (
	StatusQueued    Status = "queued"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
)

var (
	ErrLeaseLost        = errors.New("任务租约已失效")
	ErrAlreadyCommitted = errors.New("任务业务事务已提交")
	ErrShutdownTimeout  = errors.New("任务 Runner 关闭超时")
)

type Job struct {
	ID          string
	Type        string
	Payload     json.RawMessage
	Attempt     int
	MaxAttempts int
	LeaseToken  [32]byte
}

type Handler func(context.Context, Job) error

type Registry struct {
	mu       sync.RWMutex
	handlers map[string]Handler
}

func NewRegistry() *Registry {
	return &Registry{handlers: make(map[string]Handler)}
}

func (r *Registry) Register(jobType string, handler Handler) error {
	if jobType == "" || handler == nil {
		return errors.New("任务类型和 Handler 不能为空")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.handlers[jobType]; exists {
		return fmt.Errorf("任务类型 %q 已注册", jobType)
	}
	r.handlers[jobType] = handler
	return nil
}

func (r *Registry) Handler(jobType string) (Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	handler, ok := r.handlers[jobType]
	return handler, ok
}

type HandlerError struct {
	Code      string
	Message   string
	Retryable bool
}

func (e *HandlerError) Error() string { return e.Message }

func RetryableError(code, message string) error {
	return &HandlerError{Code: code, Message: message, Retryable: true}
}

func PermanentError(code, message string) error {
	return &HandlerError{Code: code, Message: message}
}

type Store interface {
	Claim(context.Context, string, time.Duration) (*Job, error)
	Renew(context.Context, string, [32]byte, time.Duration) (cancelRequested bool, err error)
	UpdateProgress(context.Context, string, [32]byte, int) error
	Succeed(context.Context, string, [32]byte) error
	Fail(context.Context, string, [32]byte, string, string, bool) error
	Cancel(context.Context, string, [32]byte) error
}
