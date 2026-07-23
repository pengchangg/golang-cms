package asset

import (
	"context"
	"database/sql"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type captureQuerier struct {
	query string
	args  []any
}

func (*captureQuerier) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("测试不应执行 ExecContext")
}

func (q *captureQuerier) QueryContext(_ context.Context, query string, args ...any) (*sql.Rows, error) {
	q.query, q.args = query, args
	return nil, errors.New("停止在查询边界")
}

func (*captureQuerier) QueryRowContext(context.Context, string, ...any) *sql.Row { return &sql.Row{} }

func TestSQLRepositoryListFiltersKindBeforeCursorPagination(t *testing.T) {
	createdAt := time.Date(2026, 7, 23, 8, 0, 0, 0, time.UTC)
	querier := &captureQuerier{}
	_, err := (SQLRepository{}).List(context.Background(), querier, ListQuery{Kind: AssetKindAudio}, 21, &Cursor{CreatedAt: createdAt, ID: "ast_cursor"})
	if err == nil || !strings.Contains(err.Error(), "停止在查询边界") {
		t.Fatalf("List() error = %v", err)
	}
	kindClause := "mime_type IN (?,?,?,?,?)"
	cursorClause := "(created_at<? OR (created_at=? AND id<?))"
	if !strings.Contains(querier.query, kindClause) || strings.Index(querier.query, kindClause) > strings.Index(querier.query, cursorClause) {
		t.Fatalf("kind 必须在游标分页前由数据库过滤: %s", querier.query)
	}
	wantArgs := []any{"audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm", createdAt, createdAt, "ast_cursor", 21}
	if !reflect.DeepEqual(querier.args, wantArgs) {
		t.Fatalf("查询参数 = %#v, want %#v", querier.args, wantArgs)
	}
}

func TestMimeTypesForAssetKindUsesFixedWhitelist(t *testing.T) {
	tests := map[AssetKind][]string{
		AssetKindImage: {"image/jpeg", "image/png", "image/gif", "image/webp", "image/avif"},
		AssetKindVideo: {"video/mp4", "video/webm"},
		AssetKindAudio: {"audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm"},
	}
	for kind, want := range tests {
		if got := mimeTypesForAssetKind(kind); !reflect.DeepEqual(got, want) {
			t.Fatalf("mimeTypesForAssetKind(%q) = %#v, want %#v", kind, got, want)
		}
	}
}
