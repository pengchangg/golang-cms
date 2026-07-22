package database

import (
	"context"
	"database/sql"
	"errors"

	mysql "github.com/go-sql-driver/mysql"
)

type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Transactor struct {
	db *sql.DB
}

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(Querier) error) error
}

type DeadlockRetryTransactor struct {
	runner TransactionRunner
}

func NewTransactor(db *sql.DB) Transactor {
	return Transactor{db: db}
}

func RetryDeadlocks(runner TransactionRunner) DeadlockRetryTransactor {
	return DeadlockRetryTransactor{runner: runner}
}

func (t DeadlockRetryTransactor) WithinTx(ctx context.Context, options *sql.TxOptions, fn func(Querier) error) (err error) {
	for attempt := 0; attempt < 3; attempt++ {
		err = t.runner.WithinTx(ctx, options, fn)
		if !isDeadlock(err) {
			return err
		}
	}
	return err
}

func (t Transactor) WithinTx(ctx context.Context, options *sql.TxOptions, fn func(Querier) error) (err error) {
	tx, err := t.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			_ = tx.Rollback()
			panic(recovered)
		}
	}()
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func isDeadlock(err error) bool {
	var mysqlError *mysql.MySQLError
	return errors.As(err, &mysqlError) && mysqlError.Number == 1213
}
