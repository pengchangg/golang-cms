package asset

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

type PrincipalProvider func(*http.Request) (identity.Principal, error)

// PublishedScopeProvider 由集成层旧的 scope 工厂引用；素材管理 Handler 不再使用它。
type PublishedScopeProvider func(*http.Request) (PublishedDownloadScope, error)

type Handler struct {
	service   *Service
	principal PrincipalProvider
}

func NewHandler(service *Service, principal PrincipalProvider) *Handler {
	return &Handler{service: service, principal: principal}
}

// RegisterRoutes 只向集成层提供路由集合；素材包本身不依赖认证客户端实现。
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/assets", h.list)
	mux.HandleFunc("POST /api/admin/v1/assets/uploads", h.createUpload)
	mux.HandleFunc("GET /api/admin/v1/assets/{asset_id}", h.get)
	mux.HandleFunc("PATCH /api/admin/v1/assets/{asset_id}", h.rename)
	mux.HandleFunc("DELETE /api/admin/v1/assets/{asset_id}", h.archive)
	mux.HandleFunc("POST /api/admin/v1/assets/{asset_id}/confirm", h.confirm)
	mux.HandleFunc("GET /api/admin/v1/assets/{asset_id}/download", h.adminDownload)
	mux.HandleFunc("GET /api/admin/v1/assets/{asset_id}/preview", h.adminPreview)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries/{entry_id}/assets/{asset_id}/preview", h.referencedPreview)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries/{entry_id}/assets/{asset_id}/download", h.referencedDownload)
}

func (h *Handler) createUpload(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var input CreateUploadRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.service.CreateUpload(r.Context(), principal, requestMeta(r), input)
	writeJSON(w, r, http.StatusCreated, result, err)
}

func (h *Handler) confirm(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var empty struct{}
	if !decodeJSON(w, r, &empty) {
		return
	}
	result, err := h.service.Confirm(r.Context(), principal, requestMeta(r), r.PathValue("asset_id"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	for key, values := range query {
		if (key != "status" && key != "mime_type" && key != "limit" && key != "cursor") || len(values) != 1 {
			httpx.WriteError(w, r, appError(apperror.KindInvalidArgument, "invalid_query", "素材查询无效"))
			return
		}
	}
	limit := 20
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil {
			httpx.WriteError(w, r, appError(apperror.KindInvalidArgument, "invalid_query", "素材查询无效"))
			return
		}
		limit = parsed
	}
	input := ListQuery{MimeType: query.Get("mime_type"), Limit: limit, Cursor: query.Get("cursor")}
	if value := query.Get("status"); value != "" {
		status := Status(value)
		input.Status = &status
	}
	result, err := h.service.List(r.Context(), principal, input)
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.Get(r.Context(), principal, r.PathValue("asset_id"))
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *Handler) rename(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var input struct {
		Filename string `json:"filename"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	result, err := h.service.Rename(r.Context(), principal, r.PathValue("asset_id"), input.Filename)
	writeJSON(w, r, http.StatusOK, result, err)
}

func (h *Handler) archive(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.Archive(r.Context(), principal, requestMeta(r), r.PathValue("asset_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) adminDownload(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.AdminDownload(r.Context(), principal, requestMeta(r), r.PathValue("asset_id"))
	redirect(w, r, result, err)
}

func (h *Handler) adminPreview(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.AdminPreview(r.Context(), principal, r.PathValue("asset_id"), r.Header.Get("If-None-Match"))
	writePreview(w, r, result, err)
}

func (h *Handler) referencedPreview(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.ReferencedPreview(r.Context(), principal, r.PathValue("model_id"), r.PathValue("entry_id"), r.PathValue("asset_id"), r.Header.Get("If-None-Match"))
	writePreview(w, r, result, err)
}

func (h *Handler) referencedDownload(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.ReferencedDownload(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id"), r.PathValue("asset_id"))
	redirect(w, r, result, err)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, appError(apperror.KindInternal, "internal_error", "管理认证未装配"))
		return identity.Principal{}, false
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return identity.Principal{}, false
	}
	return principal, true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.WriteError(w, r, appError(apperror.KindInvalidArgument, "validation_failed", "请求数据校验失败"))
		return false
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		httpx.WriteError(w, r, appError(apperror.KindInvalidArgument, "validation_failed", "请求数据校验失败"))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, r *http.Request, status int, value any, err error) {
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code == "file_too_large" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": app.Code, "message": app.Message, "request_id": httpx.RequestIDFromContext(r.Context()), "details": []map[string]any{}}})
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if status == http.StatusCreated {
		w.Header().Set("Cache-Control", "no-store")
	}
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func redirect(w http.ResponseWriter, r *http.Request, value SignedRequest, err error) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Location", value.URL)
	w.Header().Set("Cache-Control", "private, no-store")
	w.WriteHeader(http.StatusFound)
}

func writePreview(w http.ResponseWriter, r *http.Request, value Preview, err error) {
	if err != nil {
		var app *apperror.Error
		if errors.As(err, &app) && app.Code == "asset_preview_too_large" {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": app.Code, "message": app.Message, "request_id": httpx.RequestIDFromContext(r.Context()), "details": []map[string]any{}}})
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-cache")
	w.Header().Set("ETag", value.ETag)
	if value.NotModified {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	defer value.Body.Close()
	w.Header().Set("Content-Type", value.MimeType)
	w.Header().Set("Content-Disposition", "inline")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	if value.Kind == PreviewImage && value.MimeType == "image/svg+xml" {
		w.Header().Set("Content-Security-Policy", "sandbox")
	}
	if value.Size > 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(value.Size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, value.Body)
}

func requestMeta(r *http.Request) RequestMeta {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	agent := strings.ToValidUTF8(r.UserAgent(), "")
	for utf8.RuneCountInString(agent) > 512 {
		_, size := utf8.DecodeLastRuneInString(agent)
		agent = agent[:len(agent)-size]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: host, UserAgent: agent}
}
