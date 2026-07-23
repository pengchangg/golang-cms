package asset

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type testQuerier struct{}

func (testQuerier) ExecContext(context.Context, string, ...any) (sql.Result, error) {
	return nil, errors.New("测试不应执行 SQL")
}
func (testQuerier) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, errors.New("测试不应执行 SQL")
}
func (testQuerier) QueryRowContext(context.Context, string, ...any) *sql.Row { return &sql.Row{} }

type testTransactor struct{ q database.Querier }

func (t testTransactor) WithinTx(ctx context.Context, _ *sql.TxOptions, fn func(database.Querier) error) error {
	return fn(t.q)
}

type memoryAssetRepository struct {
	values        map[string]Asset
	lastListQuery ListQuery
}

func (r *memoryAssetRepository) Create(_ context.Context, _ database.Querier, value Asset) error {
	r.values[value.ID] = value
	return nil
}
func (r *memoryAssetRepository) Get(_ context.Context, _ database.Querier, id string) (Asset, error) {
	value, ok := r.values[id]
	if !ok {
		return Asset{}, appError(apperror.KindNotFound, "resource_not_found", "素材不存在")
	}
	return value, nil
}
func (r *memoryAssetRepository) Lock(ctx context.Context, q database.Querier, id string) (Asset, error) {
	return r.Get(ctx, q, id)
}
func (r *memoryAssetRepository) Confirm(_ context.Context, _ database.Querier, id, etag string, at time.Time) error {
	value := r.values[id]
	value.Status, value.ETag, value.ConfirmedAt = StatusAvailable, &etag, &at
	r.values[id] = value
	return nil
}
func (r *memoryAssetRepository) DeleteQuarantined(_ context.Context, _ database.Querier, id string) error {
	value, ok := r.values[id]
	if !ok || value.Status != StatusQuarantined {
		return appError(apperror.KindConflict, "asset_not_quarantined", "仅待确认素材可以废弃")
	}
	delete(r.values, id)
	return nil
}
func (r *memoryAssetRepository) Rename(_ context.Context, _ database.Querier, id, filename string) error {
	value := r.values[id]
	value.Filename = filename
	r.values[id] = value
	return nil
}
func (r *memoryAssetRepository) Archive(_ context.Context, _ database.Querier, id string, at time.Time) error {
	value := r.values[id]
	value.Status, value.ArchivedAt = StatusArchived, &at
	r.values[id] = value
	return nil
}
func (r *memoryAssetRepository) List(_ context.Context, _ database.Querier, input ListQuery, limit int, _ *Cursor) ([]Asset, error) {
	r.lastListQuery = input
	result := []Asset{}
	for _, value := range r.values {
		if input.Status != nil && value.Status != *input.Status || input.MimeType != "" && value.MimeType != input.MimeType {
			continue
		}
		if mimeTypes := mimeTypesForAssetKind(input.Kind); len(mimeTypes) > 0 {
			matched := false
			for _, mimeType := range mimeTypes {
				matched = matched || value.MimeType == mimeType
			}
			if !matched {
				continue
			}
		}
		result = append(result, value)
	}
	if len(result) > limit {
		result = result[:limit]
	}
	return result, nil
}

type memoryAudit struct{ events []audit.Event }

func (w *memoryAudit) Append(_ context.Context, _ database.Querier, event audit.Event) error {
	w.events = append(w.events, event)
	return nil
}

func TestServiceUploadConfirmDownloadArchive(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	repository := &memoryAssetRepository{values: map[string]Asset{}}
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.Now = func() time.Time { return now }
	auditor := &memoryAudit{}
	service, err := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: auditor, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	service.random = func(value []byte) error {
		for i := range value {
			value[i] = byte(len(repository.values) + i + 1)
		}
		return nil
	}
	principal := identity.Principal{UserID: "user", SystemPermissions: []string{permissionView, permissionUpload, permissionUpdate, permissionArchive}}
	meta := RequestMeta{RequestID: "request", IP: "127.0.0.1", UserAgent: "test"}
	body := "png bytes"
	digest := sha256Text(body)
	upload, err := service.CreateUpload(context.Background(), principal, meta, CreateUploadRequest{Filename: "用户文件.png", MimeType: "image/png", Size: int64(len(body)), SHA256: digest})
	if err != nil {
		t.Fatal(err)
	}
	if upload.Asset.Status != StatusQuarantined || upload.Asset.PreviewKind != PreviewImage || strings.Contains(upload.Asset.ObjectKey, "用户文件") || upload.Upload.Method != "PUT" {
		t.Fatalf("上传申请不正确: %+v", upload)
	}
	if _, err := store.Put(context.Background(), PutObjectRequest{ObjectKey: upload.Asset.ObjectKey, ContentType: upload.Asset.MimeType, Size: upload.Asset.Size, SHA256: upload.Asset.SHA256}, strings.NewReader(body)); err != nil {
		t.Fatal(err)
	}
	confirmed, err := service.Confirm(context.Background(), principal, meta, upload.Asset.ID)
	if err != nil {
		t.Fatal(err)
	}
	if confirmed.Status != StatusAvailable || confirmed.ETag == nil || confirmed.ConfirmedAt == nil {
		t.Fatalf("确认状态错误: %+v", confirmed)
	}
	download, err := service.AdminDownload(context.Background(), principal, meta, upload.Asset.ID)
	if err != nil || download.Method != "GET" {
		t.Fatalf("管理下载失败: %+v %v", download, err)
	}
	if err := service.Archive(context.Background(), principal, meta, upload.Asset.ID); err != nil {
		t.Fatal(err)
	}
	if repository.values[upload.Asset.ID].Status != StatusArchived {
		t.Fatal("素材未归档")
	}
	if len(auditor.events) != 4 {
		t.Fatalf("审计数量错误: %d", len(auditor.events))
	}
	for _, event := range auditor.events {
		if _, exists := event.Changes["object_key"]; exists {
			t.Fatal("审计泄露对象 Key")
		}
		if _, exists := event.Changes["url"]; exists {
			t.Fatal("审计泄露签名 URL")
		}
	}
}

func TestServiceDiscardQuarantinedDeletesObjectAndRecord(t *testing.T) {
	key := "assets/ast_pending/object"
	repository := &memoryAssetRepository{values: map[string]Asset{"ast_pending": {ID: "ast_pending", ObjectKey: key, Filename: "待确认.png", MimeType: "image/png", Size: 3, SHA256: sha256Text("png"), Status: StatusQuarantined}}}
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.objects[key] = memoryObject{data: []byte("png")}
	auditor := &memoryAudit{}
	service, err := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: auditor, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	principal := identity.Principal{UserID: "user", SystemPermissions: []string{permissionArchive}}
	if err := service.DiscardQuarantined(context.Background(), principal, RequestMeta{}, "ast_pending"); err != nil {
		t.Fatal(err)
	}
	if _, exists := repository.values["ast_pending"]; exists {
		t.Fatal("待确认素材记录未删除")
	}
	if _, _, err := store.Get(context.Background(), key); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("待确认素材对象未删除: %v", err)
	}
	if len(auditor.events) != 1 || auditor.events[0].Action != "asset_upload_discarded" {
		t.Fatalf("废弃审计错误: %+v", auditor.events)
	}
	if _, exists := auditor.events[0].Changes["object_key"]; exists {
		t.Fatal("废弃审计泄露对象 Key")
	}
}

func TestServiceDiscardQuarantinedIsIdempotentForMissingObject(t *testing.T) {
	repository := &memoryAssetRepository{values: map[string]Asset{"ast_pending": {ID: "ast_pending", ObjectKey: "assets/ast_pending/missing", Status: StatusQuarantined}}}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: NewMemoryStore(15*time.Minute, 5*time.Minute), Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	err := service.DiscardQuarantined(context.Background(), identity.Principal{SystemPermissions: []string{permissionArchive}}, RequestMeta{}, "ast_pending")
	if err != nil {
		t.Fatalf("对象不存在时废弃应成功: %v", err)
	}
}

func TestServiceDiscardQuarantinedRejectsOtherStatusesAndStoreFailure(t *testing.T) {
	repository := &memoryAssetRepository{values: map[string]Asset{
		"ast_available": {ID: "ast_available", Status: StatusAvailable},
		"ast_pending":   {ID: "ast_pending", ObjectKey: "assets/ast_pending/object", Status: StatusQuarantined},
	}}
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	principal := identity.Principal{SystemPermissions: []string{permissionArchive}}
	assertCode(t, service.DiscardQuarantined(context.Background(), identity.Principal{}, RequestMeta{}, "ast_pending"), "permission_denied")
	assertCode(t, service.DiscardQuarantined(context.Background(), principal, RequestMeta{}, "ast_available"), "asset_not_quarantined")
	store.Failure = ErrStoreUnavailable
	assertCode(t, service.DiscardQuarantined(context.Background(), principal, RequestMeta{}, "ast_pending"), "object_store_unavailable")
	if _, exists := repository.values["ast_pending"]; !exists {
		t.Fatal("对象存储失败时不应删除素材记录")
	}
}

func TestPreviewKindUsesFixedSafeMimeMapping(t *testing.T) {
	tests := map[string]PreviewKind{
		"image/svg+xml": PreviewImage, "application/pdf": PreviewPDF, "video/webm": PreviewVideo,
		"audio/ogg": PreviewAudio, "application/json": PreviewText, "text/html": PreviewNone,
	}
	for mimeType, expected := range tests {
		if actual := PreviewKindFor(mimeType); actual != expected {
			t.Fatalf("PreviewKindFor(%q) = %q, want %q", mimeType, actual, expected)
		}
	}
}

func TestServiceTextPreviewIsSameOriginAndNotAudited(t *testing.T) {
	now := time.Now().UTC()
	body := "preview"
	key := "assets/ast_text/key"
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.Now = func() time.Time { return now }
	store.objects[key] = memoryObject{data: []byte(body), metadata: ObjectMetadata{ObjectKey: key, Size: int64(len(body)), ContentType: "text/plain"}}
	digest := sha256Text(body)
	repository := &memoryAssetRepository{values: map[string]Asset{"ast_text": {ID: "ast_text", ObjectKey: key, Filename: "a.txt", MimeType: "text/plain", Size: int64(len(body)), SHA256: digest, Status: StatusAvailable}}}
	auditor := &memoryAudit{}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: auditor, Config: Config{AllowedMimeTypes: []string{"text/plain"}, MaxSize: 2 << 20, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	service.now = func() time.Time { return now }
	preview, err := service.AdminPreview(context.Background(), identity.Principal{SystemPermissions: []string{permissionView}}, "ast_text", "")
	if err != nil || preview.Body == nil || preview.Signed.URL != "" || len(auditor.events) != 0 {
		t.Fatalf("文本预览结果错误: %+v, events=%d, err=%v", preview, len(auditor.events), err)
	}
	_ = preview.Body.Close()
	cached, err := service.AdminPreview(context.Background(), identity.Principal{SystemPermissions: []string{permissionView}}, "ast_text", `W/"`+digest+`"`)
	if err != nil || !cached.NotModified || cached.Body != nil || cached.ETag != `"`+digest+`"` || store.GetCalls.Load() != 1 {
		t.Fatalf("条件预览未短路对象存储下载: preview=%+v calls=%d err=%v", cached, store.GetCalls.Load(), err)
	}
	repository.values["ast_text"] = Asset{ID: "ast_text", MimeType: "text/plain", Size: maxTextPreviewSize + 1, Status: StatusAvailable}
	_, err = service.AdminPreview(context.Background(), identity.Principal{SystemPermissions: []string{permissionView}}, "ast_text", "")
	assertCode(t, err, "asset_preview_too_large")
	oversized := make([]byte, maxTextPreviewSize+1)
	store.objects[key] = memoryObject{data: oversized, metadata: ObjectMetadata{ObjectKey: key, Size: int64(len(oversized)), ContentType: "text/plain"}}
	repository.values["ast_text"] = Asset{ID: "ast_text", ObjectKey: key, Filename: "a.txt", MimeType: "text/plain", Size: 7, Status: StatusAvailable}
	_, err = service.AdminPreview(context.Background(), identity.Principal{SystemPermissions: []string{permissionView}}, "ast_text", "")
	assertCode(t, err, "asset_preview_too_large")
}

func TestServiceArchiveAllowsCurrentPublishedReferencePerF3(t *testing.T) {
	now := time.Now().UTC()
	repository := &memoryAssetRepository{values: map[string]Asset{"ast_published": {ID: "ast_published", Status: StatusAvailable}}}
	service, err := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: NewMemoryStore(15*time.Minute, 5*time.Minute), Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	if err != nil {
		t.Fatal(err)
	}
	service.now = func() time.Time { return now }
	principal := identity.Principal{UserID: "user", SystemPermissions: []string{permissionArchive}}
	if err = service.Archive(context.Background(), principal, RequestMeta{}, "ast_published"); err != nil {
		t.Fatalf("F3 允许归档当前发布 Revision 引用的素材: %v", err)
	}
	if repository.values["ast_published"].Status != StatusArchived {
		t.Fatal("素材应归档且已发布历史引用继续有效")
	}
}

func TestServiceConfirmChecksActualSHA256(t *testing.T) {
	now := time.Now().UTC()
	repository := &memoryAssetRepository{values: map[string]Asset{"ast_x": {ID: "ast_x", ObjectKey: "assets/ast_x/key", Filename: "a.png", MimeType: "image/png", Size: 3, SHA256: sha256Text("abc"), Status: StatusQuarantined, UploadUntil: now.Add(time.Minute)}}}
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.Now = func() time.Time { return now }
	store.objects["assets/ast_x/key"] = memoryObject{data: []byte("xyz"), metadata: ObjectMetadata{ObjectKey: "assets/ast_x/key", Size: 3, ContentType: "image/png", SHA256: sha256Text("abc"), ETag: "etag"}}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	service.now = func() time.Time { return now }
	_, err := service.Confirm(context.Background(), identity.Principal{UserID: "user", SystemPermissions: []string{permissionUpload}}, RequestMeta{}, "ast_x")
	assertCode(t, err, "asset_metadata_mismatch")
	if repository.values["ast_x"].Status != StatusQuarantined {
		t.Fatal("校验失败不应使素材可用")
	}
}

func TestServiceConfirmChecksActualGETSize(t *testing.T) {
	now := time.Now().UTC()
	value := Asset{ID: "ast_x", ObjectKey: "assets/ast_x/key", Filename: "a.png", MimeType: "image/png", Size: 3, SHA256: sha256Text("abc"), Status: StatusQuarantined, UploadUntil: now.Add(time.Minute)}
	repository := &memoryAssetRepository{values: map[string]Asset{value.ID: value}}
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.objects[value.ObjectKey] = memoryObject{data: []byte("abcx"), metadata: ObjectMetadata{ObjectKey: value.ObjectKey, Size: value.Size, ContentType: value.MimeType, SHA256: value.SHA256, ETag: "etag"}}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	service.now = func() time.Time { return now }
	_, err := service.Confirm(context.Background(), identity.Principal{UserID: "user", SystemPermissions: []string{permissionUpload}}, RequestMeta{}, value.ID)
	assertCode(t, err, "asset_metadata_mismatch")
	if repository.values[value.ID].Status != StatusQuarantined {
		t.Fatal("GET 实际大小不匹配时不应使素材可用")
	}
}

func TestServiceConfirmRechecksLockedUploadExpiry(t *testing.T) {
	now := time.Now().UTC()
	value := Asset{ID: "ast_x", ObjectKey: "assets/ast_x/key", Filename: "a.png", MimeType: "image/png", Size: 3, SHA256: sha256Text("abc"), Status: StatusQuarantined, UploadUntil: now.Add(time.Minute)}
	repository := &memoryAssetRepository{values: map[string]Asset{"ast_x": value}}
	store := NewMemoryStore(15*time.Minute, 5*time.Minute)
	store.objects[value.ObjectKey] = memoryObject{data: []byte("abc"), metadata: ObjectMetadata{ObjectKey: value.ObjectKey, Size: 3, ContentType: value.MimeType, SHA256: value.SHA256, ETag: "etag"}}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: store, Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1024, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	calls := 0
	service.now = func() time.Time {
		calls++
		if calls == 1 {
			return now
		}
		return value.UploadUntil
	}

	_, err := service.Confirm(context.Background(), identity.Principal{UserID: "user", SystemPermissions: []string{permissionUpload}}, RequestMeta{}, value.ID)
	assertCode(t, err, "asset_upload_expired")
	if repository.values[value.ID].Status != StatusQuarantined {
		t.Fatal("锁内发现过期后不得确认素材")
	}
}

func TestServiceValidationAndPermission(t *testing.T) {
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: &memoryAssetRepository{values: map[string]Asset{}}, Store: NewMemoryStore(15*time.Minute, 5*time.Minute), Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 10, UploadTTL: 15 * time.Minute, DownloadTTL: 5 * time.Minute}})
	_, err := service.CreateUpload(context.Background(), identity.Principal{}, RequestMeta{}, CreateUploadRequest{})
	assertCode(t, err, "permission_denied")
	_, err = service.CreateUpload(context.Background(), identity.Principal{SystemPermissions: []string{permissionUpload}}, RequestMeta{}, CreateUploadRequest{Filename: "../secret", MimeType: "application/octet-stream", Size: 11, SHA256: "bad"})
	assertCode(t, err, "file_too_large")
}

func TestServiceListValidatesAndPassesKind(t *testing.T) {
	repository := &memoryAssetRepository{values: map[string]Asset{
		"ast_image": {ID: "ast_image", MimeType: "image/avif", Status: StatusAvailable},
		"ast_audio": {ID: "ast_audio", MimeType: "audio/mpeg", Status: StatusAvailable},
	}}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: repository, Store: NewMemoryStore(time.Minute, time.Minute), Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/avif"}, MaxSize: 1, UploadTTL: time.Minute, DownloadTTL: time.Minute}})
	principal := identity.Principal{SystemPermissions: []string{permissionView}}

	result, err := service.List(context.Background(), principal, ListQuery{Kind: AssetKindImage, Limit: 20})
	if err != nil || len(result.Items) != 1 || result.Items[0].ID != "ast_image" || repository.lastListQuery.Kind != AssetKindImage {
		t.Fatalf("图片 kind 列表结果错误: result=%+v query=%+v err=%v", result, repository.lastListQuery, err)
	}
	_, err = service.List(context.Background(), principal, ListQuery{Kind: AssetKind("document"), Limit: 20})
	assertCode(t, err, "invalid_query")
}

func TestConfirmConcurrencyLimitsAndRelease(t *testing.T) {
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: &memoryAssetRepository{values: map[string]Asset{}}, Store: NewMemoryStore(time.Minute, time.Minute), Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1, UploadTTL: time.Minute, DownloadTTL: time.Minute}})
	releaseA, err := service.acquireConfirm("usr_a")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.acquireConfirm("usr_a"); err == nil {
		t.Fatal("同用户第二个确认应被拒绝")
	}
	releaseB, err := service.acquireConfirm("usr_b")
	if err != nil {
		t.Fatal("同用户失败不应占用全局槽位")
	}
	if _, err = service.acquireConfirm("usr_c"); err == nil {
		t.Fatal("第三个确认应被拒绝")
	}
	releaseA()
	releaseC, err := service.acquireConfirm("usr_c")
	if err != nil {
		t.Fatal("释放后应允许确认")
	}
	releaseB()
	releaseC()
}

func TestConfirmRejectsHistoricalOversizedAssetBeforeStore(t *testing.T) {
	now := time.Now().UTC()
	value := Asset{ID: "ast_large", Size: maxAssetSize + 1, Status: StatusQuarantined, UploadUntil: now.Add(time.Minute)}
	store := NewMemoryStore(time.Minute, time.Minute)
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: &memoryAssetRepository{values: map[string]Asset{value.ID: value}}, Store: store, Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1, UploadTTL: time.Minute, DownloadTTL: time.Minute}})
	service.now = func() time.Time { return now }
	_, err := service.Confirm(context.Background(), identity.Principal{UserID: "usr", SystemPermissions: []string{permissionUpload}}, RequestMeta{}, value.ID)
	assertCode(t, err, "asset_confirmation_too_large")
	if store.GetCalls.Load() != 0 {
		t.Fatal("超大素材不应访问对象正文")
	}
}

func TestConfirmHistoricalOversizedAvailableAssetRemainsIdempotent(t *testing.T) {
	etag := "etag"
	value := Asset{ID: "ast_large", ObjectKey: "assets/ast_large/key", Filename: "large.bin", MimeType: "image/png", Size: maxAssetSize + 1, SHA256: sha256Text("x"), ETag: &etag, Status: StatusAvailable}
	store := NewMemoryStore(time.Minute, time.Minute)
	store.objects[value.ObjectKey] = memoryObject{metadata: ObjectMetadata{ObjectKey: value.ObjectKey, Size: value.Size, ContentType: value.MimeType, SHA256: value.SHA256, ETag: etag}}
	service, _ := NewService(Dependencies{DB: testQuerier{}, Transactor: testTransactor{q: testQuerier{}}, Repository: &memoryAssetRepository{values: map[string]Asset{value.ID: value}}, Store: store, Audit: &memoryAudit{}, Config: Config{AllowedMimeTypes: []string{"image/png"}, MaxSize: 1, UploadTTL: time.Minute, DownloadTTL: time.Minute}})
	confirmed, err := service.Confirm(context.Background(), identity.Principal{UserID: "usr", SystemPermissions: []string{permissionUpload}}, RequestMeta{}, value.ID)
	if err != nil || confirmed.Status != StatusAvailable || store.GetCalls.Load() != 0 {
		t.Fatalf("历史 available 素材确认 = (%+v, %v), GET=%d", confirmed, err, store.GetCalls.Load())
	}
}

func sha256Text(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var app *apperror.Error
	if !errors.As(err, &app) || app.Code != code {
		t.Fatalf("期望错误码 %s，得到 %v", code, err)
	}
}
