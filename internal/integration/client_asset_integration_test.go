package integration

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cms/internal/asset"
	"cms/internal/audit"
	"cms/internal/client"
	"cms/internal/platform/database"
)

func TestClientAssetDownloadUsesSingleConnectionTransaction(t *testing.T) {
	db := openClientAssetTestDB(t)
	db.SetMaxOpenConns(1)
	fixture := createClientAssetFixture(t, db)
	handler := newClientAssetTestHandler(t, db, database.NewTransactor(db))

	response := requestClientAsset(t, handler, fixture.rawKey, fixture.assetID)
	if response.Code != http.StatusFound || response.Header().Get("Location") == "" {
		t.Fatalf("下载响应 = %d, Location = %q, body = %s", response.Code, response.Header().Get("Location"), response.Body.String())
	}
	var lastUsed sql.NullTime
	if err := db.QueryRow(`SELECT last_used_at FROM api_keys WHERE id=?`, fixture.keyID).Scan(&lastUsed); err != nil || !lastUsed.Valid {
		t.Fatalf("下载后 last_used_at = %v, error = %v", lastUsed, err)
	}
}

func TestClientAssetDownloadSerializesWithRevocation(t *testing.T) {
	t.Run("撤销先持锁时下载拒绝", func(t *testing.T) {
		db := openClientAssetTestDB(t)
		db.SetMaxOpenConns(2)
		fixture := createClientAssetFixture(t, db)
		handler := newClientAssetTestHandler(t, db, database.NewTransactor(db))

		revokeTx, err := db.BeginTx(context.Background(), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer revokeTx.Rollback()
		if _, err = revokeTx.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, time.Now().UTC(), fixture.keyID); err != nil {
			t.Fatal(err)
		}
		response := make(chan *httptest.ResponseRecorder, 1)
		go func() { response <- requestClientAsset(t, handler, fixture.rawKey, fixture.assetID) }()
		select {
		case result := <-response:
			t.Fatalf("下载未等待撤销事务，响应 = %d", result.Code)
		case <-time.After(150 * time.Millisecond):
		}
		if err = revokeTx.Commit(); err != nil {
			t.Fatal(err)
		}
		result := waitResponse(t, response)
		if result.Code != http.StatusUnauthorized || !strings.Contains(result.Body.String(), `"code":"api_key_revoked"`) {
			t.Fatalf("撤销后的下载响应 = %d, body = %s", result.Code, result.Body.String())
		}
	})

	t.Run("下载先持锁时撤销等待授权提交", func(t *testing.T) {
		db := openClientAssetTestDB(t)
		db.SetMaxOpenConns(2)
		fixture := createClientAssetFixture(t, db)
		gate := &gatedTransactor{db: db, authorized: make(chan struct{}), release: make(chan struct{})}
		handler := newClientAssetTestHandler(t, db, gate)

		response := make(chan *httptest.ResponseRecorder, 1)
		go func() { response <- requestClientAsset(t, handler, fixture.rawKey, fixture.assetID) }()
		select {
		case <-gate.authorized:
		case result := <-response:
			t.Fatalf("下载授权事务提前结束，响应 = %d, body = %s", result.Code, result.Body.String())
		case <-time.After(2 * time.Second):
			t.Fatal("等待下载授权事务超时")
		}
		revoked := make(chan error, 1)
		go func() {
			_, err := db.Exec(`UPDATE api_keys SET revoked_at=? WHERE id=?`, time.Now().UTC(), fixture.keyID)
			revoked <- err
		}()
		select {
		case err := <-revoked:
			t.Fatalf("撤销未等待下载授权事务: %v", err)
		case <-time.After(150 * time.Millisecond):
		}
		close(gate.release)
		result := waitResponse(t, response)
		if result.Code != http.StatusFound {
			t.Fatalf("已完成授权的下载响应 = %d, body = %s", result.Code, result.Body.String())
		}
		if err := waitError(t, revoked); err != nil {
			t.Fatal(err)
		}
	})
}

type clientAssetFixture struct {
	keyID   string
	assetID string
	rawKey  string
}

func createClientAssetFixture(t *testing.T, db *sql.DB) clientAssetFixture {
	t.Helper()
	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	userID, modelID := "usr_asset_"+suffix, "mdl_asset_"+suffix
	fieldID, entryID, revisionID := "fld_asset_"+suffix, "ent_asset_"+suffix, "rev_asset_"+suffix
	keyID := "key_asset_" + suffix
	assetHex := fmt.Sprintf("%032x", time.Now().UnixNano())
	assetID, objectKey := "ast_"+assetHex, "assets/ast_"+assetHex+"/0123456789abcdef0123456789abcdef"
	prefixDigest := sha256.Sum256([]byte(suffix))
	prefix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(prefixDigest[:]))[:12]
	secret, salt := []byte("0123456789abcdef0123456789abcdef"), []byte("0123456789abcdef")
	digest := sha256.Sum256(append(append([]byte(nil), salt...), secret...))
	rawKey := "cmsk_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(secret)
	now := time.Now().UTC()

	t.Cleanup(func() {
		for _, cleanup := range []struct {
			query string
			arg   string
		}{
			{`DELETE FROM asset_references WHERE revision_id=?`, revisionID},
			{`DELETE FROM content_published_pointers WHERE entry_id=?`, entryID},
			{`DELETE FROM api_key_model_scopes WHERE api_key_id=?`, keyID},
			{`DELETE FROM api_keys WHERE id=?`, keyID},
			{`DELETE FROM assets WHERE id=?`, assetID},
			{`DELETE FROM content_revisions WHERE id=?`, revisionID},
			{`DELETE FROM content_entries WHERE id=?`, entryID},
			{`DELETE FROM content_fields WHERE id=?`, fieldID},
			{`DELETE FROM content_models WHERE id=?`, modelID},
			{`DELETE FROM users WHERE id=?`, userID},
		} {
			if _, err := db.Exec(cleanup.query, cleanup.arg); err != nil {
				t.Errorf("清理素材下载测试夹具失败: %v", err)
			}
		}
	})

	statements := []struct {
		query string
		args  []any
	}{
		{`INSERT INTO users (id,display_name,enabled,created_at,updated_at) VALUES (?, 'asset tx', TRUE, ?, ?)`, []any{userID, now, now}},
		{`INSERT INTO content_models (id,model_key,display_name,description,status,created_at,updated_at) VALUES (?, ?, 'asset tx', '', 'active', ?, ?)`, []any{modelID, "asset_" + suffix, now, now}},
		{`INSERT INTO content_fields (id,model_id,parent_id,field_key,display_name,description,field_type,is_required,default_value,constraints,status,depth,position,created_at,updated_at) VALUES (?, ?, NULL, 'media', 'media', '', 'single_media', FALSE, CAST('null' AS JSON), CAST('{}' AS JSON), 'active', 0, 0, ?, ?)`, []any{fieldID, modelID, now, now}},
		{`INSERT INTO content_entries (id,model_id,status,created_by,created_at,updated_at) VALUES (?, ?, 'draft', ?, ?, ?)`, []any{entryID, modelID, userID, now, now}},
		{`INSERT INTO content_revisions (id,entry_id,model_id,revision_number,content,workflow_status,created_by,submitted_by,submitted_at,created_at) VALUES (?, ?, ?, 1, CAST('{}' AS JSON), 'published', ?, ?, ?, ?)`, []any{revisionID, entryID, modelID, userID, userID, now, now}},
		{`INSERT INTO content_published_pointers (entry_id,model_id,revision_id,published_at) VALUES (?, ?, ?, ?)`, []any{entryID, modelID, revisionID, now}},
		{`INSERT INTO assets (id,object_key,filename,mime_type,size,sha256,etag,status,created_by,created_at,confirmed_at,archived_at,upload_expires_at) VALUES (?, ?, 'file.txt', 'text/plain', 1, ?, 'etag', 'available', ?, ?, ?, NULL, ?)`, []any{assetID, objectKey, strings.Repeat("0", 64), userID, now, now, now.Add(time.Minute)}},
		{`INSERT INTO asset_references (revision_id,entry_id,model_id,field_id,asset_id,json_pointer,position) VALUES (?, ?, ?, ?, ?, _binary'/media', 0)`, []any{revisionID, entryID, modelID, fieldID, assetID}},
		{`INSERT INTO api_keys (id,name,prefix,salt,secret_hash,expires_at,revoked_at,last_used_at,rotated_from_id,replaced_by_id,created_by,created_at) VALUES (?, 'asset tx', ?, ?, ?, NULL, NULL, NULL, NULL, NULL, ?, ?)`, []any{keyID, prefix, salt, digest[:], userID, now}},
		{`INSERT INTO api_key_model_scopes (api_key_id,model_id) VALUES (?, ?)`, []any{keyID, modelID}},
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	return clientAssetFixture{keyID: keyID, assetID: assetID, rawKey: rawKey}
}

func newClientAssetTestHandler(t *testing.T, db *sql.DB, tx TransactionRunner) ClientAssetHandler {
	t.Helper()
	clientService := client.NewService(client.Dependencies{DB: db, Transactor: database.NewTransactor(db), Repository: client.NewRepository()})
	assetService, err := asset.NewService(asset.Dependencies{
		DB: db, Transactor: database.NewTransactor(db), Repository: asset.SQLRepository{},
		Store: asset.NewMemoryStore(15*time.Minute, 5*time.Minute), Audit: testAuditWriter{},
		Config: asset.Config{AllowedMimeTypes: []string{"text/plain"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	return ClientAssetHandler{Tx: tx, Client: clientService, Assets: assetService}
}

func requestClientAsset(t *testing.T, handler ClientAssetHandler, rawKey, assetID string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/content/v1/assets/"+assetID, nil)
	request.SetPathValue("asset_id", assetID)
	request.Header.Set("Authorization", "Bearer "+rawKey)
	response := httptest.NewRecorder()
	handler.download(response, request)
	return response
}

func waitResponse(t *testing.T, responses <-chan *httptest.ResponseRecorder) *httptest.ResponseRecorder {
	t.Helper()
	select {
	case response := <-responses:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("等待素材下载响应超时")
		return nil
	}
}

func waitError(t *testing.T, errors <-chan error) error {
	t.Helper()
	select {
	case err := <-errors:
		return err
	case <-time.After(2 * time.Second):
		t.Fatal("等待并发数据库操作超时")
		return nil
	}
}

type gatedTransactor struct {
	db         *sql.DB
	authorized chan struct{}
	release    chan struct{}
}

func (g *gatedTransactor) WithinTx(ctx context.Context, options *sql.TxOptions, fn func(database.Querier) error) error {
	tx, err := g.db.BeginTx(ctx, options)
	if err != nil {
		return err
	}
	if err = fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	close(g.authorized)
	select {
	case <-g.release:
	case <-ctx.Done():
		_ = tx.Rollback()
		return ctx.Err()
	}
	return tx.Commit()
}

type testAuditWriter struct{}

func (testAuditWriter) Append(context.Context, database.Querier, audit.Event) error { return nil }

func openClientAssetTestDB(t *testing.T) *sql.DB {
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
