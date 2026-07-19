package client

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/platform/httpx"
)

type directTx struct{}

func (directTx) WithinTx(_ context.Context, _ *sql.TxOptions, fn func(database.Querier) error) error {
	return fn(nil)
}

type allow struct{}

func (allow) RequireSystemPermission(_ context.Context, p identity.Principal, required string) error {
	for _, value := range p.SystemPermissions {
		if value == required {
			return nil
		}
	}
	return appError(apperror.KindPermissionDenied, "permission_denied", "权限不足")
}

type auditLog struct{ events []audit.Event }

func (a *auditLog) Append(_ context.Context, _ database.Querier, event audit.Event) error {
	a.events = append(a.events, event)
	return nil
}

type memoryRepository struct {
	keys     map[string]APIKey
	models   map[string]bool
	touches  int
	touchErr error
}

func newMemoryRepository() *memoryRepository {
	return &memoryRepository{keys: map[string]APIKey{}, models: map[string]bool{"model-a": true, "model-b": true}}
}
func (m *memoryRepository) List(_ context.Context, _ database.Querier, status APIKeyStatus, limit int, cursor *APIKeyCursor, now time.Time) ([]APIKey, error) {
	result := []APIKey{}
	for _, key := range m.keys {
		key.Status = statusAt(key, now)
		if status != "" && key.Status != status {
			continue
		}
		result = append(result, publicKey(key))
		if len(result) == limit {
			break
		}
	}
	return result, nil
}
func (m *memoryRepository) Get(_ context.Context, _ database.Querier, id string, _ bool, now time.Time) (APIKey, error) {
	key, ok := m.keys[id]
	if !ok {
		return APIKey{}, appError(apperror.KindNotFound, "resource_not_found", "API Key 不存在")
	}
	key.Status = statusAt(key, now)
	return cloneKey(key), nil
}
func (m *memoryRepository) FindByPrefix(_ context.Context, _ database.Querier, prefix string, now time.Time) (APIKey, error) {
	for _, key := range m.keys {
		if key.Prefix == prefix {
			key.Status = statusAt(key, now)
			return cloneKey(key), nil
		}
	}
	return APIKey{}, sql.ErrNoRows
}
func (m *memoryRepository) ValidateActiveModels(_ context.Context, _ database.Querier, ids []string) error {
	for _, id := range ids {
		if !m.models[id] {
			return appError(apperror.KindNotFound, "resource_not_found", "模型不存在")
		}
	}
	return nil
}
func (m *memoryRepository) Create(_ context.Context, _ database.Querier, key APIKey) error {
	m.keys[key.ID] = cloneKey(key)
	return nil
}
func (m *memoryRepository) Revoke(_ context.Context, _ database.Querier, id, replacement string, now time.Time) error {
	key := m.keys[id]
	key.RevokedAt = &now
	key.Status = APIKeyRevoked
	if replacement != "" {
		key.ReplacedByID = &replacement
	}
	m.keys[id] = key
	return nil
}
func (m *memoryRepository) TouchLastUsed(_ context.Context, _ database.Querier, id string, now time.Time) error {
	m.touches++
	if m.touchErr != nil {
		return m.touchErr
	}
	key := m.keys[id]
	if key.LastUsedAt == nil || !key.LastUsedAt.After(now.Add(-5*time.Minute)) {
		key.LastUsedAt = &now
		m.keys[id] = key
	}
	return nil
}
func cloneKey(key APIKey) APIKey {
	key.ModelIDs = append([]string(nil), key.ModelIDs...)
	key.Salt = append([]byte(nil), key.Salt...)
	key.Hash = append([]byte(nil), key.Hash...)
	return key
}
func publicKey(key APIKey) APIKey { key = cloneKey(key); key.Salt = nil; key.Hash = nil; return key }
func statusAt(key APIKey, now time.Time) APIKeyStatus {
	if key.RevokedAt != nil {
		return APIKeyRevoked
	}
	if key.ExpiresAt != nil && !now.Before(*key.ExpiresAt) {
		return APIKeyExpired
	}
	return APIKeyActive
}

func testService() (*Service, *memoryRepository, *auditLog) {
	repository := newMemoryRepository()
	audits := &auditLog{}
	service := NewService(Dependencies{Transactor: directTx{}, Repository: repository, Authorizer: allow{}, Audit: audits})
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	counter := byte(0)
	service.random = func(value []byte) error {
		counter++
		for i := range value {
			value[i] = counter
		}
		return nil
	}
	ids := 0
	service.newID = func(prefix string) (string, error) { ids++; return prefix + string(rune('0'+ids)), nil }
	return service, repository, audits
}
func adminPrincipal() identity.Principal {
	return identity.Principal{UserID: "user-1", SystemPermissions: []string{"api_keys.create", "api_keys.revoke", "api_keys.view"}}
}

func TestAPIKeyLifecycleFormatHashRotationAndAudit(t *testing.T) {
	service, repository, audits := testService()
	expires := service.now().Add(time.Hour)
	created, err := service.Create(context.Background(), adminPrincipal(), RequestMeta{"req-1", "192.0.2.1", "test"}, CreateAPIKeyRequest{Name: "  public  ", ModelIDs: []string{"model-b", "model-a", "model-a"}, ExpiresAt: &expires})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Key, "cmsk_") || len(created.Prefix) != 12 || created.Name != "public" || len(created.ModelIDs) != 2 {
		t.Fatalf("created=%#v", created)
	}
	stored := repository.keys[created.ID]
	if len(stored.Salt) != 16 || len(stored.Hash) != sha256.Size || strings.Contains(string(stored.Hash), created.Key) {
		t.Fatalf("敏感存储错误: %#v", stored)
	}
	authenticated, err := service.Authenticate(context.Background(), created.Key)
	if err != nil || authenticated.ID != created.ID || repository.touches != 1 {
		t.Fatalf("auth=%#v err=%v touches=%d", authenticated, err, repository.touches)
	}
	repository.touchErr = errors.New("write unavailable")
	if _, err = service.Authenticate(context.Background(), created.Key); err != nil {
		t.Fatalf("last_used 写入失败影响鉴权: %v", err)
	}
	if repository.touches != 1 {
		t.Fatalf("五分钟内重复更新 last_used_at: %d", repository.touches)
	}
	rotated, err := service.Rotate(context.Background(), adminPrincipal(), RequestMeta{"req-2", "192.0.2.1", "test"}, created.ID, RotateAPIKeyRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if rotated.Key == created.Key || repository.keys[created.ID].ReplacedByID == nil || *repository.keys[created.ID].ReplacedByID != rotated.ID {
		t.Fatalf("rotation old=%#v new=%#v", repository.keys[created.ID], rotated)
	}
	assertCode(t, authenticateError(service, created.Key), "api_key_revoked")
	if len(audits.events) != 2 || audits.events[0].Action != "api_key_created" || audits.events[1].Action != "api_key_rotated" {
		t.Fatalf("audits=%#v", audits.events)
	}
	encoded, _ := json.Marshal(audits.events)
	if strings.Contains(string(encoded), created.Key) || strings.Contains(string(encoded), "secret_hash") || strings.Contains(string(encoded), "salt") {
		t.Fatalf("审计泄露 secret: %s", encoded)
	}
}

func TestAPIKeyValidationPermissionExpirationAndRevocation(t *testing.T) {
	service, repository, _ := testService()
	past := service.now().Add(-time.Second)
	_, err := service.Create(context.Background(), adminPrincipal(), RequestMeta{}, CreateAPIKeyRequest{Name: " ", ModelIDs: nil, ExpiresAt: &past})
	assertCode(t, err, "validation_failed")
	_, err = service.Create(context.Background(), identity.Principal{}, RequestMeta{}, CreateAPIKeyRequest{Name: "key", ModelIDs: []string{"model-a"}})
	assertCode(t, err, "permission_denied")
	created, err := service.Create(context.Background(), adminPrincipal(), RequestMeta{"req", "ip", "ua"}, CreateAPIKeyRequest{Name: "key", ModelIDs: []string{"model-a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.Revoke(context.Background(), adminPrincipal(), RequestMeta{"req", "ip", "ua"}, created.ID); err != nil {
		t.Fatal(err)
	}
	assertCode(t, service.Revoke(context.Background(), adminPrincipal(), RequestMeta{"req", "ip", "ua"}, created.ID), "api_key_already_revoked")
	assertCode(t, authenticateError(service, "bad"), "invalid_api_key")
	delete(repository.keys, created.ID)
	assertCode(t, authenticateError(service, created.Key), "invalid_api_key")
}

func TestParseRawKeyAcceptsURLSafeUnderscoreInSecret(t *testing.T) {
	secret := base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0xfb}, 32))
	if !strings.Contains(secret, "_") {
		t.Fatalf("测试 secret 必须包含下划线: %s", secret)
	}
	prefix, decoded, err := parseRawKey("cmsk_abcdefgh2345_" + secret)
	if err != nil || prefix != "abcdefgh2345" || !bytes.Equal(decoded, bytes.Repeat([]byte{0xfb}, 32)) {
		t.Fatalf("prefix=%q decoded=%x err=%v", prefix, decoded, err)
	}
}

func authenticateError(service *Service, key string) error {
	_, err := service.Authenticate(context.Background(), key)
	return err
}
func assertCode(t *testing.T, err error, code string) {
	t.Helper()
	var target *apperror.Error
	if !errors.As(err, &target) || target.Code != code {
		t.Fatalf("error=%v want code=%s", err, code)
	}
}

type fakeReader struct {
	models []content.PublishedModel
	entry  content.PublishedEntry
	query  content.PublishedQuery
	scope  []string
	calls  int
}

func (f *fakeReader) ListPublishedModels(_ context.Context, scope []string) ([]content.PublishedModel, error) {
	f.calls++
	f.scope = scope
	return f.models, nil
}
func (f *fakeReader) GetPublishedModel(_ context.Context, _ string, scope []string) (content.PublishedModel, error) {
	f.calls++
	f.scope = scope
	return f.models[0], nil
}
func (f *fakeReader) ListPublishedEntries(_ context.Context, _ string, scope []string, q content.PublishedQuery) (content.PublishedEntryPage, error) {
	f.calls++
	f.scope = scope
	f.query = q
	return content.PublishedEntryPage{Items: []content.PublishedEntry{f.entry}}, nil
}
func (f *fakeReader) GetPublishedEntry(_ context.Context, _, _ string, scope []string, expand []string) (content.PublishedEntry, error) {
	f.calls++
	f.scope = scope
	f.query.Expand = expand
	return f.entry, nil
}

func TestContentHTTPFourGETQueryBearerETagAnd304(t *testing.T) {
	service, repository, _ := testService()
	created, err := service.Create(context.Background(), adminPrincipal(), RequestMeta{"req", "ip", "ua"}, CreateAPIKeyRequest{Name: "key", ModelIDs: []string{"model-a"}})
	if err != nil {
		t.Fatal(err)
	}
	model := content.PublishedModel{ID: "model-a", Key: "articles", DisplayName: "Articles", Description: "", UpdatedAt: service.now(), Fields: []content.PublishedField{}}
	entry := content.PublishedEntry{ID: "entry-1", ModelID: "model-a", ModelKey: "articles", RevisionID: "revision-1", RevisionNumber: 1, Content: json.RawMessage(`{"z":1,"a":2}`), Expanded: map[string]any{}, PublishedAt: service.now(), UpdatedAt: service.now()}
	reader := &fakeReader{models: []content.PublishedModel{model}, entry: entry}
	mux := http.NewServeMux()
	NewContentHandler(service, reader).RegisterRoutes(mux)
	handler := httpx.RequestID(mux)
	paths := []string{"/api/content/v1/models", "/api/content/v1/models/articles", "/api/content/v1/models/articles/entries?limit=10&filter=%7B%22score%22%3A%7B%22gte%22%3A1%7D%7D&relation_filter=%7B%22author%22%3A%7B%22contains%22%3A%22entry-2%22%7D%7D&sort=-published_at&expand=author", "/api/content/v1/models/articles/entries/entry-1?expand=author"}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+created.Key)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK || response.Header().Get("ETag") == "" || response.Header().Get("Cache-Control") != "private, no-cache" {
			t.Fatalf("%s status=%d headers=%v body=%s", path, response.Code, response.Header(), response.Body.String())
		}
		etag := response.Header().Get("ETag")
		conditional := httptest.NewRequest(http.MethodGet, path, nil)
		conditional.Header.Set("Authorization", "Bearer "+created.Key)
		conditional.Header.Set("If-None-Match", "W/"+etag)
		cached := httptest.NewRecorder()
		handler.ServeHTTP(cached, conditional)
		if cached.Code != http.StatusNotModified || cached.Body.Len() != 0 || cached.Header().Get("ETag") != etag {
			t.Fatalf("304 status=%d body=%q headers=%v", cached.Code, cached.Body.String(), cached.Header())
		}
	}
	if reader.query.Limit != 10 || len(reader.query.Filters) != 1 || len(reader.query.RelationFilters) != 1 || len(reader.scope) != 1 || reader.scope[0] != "model-a" {
		t.Fatalf("query=%#v scope=%v", reader.query, reader.scope)
	}
	repository.touchErr = nil
}

func TestContentHTTPAuthenticatesAndValidatesBeforeReader(t *testing.T) {
	service, _, _ := testService()
	reader := &fakeReader{}
	mux := http.NewServeMux()
	NewContentHandler(service, reader).RegisterRoutes(mux)
	handler := httpx.RequestID(mux)
	for _, test := range []struct {
		header, path, code string
		status             int
	}{{"", "/api/content/v1/models", "api_key_required", 401}, {"Basic value", "/api/content/v1/models", "invalid_api_key", 401}, {"Bearer bad", "/api/content/v1/models", "invalid_api_key", 401}, {"Bearer bad", "/api/content/v1/models/articles/entries?sort=,id", "invalid_api_key", 401}} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.header != "" {
			request.Header.Set("Authorization", test.header)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.status || !strings.Contains(response.Body.String(), test.code) {
			t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if reader.calls != 0 {
		t.Fatalf("reader calls=%d", reader.calls)
	}
}

func TestContentHTTPRejectsInvalidQueryAfterAuthentication(t *testing.T) {
	service, _, _ := testService()
	created, err := service.Create(context.Background(), adminPrincipal(), RequestMeta{"req", "ip", "ua"}, CreateAPIKeyRequest{Name: "key", ModelIDs: []string{"model-a"}})
	if err != nil {
		t.Fatal(err)
	}
	reader := &fakeReader{}
	mux := http.NewServeMux()
	NewContentHandler(service, reader).RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/content/v1/models/articles/entries?sort=,id", nil)
	request.Header.Set("Authorization", "Bearer "+created.Key)
	response := httptest.NewRecorder()
	httpx.RequestID(mux).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "invalid_query") || reader.calls != 0 {
		t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), reader.calls)
	}
}

func TestAdminHTTPRequiresExpiresAtAndDoesNotReturnSensitiveMaterial(t *testing.T) {
	service, _, _ := testService()
	mux := http.NewServeMux()
	NewAdminHandler(service, func(*http.Request) (identity.Principal, error) { return adminPrincipal(), nil }).RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/api-keys", strings.NewReader(`{"name":"key","model_ids":["model-a"]}`))
	response := httptest.NewRecorder()
	httpx.RequestID(mux).ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "validation_failed") {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/v1/api-keys", strings.NewReader(`{"name":"key","model_ids":["model-a"],"expires_at":null}`))
	request.RemoteAddr = "192.0.2.1:1234"
	response = httptest.NewRecorder()
	httpx.RequestID(mux).ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" || strings.Contains(response.Body.String(), "salt") || strings.Contains(response.Body.String(), "hash") {
		t.Fatalf("status=%d headers=%v body=%s", response.Code, response.Header(), response.Body.String())
	}
}
