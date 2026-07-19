package permission

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
	mux.HandleFunc("GET /api/admin/v1/roles", h.list)
	mux.HandleFunc("POST /api/admin/v1/roles", h.create)
	mux.HandleFunc("GET /api/admin/v1/roles/{role_id}", h.get)
	mux.HandleFunc("PATCH /api/admin/v1/roles/{role_id}", h.update)
	mux.HandleFunc("DELETE /api/admin/v1/roles/{role_id}", h.delete)
	mux.HandleFunc("PUT /api/admin/v1/users/{user_id}/roles", h.replaceUserRoles)
	mux.HandleFunc("PUT /api/admin/v1/roles/{role_id}/system-permissions", h.replaceSystem)
	mux.HandleFunc("PUT /api/admin/v1/roles/{role_id}/model-permissions", h.replaceModels)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListRoles(r.Context(), p)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": result})
}
func (h *Handler) create(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request CreateRoleRequest
	if !decode(w, r, &request) {
		return
	}
	result, err := h.service.CreateRole(r.Context(), p, requestMeta(r), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.service.GetRole(r.Context(), p, r.PathValue("role_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) update(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request UpdateRoleRequest
	if !decode(w, r, &request) {
		return
	}
	result, err := h.service.UpdateRole(r.Context(), p, requestMeta(r), r.PathValue("role_id"), request)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) delete(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if err := h.service.DeleteRole(r.Context(), p, requestMeta(r), r.PathValue("role_id")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
func (h *Handler) replaceUserRoles(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ReplaceUserRolesRequest
	if !decode(w, r, &request) {
		return
	}
	if request.RoleIDs == nil {
		validationError(w, r, "/role_ids", "required", "role_ids 必填")
		return
	}
	result, err := h.service.ReplaceUserRoles(r.Context(), p, requestMeta(r), r.PathValue("user_id"), request.RoleIDs)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) replaceSystem(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ReplaceSystemPermissionsRequest
	if !decode(w, r, &request) {
		return
	}
	if request.Permissions == nil {
		validationError(w, r, "/permissions", "required", "permissions 必填")
		return
	}
	result, err := h.service.ReplaceSystemPermissions(r.Context(), p, requestMeta(r), r.PathValue("role_id"), request.Permissions)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) replaceModels(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request ReplaceModelPermissionsRequest
	if !decode(w, r, &request) {
		return
	}
	if request.Grants == nil {
		validationError(w, r, "/grants", "required", "grants 必填")
		return
	}
	result, err := h.service.ReplaceModelPermissions(r.Context(), p, requestMeta(r), r.PathValue("role_id"), request.Grants)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"})
		return identity.Principal{}, false
	}
	p, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return identity.Principal{}, false
	}
	return p, true
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		validationError(w, r, "", "invalid_json", "请求体不是合法 JSON")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		validationError(w, r, "", "invalid_json", "请求体只能包含一个 JSON 值")
		return false
	}
	return true
}
func validationError(w http.ResponseWriter, r *http.Request, path, code, message string) {
	httpx.WriteError(w, r, validation([]map[string]any{detail(path, code, message)}))
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
	agent := []rune(r.UserAgent())
	if len(agent) > 512 {
		agent = agent[:512]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: ip, UserAgent: string(agent)}
}

type Module struct{ handler *Handler }

func NewModule(service *Service, principal PrincipalProvider) Module {
	return Module{handler: NewHandler(service, principal)}
}
func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
