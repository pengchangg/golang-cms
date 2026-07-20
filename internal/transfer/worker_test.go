package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"cms/internal/platform/task"
	"cms/internal/schema"
)

func TestModelSnapshotMatchesUpdatedAtAndFieldsStrictly(t *testing.T) {
	now := time.Date(2026, 7, 20, 1, 2, 3, 4000, time.UTC)
	field := schema.ContentField{ID: "fld_1", Key: "title", Type: schema.FieldTypeSingleLineText, Status: schema.StatusActive, Children: []schema.ContentField{}}
	current := schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1", UpdatedAt: now}, Fields: []schema.ContentField{field}}
	encoded, err := json.Marshal(ActiveRootFields(current.Fields))
	if err != nil {
		t.Fatal(err)
	}
	var snapshotFields []schema.ContentField
	if err = json.Unmarshal(encoded, &snapshotFields); err != nil {
		t.Fatal(err)
	}
	if !modelSnapshotMatches("mdl_1", now, snapshotFields, current) {
		t.Fatal("完全一致的模型快照应匹配")
	}
	changedTime := current
	changedTime.UpdatedAt = now.Add(time.Microsecond)
	if modelSnapshotMatches("mdl_1", now, snapshotFields, changedTime) {
		t.Fatal("updated_at 变化后不应匹配")
	}
	changedField := current
	changedField.Fields = append([]schema.ContentField(nil), current.Fields...)
	changedField.Fields[0].Required = true
	if modelSnapshotMatches("mdl_1", now, snapshotFields, changedField) {
		t.Fatal("字段变化后不应匹配")
	}
}

func TestAttemptObjectKeyIsolatedByAttemptAndLease(t *testing.T) {
	first, second := [32]byte{1}, [32]byte{2}
	key := attemptObjectKey("job_1", 2, first, "result.csv")
	if key == attemptObjectKey("job_1", 3, first, "result.csv") || key == attemptObjectKey("job_1", 2, second, "result.csv") {
		t.Fatal("对象 key 未按 attempt 和 lease token 隔离")
	}
	if !strings.Contains(key, "/attempt-2-") || !strings.HasSuffix(key, "/result.csv") {
		t.Fatalf("对象 key 格式错误: %s", key)
	}
}

type committedJobRepository struct {
	Repository
	ready  chan struct{}
	clears atomic.Int32
}

func (r *committedJobRepository) GetJobByID(context.Context, string) (Job, error) {
	<-r.ready
	committedAt := time.Now().UTC()
	return Job{ID: "job_1", Type: JobCSVImport, CommittedAt: &committedAt}, nil
}

func (r *committedJobRepository) ClearStaging(context.Context, string, [32]byte) error {
	r.clears.Add(1)
	return nil
}

func TestJobHandlerConcurrentCallsDoNotWriteSharedClock(t *testing.T) {
	const workers = 32
	repository := &committedJobRepository{ready: make(chan struct{})}
	handler := (&JobHandler{Repository: repository}).TaskHandler()
	errorsFound := make(chan error, workers)
	var started sync.WaitGroup
	started.Add(workers)
	for range workers {
		go func() {
			started.Done()
			errorsFound <- handler(context.Background(), task.Job{ID: "job_1"})
		}()
	}
	started.Wait()
	close(repository.ready)
	for range workers {
		if err := <-errorsFound; !errors.Is(err, task.ErrAlreadyCommitted) {
			t.Errorf("并发处理已提交任务返回错误: %v", err)
		}
	}
	if repository.clears.Load() != workers {
		t.Fatalf("清理次数 = %d，期望 %d", repository.clears.Load(), workers)
	}
}
