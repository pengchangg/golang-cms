package transfer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"cms/internal/platform/database"
	"cms/internal/platform/task"
	"cms/internal/schema"
)

type Repository interface {
	IsEmergencyAdmin(context.Context, database.Querier, string) (bool, error)
	FindIdempotent(context.Context, database.Querier, string, string, string, string) (Job, string, error)
	CreateJob(context.Context, database.Querier, Job, string, string, string, string) error
	GetJob(context.Context, database.Querier, string, string, bool) (Job, error)
	ListJobs(context.Context, database.Querier, string, JobFilter, bool) ([]Job, error)
	Cancel(context.Context, database.Querier, string, string, time.Time, bool) (Job, error)
	Retry(context.Context, database.Querier, string, string, time.Time, bool) (Job, error)
	ListErrors(context.Context, database.Querier, string, string, int, int, bool) ([]TransferError, bool, error)
	ClearAttempt(context.Context, string, [32]byte) error
	ClearStaging(context.Context, string, [32]byte) error
	Stage(context.Context, string, [32]byte, int, json.RawMessage) error
	LoadStaged(context.Context, string, func(int, json.RawMessage) error) error
	AddErrors(context.Context, string, [32]byte, []TransferError) error
	SetFile(context.Context, string, [32]byte, string, string, time.Time) error
	MarkCommitted(context.Context, database.Querier, string, [32]byte, time.Time) error
	UpdateProgress(context.Context, string, [32]byte, int) error
	CurrentModel(context.Context, string) (schema.ContentModel, error)
}

type SQLRepository struct{ DB database.Querier }

func NewRepository(db database.Querier) *SQLRepository { return &SQLRepository{DB: db} }

func (r *SQLRepository) IsEmergencyAdmin(ctx context.Context, q database.Querier, userID string) (bool, error) {
	var emergency bool
	err := q.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN local_credentials lc ON lc.user_id=u.id WHERE u.id=? AND u.enabled=TRUE AND lc.emergency_admin=TRUE)`, userID).Scan(&emergency)
	return emergency, err
}

func (r *SQLRepository) GetJobByID(ctx context.Context, id string) (Job, error) {
	job, _, err := scanJob(r.DB.QueryRowContext(ctx, jobSelect+` WHERE b.id=?`, id))
	return job, err
}

func (r *SQLRepository) CurrentModel(ctx context.Context, id string) (schema.ContentModel, error) {
	return (schema.SQLRepository{}).GetModel(ctx, r.DB, id)
}

func (r *SQLRepository) FindIdempotent(ctx context.Context, q database.Querier, actor, method, path, key string) (Job, string, error) {
	job, hash, err := scanJob(q.QueryRowContext(ctx, jobSelect+` WHERE t.created_by=? AND t.request_method=? AND t.request_path=? AND t.idempotency_key=? AND t.idempotency_expires_at>NOW(6) FOR UPDATE`, actor, method, path, key))
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, "", nil
	}
	return job, hash, err
}
func (r *SQLRepository) CreateJob(ctx context.Context, q database.Querier, job Job, method, path, key, hash string) error {
	if _, err := q.ExecContext(ctx, `INSERT INTO background_jobs(id,type,status,model_id,payload,progress,attempt,max_attempts,available_at,created_by,created_at) VALUES(?,?,'queued',?,?,0,0,3,?,?,?)`, job.ID, job.Type, job.ModelID, job.RequestSnapshot, job.CreatedAt, job.CreatedBy, job.CreatedAt); err != nil {
		return err
	}
	_, err := q.ExecContext(ctx, `INSERT INTO transfer_jobs(job_id,created_by,model_snapshot,request_snapshot,source_object_key,request_method,request_path,idempotency_key,request_hash,idempotency_expires_at) VALUES(?,?,?,?,?,?,?,?,?,?)`, job.ID, job.CreatedBy, job.ModelSnapshot, job.RequestSnapshot, nullString(job.SourceObjectKey), method, path, key, hash, job.CreatedAt.Add(24*time.Hour))
	return err
}
func (r *SQLRepository) GetJob(ctx context.Context, q database.Querier, actor, id string, global bool) (Job, error) {
	query, args := jobSelect+` WHERE b.id=?`, []any{id}
	if !global {
		query += ` AND b.created_by=?`
		args = append(args, actor)
	}
	job, _, err := scanJob(q.QueryRowContext(ctx, query, args...))
	return job, err
}
func (r *SQLRepository) ListJobs(ctx context.Context, q database.Querier, actor string, filter JobFilter, global bool) ([]Job, error) {
	query, args := jobSelect+` WHERE TRUE`, []any{}
	if !global {
		query += ` AND b.created_by=?`
		args = append(args, actor)
	}
	if filter.Type != "" {
		query += ` AND b.type=?`
		args = append(args, filter.Type)
	}
	if filter.Status != "" {
		query += ` AND b.status=?`
		args = append(args, filter.Status)
	}
	if filter.ModelID != "" {
		query += ` AND b.model_id=?`
		args = append(args, filter.ModelID)
	}
	if filter.Cursor != "" {
		query += ` AND (b.created_at<(SELECT created_at FROM background_jobs WHERE id=?) OR (b.created_at=(SELECT created_at FROM background_jobs WHERE id=?) AND b.id<?))`
		args = append(args, filter.Cursor, filter.Cursor, filter.Cursor)
	}
	query += ` ORDER BY b.created_at DESC,b.id DESC LIMIT ?`
	args = append(args, filter.Limit+1)
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Job{}
	for rows.Next() {
		job, _, err := scanJob(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, rows.Err()
}
func (r *SQLRepository) Cancel(ctx context.Context, q database.Querier, actor, id string, now time.Time, global bool) (Job, error) {
	job, err := r.GetJob(ctx, q, actor, id, global)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, notFound()
	}
	if err != nil {
		return Job{}, err
	}
	if job.Status == JobRunning && job.CancelRequestedAt != nil {
		return job, nil
	}
	query, args := `UPDATE background_jobs b JOIN transfer_jobs t ON t.job_id=b.id SET b.status=IF(b.status='queued','canceled',b.status),b.cancel_requested_at=IF(b.status='running',?,b.cancel_requested_at),b.finished_at=IF(b.status='queued',?,b.finished_at) WHERE b.id=?`, []any{now, now, id}
	if !global {
		query += ` AND b.created_by=?`
		args = append(args, actor)
	}
	query += ` AND b.status IN ('queued','running') AND t.committed_at IS NULL`
	result, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return Job{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return Job{}, conflict("job_not_cancelable", "任务状态不允许取消")
	}
	return r.GetJob(ctx, q, actor, id, global)
}
func (r *SQLRepository) Retry(ctx context.Context, q database.Querier, actor, id string, now time.Time, global bool) (Job, error) {
	job, err := r.GetJob(ctx, q, actor, id, global)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, notFound()
	}
	if err != nil {
		return Job{}, err
	}
	if job.Status != JobFailed || job.Attempt >= job.MaxAttempts {
		return Job{}, conflict("job_not_retryable", "任务状态或次数不允许重试")
	}
	if _, err = q.ExecContext(ctx, `DELETE FROM transfer_job_errors WHERE job_id=?`, id); err != nil {
		return Job{}, err
	}
	if _, err = q.ExecContext(ctx, `DELETE FROM transfer_job_rows WHERE job_id=?`, id); err != nil {
		return Job{}, err
	}
	if _, err = q.ExecContext(ctx, `UPDATE transfer_jobs SET result_object_key=NULL,error_object_key=NULL,errors_truncated=FALSE WHERE job_id=?`, id); err != nil {
		return Job{}, err
	}
	query, args := `UPDATE background_jobs SET status='queued',available_at=?,progress=0,error_code=NULL,error_message=NULL,finished_at=NULL,expires_at=NULL,cancel_requested_at=NULL WHERE id=?`, []any{now, id}
	if !global {
		query += ` AND created_by=?`
		args = append(args, actor)
	}
	query += ` AND status='failed' AND attempt<max_attempts`
	result, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		return Job{}, err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return Job{}, conflict("job_not_retryable", "任务状态或次数不允许重试")
	}
	return r.GetJob(ctx, q, actor, id, global)
}
func (r *SQLRepository) ListErrors(ctx context.Context, q database.Querier, actor, id string, limit, after int, global bool) ([]TransferError, bool, error) {
	job, err := r.GetJob(ctx, q, actor, id, global)
	if err != nil {
		return nil, false, err
	}
	rows, err := q.QueryContext(ctx, `SELECT source_row,field_key,error_code,error_message FROM transfer_job_errors WHERE job_id=? AND sequence_no>? ORDER BY sequence_no LIMIT ?`, id, after, limit)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	items := []TransferError{}
	for rows.Next() {
		var item TransferError
		if err := rows.Scan(&item.Row, &item.Field, &item.Code, &item.Message); err != nil {
			return nil, false, err
		}
		items = append(items, item)
	}
	return items, job.ErrorsTruncated, rows.Err()
}
func (r *SQLRepository) ClearAttempt(ctx context.Context, id string, token [32]byte) error {
	for _, query := range []string{
		`DELETE e FROM transfer_job_errors e JOIN background_jobs b ON b.id=e.job_id AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6) WHERE e.job_id=?`,
		`DELETE r FROM transfer_job_rows r JOIN background_jobs b ON b.id=r.job_id AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6) WHERE r.job_id=?`,
	} {
		result, err := r.DB.ExecContext(ctx, query, token[:], id)
		if err != nil {
			return err
		}
		if err = r.requireLease(ctx, id, token, result); err != nil {
			return err
		}
	}
	result, err := r.DB.ExecContext(ctx, `UPDATE transfer_jobs t JOIN background_jobs b ON b.id=t.job_id SET t.result_object_key=NULL,t.error_object_key=NULL,t.errors_truncated=FALSE,b.expires_at=NULL WHERE t.job_id=? AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6)`, id, token[:])
	if err != nil {
		return err
	}
	return requireOne(result)
}
func (r *SQLRepository) ClearStaging(ctx context.Context, id string, token [32]byte) error {
	result, err := r.DB.ExecContext(ctx, `DELETE r FROM transfer_job_rows r JOIN background_jobs b ON b.id=r.job_id AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6) WHERE r.job_id=?`, token[:], id)
	if err != nil {
		return err
	}
	return r.requireLease(ctx, id, token, result)
}
func (r *SQLRepository) Stage(ctx context.Context, id string, token [32]byte, row int, raw json.RawMessage) error {
	result, err := r.DB.ExecContext(ctx, `INSERT INTO transfer_job_rows(job_id,source_row,normalized_content) SELECT ?,?,? FROM background_jobs WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`, id, row, []byte(raw), id, token[:])
	if err != nil {
		return err
	}
	return requireOne(result)
}
func (r *SQLRepository) LoadStaged(ctx context.Context, id string, fn func(int, json.RawMessage) error) error {
	rows, err := r.DB.QueryContext(ctx, `SELECT source_row,normalized_content FROM transfer_job_rows WHERE job_id=? ORDER BY source_row`, id)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var row int
		var raw []byte
		if err := rows.Scan(&row, &raw); err != nil {
			return err
		}
		if err = fn(row, json.RawMessage(raw)); err != nil {
			return err
		}
	}
	return rows.Err()
}
func (r *SQLRepository) AddErrors(ctx context.Context, id string, token [32]byte, items []TransferError) error {
	for i, item := range items {
		if i >= MaxRows {
			result, err := r.DB.ExecContext(ctx, `UPDATE transfer_jobs t JOIN background_jobs b ON b.id=t.job_id SET t.errors_truncated=TRUE WHERE t.job_id=? AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6)`, id, token[:])
			if err != nil {
				return err
			}
			if err = requireOne(result); err != nil {
				return err
			}
			break
		}
		result, err := r.DB.ExecContext(ctx, `INSERT INTO transfer_job_errors(job_id,sequence_no,source_row,field_key,error_code,error_message) SELECT ?,?,?,?,?,? FROM background_jobs WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`, id, i+1, item.Row, item.Field, item.Code, item.Message, id, token[:])
		if err != nil {
			return err
		}
		if err = requireOne(result); err != nil {
			return err
		}
	}
	return nil
}

func (r *SQLRepository) SetFile(ctx context.Context, id string, token [32]byte, kind, key string, expiresAt time.Time) error {
	column := "result_object_key"
	if kind == "errors" {
		column = "error_object_key"
	} else if kind != "result" {
		return fmt.Errorf("任务文件类型无效")
	}
	result, err := r.DB.ExecContext(ctx, `UPDATE transfer_jobs t JOIN background_jobs b ON b.id=t.job_id SET t.`+column+`=?,b.expires_at=? WHERE t.job_id=? AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6)`, key, expiresAt, id, token[:])
	if err != nil {
		return err
	}
	return requireOne(result)
}

func (r *SQLRepository) MarkCommitted(ctx context.Context, q database.Querier, id string, token [32]byte, at time.Time) error {
	result, err := q.ExecContext(ctx, `UPDATE transfer_jobs t JOIN background_jobs b ON b.id=t.job_id SET t.committed_at=? WHERE t.job_id=? AND t.committed_at IS NULL AND b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6)`, at, id, token[:])
	if err != nil {
		return err
	}
	return requireOne(result)
}

func (r *SQLRepository) UpdateProgress(ctx context.Context, id string, token [32]byte, progress int) error {
	if progress < 0 || progress > 99 {
		return errors.New("transfer 任务执行进度必须在 0 到 99 之间")
	}
	result, err := r.DB.ExecContext(ctx, `UPDATE background_jobs SET progress=? WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6) AND progress<=?`, progress, id, token[:], progress)
	if err != nil {
		return err
	}
	return r.requireLease(ctx, id, token, result)
}

func (r *SQLRepository) requireLease(ctx context.Context, id string, token [32]byte, result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil || changed > 0 {
		return err
	}
	var valid bool
	err = r.DB.QueryRowContext(ctx, `SELECT TRUE FROM background_jobs WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`, id, token[:]).Scan(&valid)
	if errors.Is(err, sql.ErrNoRows) {
		return task.ErrLeaseLost
	}
	return err
}

func requireOne(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return task.ErrLeaseLost
	}
	return nil
}

const jobSelect = `SELECT b.id,b.type,b.status,b.model_id,b.progress,b.attempt,b.max_attempts,b.cancel_requested_at,b.error_code,b.error_message,b.created_by,b.created_at,b.started_at,b.finished_at,b.expires_at,t.model_snapshot,t.request_snapshot,t.source_object_key,t.result_object_key,t.error_object_key,t.committed_at,t.errors_truncated,t.request_hash FROM background_jobs b JOIN transfer_jobs t ON t.job_id=b.id`

type scanner interface{ Scan(...any) error }

func scanJob(row scanner) (Job, string, error) {
	var j Job
	var cancel, started, finished, expires, committed sql.NullTime
	var code, message, source, result, report sql.NullString
	var model, request []byte
	var hash string
	err := row.Scan(&j.ID, &j.Type, &j.Status, &j.ModelID, &j.Progress, &j.Attempt, &j.MaxAttempts, &cancel, &code, &message, &j.CreatedBy, &j.CreatedAt, &started, &finished, &expires, &model, &request, &source, &result, &report, &committed, &j.ErrorsTruncated, &hash)
	if err != nil {
		return j, "", err
	}
	j.CancelRequestedAt = timePtr(cancel)
	j.StartedAt = timePtr(started)
	j.FinishedAt = timePtr(finished)
	j.ExpiresAt = timePtr(expires)
	j.CommittedAt = timePtr(committed)
	j.ErrorCode = stringPtr(code)
	j.ErrorMessage = stringPtr(message)
	j.SourceObjectKey = source.String
	j.ResultObjectKey = result.String
	j.ErrorObjectKey = report.String
	j.ModelSnapshot = model
	j.RequestSnapshot = request
	j.CreatedAt = j.CreatedAt.UTC()
	return j, hash, nil
}
func timePtr(v sql.NullTime) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}
func stringPtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}
func nullString(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func (r *SQLRepository) String() string { return fmt.Sprintf("transfer repository %T", r.DB) }
