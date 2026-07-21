package schema

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
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
	mux.HandleFunc("GET /api/admin/v1/models", h.listModels)
	mux.HandleFunc("POST /api/admin/v1/models", h.createModel)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}", h.getModel)
	mux.HandleFunc("PATCH /api/admin/v1/models/{model_id}", h.updateModel)
	mux.HandleFunc("DELETE /api/admin/v1/models/{model_id}", h.archiveModel)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/fields", h.listFields)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/fields", h.createField)
	mux.HandleFunc("PUT /api/admin/v1/models/{model_id}/fields/order", h.updateFieldOrder)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/fields/{parent_field_id}/children", h.createChildField)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/fields/{field_id}", h.getField)
	mux.HandleFunc("PATCH /api/admin/v1/models/{model_id}/fields/{field_id}", h.updateField)
	mux.HandleFunc("DELETE /api/admin/v1/models/{model_id}/fields/{field_id}", h.archiveField)
}

func (h *Handler) listModels(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var status *ResourceStatus
	if value := r.URL.Query().Get("status"); value != "" {
		parsed := ResourceStatus(value)
		if parsed != StatusActive && parsed != StatusArchived {
			writeValidation(w, r, "/status", "invalid_value", "status 必须是 active 或 archived")
			return
		}
		status = &parsed
	}
	items, err := h.service.ListModels(r.Context(), principal, status)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *Handler) createModel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateContentModelRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateModel(r.Context(), principal, requestMeta(r), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (h *Handler) getModel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetModel(r.Context(), principal, r.PathValue("model_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) updateModel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request UpdateContentModelRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.UpdateModel(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) archiveModel(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.ArchiveModel(r.Context(), principal, requestMeta(r), r.PathValue("model_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) listFields(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListFields(r.Context(), principal, r.PathValue("model_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}
func (h *Handler) createField(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ContentFieldInput
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateField(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (h *Handler) updateFieldOrder(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request UpdateFieldOrderRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	if err := h.service.UpdateFieldOrder(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), request); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) createChildField(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ContentFieldInput
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.CreateChildField(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("parent_field_id"), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (h *Handler) getField(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetField(r.Context(), principal, r.PathValue("model_id"), r.PathValue("field_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) updateField(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ContentFieldPatch
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.UpdateField(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("field_id"), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) archiveField(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.ArchiveField(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("field_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
func writeValidation(w http.ResponseWriter, r *http.Request, path, code, message string) {
	details := []map[string]any{{"path": path, "code": code, "message": message}}
	httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: details})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
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
