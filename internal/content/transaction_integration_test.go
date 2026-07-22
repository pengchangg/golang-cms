package content

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"cms/internal/platform/database"
	"cms/internal/schema"
)

func TestHasAnyContentUsesSingleConnectionTransaction(t *testing.T) {
	db := openTransactionTestDB(t)
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	service := NewService(Dependencies{DB: db, Repository: SQLRepository{}})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := service.HasAnyContent(ctx, tx, "mdl_missing"); err != nil {
		t.Fatalf("HasAnyContent() error = %v", err)
	}
}

func TestLockedReadsIgnoreEarlierRepeatableReadSnapshot(t *testing.T) {
	db := openTransactionTestDB(t)
	db.SetMaxOpenConns(2)
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())
	userID, modelID, fieldID := "usr_tx_"+suffix, "mdl_tx_"+suffix, "fld_tx_"+suffix
	now := time.Now().UTC()
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			arg   string
		}{
			{`DELETE FROM content_entries WHERE model_id=?`, modelID},
			{`DELETE FROM content_fields WHERE model_id=?`, modelID},
			{`DELETE FROM content_models WHERE id=?`, modelID},
			{`DELETE FROM users WHERE id=?`, userID},
		} {
			if _, err := db.Exec(cleanup.query, cleanup.arg); err != nil {
				t.Errorf("清理 MySQL 测试夹具失败: %v", err)
			}
		}
	})
	if _, err := db.Exec(`INSERT INTO users (id,display_name,enabled,created_at,updated_at) VALUES (?, 'tx', TRUE, ?, ?)`, userID, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO content_models (id,model_key,display_name,description,status,created_at,updated_at) VALUES (?, ?, 'tx', '', 'active', ?, ?)`, modelID, "tx_"+suffix, now, now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO content_fields (id,model_id,parent_id,field_key,display_name,description,field_type,is_required,default_value,constraints,status,depth,position,created_at,updated_at) VALUES (?, ?, NULL, 'value', 'value', '', 'single_line_text', FALSE, CAST('null' AS JSON), CAST('{}' AS JSON), 'active', 0, 0, ?, ?)`, fieldID, modelID, now, now); err != nil {
		t.Fatal(err)
	}
	t.Run("内容存在性使用锁定当前读", func(t *testing.T) {
		tx := beginRepeatableRead(t, db)
		defer tx.Rollback()
		var fieldType string
		if err := tx.QueryRow(`SELECT field_type FROM content_fields WHERE id=?`, fieldID).Scan(&fieldType); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO content_entries (id,model_id,status,created_by,created_at,updated_at) VALUES (?, ?, 'draft', ?, ?, ?)`, "ent_tx_"+suffix, modelID, userID, now, now); err != nil {
			t.Fatal(err)
		}
		if _, err := (schema.SQLRepository{}).LockModel(context.Background(), tx, modelID); err != nil {
			t.Fatal(err)
		}
		exists, err := (SQLRepository{}).HasAnyContent(context.Background(), tx, modelID)
		if err != nil || !exists {
			t.Fatalf("HasAnyContent() = %v, %v", exists, err)
		}
	})

	if _, err := db.Exec(`DELETE FROM content_entries WHERE model_id=?`, modelID); err != nil {
		t.Fatal(err)
	}
	t.Run("锁定后读取最新字段树", func(t *testing.T) {
		tx := beginRepeatableRead(t, db)
		defer tx.Rollback()
		var fieldType string
		if err := tx.QueryRow(`SELECT field_type FROM content_fields WHERE id=?`, fieldID).Scan(&fieldType); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`UPDATE content_fields SET field_type='integer',updated_at=? WHERE id=?`, now.Add(time.Second), fieldID); err != nil {
			t.Fatal(err)
		}
		model, err := (schema.SQLRepository{}).LockModelSchema(context.Background(), tx, modelID)
		if err != nil {
			t.Fatal(err)
		}
		if len(model.Fields) != 1 || model.Fields[0].Type != schema.FieldTypeInteger {
			t.Fatalf("锁定字段树 = %+v", model.Fields)
		}
	})
}

func openTransactionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("CMS_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 CMS_TEST_MYSQL_DSN")
	}
	db, err := database.Open(context.Background(), dsn, false, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func beginRepeatableRead(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
