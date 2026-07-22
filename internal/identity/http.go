package identity

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

type PrincipalProvider func(*http.Request) (Principal, error)
type Handler struct {
	service   *UserService
	principal PrincipalProvider
}

func NewHandler(service *UserService, principal PrincipalProvider) *Handler {
	return &Handler{service: service, principal: principal}
}
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/users", h.list)
	mux.HandleFunc("POST /api/admin/v1/users", h.create)
	mux.HandleFunc("GET /api/admin/v1/users/{user_id}", h.get)
	mux.HandleFunc("PATCH /api/admin/v1/users/{user_id}", h.setStatus)
	mux.HandleFunc("PATCH /api/admin/v1/users/{user_id}/phone", h.updatePhone)
}

func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	filter := UserFilter{Limit: 20, Query: r.URL.Query().Get("query"), Cursor: r.URL.Query().Get("cursor")}
	if value := r.URL.Query().Get("status"); value != "" {
		parsed := UserStatus(value)
		if parsed != UserEnabled && parsed != UserDisabled {
			writeValidation(w, r, "/status", "invalid_value", "status 必须是 enabled 或 disabled")
			return
		}
		filter.Status = &parsed
	}
	if value := r.URL.Query().Get("auth_method"); value != "" {
		parsed := AuthMethod(value)
		if parsed != AuthMethodLocal && parsed != AuthMethodSMS {
			writeValidation(w, r, "/auth_method", "invalid_value", "auth_method 必须是 local 或 sms")
			return
		}
		filter.AuthMethod = &parsed
	}
	if value := r.URL.Query().Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			writeValidation(w, r, "/limit", "out_of_range", "limit 必须为 1 至 100")
			return
		}
		filter.Limit = parsed
	}
	if len([]rune(filter.Query)) > 120 {
		writeValidation(w, r, "/query", "out_of_range", "query 长度不能超过 120")
		return
	}
	result, err := h.service.List(r.Context(), principal, filter)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateUserRequest
	if !decode(w, r, &request) {
		return
	}
	if request.RoleIDs == nil {
		writeValidation(w, r, "/role_ids", "required", "role_ids 必填")
		return
	}
	result, err := h.service.Create(r.Context(), principal, requestMeta(r), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.Get(r.Context(), principal, r.PathValue("user_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) setStatus(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request struct {
		Status UserStatus `json:"status"`
	}
	if !decode(w, r, &request) {
		return
	}
	if request.Status != UserEnabled && request.Status != UserDisabled {
		writeValidation(w, r, "/status", "invalid_value", "status 必须是 enabled 或 disabled")
		return
	}
	result, err := h.service.SetStatus(r.Context(), principal, requestMeta(r), r.PathValue("user_id"), request.Status)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) updatePhone(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request UpdatePhoneRequest
	if !decode(w, r, &request) {
		return
	}
	result, err := h.service.UpdatePhone(r.Context(), principal, requestMeta(r), r.PathValue("user_id"), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"})
		return Principal{}, false
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return Principal{}, false
	}
	return principal, true
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
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
	httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": path, "code": code, "message": message}}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
func requestMeta(r *http.Request) RequestMeta {
	agent := []rune(r.UserAgent())
	if len(agent) > 512 {
		agent = agent[:512]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: httpx.ClientIPFromRequest(r), UserAgent: string(agent)}
}

type Module struct{ handler *Handler }

func NewModule(service *UserService, principal PrincipalProvider) Module {
	return Module{handler: NewHandler(service, principal)}
}
func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
