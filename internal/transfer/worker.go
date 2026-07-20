package transfer

import (
	"context"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"sort"
	"strconv"
	"time"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/database"
	"cms/internal/platform/task"
	"cms/internal/schema"
)

type DraftValidator interface {
	ValidateDrafts(context.Context, string, content.DraftSource) []TransferError
}
type ExportSource interface {
	Stream(context.Context, Job, func(json.RawMessage) error) error
}
type PrincipalSnapshot interface {
	Principal(context.Context, string) (identity.Principal, error)
}

// JobHandler 只实现 CSV 业务副作用；领取、续租和终态由 platform/task 负责。
type JobHandler struct {
	Repository Repository
	Store      ObjectStore
	Importer   Importer
	Validator  DraftValidator
	Exporter   ExportSource
	Principals PrincipalSnapshot
	Now        func() time.Time
}

func NewJobHandler(handler JobHandler) *JobHandler {
	if handler.Now == nil {
		handler.Now = func() time.Time { return time.Now().UTC() }
	}
	return &handler
}

func (h *JobHandler) TaskHandler() task.Handler {
	return func(ctx context.Context, leased task.Job) error { return h.handle(ctx, leased.ID, leased.LeaseToken) }
}

// Handle 仅供不执行任务副作用的兼容调用；Worker 必须通过 TaskHandler 传入租约。
func (h *JobHandler) Handle(ctx context.Context, jobID string) error {
	return errors.New("Worker 必须通过 TaskHandler 携带任务租约")
}

func (h *JobHandler) handle(ctx context.Context, jobID string, token [32]byte) error {
	job, err := h.getJob(ctx, jobID)
	if err != nil {
		return err
	}
	now := h.Now
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	if job.Type == JobCSVImport && job.CommittedAt != nil {
		_ = h.Repository.ClearStaging(context.WithoutCancel(ctx), job.ID, token)
		return task.ErrAlreadyCommitted
	}
	principal, err := h.Principals.Principal(ctx, job.CreatedBy)
	if err != nil || !hasSystem(principal, "transfers.execute") {
		return task.PermanentError("permission_denied", "执行权限已失效")
	}
	if job.Type == JobCSVImport {
		return h.importCSV(ctx, job, token, principal, now)
	}
	if job.Type == JobCSVExport {
		return h.exportCSV(ctx, job, token, now)
	}
	return task.PermanentError("invalid_job_type", "任务类型无效")
}

func (h *JobHandler) getJob(ctx context.Context, id string) (Job, error) {
	if repository, ok := h.Repository.(interface {
		GetJobByID(context.Context, string) (Job, error)
	}); ok {
		return repository.GetJobByID(ctx, id)
	}
	return Job{}, errors.New("transfer Repository 未实现 Worker 任务查询")
}

func (h *JobHandler) importCSV(ctx context.Context, job Job, token [32]byte, principal identity.Principal, now func() time.Time) error {
	if !hasModel(principal, job.ModelID, "content.create") {
		return task.PermanentError("permission_denied", "执行权限已失效")
	}
	var snapshot struct {
		ID        string                `json:"id"`
		UpdatedAt time.Time             `json:"updated_at"`
		Fields    []schema.ContentField `json:"fields"`
	}
	if json.Unmarshal(job.ModelSnapshot, &snapshot) != nil {
		return task.PermanentError("model_changed", "模型快照无效")
	}
	current, err := h.Repository.CurrentModel(ctx, job.ModelID)
	if err != nil || !modelSnapshotMatches(snapshot.ID, snapshot.UpdatedAt, snapshot.Fields, current) {
		return task.PermanentError("model_changed", "模型已变化")
	}
	if err := h.Repository.ClearAttempt(ctx, job.ID, token); err != nil {
		return err
	}
	if err := h.Repository.UpdateProgress(ctx, job.ID, token, 5); err != nil {
		return err
	}
	body, err := h.Store.Get(ctx, job.SourceObjectKey)
	if err != nil {
		return task.RetryableError("object_store_unavailable", "读取导入文件失败")
	}
	defer body.Close()
	err = ParseCSV(body, snapshot.Fields, func(row int, raw json.RawMessage) error { return h.Repository.Stage(ctx, job.ID, token, row, raw) })
	if err != nil {
		var csvErr *CSVError
		if errors.As(err, &csvErr) {
			return h.failValidation(ctx, job.ID, token, csvErr.Detail, now)
		}
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = h.Repository.UpdateProgress(ctx, job.ID, token, 45); err != nil {
		return err
	}
	source := func(yield func(content.ImportDraft) error) error {
		return h.Repository.LoadStaged(ctx, job.ID, func(_ int, raw json.RawMessage) error {
			return yield(content.ImportDraft{Content: append(json.RawMessage(nil), raw...)})
		})
	}
	if h.Validator == nil {
		return task.PermanentError("validation_unavailable", "导入校验器未装配")
	}
	errorsFound := h.Validator.ValidateDrafts(ctx, job.ModelID, source)
	if err = ctx.Err(); err != nil {
		return err
	}
	sort.Slice(errorsFound, func(i, j int) bool {
		if errorsFound[i].Row != errorsFound[j].Row {
			return errorsFound[i].Row < errorsFound[j].Row
		}
		if errorsFound[i].Field != errorsFound[j].Field {
			return errorsFound[i].Field < errorsFound[j].Field
		}
		return errorsFound[i].Code < errorsFound[j].Code
	})
	if len(errorsFound) > 0 {
		if err = h.Repository.AddErrors(ctx, job.ID, token, errorsFound); err != nil {
			return err
		}
		if err = h.writeErrorReport(ctx, job.ID, token, errorsFound, now); err != nil {
			return err
		}
		return task.PermanentError("validation_failed", "CSV 内容校验失败")
	}
	if err = h.Repository.UpdateProgress(ctx, job.ID, token, 70); err != nil {
		return err
	}
	if h.Importer == nil {
		return task.PermanentError("importer_unavailable", "内容导入器未装配")
	}
	// 正式导入事务开始后不再响应关闭或用户取消；租约 fencing 仍决定事务能否提交。
	commitCtx := context.WithoutCancel(ctx)
	commitSource := func(yield func(content.ImportDraft) error) error {
		return h.Repository.LoadStaged(commitCtx, job.ID, func(_ int, raw json.RawMessage) error {
			return yield(content.ImportDraft{Content: append(json.RawMessage(nil), raw...)})
		})
	}
	if err = h.Importer.ImportDrafts(commitCtx, principal, content.RequestMeta{}, job.ModelID, commitSource, func(q database.Querier) error {
		return h.Repository.MarkCommitted(commitCtx, q, job.ID, token, now())
	}); err != nil {
		if errors.Is(err, task.ErrLeaseLost) {
			return task.ErrLeaseLost
		}
		return task.PermanentError("validation_failed", "正式写入失败")
	}
	_ = h.Repository.UpdateProgress(commitCtx, job.ID, token, 95)
	_ = h.Repository.ClearStaging(commitCtx, job.ID, token)
	return task.ErrAlreadyCommitted
}

func (h *JobHandler) failValidation(ctx context.Context, id string, token [32]byte, detail TransferError, now func() time.Time) error {
	if err := h.Repository.AddErrors(ctx, id, token, []TransferError{detail}); err != nil {
		return err
	}
	if err := h.writeErrorReport(ctx, id, token, []TransferError{detail}, now); err != nil {
		return err
	}
	return task.PermanentError(detail.Code, "CSV 校验失败")
}

func (h *JobHandler) exportCSV(ctx context.Context, job Job, token [32]byte, now func() time.Time) error {
	var snapshot struct {
		ID        string                `json:"id"`
		UpdatedAt time.Time             `json:"updated_at"`
		Fields    []schema.ContentField `json:"fields"`
	}
	if json.Unmarshal(job.ModelSnapshot, &snapshot) != nil {
		return task.PermanentError("model_changed", "模型快照无效")
	}
	current, err := h.Repository.CurrentModel(ctx, job.ModelID)
	if err != nil || !modelSnapshotMatches(snapshot.ID, snapshot.UpdatedAt, snapshot.Fields, current) {
		return task.PermanentError("model_changed", "模型已变化")
	}
	if h.Exporter == nil {
		return task.PermanentError("exporter_unavailable", "导出数据源未装配")
	}
	if err := h.Repository.ClearAttempt(ctx, job.ID, token); err != nil {
		return err
	}
	if err := h.Repository.UpdateProgress(ctx, job.ID, token, 10); err != nil {
		return err
	}
	key := attemptObjectKey(job.ID, job.Attempt, token, "result.csv")
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		err := WriteCSV(writer, snapshot.Fields, func(yield func(json.RawMessage) error) error { return h.Exporter.Stream(ctx, job, yield) })
		_ = writer.CloseWithError(err)
		done <- err
	}()
	putErr := h.Store.Put(ctx, key, "text/csv; charset=utf-8", reader)
	_ = reader.CloseWithError(putErr)
	writeErr := <-done
	if putErr != nil {
		return task.RetryableError("object_store_unavailable", "写入导出文件失败")
	}
	if writeErr != nil {
		return writeErr
	}
	if err := h.Repository.UpdateProgress(ctx, job.ID, token, 90); err != nil {
		return err
	}
	return h.Repository.SetFile(ctx, job.ID, token, "result", key, now().Add(7*24*time.Hour))
}

func (h *JobHandler) writeErrorReport(ctx context.Context, id string, token [32]byte, items []TransferError, now func() time.Time) error {
	reader, writer := io.Pipe()
	done := make(chan error, 1)
	go func() {
		_, err := writer.Write([]byte{0xef, 0xbb, 0xbf})
		out := csv.NewWriter(writer)
		out.UseCRLF = true
		if err == nil {
			err = out.Write([]string{"row", "field", "code", "message"})
		}
		for _, item := range items {
			if err != nil {
				break
			}
			err = out.Write([]string{strconv.Itoa(item.Row), item.Field, item.Code, formulaSafe(item.Message)})
		}
		out.Flush()
		if err == nil {
			err = out.Error()
		}
		_ = writer.CloseWithError(err)
		done <- err
	}()
	job, err := h.getJob(ctx, id)
	if err != nil {
		return err
	}
	key := attemptObjectKey(id, job.Attempt, token, "errors.csv")
	putErr := h.Store.Put(ctx, key, "text/csv; charset=utf-8", reader)
	_ = reader.CloseWithError(putErr)
	writeErr := <-done
	if putErr != nil {
		return putErr
	}
	if writeErr != nil {
		return writeErr
	}
	return h.Repository.SetFile(ctx, id, token, "errors", key, now().Add(7*24*time.Hour))
}

func attemptObjectKey(id string, attempt int, token [32]byte, name string) string {
	digest := sha256.Sum256(token[:])
	return "transfers/" + id + "/attempt-" + strconv.Itoa(attempt) + "-" + hex.EncodeToString(digest[:8]) + "/" + name
}

func modelSnapshotMatches(id string, updatedAt time.Time, fields []schema.ContentField, current schema.ContentModel) bool {
	snapshotFields, snapshotErr := json.Marshal(fields)
	currentFields, currentErr := json.Marshal(ActiveRootFields(current.Fields))
	return snapshotErr == nil && currentErr == nil && id == current.ID && updatedAt.Equal(current.UpdatedAt) && string(snapshotFields) == string(currentFields)
}
