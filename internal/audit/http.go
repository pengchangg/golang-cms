package audit

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

type PrincipalProvider func(*http.Request) (Principal, error)
type Handler struct {
	reader    *Reader
	principal PrincipalProvider
}

func NewHandler(reader *Reader, principal PrincipalProvider) *Handler {
	return &Handler{reader: reader, principal: principal}
}
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/audit/events", h.list)
	mux.HandleFunc("GET /api/admin/v1/audit/events/{event_id}", h.get)
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	result, err := h.reader.Get(r.Context(), p, r.PathValue("event_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	filter := Filter{ActorType: query.Get("actor_type"), ActorID: query.Get("actor_id"), Action: query.Get("action"), ResourceType: query.Get("resource_type"), ResourceID: query.Get("resource_id"), Result: query.Get("result"), Cursor: query.Get("cursor"), Limit: 20}
	if filter.ActorType != "" && filter.ActorType != "user" && filter.ActorType != "system" {
		invalid(w, r, "/actor_type", "invalid_value", "actor_type 必须是 user 或 system")
		return
	}
	if filter.Result != "" && filter.Result != "success" && filter.Result != "failure" {
		invalid(w, r, "/result", "invalid_value", "result 必须是 success 或 failure")
		return
	}
	if filter.Action != "" && !auditCode.MatchString(filter.Action) {
		invalid(w, r, "/action", "invalid_format", "action 格式不合法")
		return
	}
	if filter.ResourceType != "" && !auditCode.MatchString(filter.ResourceType) {
		invalid(w, r, "/resource_type", "invalid_format", "resource_type 格式不合法")
		return
	}
	if value := query.Get("limit"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > 100 {
			invalid(w, r, "/limit", "out_of_range", "limit 必须为 1 至 100")
			return
		}
		filter.Limit = parsed
	}
	for value, target := range map[string]**time.Time{"occurred_from": &filter.OccurredFrom, "occurred_to": &filter.OccurredTo} {
		if raw := query.Get(value); raw != "" {
			parsed, err := time.Parse(time.RFC3339, raw)
			if err != nil {
				invalid(w, r, "/"+value, "invalid_format", value+" 必须是 RFC 3339 时间")
				return
			}
			parsed = parsed.UTC()
			*target = &parsed
		}
	}
	if filter.OccurredFrom != nil && filter.OccurredTo != nil && filter.OccurredFrom.After(*filter.OccurredTo) {
		invalid(w, r, "/occurred_from", "invalid_range", "occurred_from 不得晚于 occurred_to")
		return
	}
	result, err := h.reader.List(r.Context(), p, filter)
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
	p, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return Principal{}, false
	}
	return p, true
}
func invalid(w http.ResponseWriter, r *http.Request, path, code, message string) {
	httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: []map[string]any{{"path": path, "code": code, "message": message}}})
}
func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type Module struct{ handler *Handler }

func NewModule(reader *Reader, principal PrincipalProvider) Module {
	return Module{handler: NewHandler(reader, principal)}
}
func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
