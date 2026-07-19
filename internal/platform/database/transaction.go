package database

import (
	"context"
	"database/sql"
)

type Querier interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type Transactor struct {
	db *sql.DB
}

func NewTransactor(db *sql.DB) Transactor {
	return Transactor{db: db}
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
