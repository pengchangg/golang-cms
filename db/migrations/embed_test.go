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

func TestHighRiskRoleMigrationsAreSingleStatements(t *testing.T) {
	for _, name := range []string{"000043_roles_kind.up.sql", "000044_reserve_high_risk_role_key.up.sql", "000045_builtin_high_risk_role.up.sql"} {
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

func TestSMSChallengeUserMigrationIsSingleStatement(t *testing.T) {
	contents, err := Files.ReadFile("000046_sms_challenge_user.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.TrimSpace(string(contents))
	if !strings.HasSuffix(sql, ";") || strings.Count(sql, ";") != 1 {
		t.Fatal("000046_sms_challenge_user.up.sql 必须且只能包含一个 SQL statement")
	}
}

func TestOIDCSessionCleanupMigrationsAreSingleStatements(t *testing.T) {
	for _, name := range []string{"000047_revoke_oidc_sessions.up.sql", "000048_delete_oidc_sessions.up.sql", "000049_narrow_session_auth_method.up.sql"} {
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
