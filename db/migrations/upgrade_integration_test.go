package migrations

import (
	"context"
	"database/sql"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/go-sql-driver/mysql"

	"cms/internal/platform/migrate"
)

func TestOIDCSessionCleanupUpgradeOnMySQL(t *testing.T) {
	dsn := os.Getenv("CMS_MIGRATION_UPGRADE_DSN")
	if dsn == "" {
		t.Skip("未设置 CMS_MIGRATION_UPGRADE_DSN")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	var tableCount int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("迁移升级测试要求专用空数据库，当前已有 %d 张表", tableCount)
	}
	all, err := migrate.Load(Files)
	if err != nil {
		t.Fatal(err)
	}
	if err = migrate.Up(ctx, db, all[:42]); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = db.ExecContext(ctx, "INSERT INTO users (id,display_name,enabled,created_at,updated_at) VALUES ('usr_oidc_history','历史 OIDC 用户',TRUE,?,?)", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO roles (id,`key`,display_name,description,created_at,updated_at) VALUES ('rol_legacy_high_risk','high_risk_admin','历史高危角色','保留',?,?)", now, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO user_roles (user_id,role_id,created_at) VALUES ('usr_oidc_history','rol_legacy_high_risk',?)", now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO role_system_permissions (role_id,permission,created_at) VALUES ('rol_legacy_high_risk','users.view',?)", now); err != nil {
		t.Fatal(err)
	}
	if err = migrate.Up(ctx, db, all[:46]); err != nil {
		t.Fatal(err)
	}
	var legacyKey, legacyKind string
	if err = db.QueryRowContext(ctx, "SELECT `key`,kind FROM roles WHERE id='rol_legacy_high_risk'").Scan(&legacyKey, &legacyKind); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacyKey, "legacy_high_risk_admin_") || legacyKind != "custom" {
		t.Fatalf("历史同名角色未安全保留: key=%s kind=%s", legacyKey, legacyKind)
	}
	var legacyBindings, builtinRoles int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM user_roles ur JOIN role_system_permissions rsp ON rsp.role_id=ur.role_id WHERE ur.user_id='usr_oidc_history' AND ur.role_id='rol_legacy_high_risk' AND rsp.permission='users.view'").Scan(&legacyBindings); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM roles WHERE `key`='high_risk_admin' AND kind='high_risk'").Scan(&builtinRoles); err != nil {
		t.Fatal(err)
	}
	if legacyBindings != 1 || builtinRoles != 1 {
		t.Fatalf("高危角色升级结果错误: legacy_bindings=%d builtin_roles=%d", legacyBindings, builtinRoles)
	}
	if _, err = db.ExecContext(ctx, "INSERT INTO sessions (id_hash,user_id,auth_method,created_at,last_seen_at,idle_expires_at,expires_at) VALUES (UNHEX(REPEAT('01',32)),'usr_oidc_history','oidc',?,?,?,?)", now, now, now.Add(time.Hour), now.Add(24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err = migrate.Up(ctx, db, all[:47]); err != nil {
		t.Fatal(err)
	}
	var revokedOIDCSessions int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE user_id='usr_oidc_history' AND auth_method='oidc' AND revoked_at IS NOT NULL").Scan(&revokedOIDCSessions); err != nil {
		t.Fatal(err)
	}
	if revokedOIDCSessions != 1 {
		t.Fatalf("000047 未先撤销历史 OIDC 会话: %d", revokedOIDCSessions)
	}
	if err = migrate.Up(ctx, db, all[:48]); err != nil {
		t.Fatal(err)
	}
	var oidcSessions int
	if err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM sessions WHERE user_id='usr_oidc_history'").Scan(&oidcSessions); err != nil {
		t.Fatal(err)
	}
	if oidcSessions != 0 {
		t.Fatalf("历史 OIDC 会话未清理: %d", oidcSessions)
	}
	if err = migrate.Up(ctx, db, all); err != nil {
		t.Fatal(err)
	}
	var columnType string
	if err = db.QueryRowContext(ctx, "SELECT COLUMN_TYPE FROM information_schema.columns WHERE table_schema=DATABASE() AND table_name='sessions' AND column_name='auth_method'").Scan(&columnType); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(columnType, "oidc") || !strings.Contains(columnType, "local") || !strings.Contains(columnType, "sms") {
		t.Fatalf("sessions.auth_method 类型错误: %s", columnType)
	}
	if err = migrate.Up(ctx, db, all); err != nil {
		t.Fatalf("完整迁移二次执行不幂等: %v", err)
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	rows, err := conn.QueryContext(ctx, "SELECT table_name FROM information_schema.tables WHERE table_schema=DATABASE()")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	expectedTables := []string{
		"api_key_model_scopes", "api_keys", "asset_references", "assets", "audit_events", "auth_rate_limits", "background_jobs", "captcha_challenges",
		"content_draft_pointers", "content_entries", "content_field_values", "content_fields", "content_models", "content_published_pointers", "content_relations", "content_revisions", "content_unique_values", "content_workflow_events",
		"local_credentials", "oidc_identities", "oidc_login_states", "role_model_permissions", "role_system_permissions", "roles", "schema_migrations", "sessions", "sms_challenges", "sms_credentials",
		"transfer_job_errors", "transfer_job_rows", "transfer_jobs", "transfer_uploads", "user_roles", "users",
	}
	slices.Sort(tables)
	if !slices.Equal(tables, expectedTables) {
		t.Fatalf("升级测试数据库表集合不匹配，拒绝清理: got=%v want=%v", tables, expectedTables)
	}
	if len(tables) > 0 {
		if _, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=0"); err != nil {
			t.Fatal(err)
		}
		defer func() { _, _ = conn.ExecContext(context.Background(), "SET FOREIGN_KEY_CHECKS=1") }()
		quoted := make([]string, len(tables))
		for index, name := range tables {
			quoted[index] = "`" + strings.ReplaceAll(name, "`", "``") + "`"
		}
		if _, err = conn.ExecContext(ctx, "DROP TABLE "+strings.Join(quoted, ",")); err != nil {
			t.Fatal(err)
		}
		if _, err = conn.ExecContext(ctx, "SET FOREIGN_KEY_CHECKS=1"); err != nil {
			t.Fatal(err)
		}
	}
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE()").Scan(&tableCount); err != nil {
		t.Fatal(err)
	}
	if tableCount != 0 {
		t.Fatalf("升级测试清理后仍有 %d 张表", tableCount)
	}
}
