package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	mysql "github.com/go-sql-driver/mysql"
)

type errorTransactor struct {
	errors []error
	calls  int
}

func (t *errorTransactor) WithinTx(_ context.Context, _ *sql.TxOptions, _ func(Querier) error) error {
	err := t.errors[t.calls]
	t.calls++
	return err
}

func TestIsDeadlockOnlyMatchesMySQL1213(t *testing.T) {
	if !isDeadlock(&mysql.MySQLError{Number: 1213}) {
		t.Fatal("MySQL 1213 未识别为死锁")
	}
	if !isDeadlock(errors.Join(errors.New("事务失败"), &mysql.MySQLError{Number: 1213})) {
		t.Fatal("包装后的 MySQL 1213 未识别为死锁")
	}
	if isDeadlock(&mysql.MySQLError{Number: 1205}) || isDeadlock(errors.New("deadlock")) {
		t.Fatal("非 MySQL 1213 错误被识别为死锁")
	}
}

func TestDeadlockRetryTransactorRetriesOnlyMySQL1213(t *testing.T) {
	deadlock := &mysql.MySQLError{Number: 1213}
	runner := &errorTransactor{errors: []error{deadlock, deadlock, nil}}
	if err := RetryDeadlocks(runner).WithinTx(context.Background(), nil, func(Querier) error { return nil }); err != nil || runner.calls != 3 {
		t.Fatalf("WithinTx() error = %v, calls = %d", err, runner.calls)
	}

	runner = &errorTransactor{errors: []error{errors.New("业务失败")}}
	if err := RetryDeadlocks(runner).WithinTx(context.Background(), nil, func(Querier) error { return nil }); err == nil || runner.calls != 1 {
		t.Fatalf("WithinTx() error = %v, calls = %d", err, runner.calls)
	}
}
