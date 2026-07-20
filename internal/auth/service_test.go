package auth

import (
	"bytes"
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakePermissions struct{}

func (fakePermissions) Permissions(context.Context, string) ([]string, []identity.ModelPermissions, error) {
	return []string{"models.view", "models.view"}, []identity.ModelPermissions{{ModelID: "b", Permissions: []string{"content.view"}}, {ModelID: "a", Permissions: []string{"content.update", "content.update"}}}, nil
}

type fakeModels struct{}

func (fakeModels) ActiveModelSummaries(_ context.Context, ids []string) ([]SessionModelSummary, error) {
	result := []SessionModelSummary{}
	for _, id := range ids {
		if id == "a" {
			result = append(result, SessionModelSummary{ID: "a", Key: "articles", DisplayName: "文章"})
		}
	}
	return result, nil
}

type fakeOIDC struct{ nonce, verifier, challenge string }

func (f *fakeOIDC) AuthorizationURL(state, nonce, challenge string) string {
	f.nonce, f.challenge = nonce, challenge
	return "https://id.example.com/auth?state=" + url.QueryEscape(state)
}
func (f *fakeOIDC) Exchange(_ context.Context, code, verifier, nonce string) (OIDCIdentity, error) {
	f.verifier = verifier
	if code != "code" || nonce != f.nonce {
		return OIDCIdentity{}, ErrNotFound
	}
	return OIDCIdentity{Issuer: "https://id.example.com", Subject: "subject", DisplayName: "OIDC User"}, nil
}

type memoryStore struct {
	local           User
	states          map[string]LoginState
	sessions        map[string]Session
	failures        []audit.Event
	failureErr      error
	failureCtxErr   error
	failureDeadline bool
}

func newMemoryStore() *memoryStore {
	return &memoryStore{states: map[string]LoginState{}, sessions: map[string]Session{}}
}
func key(value []byte) string { return string(value) }
func (m *memoryStore) SaveLoginState(_ context.Context, state LoginState) error {
	m.states[key(state.Hash)] = state
	return nil
}
func (m *memoryStore) ConsumeLoginState(_ context.Context, hash, bindingHash []byte, now time.Time) (LoginState, error) {
	state, ok := m.states[key(hash)]
	if !ok || !now.Before(state.ExpiresAt) || !bytes.Equal(state.BindingHash, bindingHash) {
		return LoginState{}, ErrNotFound
	}
	delete(m.states, key(hash))
	return state, nil
}
func (m *memoryStore) DeleteExpiredLoginStates(_ context.Context, now time.Time, limit int) error {
	for hash, state := range m.states {
		if limit == 0 {
			break
		}
		if !now.Before(state.ExpiresAt) {
			delete(m.states, hash)
			limit--
		}
	}
	return nil
}
func (m *memoryStore) FindLocalUser(_ context.Context, username string) (User, error) {
	if username != "admin" {
		return User{}, ErrNotFound
	}
	return m.local, nil
}
func (m *memoryStore) CompleteOIDCLogin(_ context.Context, value OIDCIdentity, id string, _ time.Time, session NewSession, _ audit.Event) (User, error) {
	user := User{ID: id, DisplayName: value.DisplayName, Email: value.Email, Enabled: true}
	session.UserID = id
	m.sessions[key(session.Hash)] = Session{UserID: id, DisplayName: value.DisplayName, Enabled: true, AuthMethod: session.AuthMethod, IdleExpiresAt: session.IdleExpiresAt, ExpiresAt: session.ExpiresAt}
	return user, nil
}
func (m *memoryStore) CreateSession(_ context.Context, value NewSession, _ audit.Event) error {
	m.sessions[key(value.Hash)] = Session{UserID: value.UserID, DisplayName: m.local.DisplayName, Enabled: true, AuthMethod: value.AuthMethod, IdleExpiresAt: value.IdleExpiresAt, ExpiresAt: value.ExpiresAt}
	if value.AuthMethod == identity.AuthMethodOIDC {
		session := m.sessions[key(value.Hash)]
		session.DisplayName = "OIDC User"
		m.sessions[key(value.Hash)] = session
	}
	return nil
}
func (m *memoryStore) Session(_ context.Context, hash []byte) (Session, error) {
	value, ok := m.sessions[key(hash)]
	if !ok {
		return Session{}, ErrNotFound
	}
	return value, nil
}
func (m *memoryStore) TouchSession(_ context.Context, hash []byte, _ time.Time, idle time.Time) error {
	value := m.sessions[key(hash)]
	value.IdleExpiresAt = idle
	m.sessions[key(hash)] = value
	return nil
}
func (m *memoryStore) RevokeSession(_ context.Context, hash []byte, _ time.Time, _ audit.Event) error {
	if _, ok := m.sessions[key(hash)]; !ok {
		return ErrNotFound
	}
	delete(m.sessions, key(hash))
	return nil
}
func (m *memoryStore) AppendFailure(ctx context.Context, event audit.Event) error {
	m.failureCtxErr = ctx.Err()
	_, m.failureDeadline = ctx.Deadline()
	m.failures = append(m.failures, event)
	return m.failureErr
}
func (m *memoryStore) UpsertEmergencyAdmin(context.Context, string, string, string, string, time.Time, audit.Event) error {
	for hash, session := range m.sessions {
		if session.UserID == m.local.ID {
			delete(m.sessions, hash)
		}
	}
	return nil
}

func testService(t *testing.T, store *memoryStore, oidcClient OIDCClient, clock *fakeClock) *Service {
	t.Helper()
	service, err := NewService(store, fakePermissions{}, fakeModels{}, oidcClient, clock, bytes.NewReader(bytes.Repeat([]byte{7}, 4096)), "01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestLocalLoginSessionTimeoutAndDisabledUser(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 18, 1, 2, 3, 0, time.UTC)}
	store := newMemoryStore()
	hash, err := HashPassword("correct horse", bytes.NewReader(bytes.Repeat([]byte{1}, 16)))
	if err != nil {
		t.Fatal(err)
	}
	store.local = User{ID: "usr_local", DisplayName: "Admin", Enabled: true, PasswordHash: hash}
	service := testService(t, store, nil, clock)
	meta := RequestMeta{RequestID: "req", IP: "127.0.0.1", UserAgent: "test"}
	result, err := service.LocalLogin(context.Background(), "admin", "correct horse", meta)
	if err != nil {
		t.Fatalf("LocalLogin() error = %v", err)
	}
	if result.Response.ExpiresAt.Sub(clock.now) != AbsoluteExpiry || result.Response.IdleExpiresAt.Sub(clock.now) != IdleTimeout {
		t.Fatal("会话超时不符合冻结契约")
	}
	if got := result.Response.Principal.SystemPermissions; len(got) != 1 || got[0] != "models.view" {
		t.Fatalf("权限未规范化: %v", got)
	}
	if len(result.Response.ContentModels) != 1 || result.Response.ContentModels[0].DisplayName != "文章" {
		t.Fatalf("会话模型摘要错误: %v", result.Response.ContentModels)
	}
	clock.now = clock.now.Add(10 * time.Minute)
	response, err := service.CurrentSession(context.Background(), result.Raw)
	if err != nil || response.IdleExpiresAt.Sub(clock.now) != IdleTimeout {
		t.Fatalf("会话刷新失败: %+v %v", response, err)
	}
	sessionHash := service.digest("session", result.Raw)
	value := store.sessions[key(sessionHash)]
	value.Enabled = false
	store.sessions[key(sessionHash)] = value
	if _, err := service.CurrentSession(context.Background(), result.Raw); err == nil {
		t.Fatal("禁用用户的会话仍然有效")
	}
}

func TestLocalLoginRejectsPasswordAndAuditsFailure(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	hash, _ := HashPassword("right", bytes.NewReader(bytes.Repeat([]byte{2}, 16)))
	store.local = User{ID: "usr_local", Enabled: true, PasswordHash: hash}
	service := testService(t, store, nil, clock)
	_, err := service.LocalLogin(context.Background(), "admin", "wrong", RequestMeta{RequestID: "req", IP: "127.0.0.1"})
	if err == nil || len(store.failures) != 1 || store.failures[0].FailureCode == nil || *store.failures[0].FailureCode != "invalid_credentials" {
		t.Fatalf("失败结果不正确: %v %+v", err, store.failures)
	}
}

func TestOIDCUsesPKCEAndConsumesStateOnce(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	provider := &fakeOIDC{}
	service := testService(t, store, provider, clock)
	location, binding, err := service.StartOIDC(context.Background(), "/models?status=active", RequestMeta{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(location)
	state := parsed.Query().Get("state")
	result, returnTo, err := service.CompleteOIDC(context.Background(), "code", state, binding, RequestMeta{RequestID: "req", IP: "127.0.0.1"})
	if err != nil || result.Raw == "" || returnTo != "/models?status=active" || provider.verifier == "" || provider.challenge == "" {
		t.Fatalf("OIDC 完成失败: %v", err)
	}
	if _, _, err := service.CompleteOIDC(context.Background(), "code", state, binding, RequestMeta{RequestID: "req", IP: "127.0.0.1"}); err == nil {
		t.Fatal("OIDC state 被重放")
	}
}

func TestOIDCWrongBrowserBindingDoesNotConsumeState(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	provider := &fakeOIDC{}
	service := testService(t, store, provider, clock)
	location, binding, err := service.StartOIDC(context.Background(), "", RequestMeta{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(location)
	state := parsed.Query().Get("state")
	meta := RequestMeta{RequestID: "req", IP: "127.0.0.1"}
	if _, _, err := service.CompleteOIDC(context.Background(), "code", state, "wrong-binding", meta); err == nil {
		t.Fatal("错误浏览器绑定被接受")
	}
	if _, _, err := service.CompleteOIDC(context.Background(), "code", state, binding, meta); err != nil {
		t.Fatalf("错误绑定抢先消费了合法 state: %v", err)
	}
}

func TestStartOIDCRejectsExternalReturnTo(t *testing.T) {
	service := testService(t, newMemoryStore(), &fakeOIDC{}, &fakeClock{now: time.Now().UTC()})
	for _, value := range []string{"https://evil.example.com", "//evil.example.com", "/\\evil"} {
		if _, _, err := service.StartOIDC(context.Background(), value, RequestMeta{IP: "127.0.0.1"}); err == nil {
			t.Fatalf("接受了 return_to %q", value)
		}
	}
}

func TestResetEmergencyAdminRevokesExistingSessions(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	store.local = User{ID: "usr_local", DisplayName: "Admin", Enabled: true}
	service := testService(t, store, nil, clock)
	raw := "old-cookie"
	store.sessions[key(service.digest("session", raw))] = Session{UserID: store.local.ID, Enabled: true, IdleExpiresAt: clock.now.Add(time.Hour), ExpiresAt: clock.now.Add(time.Hour)}
	if err := service.ResetEmergencyAdmin(context.Background(), "", "admin", "Admin", "new-password", RequestMeta{RequestID: "req", IP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.CurrentSession(context.Background(), raw); err == nil {
		t.Fatal("重置后旧 cookie 仍可恢复会话")
	}
}

func TestLocalLoginRateLimitAndBoundedKeys(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	hash, _ := HashPassword("password", bytes.NewReader(bytes.Repeat([]byte{3}, 16)))
	store.local = User{ID: "usr_local", Enabled: true, PasswordHash: hash}
	service := testService(t, store, nil, clock)
	service.localIP = newRateLimiter(1, 2, time.Minute)
	service.localUser = newRateLimiter(1, 2, time.Minute)
	meta := RequestMeta{RequestID: "req", IP: "127.0.0.1"}
	if _, err := service.LocalLogin(context.Background(), "admin", "password", meta); err != nil {
		t.Fatal(err)
	}
	if _, err := service.LocalLogin(context.Background(), "admin", "password", meta); err == nil {
		t.Fatal("本地登录未限速")
	}
	limiter := newRateLimiter(1, 2, time.Minute)
	if !limiter.allow("a", clock.now) || !limiter.allow("b", clock.now) || limiter.allow("c", clock.now) || len(limiter.entries) != 2 {
		t.Fatalf("限速键表未保持有界: %d", len(limiter.entries))
	}
}

func TestStartOIDCCleansExpiredStatesWithBound(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	store.states["expired"] = LoginState{ExpiresAt: clock.now.Add(-time.Second)}
	service := testService(t, store, &fakeOIDC{}, clock)
	if _, _, err := service.StartOIDC(context.Background(), "", RequestMeta{IP: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := store.states["expired"]; ok {
		t.Fatal("过期 OIDC state 未删除")
	}
}

func TestFailureAuditSurvivesCancellationAndPropagatesError(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	service := testService(t, store, nil, clock)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	store.failureErr = errors.New("audit unavailable")
	_, err := service.LocalLogin(ctx, "missing", "password", RequestMeta{RequestID: "req", IP: "127.0.0.1"})
	if err == nil || !strings.Contains(err.Error(), "audit unavailable") {
		t.Fatalf("审计错误被静默丢弃: %v", err)
	}
	if store.failureCtxErr != nil || !store.failureDeadline {
		t.Fatalf("失败审计上下文错误: err=%v deadline=%v", store.failureCtxErr, store.failureDeadline)
	}
}
