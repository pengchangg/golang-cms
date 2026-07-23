package configuration

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/client"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

const maxRequestBytes = int64(128 << 10)
const maxPublishedNamespaceBytes = 8 << 20

type PrincipalProvider func(*http.Request) (identity.Principal, error)

type AdminHandler struct {
	service   *Service
	principal PrincipalProvider
}

func NewAdminHandler(service *Service, principal PrincipalProvider) *AdminHandler {
	return &AdminHandler{service: service, principal: principal}
}

func (h *AdminHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/configurations", h.listNamespaces)
	mux.HandleFunc("POST /api/admin/v1/configurations", h.createNamespace)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}", h.getNamespace)
	mux.HandleFunc("PATCH /api/admin/v1/configurations/{namespace_id}", h.updateNamespace)
	mux.HandleFunc("DELETE /api/admin/v1/configurations/{namespace_id}", h.archiveNamespace)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}/items", h.listItems)
	mux.HandleFunc("POST /api/admin/v1/configurations/{namespace_id}/items", h.createItem)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}/items/{item_id}", h.getItem)
	mux.HandleFunc("PATCH /api/admin/v1/configurations/{namespace_id}/items/{item_id}", h.updateItem)
	mux.HandleFunc("DELETE /api/admin/v1/configurations/{namespace_id}/items/{item_id}", h.archiveItem)
	mux.HandleFunc("POST /api/admin/v1/configurations/{namespace_id}/items/{item_id}/drafts", h.createDraft)
	mux.HandleFunc("PATCH /api/admin/v1/configurations/{namespace_id}/items/{item_id}/draft", h.updateDraft)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}/items/{item_id}/value", h.getItemValue)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}/items/{item_id}/revisions", h.listRevisions)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}/items/{item_id}/revisions/{revision_id}", h.getRevision)
	mux.HandleFunc("GET /api/admin/v1/configurations/{namespace_id}/items/{item_id}/workflow-events", h.listWorkflowEvents)
	mux.HandleFunc("POST /api/admin/v1/configurations/{namespace_id}/items/{item_id}/submit", h.submit)
	mux.HandleFunc("POST /api/admin/v1/configurations/{namespace_id}/items/{item_id}/approve", h.approve)
	mux.HandleFunc("POST /api/admin/v1/configurations/{namespace_id}/items/{item_id}/reject", h.reject)
	mux.HandleFunc("POST /api/admin/v1/configurations/{namespace_id}/items/{item_id}/unpublish", h.unpublish)
}

func (h *AdminHandler) listNamespaces(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok || !validateOnlyStatusQuery(w, r) {
		return
	}
	status, ok := parseStatus(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListNamespaces(r.Context(), p, status)
	writeJSON(w, r, http.StatusOK, struct {
		Items []Namespace `json:"items"`
	}{result}, err)
}

func (h *AdminHandler) createNamespace(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateNamespaceRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateNamespace(r.Context(), p, requestMeta(r), request)
	writeJSON(w, r, http.StatusCreated, result, err)
}

func (h *AdminHandler) getNamespace(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetNamespace(r.Context(), p, r.PathValue("namespace_id"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) updateNamespace(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request NamespacePatch
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.UpdateNamespace(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), request)
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) archiveNamespace(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.ArchiveNamespace(r.Context(), p, requestMeta(r), r.PathValue("namespace_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) listItems(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok || !validateOnlyStatusQuery(w, r) {
		return
	}
	status, ok := parseStatus(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListItems(r.Context(), p, r.PathValue("namespace_id"), status)
	writeJSON(w, r, http.StatusOK, struct {
		Items []Item `json:"items"`
	}{result}, err)
}

func (h *AdminHandler) createItem(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateItemRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateItem(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), request)
	writeJSON(w, r, http.StatusCreated, result, err)
}

func (h *AdminHandler) getItem(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetItem(r.Context(), p, r.PathValue("namespace_id"), r.PathValue("item_id"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) getItemValue(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetItemValue(r.Context(), p, r.PathValue("namespace_id"), r.PathValue("item_id"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) listRevisions(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	limit, ok := parsePaginationQuery(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListRevisions(r.Context(), p, r.PathValue("namespace_id"), r.PathValue("item_id"), limit, r.URL.Query().Get("cursor"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) getRevision(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok || !validateNoQuery(w, r) {
		return
	}
	result, err := h.service.GetRevision(r.Context(), p, r.PathValue("namespace_id"), r.PathValue("item_id"), r.PathValue("revision_id"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) listWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	limit, ok := parsePaginationQuery(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListWorkflowEvents(r.Context(), p, r.PathValue("namespace_id"), r.PathValue("item_id"), limit, r.URL.Query().Get("cursor"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) updateItem(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ItemPatch
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.UpdateItem(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), r.PathValue("item_id"), request)
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) archiveItem(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.ArchiveItem(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), r.PathValue("item_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *AdminHandler) createDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateDraftRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateDraft(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), r.PathValue("item_id"), request)
	writeJSON(w, r, http.StatusCreated, result, err)
}

func (h *AdminHandler) updateDraft(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request UpdateDraftRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.UpdateDraft(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), r.PathValue("item_id"), request)
	writeJSON(w, r, http.StatusCreated, result, err)
}

func (h *AdminHandler) submit(w http.ResponseWriter, r *http.Request) {
	h.workflow(w, r, func(ctx context.Context, p identity.Principal, meta RequestMeta, namespaceID, itemID string, request RevisionConditionRequest) (ItemValue, error) {
		return h.service.Submit(ctx, p, meta, namespaceID, itemID, request)
	})
}

func (h *AdminHandler) approve(w http.ResponseWriter, r *http.Request) {
	h.workflow(w, r, func(ctx context.Context, p identity.Principal, meta RequestMeta, namespaceID, itemID string, request RevisionConditionRequest) (ItemValue, error) {
		return h.service.Approve(ctx, p, meta, namespaceID, itemID, request)
	})
}

func (h *AdminHandler) unpublish(w http.ResponseWriter, r *http.Request) {
	h.workflow(w, r, func(ctx context.Context, p identity.Principal, meta RequestMeta, namespaceID, itemID string, request RevisionConditionRequest) (ItemValue, error) {
		return h.service.Unpublish(ctx, p, meta, namespaceID, itemID, request)
	})
}

func (h *AdminHandler) workflow(w http.ResponseWriter, r *http.Request, execute func(context.Context, identity.Principal, RequestMeta, string, string, RevisionConditionRequest) (ItemValue, error)) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request RevisionConditionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := execute(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), r.PathValue("item_id"), request)
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) reject(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request RejectRevisionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.Reject(r.Context(), p, requestMeta(r), r.PathValue("namespace_id"), r.PathValue("item_id"), request)
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *AdminHandler) authenticate(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"})
		return identity.Principal{}, false
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return identity.Principal{}, false
	}
	return principal, true
}

type ContentPrincipal struct {
	ID                 string
	ModelIDs           []string
	ConfigNamespaceIDs []string
}

type Authenticate interface {
	Authenticate(context.Context, string) (ContentPrincipal, error)
}

type AuthenticateFunc func(context.Context, string) (ContentPrincipal, error)

func (f AuthenticateFunc) Authenticate(ctx context.Context, token string) (ContentPrincipal, error) {
	return f(ctx, token)
}

type Protection interface {
	Acquire(string, string) (func(), time.Duration, error)
}

type ProtectionFunc func(string, string) (func(), time.Duration, error)

func (f ProtectionFunc) Acquire(keyID, ip string) (func(), time.Duration, error) {
	return f(keyID, ip)
}

type ContentHandler struct {
	reader       PublishedReader
	authenticate Authenticate
	protection   Protection
}

func NewContentHandler(reader PublishedReader, authenticate Authenticate, protection Protection) *ContentHandler {
	return &ContentHandler{reader: reader, authenticate: authenticate, protection: protection}
}

func (h *ContentHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/content/v1/configurations/{namespace_key}", h.getNamespace)
	mux.HandleFunc("GET /api/content/v1/configurations/{namespace_key}/{item_key}", h.getItem)
}

func (h *ContentHandler) getNamespace(w http.ResponseWriter, r *http.Request) {
	principal, release, ok := h.authorize(w, r)
	if !ok {
		return
	}
	defer release()
	if len(r.URL.Query()) != 0 {
		httpx.WriteError(w, r, invalidQuery())
		return
	}
	result, err := h.reader.GetPublishedNamespace(r.Context(), r.PathValue("namespace_key"), principal.ConfigNamespaceIDs, principal.ModelIDs)
	h.respond(w, r, result, err, maxPublishedNamespaceBytes)
}

func (h *ContentHandler) getItem(w http.ResponseWriter, r *http.Request) {
	principal, release, ok := h.authorize(w, r)
	if !ok {
		return
	}
	defer release()
	if len(r.URL.Query()) != 0 {
		httpx.WriteError(w, r, invalidQuery())
		return
	}
	result, err := h.reader.GetPublishedItem(r.Context(), r.PathValue("namespace_key"), r.PathValue("item_key"), principal.ConfigNamespaceIDs, principal.ModelIDs)
	h.respond(w, r, result, err, maxValueJSONBytes+1<<20)
}

func (h *ContentHandler) authorize(w http.ResponseWriter, r *http.Request) (ContentPrincipal, func(), bool) {
	if h.authenticate == nil {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "api_key_required", Message: "缺少 API Key"})
		return ContentPrincipal{}, nil, false
	}
	raw, err := client.ParseBearerToken(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return ContentPrincipal{}, nil, false
	}
	principal, err := h.authenticate.Authenticate(r.Context(), raw)
	if err != nil {
		httpx.WriteError(w, r, err)
		return principal, nil, false
	}
	release := func() {}
	if h.protection != nil {
		var retry time.Duration
		release, retry, err = h.protection.Acquire(principal.ID, httpx.ClientIPFromRequest(r))
		if err != nil {
			if retry > 0 {
				seconds := int64((retry + time.Second - 1) / time.Second)
				w.Header().Set("Retry-After", strconv.FormatInt(seconds, 10))
			}
			httpx.WriteError(w, r, err)
			return principal, nil, false
		}
	}
	return principal, release, true
}

func (h *ContentHandler) respond(w http.ResponseWriter, r *http.Request, value any, resultErr error, limit int) {
	if resultErr != nil {
		httpx.WriteError(w, r, resultErr)
		return
	}
	body, err := json.Marshal(value)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if len(body) > limit {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindPayloadTooLarge, Code: "configuration_response_too_large", Message: "配置展开响应超过大小上限"})
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

func decodeRequest(w http.ResponseWriter, r *http.Request, destination any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindPayloadTooLarge, Code: "request_body_too_large", Message: "请求体超过大小上限"})
			return false
		}
		writeInvalidJSON(w, r)
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeInvalidJSON(w, r)
		return false
	}
	return true
}

func writeInvalidJSON(w http.ResponseWriter, r *http.Request) {
	httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": "", "code": "invalid_json", "message": "请求体必须是单个严格 JSON 值"}}})
}

func validateOnlyStatusQuery(w http.ResponseWriter, r *http.Request) bool {
	for key, values := range r.URL.Query() {
		if key != "status" || len(values) != 1 {
			httpx.WriteError(w, r, invalidQuery())
			return false
		}
	}
	return true
}

func parseStatus(w http.ResponseWriter, r *http.Request) (*ResourceStatus, bool) {
	value := r.URL.Query().Get("status")
	if value == "" {
		return nil, true
	}
	status := ResourceStatus(value)
	if status != StatusActive && status != StatusArchived {
		httpx.WriteError(w, r, invalidQuery())
		return nil, false
	}
	return &status, true
}

func parsePaginationQuery(w http.ResponseWriter, r *http.Request) (int, bool) {
	for key, values := range r.URL.Query() {
		if (key != "limit" && key != "cursor") || len(values) != 1 || values[0] == "" {
			httpx.WriteError(w, r, invalidQuery())
			return 0, false
		}
	}
	limit := 20
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			httpx.WriteError(w, r, invalidQuery())
			return 0, false
		}
		limit = parsed
	}
	return limit, true
}

func validateNoQuery(w http.ResponseWriter, r *http.Request) bool {
	if len(r.URL.Query()) == 0 {
		return true
	}
	httpx.WriteError(w, r, invalidQuery())
	return false
}

func invalidQuery() error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "invalid_query", Message: "配置查询无效"}
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any, err error) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func requestMeta(r *http.Request) RequestMeta {
	userAgent := r.UserAgent()
	for utf8.RuneCountInString(userAgent) > 512 {
		_, size := utf8.DecodeLastRuneInString(userAgent)
		userAgent = userAgent[:len(userAgent)-size]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: httpx.ClientIPFromRequest(r), UserAgent: userAgent}
}

type Module struct {
	admin   *AdminHandler
	content *ContentHandler
}

func NewModule(service *Service, reader PublishedReader, principal PrincipalProvider, authenticate Authenticate, protection Protection) Module {
	return Module{admin: NewAdminHandler(service, principal), content: NewContentHandler(reader, authenticate, protection)}
}

func (m Module) RegisterAdminRoutes(mux *http.ServeMux)   { m.admin.RegisterRoutes(mux) }
func (m Module) RegisterContentRoutes(mux *http.ServeMux) { m.content.RegisterRoutes(mux) }
func (m Module) RegisterRoutes(mux *http.ServeMux) {
	m.RegisterAdminRoutes(mux)
	m.RegisterContentRoutes(mux)
}
