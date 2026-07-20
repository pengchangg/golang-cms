package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"cms/internal/platform/httpx"
)

func testHandler(t *testing.T) (*Service, *memoryStore, http.Handler) {
	t.Helper()
	clock := &fakeClock{now: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)}
	store := newMemoryStore()
	hash, err := HashPassword("password", strings.NewReader("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	store.local = User{ID: "usr_local", DisplayName: "Admin", Enabled: true, PasswordHash: hash}
	service := testService(t, store, &fakeOIDC{}, clock)
	module, err := NewModule(service, "https://CMS.EXAMPLE.COM:443", true)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	module.RegisterRoutes(mux)
	return service, store, httpx.RequestID(mux)
}

func loginRequest(handler http.Handler, origin string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/local/login", strings.NewReader(`{"username":"admin","password":"password"}`))
	request.Header.Set("Content-Type", "application/json")
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func TestLocalLoginRequiresExactOriginAndSetsSecureCookie(t *testing.T) {
	_, _, handler := testHandler(t)
	missing := loginRequest(handler, "")
	if missing.Code != http.StatusForbidden || errorCode(t, missing) != "origin_required" {
		t.Fatalf("missing Origin = %d", missing.Code)
	}
	mismatch := loginRequest(handler, "https://evil.example.com")
	if mismatch.Code != http.StatusForbidden || errorCode(t, mismatch) != "origin_mismatch" {
		t.Fatalf("mismatch Origin = %d", mismatch.Code)
	}
	response := loginRequest(handler, "https://cms.example.com")
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteLaxMode || cookies[0].Path != "/api/admin/v1" || cookies[0].Domain != "" {
		t.Fatalf("cookie = %+v", cookies)
	}
}

func TestLocalLoginOverHTTPDoesNotSetSecureCookie(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 18, 1, 0, 0, 0, time.UTC)}
	store := newMemoryStore()
	hash, err := HashPassword("password", strings.NewReader("0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	store.local = User{ID: "usr_local", DisplayName: "Admin", Enabled: true, PasswordHash: hash}
	service := testService(t, store, &fakeOIDC{}, clock)
	module, err := NewModule(service, "http://localhost:8080", true)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	module.RegisterRoutes(mux)
	response := loginRequest(httpx.RequestID(mux), "http://localhost:8080")
	if response.Code != http.StatusOK {
		t.Fatalf("login status = %d, body = %s", response.Code, response.Body.String())
	}
	cookie := response.Result().Cookies()[0]
	if cookie.Secure || !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("cookie = %+v", cookie)
	}
}

func TestLogoutChecksOriginThenSessionThenCSRF(t *testing.T) {
	service, _, handler := testHandler(t)
	login := loginRequest(handler, "https://cms.example.com")
	cookie := login.Result().Cookies()[0]
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/logout", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if errorCode(t, response) != "origin_required" {
		t.Fatal("未优先校验 Origin")
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/logout", nil)
	request.Header.Set("Origin", "https://cms.example.com")
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if errorCode(t, response) != "csrf_token_required" {
		t.Fatalf("csrf error = %s", response.Body.String())
	}
	request = httptest.NewRequest(http.MethodPost, "/api/admin/v1/auth/logout", nil)
	request.Header.Set("Origin", "https://cms.example.com")
	request.Header.Set(CSRFHeader, service.csrf(cookie.Value))
	request.AddCookie(cookie)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("logout status = %d", response.Code)
	}
	cleared := response.Result().Cookies()[0]
	if cleared.MaxAge != -1 || !cleared.HttpOnly || !cleared.Secure || cleared.Path != "/api/admin/v1" {
		t.Fatalf("clear cookie = %+v", cleared)
	}
}

func TestSessionDoesNotCreateCookie(t *testing.T) {
	_, _, handler := testHandler(t)
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/session", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || response.Header().Get("Set-Cookie") != "" || errorCode(t, response) != "session_invalid" {
		t.Fatalf("session response = %d %s", response.Code, response.Body.String())
	}
}

func TestOIDCProviderErrorConsumesState(t *testing.T) {
	_, _, handler := testHandler(t)
	start := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/oidc/start", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, start)
	location := response.Header().Get("Location")
	state := location[strings.LastIndex(location, "=")+1:]
	var binding *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == OIDCCookieName {
			binding = cookie
		}
	}
	if binding == nil || !binding.HttpOnly || !binding.Secure || binding.SameSite != http.SameSiteLaxMode || binding.Path != routePrefix+"/oidc/callback" || binding.MaxAge != 600 {
		t.Fatalf("OIDC binding cookie = %+v", binding)
	}
	callback := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/oidc/callback?error=access_denied&state="+state, nil)
	callback.AddCookie(binding)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, callback)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("callback status = %d", response.Code)
	}
	cleared := response.Result().Cookies()[0]
	if cleared.Name != OIDCCookieName || cleared.MaxAge != -1 || cleared.Path != binding.Path {
		t.Fatalf("OIDC binding cookie 未清理: %+v", cleared)
	}
	replay := httptest.NewRequest(http.MethodGet, "/api/admin/v1/auth/oidc/callback?code=code&state="+state, nil)
	replay.AddCookie(binding)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, replay)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("replay status = %d", response.Code)
	}
}

func TestOIDCCallbackRequiresBrowserBinding(t *testing.T) {
	_, _, handler := testHandler(t)
	start := httptest.NewRequest(http.MethodGet, routePrefix+"/oidc/start", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, start)
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callback := httptest.NewRequest(http.MethodGet, routePrefix+"/oidc/callback?code=code&state="+url.QueryEscape(location.Query().Get("state")), nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, callback)
	if response.Code != http.StatusBadRequest || errorCode(t, response) != "invalid_oidc_callback" {
		t.Fatalf("missing binding response = %d %s", response.Code, response.Body.String())
	}
}

func TestProtectInjectsPrincipal(t *testing.T) {
	service, store, _ := testHandler(t)
	module, err := NewModule(service, "https://cms.example.com", true)
	if err != nil {
		t.Fatal(err)
	}
	login, err := service.LocalLogin(context.Background(), "admin", "password", RequestMeta{RequestID: "req", IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	_ = store
	protected := module.Protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := PrincipalFromContext(r.Context())
		if !ok || principal.UserID != "usr_local" {
			t.Fatalf("principal = %+v, %v", principal, ok)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models", nil)
	request.Header.Set("Origin", "https://cms.example.com")
	request.Header.Set(CSRFHeader, login.Response.CSRFToken)
	request.AddCookie(&http.Cookie{Name: CookieName, Value: login.Raw})
	response := httptest.NewRecorder()
	protected.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func errorCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error: %v", err)
	}
	return body.Error.Code
}
