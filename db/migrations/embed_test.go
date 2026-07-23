package migrations

import (
	"io/fs"
	"strings"
	"testing"
)

func TestMigrationsAreSingleStatements(t *testing.T) {
	names, err := fs.Glob(Files, "*.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if len(names) == 0 {
		t.Fatal("未嵌入任何迁移")
	}
	for _, name := range names {
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

func TestAuthCleanupIndexMigrationIsSingleStatement(t *testing.T) {
	contents, err := Files.ReadFile("000050_auth_cleanup_indexes.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.TrimSpace(string(contents))
	if !strings.HasSuffix(sql, ";") || strings.Count(sql, ";") != 1 || !strings.Contains(sql, "idx_sessions_idle_cleanup") || !strings.Contains(sql, "idx_sessions_absolute_cleanup") || !strings.Contains(sql, "idx_sessions_revoked_cleanup") {
		t.Fatal("000050_auth_cleanup_indexes.up.sql 必须以单条语句增加三个清理索引")
	}
}
