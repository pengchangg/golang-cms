package schema

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
)

func TestSQLRepositoryUpdateFieldOrderUsesSafeIntermediatePositions(t *testing.T) {
	querier := &orderQuerier{}
	parentID := "fld_parent"
	err := (SQLRepository{}).UpdateFieldOrder(context.Background(), querier, "mdl_1", &parentID, []string{"fld_b", "fld_a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(querier.execs) != 3 {
		t.Fatalf("exec count = %d", len(querier.execs))
	}
	prepare := querier.execs[0]
	if !strings.Contains(prepare.query, "position = -position - 1") || !strings.Contains(prepare.query, "parent_id <=> ?") || !strings.Contains(prepare.query, "status = 'active'") {
		t.Fatalf("prepare query = %s", prepare.query)
	}
	if prepare.args[0] != "mdl_1" || prepare.args[1] != &parentID {
		t.Fatalf("prepare args = %#v", prepare.args)
	}
	for index, wantID := range []string{"fld_b", "fld_a"} {
		write := querier.execs[index+1]
		if !strings.Contains(write.query, "parent_id <=> ?") || !strings.Contains(write.query, "status = 'active'") {
			t.Fatalf("write query = %s", write.query)
		}
		if write.args[0] != index || write.args[1] != wantID || write.args[2] != "mdl_1" || write.args[3] != &parentID {
			t.Fatalf("write args = %#v", write.args)
		}
	}
}

func TestSQLRepositoryUpdateFieldOrderRejectsMissingSibling(t *testing.T) {
	querier := &orderQuerier{affected: []int64{1, 0}}
	err := (SQLRepository{}).UpdateFieldOrder(context.Background(), querier, "mdl_1", nil, []string{"fld_missing"})
	assertErrorCode(t, err, "field_order_conflict")
}

type orderExec struct {
	query string
	args  []any
}

type orderQuerier struct {
	execs    []orderExec
	affected []int64
}

func (q *orderQuerier) ExecContext(_ context.Context, query string, args ...any) (sql.Result, error) {
	q.execs = append(q.execs, orderExec{query: query, args: args})
	affected := int64(1)
	if len(q.affected) >= len(q.execs) {
		affected = q.affected[len(q.execs)-1]
	}
	return orderResult(affected), nil
}

func (*orderQuerier) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("unexpected query")
}

func (*orderQuerier) QueryRowContext(context.Context, string, ...any) *sql.Row { return &sql.Row{} }

type orderResult int64

func (r orderResult) LastInsertId() (int64, error) { return 0, nil }
func (r orderResult) RowsAffected() (int64, error) { return int64(r), nil }
