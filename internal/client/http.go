package client

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

type PrincipalProvider func(*http.Request) (identity.Principal, error)

type AdminHandler struct {
	service   *Service
	principal PrincipalProvider
}

func NewAdminHandler(service *Service, principal PrincipalProvider) *AdminHandler {
	return &AdminHandler{service, principal}
}
func (h *AdminHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/api-keys", h.list)
	mux.HandleFunc("POST /api/admin/v1/api-keys", h.create)
	mux.HandleFunc("GET /api/admin/v1/api-keys/{api_key_id}", h.get)
	mux.HandleFunc("DELETE /api/admin/v1/api-keys/{api_key_id}", h.revoke)
	mux.HandleFunc("POST /api/admin/v1/api-keys/{api_key_id}/rotate", h.rotate)
}
func (h *AdminHandler) authenticate(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, appError(apperror.KindUnauthenticated, "session_invalid", "管理会话无效"))
		return identity.Principal{}, false
	}
	value, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return value, false
	}
	return value, true
}
func (h *AdminHandler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	for name, values := range r.URL.Query() {
		if (name != "status" && name != "limit" && name != "cursor") || len(values) != 1 {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 100 {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
		limit = value
	}
	result, err := h.service.List(r.Context(), p, APIKeyStatus(r.URL.Query().Get("status")), limit, r.URL.Query().Get("cursor"))
	writeJSON(w, r, http.StatusOK, result, err, false)
}
func (h *AdminHandler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.Get(r.Context(), p, r.PathValue("api_key_id"))
	writeJSON(w, r, http.StatusOK, result, err, false)
}
func (h *AdminHandler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var value CreateAPIKeyRequest
	if !decodeBody(w, r, &value) {
		return
	}
	if !value.expiresAtSet {
		httpx.WriteError(w, r, validation(map[string]any{"path": "/expires_at", "code": "required", "message": "expires_at 为必填项"}))
		return
	}
	result, err := h.service.Create(r.Context(), p, requestMeta(r), value)
	writeJSON(w, r, http.StatusCreated, result, err, true)
}
func (h *AdminHandler) revoke(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.Revoke(r.Context(), p, requestMeta(r), r.PathValue("api_key_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *AdminHandler) rotate(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var value RotateAPIKeyRequest
	if !decodeBody(w, r, &value) {
		return
	}
	result, err := h.service.Rotate(r.Context(), p, requestMeta(r), r.PathValue("api_key_id"), value)
	writeJSON(w, r, http.StatusCreated, result, err, true)
}

type ContentHandler struct {
	service *Service
	reader  content.PublishedContentReader
}

func NewContentHandler(service *Service, reader content.PublishedContentReader) *ContentHandler {
	return &ContentHandler{service, reader}
}
func (h *ContentHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/content/v1/models", h.listModels)
	mux.HandleFunc("GET /api/content/v1/models/{model_key}", h.getModel)
	mux.HandleFunc("GET /api/content/v1/models/{model_key}/entries", h.listEntries)
	mux.HandleFunc("GET /api/content/v1/models/{model_key}/entries/{entry_id}", h.getEntry)
}
func (h *ContentHandler) key(w http.ResponseWriter, r *http.Request) (AuthenticatedKey, bool) {
	values := r.Header.Values("Authorization")
	if len(values) == 0 {
		httpx.WriteError(w, r, appError(apperror.KindUnauthenticated, "api_key_required", "缺少 API Key"))
		return AuthenticatedKey{}, false
	}
	if len(values) != 1 || strings.Contains(values[0], ",") {
		httpx.WriteError(w, r, invalidAPIKey())
		return AuthenticatedKey{}, false
	}
	parts := strings.Split(values[0], " ")
	if len(parts) != 2 || parts[0] != "Bearer" || parts[1] == "" {
		httpx.WriteError(w, r, invalidAPIKey())
		return AuthenticatedKey{}, false
	}
	key, err := h.service.Authenticate(r.Context(), parts[1])
	if err != nil {
		httpx.WriteError(w, r, err)
		return key, false
	}
	return key, true
}
func (h *ContentHandler) listModels(w http.ResponseWriter, r *http.Request) {
	key, ok := h.key(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		httpx.WriteError(w, r, invalidQuery())
		return
	}
	items, err := h.reader.ListPublishedModels(r.Context(), key.ModelIDs)
	type summary struct {
		ID          string    `json:"id"`
		Key         string    `json:"key"`
		DisplayName string    `json:"display_name"`
		Description string    `json:"description"`
		UpdatedAt   time.Time `json:"updated_at"`
	}
	summaries := make([]summary, len(items))
	for i, item := range items {
		summaries[i] = summary{item.ID, item.Key, item.DisplayName, item.Description, item.UpdatedAt}
	}
	h.respond(w, r, struct {
		Items []summary `json:"items"`
	}{summaries}, err)
}
func (h *ContentHandler) getModel(w http.ResponseWriter, r *http.Request) {
	key, ok := h.key(w, r)
	if !ok {
		return
	}
	if len(r.URL.Query()) != 0 {
		httpx.WriteError(w, r, invalidQuery())
		return
	}
	item, err := h.reader.GetPublishedModel(r.Context(), r.PathValue("model_key"), key.ModelIDs)
	h.respond(w, r, item, err)
}
func (h *ContentHandler) listEntries(w http.ResponseWriter, r *http.Request) {
	key, ok := h.key(w, r)
	if !ok {
		return
	}
	query, err := parsePublishedQuery(r.URL.Query())
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := h.reader.ListPublishedEntries(r.Context(), r.PathValue("model_key"), key.ModelIDs, query)
	h.respond(w, r, item, err)
}
func (h *ContentHandler) getEntry(w http.ResponseWriter, r *http.Request) {
	key, ok := h.key(w, r)
	if !ok {
		return
	}
	for name, values := range r.URL.Query() {
		if name != "expand" || len(values) != 1 {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
	}
	expand, err := parseCSV(r.URL.Query().Get("expand"), 3)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	item, err := h.reader.GetPublishedEntry(r.Context(), r.PathValue("model_key"), r.PathValue("entry_id"), key.ModelIDs, expand)
	h.respond(w, r, item, err)
}
func (h *ContentHandler) respond(w http.ResponseWriter, r *http.Request, value any, err error) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	sum := sha256.Sum256(body)
	etag := `"sha256-` + base64.RawURLEncoding.EncodeToString(sum[:]) + `"`
	w.Header().Set("ETag", etag)
	w.Header().Set("Cache-Control", "private, no-cache")
	if matchesETag(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

func matchesETag(header, current string) bool {
	opaque := strings.TrimPrefix(current, "W/")
	for _, value := range strings.Split(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || strings.TrimPrefix(value, "W/") == opaque {
			return true
		}
	}
	return false
}
func decodeBody(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.WriteError(w, r, validation(map[string]any{"path": "", "code": "invalid_json", "message": "请求体不是合法 JSON"}))
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		httpx.WriteError(w, r, validation(map[string]any{"path": "", "code": "invalid_json", "message": "请求体只能包含一个 JSON 值"}))
		return false
	}
	return true
}
func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any, err error, noStore bool) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if noStore {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func requestMeta(r *http.Request) RequestMeta {
	ip := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	}
	ua := r.UserAgent()
	for utf8.RuneCountInString(ua) > 512 {
		_, size := utf8.DecodeLastRuneInString(ua)
		ua = ua[:len(ua)-size]
	}
	return RequestMeta{httpx.RequestIDFromContext(r.Context()), ip, ua}
}

type Module struct {
	admin   *AdminHandler
	content *ContentHandler
}

func NewModule(service *Service, reader content.PublishedContentReader, principal PrincipalProvider) Module {
	return Module{NewAdminHandler(service, principal), NewContentHandler(service, reader)}
}
func (m Module) RegisterRoutes(mux *http.ServeMux) {
	m.admin.RegisterRoutes(mux)
	m.content.RegisterRoutes(mux)
}
