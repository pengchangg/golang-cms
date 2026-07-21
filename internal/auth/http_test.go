package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
	service := testService(t, store, clock)
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
	service := testService(t, store, clock)
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

func TestCaptchaRequiresOriginAndSetsBrowserBinding(t *testing.T) {
	_, _, handler := testHandler(t)
	start := httptest.NewRequest(http.MethodPost, routePrefix+"/captcha/challenges", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, start)
	if response.Code != http.StatusForbidden || errorCode(t, response) != "origin_required" {
		t.Fatalf("missing origin = %d %s", response.Code, response.Body.String())
	}
	start = httptest.NewRequest(http.MethodPost, routePrefix+"/captcha/challenges", nil)
	start.Header.Set("Origin", "https://cms.example.com")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, start)
	if response.Code != http.StatusCreated {
		t.Fatalf("captcha response = %d %s", response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CaptchaBindingCookie || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode || cookies[0].Path != routePrefix {
		t.Fatalf("captcha binding cookie = %+v", cookies)
	}
}

func TestSMSHTTPFlowSetsSessionAndClearsBinding(t *testing.T) {
	_, store, handler := testHandler(t)
	store.phones["+8613800138000"] = User{ID: "usr_sms", DisplayName: "短信用户", Enabled: true}
	captchaRequest := httptest.NewRequest(http.MethodPost, routePrefix+"/captcha/challenges", nil)
	captchaRequest.Header.Set("Origin", "https://cms.example.com")
	captchaResponse := httptest.NewRecorder()
	handler.ServeHTTP(captchaResponse, captchaRequest)
	var captcha CaptchaResponse
	if err := json.Unmarshal(captchaResponse.Body.Bytes(), &captcha); err != nil {
		t.Fatal(err)
	}
	binding := captchaResponse.Result().Cookies()[0]
	smsRequest := httptest.NewRequest(http.MethodPost, routePrefix+"/sms/challenges", strings.NewReader(`{"phone":"13800138000","captcha_challenge_id":"`+captcha.ChallengeID+`","captcha_x":180,"captcha_y":40}`))
	smsRequest.Header.Set("Origin", "https://cms.example.com")
	smsRequest.AddCookie(binding)
	smsResponse := httptest.NewRecorder()
	handler.ServeHTTP(smsResponse, smsRequest)
	if smsResponse.Code != http.StatusCreated {
		t.Fatalf("sms challenge = %d %s", smsResponse.Code, smsResponse.Body.String())
	}
	var challenge SMSChallengeResponse
	if err := json.Unmarshal(smsResponse.Body.Bytes(), &challenge); err != nil {
		t.Fatal(err)
	}
	verify := httptest.NewRequest(http.MethodPost, routePrefix+"/sms/challenges/"+challenge.ChallengeID+"/verify", strings.NewReader(`{"code":"123456"}`))
	verify.Header.Set("Origin", "https://cms.example.com")
	verify.AddCookie(binding)
	verifyResponse := httptest.NewRecorder()
	handler.ServeHTTP(verifyResponse, verify)
	if verifyResponse.Code != http.StatusOK {
		t.Fatalf("sms verify = %d %s", verifyResponse.Code, verifyResponse.Body.String())
	}
	cookies := verifyResponse.Result().Cookies()
	if len(cookies) != 2 || cookies[0].Name != CookieName || cookies[1].Name != CaptchaBindingCookie || cookies[1].MaxAge != -1 {
		t.Fatalf("verify cookies = %+v", cookies)
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
