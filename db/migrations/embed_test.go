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

func TestAuditActorDisplayNameMigrationsAreSingleStatements(t *testing.T) {
	for _, name := range []string{"000034_audit_actor_display_name.up.sql", "000035_audit_actor_display_name_backfill.up.sql"} {
		contents, err := Files.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		sql := strings.TrimSpace(string(contents))
		if !strings.HasSuffix(sql, ";") || strings.Count(sql, ";") != 1 {
			t.Fatalf("%s 必须且只能包含一个 SQL statement", name)
		}
	}
}
