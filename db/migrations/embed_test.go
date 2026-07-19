package migrations

import (
	"strings"
	"testing"
)

func TestContentFieldValuesBackfillIsSingleStatement(t *testing.T) {
	contents, err := Files.ReadFile("000026_content_field_values_backfill.up.sql")
	if err != nil {
		t.Fatalf("回填迁移不存在: %v", err)
	}

	sql := strings.TrimSpace(string(contents))
	if !strings.HasSuffix(sql, ";") || strings.Count(sql, ";") != 1 {
		t.Fatal("回填迁移必须且只能包含一个 SQL statement")
	}
}
