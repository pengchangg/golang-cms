package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

const routePrefix = "/api/admin/v1/auth"

type Module struct {
	service           *Service
	origin            string
	secureCookies     bool
	localLoginEnabled bool
}

type principalContextKey struct{}

// Protect 为已认证管理路由统一执行会话、Origin 和 CSRF 校验。
func (m *Module) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		unsafe := r.Method == http.MethodPost || r.Method == http.MethodPut || r.Method == http.MethodPatch || r.Method == http.MethodDelete
		if unsafe {
			if err := m.checkOrigin(r); err != nil {
				httpx.WriteError(w, r, err)
				return
			}
		}
		raw, err := sessionCookie(r)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		response, err := m.service.CurrentSession(r.Context(), raw)
		if err != nil {
			httpx.WriteError(w, r, err)
			return
		}
		if unsafe {
			if err := m.service.CheckCSRF(raw, r.Header.Get(CSRFHeader)); err != nil {
				httpx.WriteError(w, r, err)
				return
			}
		}
		ctx := context.WithValue(r.Context(), principalContextKey{}, response.Principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func PrincipalFromContext(ctx context.Context) (identity.Principal, bool) {
	principal, ok := ctx.Value(principalContextKey{}).(identity.Principal)
	return principal, ok
}

func NewModule(service *Service, baseURL string, localLoginEnabled bool) (*Module, error) {
	parsed, err := url.Parse(baseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("APP_BASE_URL 不合法")
	}
	origin, err := canonicalOrigin(parsed)
	if err != nil {
		return nil, err
	}
	return &Module{service: service, origin: origin, secureCookies: strings.EqualFold(parsed.Scheme, "https"), localLoginEnabled: localLoginEnabled}, nil
}

func (m *Module) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET "+routePrefix+"/oidc/start", m.startOIDC)
	mux.HandleFunc("GET "+routePrefix+"/oidc/callback", m.completeOIDC)
	mux.HandleFunc("POST "+routePrefix+"/local/login", m.localLogin)
	mux.HandleFunc("GET "+routePrefix+"/session", m.session)
	mux.HandleFunc("POST "+routePrefix+"/logout", m.logout)
}

func (m *Module) startOIDC(w http.ResponseWriter, r *http.Request) {
	returnTo := r.URL.Query().Get("return_to")
	location, binding, err := m.service.StartOIDC(r.Context(), returnTo, requestMeta(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m.setOIDCCookie(w, binding)
	http.Redirect(w, r, location, http.StatusFound)
}

func (m *Module) completeOIDC(w http.ResponseWriter, r *http.Request) {
	binding := ""
	if cookie, err := r.Cookie(OIDCCookieName); err == nil {
		binding = cookie.Value
	}
	m.clearOIDCCookie(w)
	query := r.URL.Query()
	for key, values := range query {
		if key != "code" && key != "state" && key != "error" && key != "error_description" {
			httpx.WriteError(w, r, appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调参数无效"))
			return
		}
		if len(values) != 1 {
			httpx.WriteError(w, r, appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调参数无效"))
			return
		}
	}
	if query.Get("error") != "" {
		httpx.WriteError(w, r, m.service.RejectOIDCCallback(r.Context(), query.Get("state"), binding, requestMeta(r)))
		return
	}
	result, returnTo, err := m.service.CompleteOIDC(r.Context(), query.Get("code"), query.Get("state"), binding, requestMeta(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m.setCookie(w, result.Raw, result.Response.ExpiresAt)
	if returnTo == "" {
		returnTo = "/"
	}
	w.Header().Set("Location", returnTo)
	w.WriteHeader(http.StatusSeeOther)
}

func (m *Module) localLogin(w http.ResponseWriter, r *http.Request) {
	if err := m.checkOrigin(r); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if !m.localLoginEnabled {
		httpx.WriteError(w, r, appError(apperror.KindPermissionDenied, "local_login_disabled", "本地应急网页登录已关闭"))
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil || body.Username == "" || body.Password == "" || len(body.Username) > 128 || len(body.Password) > 1024 {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": "", "code": "invalid_body", "message": "登录请求格式无效"}}})
		return
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": "", "code": "invalid_body", "message": "登录请求格式无效"}}})
		return
	}
	result, err := m.service.LocalLogin(r.Context(), body.Username, body.Password, requestMeta(r))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m.setCookie(w, result.Raw, result.Response.ExpiresAt)
	writeJSON(w, http.StatusOK, result.Response)
}

func (m *Module) session(w http.ResponseWriter, r *http.Request) {
	raw, err := sessionCookie(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	response, err := m.service.CurrentSession(r.Context(), raw)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, response)
}

func (m *Module) logout(w http.ResponseWriter, r *http.Request) {
	if err := m.checkOrigin(r); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	raw, err := sessionCookie(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if _, err := m.service.CurrentSession(r.Context(), raw); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := m.service.CheckCSRF(raw, r.Header.Get(CSRFHeader)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if err := m.service.Logout(r.Context(), raw, requestMeta(r)); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	m.clearCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (m *Module) checkOrigin(r *http.Request) error {
	value := r.Header.Get("Origin")
	if value == "" {
		return appError(apperror.KindPermissionDenied, "origin_required", "缺少 Origin 请求头")
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return appError(apperror.KindPermissionDenied, "origin_mismatch", "请求 Origin 不匹配")
	}
	origin, err := canonicalOrigin(parsed)
	if err != nil || origin != m.origin {
		return appError(apperror.KindPermissionDenied, "origin_mismatch", "请求 Origin 不匹配")
	}
	return nil
}

func canonicalOrigin(value *url.URL) (string, error) {
	scheme := strings.ToLower(value.Scheme)
	if scheme != "http" && scheme != "https" || value.User != nil || value.Host == "" || value.Path != "" && value.Path != "/" || value.RawQuery != "" || value.Fragment != "" {
		return "", errors.New("origin 不合法")
	}
	host := strings.ToLower(value.Hostname())
	port := value.Port()
	if port == "" {
		if scheme == "https" {
			port = "443"
		} else {
			port = "80"
		}
	}
	if net.ParseIP(host) != nil && strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	return scheme + "://" + net.JoinHostPort(strings.Trim(host, "[]"), port), nil
}

func (m *Module) setCookie(w http.ResponseWriter, raw string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Value: raw, Path: "/api/admin/v1", HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, Expires: expires.UTC()})
}

func (m *Module) clearCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: CookieName, Path: "/api/admin/v1", HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}

func (m *Module) setOIDCCookie(w http.ResponseWriter, binding string) {
	http.SetCookie(w, &http.Cookie{Name: OIDCCookieName, Value: binding, Path: routePrefix + "/oidc/callback", HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: 600})
}

func (m *Module) clearOIDCCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: OIDCCookieName, Path: routePrefix + "/oidc/callback", HttpOnly: true, Secure: m.secureCookies, SameSite: http.SameSiteLaxMode, MaxAge: -1, Expires: time.Unix(1, 0).UTC()})
}

func sessionCookie(r *http.Request) (string, error) {
	cookie, err := r.Cookie(CookieName)
	if err != nil || cookie.Value == "" {
		return "", sessionInvalid()
	}
	return cookie.Value, nil
}

func requestMeta(r *http.Request) RequestMeta {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	ua := r.UserAgent()
	if len(ua) > 512 {
		ua = ua[:512]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: host, UserAgent: ua}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
