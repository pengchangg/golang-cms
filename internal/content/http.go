package content

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

type PrincipalProvider func(*http.Request) (identity.Principal, error)

type Handler struct {
	service   *Service
	principal PrincipalProvider
}

func NewHandler(service *Service, principal PrincipalProvider) *Handler {
	return &Handler{service: service, principal: principal}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries", h.listEntries)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/entries", h.createEntry)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries/{entry_id}", h.getEntry)
	mux.HandleFunc("PATCH /api/admin/v1/models/{model_id}/entries/{entry_id}", h.updateEntry)
	mux.HandleFunc("DELETE /api/admin/v1/models/{model_id}/entries/{entry_id}", h.archiveEntry)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries/{entry_id}/revisions", h.listRevisions)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries/{entry_id}/revisions/{revision_id}", h.getRevision)
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	status := StatusDraft
	if value := r.URL.Query().Get("status"); value != "" {
		status = EntryStatus(value)
		if status != StatusDraft && status != StatusArchived {
			writeValidation(w, r, "/status", "invalid_value", "status 必须是 draft 或 archived")
			return
		}
	}
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListEntries(r.Context(), principal, r.PathValue("model_id"), status, limit, r.URL.Query().Get("cursor"))
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) createEntry(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateEntryRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateEntry(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), request)
	writeResult(w, r, http.StatusCreated, result, err)
}

func (h *Handler) getEntry(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetEntry(r.Context(), principal, r.PathValue("model_id"), r.PathValue("entry_id"))
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) updateEntry(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request UpdateEntryRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.UpdateEntry(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id"), request)
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) archiveEntry(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.ArchiveEntry(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) listRevisions(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListRevisions(r.Context(), principal, r.PathValue("model_id"), r.PathValue("entry_id"), limit, r.URL.Query().Get("cursor"))
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) getRevision(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetRevision(r.Context(), principal, r.PathValue("model_id"), r.PathValue("entry_id"), r.PathValue("revision_id"))
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
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

func parseLimit(w http.ResponseWriter, r *http.Request) (int, bool) {
	value := r.URL.Query().Get("limit")
	if value == "" {
		return 20, true
	}
	limit, err := strconv.Atoi(value)
	if err != nil || limit < 1 || limit > 100 {
		writeValidation(w, r, "/limit", "out_of_range", "limit 必须在 1 到 100 之间")
		return 0, false
	}
	return limit, true
}

func decodeRequest(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeValidation(w, r, "", "invalid_json", "请求体不是合法 JSON")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		writeValidation(w, r, "", "invalid_json", "请求体只能包含一个 JSON 值")
		return false
	}
	return true
}

func writeResult(w http.ResponseWriter, r *http.Request, status int, result any, err error) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(result)
}

func writeValidation(w http.ResponseWriter, r *http.Request, path, code, message string) {
	httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": path, "code": code, "message": message}}})
}

func requestMeta(r *http.Request) RequestMeta {
	ip := strings.TrimSpace(r.RemoteAddr)
	if host, _, err := net.SplitHostPort(ip); err == nil {
		ip = host
	}
	if parsed := net.ParseIP(ip); parsed != nil {
		ip = parsed.String()
	}
	userAgent := []rune(r.UserAgent())
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: ip, UserAgent: string(userAgent)}
}

type Module struct{ handler *Handler }

func NewModule(service *Service, principal PrincipalProvider) Module {
	return Module{handler: NewHandler(service, principal)}
}

func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
