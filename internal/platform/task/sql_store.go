package task

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const claimSQL = `SELECT b.id,b.type,b.payload,b.attempt,b.max_attempts,b.cancel_requested_at,t.committed_at
FROM background_jobs b
LEFT JOIN transfer_jobs t ON t.job_id=b.id
WHERE (b.status='queued' AND b.available_at<=NOW(6))
   OR (b.status='running' AND b.lease_expires_at<NOW(6))
ORDER BY b.available_at ASC,b.id ASC
LIMIT 1
FOR UPDATE SKIP LOCKED`

const claimUpdateSQL = `UPDATE background_jobs
SET status='running',attempt=attempt+1,lease_token=?,lease_owner=?,
    lease_expires_at=DATE_ADD(NOW(6),INTERVAL ? MICROSECOND),
    started_at=COALESCE(started_at,NOW(6)),finished_at=NULL,error_code=NULL,error_message=NULL
WHERE id=? AND status IN ('queued','running')`

const reconcileCommittedSQL = `UPDATE background_jobs
SET status='succeeded',progress=100,finished_at=NOW(6),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL
WHERE id=? AND type='csv_import' AND status IN ('queued','running')
  AND EXISTS(SELECT 1 FROM transfer_jobs WHERE job_id=background_jobs.id AND committed_at IS NOT NULL)`

const renewSQL = `UPDATE background_jobs
SET lease_expires_at=DATE_ADD(NOW(6),INTERVAL ? MICROSECOND)
WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`

const progressSQL = `UPDATE background_jobs SET progress=?
WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6) AND progress<=?`

const succeedSQL = `UPDATE background_jobs
SET status='succeeded',progress=100,finished_at=NOW(6),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL
WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)
  AND (cancel_requested_at IS NULL OR EXISTS(SELECT 1 FROM transfer_jobs WHERE job_id=background_jobs.id AND committed_at IS NOT NULL))`

const cancelSQL = `UPDATE background_jobs
SET status='canceled',finished_at=NOW(6),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL
WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`

const failSQL = `UPDATE background_jobs
SET status=IF(? AND attempt<max_attempts,'queued','failed'),
    available_at=IF(? AND attempt<max_attempts,DATE_ADD(NOW(6),INTERVAL IF(attempt=1,30,120) SECOND),available_at),
    error_code=IF(? AND attempt<max_attempts,NULL,?),error_message=IF(? AND attempt<max_attempts,NULL,?),
    finished_at=IF(? AND attempt<max_attempts,NULL,NOW(6)),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL
WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`

type SQLStore struct {
	db     *sql.DB
	random func([]byte) error
}

func NewSQLStore(db *sql.DB) (*SQLStore, error) {
	if db == nil {
		return nil, errors.New("任务数据库不能为空")
	}
	return &SQLStore{db: db, random: func(value []byte) error {
		_, err := rand.Read(value)
		return err
	}}, nil
}

func (s *SQLStore) Claim(ctx context.Context, owner string, lease time.Duration) (_ *Job, err error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
	if err != nil {
		return nil, fmt.Errorf("开始领取任务事务: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	var job Job
	var cancelRequested sql.NullTime
	var committedAt sql.NullTime
	err = tx.QueryRowContext(ctx, claimSQL).Scan(&job.ID, &job.Type, &job.Payload, &job.Attempt, &job.MaxAttempts, &cancelRequested, &committedAt)
	if errors.Is(err, sql.ErrNoRows) {
		_ = tx.Rollback()
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("锁定待领取任务: %w", err)
	}
	if job.Type == "csv_import" && committedAt.Valid {
		_, err = tx.ExecContext(ctx, reconcileCommittedSQL, job.ID)
		if err == nil {
			err = tx.Commit()
		}
		return nil, err
	}
	if cancelRequested.Valid {
		_, err = tx.ExecContext(ctx, `UPDATE background_jobs SET status='canceled',finished_at=NOW(6),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL WHERE id=? AND status IN ('queued','running')`, job.ID)
		if err == nil {
			err = tx.Commit()
		}
		return nil, err
	}
	if job.Attempt >= job.MaxAttempts {
		_, err = tx.ExecContext(ctx, `UPDATE background_jobs SET status='failed',error_code='lease_expired',error_message='任务租约过期且重试次数已耗尽',finished_at=NOW(6),lease_token=NULL,lease_owner=NULL,lease_expires_at=NULL WHERE id=? AND status IN ('queued','running')`, job.ID)
		if err == nil {
			err = tx.Commit()
		}
		return nil, err
	}
	if err = s.random(job.LeaseToken[:]); err != nil {
		return nil, fmt.Errorf("生成任务租约 token: %w", err)
	}
	if _, err = tx.ExecContext(ctx, claimUpdateSQL, job.LeaseToken[:], owner, lease.Microseconds(), job.ID); err != nil {
		return nil, fmt.Errorf("领取任务: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("提交领取任务事务: %w", err)
	}
	job.Attempt++
	return &job, nil
}

func (s *SQLStore) Renew(ctx context.Context, id string, token [32]byte, lease time.Duration) (bool, error) {
	result, err := s.db.ExecContext(ctx, renewSQL, lease.Microseconds(), id, token[:])
	if err != nil {
		return false, err
	}
	if err := requireChanged(result); err != nil {
		return false, err
	}
	var requested bool
	if err := s.db.QueryRowContext(ctx, `SELECT cancel_requested_at IS NOT NULL FROM background_jobs WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`, id, token[:]).Scan(&requested); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, ErrLeaseLost
		}
		return false, err
	}
	return requested, nil
}

func (s *SQLStore) UpdateProgress(ctx context.Context, id string, token [32]byte, progress int) error {
	if progress < 0 || progress > 100 {
		return errors.New("任务进度必须在 0 到 100 之间")
	}
	result, err := s.db.ExecContext(ctx, progressSQL, progress, id, token[:], progress)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 1 {
		return nil
	}
	var current int
	if err := s.db.QueryRowContext(ctx, `SELECT progress FROM background_jobs WHERE id=? AND status='running' AND lease_token=? AND lease_expires_at>=NOW(6)`, id, token[:]).Scan(&current); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrLeaseLost
		}
		return err
	}
	if current != progress {
		return errors.New("任务进度不得倒退")
	}
	return nil
}

func (s *SQLStore) Succeed(ctx context.Context, id string, token [32]byte) error {
	result, err := s.db.ExecContext(ctx, succeedSQL, id, token[:])
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *SQLStore) Fail(ctx context.Context, id string, token [32]byte, code, message string, retryable bool) error {
	result, err := s.db.ExecContext(ctx, failSQL, retryable, retryable, retryable, code, retryable, message, retryable, id, token[:])
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func (s *SQLStore) Cancel(ctx context.Context, id string, token [32]byte) error {
	result, err := s.db.ExecContext(ctx, cancelSQL, id, token[:])
	if err != nil {
		return err
	}
	return requireChanged(result)
}

func requireChanged(result sql.Result) error {
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return ErrLeaseLost
	}
	return nil
}
