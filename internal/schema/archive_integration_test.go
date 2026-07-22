package schema

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/database"
)

func TestArchiveModelInboundRelationUsesCurrentRead(t *testing.T) {
	db := openArchiveTestDB(t)
	db.SetMaxOpenConns(2)
	fixture := createArchiveTestModels(t, db)
	repository := SQLRepository{}

	archiveTx := beginArchiveTestTx(t, db)
	defer archiveTx.Rollback()
	inbound, err := repository.InboundRelationModelIDs(context.Background(), archiveTx, fixture.targetID, false)
	if err != nil || len(inbound) != 0 {
		t.Fatalf("初始入站关系 = %v, error = %v", inbound, err)
	}
	if err := insertArchiveTestRelation(context.Background(), db, fixture); err != nil {
		t.Fatal(err)
	}
	if _, err = repository.LockModels(context.Background(), archiveTx, []string{fixture.targetID}); err != nil {
		t.Fatal(err)
	}
	inbound, err = repository.InboundRelationModelIDs(context.Background(), archiveTx, fixture.targetID, true)
	if err != nil || len(inbound) != 1 || inbound[0] != fixture.sourceID {
		t.Fatalf("锁定当前读入站关系 = %v, error = %v", inbound, err)
	}
}

func TestRelationWriteObservesConcurrentModelArchive(t *testing.T) {
	db := openArchiveTestDB(t)
	db.SetMaxOpenConns(2)
	fixture := createArchiveTestModels(t, db)
	repository := SQLRepository{}

	archiveTx := beginArchiveTestTx(t, db)
	defer archiveTx.Rollback()
	models, err := repository.LockModels(context.Background(), archiveTx, []string{fixture.targetID})
	if err != nil {
		t.Fatal(err)
	}
	target := models[fixture.targetID]
	target.Status = StatusArchived
	target.UpdatedAt = time.Now().UTC()
	if err = repository.UpdateModel(context.Background(), archiveTx, target); err != nil {
		t.Fatal(err)
	}

	started := make(chan struct{})
	service := NewService(Dependencies{
		DB: db, Transactor: database.NewTransactor(db), Repository: signalingArchiveRepository{SQLRepository: repository, started: started},
		Authorizer: archiveTestAuthorizer{}, Audit: archiveTestAudit{},
	})
	service.newID = func(string) (string, error) { return fixture.fieldID, nil }
	result := make(chan error, 1)
	go func() {
		_, err := service.CreateField(context.Background(), identity.Principal{UserID: "usr_test"}, RequestMeta{}, fixture.sourceID, ContentFieldInput{Key: "relation", DisplayName: "Relation", Type: FieldTypeSingleRelation, DefaultValue: json.RawMessage(`null`), Constraints: FieldConstraints{TargetModelID: &fixture.targetID}, Children: []ContentFieldInput{}})
		result <- err
	}()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("关系字段写入未进入模型锁")
	}
	select {
	case err := <-result:
		t.Fatalf("关系写事务未等待模型归档锁: %v", err)
	case <-time.After(150 * time.Millisecond):
	}
	if err = archiveTx.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		assertErrorCode(t, err, "target_model_invalid")
	case <-time.After(2 * time.Second):
		t.Fatal("等待关系写事务超时")
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM content_fields WHERE id=?`, fixture.fieldID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("归档目标关系字段数量 = %d, error = %v", count, err)
	}
}

type signalingArchiveRepository struct {
	SQLRepository
	started chan struct{}
}

func (r signalingArchiveRepository) LockModels(ctx context.Context, q database.Querier, ids []string) (map[string]ContentModelSummary, error) {
	select {
	case <-r.started:
	default:
		close(r.started)
	}
	return r.SQLRepository.LockModels(ctx, q, ids)
}

type archiveTestAuthorizer struct{}

func (archiveTestAuthorizer) RequireSystemPermission(context.Context, identity.Principal, string) error {
	return nil
}

type archiveTestAudit struct{}

func (archiveTestAudit) Append(context.Context, database.Querier, audit.Event) error { return nil }

type archiveTestFixture struct {
	sourceID string
	targetID string
	fieldID  string
}

func createArchiveTestModels(t *testing.T, db *sql.DB) archiveTestFixture {
	t.Helper()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	fixture := archiveTestFixture{sourceID: "mdl_src_" + suffix, targetID: "mdl_dst_" + suffix, fieldID: "fld_rel_" + suffix}
	now := time.Now().UTC()
	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			arg   string
		}{
			{`DELETE FROM content_fields WHERE id=?`, fixture.fieldID},
			{`DELETE FROM content_models WHERE id=?`, fixture.sourceID},
			{`DELETE FROM content_models WHERE id=?`, fixture.targetID},
		} {
			if _, err := db.Exec(cleanup.query, cleanup.arg); err != nil {
				t.Errorf("清理模型归档测试夹具失败: %v", err)
			}
		}
	})
	for _, model := range []struct{ id, key string }{{fixture.sourceID, "src_" + suffix}, {fixture.targetID, "dst_" + suffix}} {
		if _, err := db.Exec(`INSERT INTO content_models (id,model_key,display_name,description,status,created_at,updated_at) VALUES (?,?,'tx','', 'active',?,?)`, model.id, model.key, now, now); err != nil {
			t.Fatal(err)
		}
	}
	return fixture
}

func insertArchiveTestRelation(ctx context.Context, q database.Querier, fixture archiveTestFixture) error {
	constraints, err := json.Marshal(FieldConstraints{TargetModelID: &fixture.targetID})
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	_, err = q.ExecContext(ctx, `INSERT INTO content_fields (id,model_id,parent_id,field_key,display_name,description,field_type,is_required,default_value,constraints,status,depth,position,created_at,updated_at) VALUES (?,?,NULL,'relation','relation','','single_relation',FALSE,CAST('null' AS JSON),?,'active',0,0,?,?)`, fixture.fieldID, fixture.sourceID, constraints, now, now)
	return err
}

func openArchiveTestDB(t *testing.T) *sql.DB {
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

func beginArchiveTestTx(t *testing.T, db *sql.DB) *sql.Tx {
	t.Helper()
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelRepeatableRead})
	if err != nil {
		t.Fatal(err)
	}
	return tx
}
