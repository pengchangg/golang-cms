package content

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

const maxJSONRequestBytes = int64(1 << 20)

var errRequestBodyTooLarge = &apperror.Error{Kind: apperror.KindPayloadTooLarge, Code: "request_body_too_large", Message: "请求体超过 1 MiB 上限"}

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
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/entries/{entry_id}/submit", h.submit)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/entries/{entry_id}/approve", h.approve)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/entries/{entry_id}/reject", h.reject)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/entries/{entry_id}/unpublish", h.unpublish)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/entries/{entry_id}/workflow-events", h.listWorkflowEvents)
}

func (h *Handler) submit(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request RevisionConditionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.Submit(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id"), request)
	writeResult(w, r, http.StatusOK, result, err)
}
func (h *Handler) approve(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request RevisionConditionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.Approve(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id"), request)
	writeResult(w, r, http.StatusOK, result, err)
}
func (h *Handler) reject(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request RejectRevisionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.Reject(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id"), request)
	writeResult(w, r, http.StatusOK, result, err)
}
func (h *Handler) unpublish(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var request RevisionConditionRequest
	if !decodeRequest(w, r, &request) {
		return
	}
	result, err := h.service.Unpublish(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), r.PathValue("entry_id"), request)
	writeResult(w, r, http.StatusOK, result, err)
}
func (h *Handler) listWorkflowEvents(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	limit, ok := parseLimit(w, r)
	if !ok {
		return
	}
	result, err := h.service.ListWorkflowEvents(r.Context(), principal, r.PathValue("model_id"), r.PathValue("entry_id"), limit, r.URL.Query().Get("cursor"))
	writeResult(w, r, http.StatusOK, result, err)
}

func (h *Handler) listEntries(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	queryValues := r.URL.Query()
	allowed := map[string]bool{"status": true, "workflow_status": true, "limit": true, "cursor": true, "filter": true, "relation_filter": true, "sort": true, "expand": true, "include_total": true}
	for key, values := range queryValues {
		if !allowed[key] || len(values) != 1 {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
	}
	for _, key := range []string{"workflow_status", "filter", "relation_filter", "sort", "expand"} {
		if values, exists := queryValues[key]; exists && values[0] == "" {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
	}
	status := StatusDraft
	if value := queryValues.Get("status"); value != "" {
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
	query := AdminEntryQuery{Status: status, Limit: limit, Cursor: queryValues.Get("cursor")}
	if value := queryValues.Get("workflow_status"); value != "" {
		workflowStatus := WorkflowStatus(value)
		if !validWorkflowStatus(workflowStatus) {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
		query.WorkflowStatus = &workflowStatus
	}
	if values, exists := queryValues["include_total"]; exists {
		if values[0] != "true" && values[0] != "false" {
			httpx.WriteError(w, r, invalidQuery())
			return
		}
		query.IncludeTotal = values[0] == "true"
	}
	var err error
	if query.Filters, err = parseAdminFilters(queryValues.Get("filter")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if query.RelationFilters, err = parseAdminRelationFilters(queryValues.Get("relation_filter")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if query.Sort, err = parseAdminSort(queryValues.Get("sort")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if query.Expand, err = parseAdminList(queryValues.Get("expand")); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	result, err := h.service.ListEntries(r.Context(), principal, r.PathValue("model_id"), query)
	writeResult(w, r, http.StatusOK, result, err)
}

func validWorkflowStatus(value WorkflowStatus) bool {
	return value == WorkflowDraft || value == WorkflowPendingReview || value == WorkflowRejected || value == WorkflowPublished || value == WorkflowUnpublished
}

func parseAdminFilters(value string) ([]PublishedFilter, error) {
	if value == "" {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &raw) != nil || raw == nil || len(raw) > 5 {
		return nil, invalidQuery()
	}
	result := make([]PublishedFilter, 0, len(raw))
	for key, item := range raw {
		var operators map[string]json.RawMessage
		if key == "" || json.Unmarshal(item, &operators) != nil || len(operators) != 1 {
			return nil, invalidQuery()
		}
		for operator, operand := range operators {
			result = append(result, PublishedFilter{FieldKey: key, Operator: operator, Value: operand})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FieldKey < result[j].FieldKey })
	return result, nil
}

func parseAdminRelationFilters(value string) ([]PublishedRelationFilter, error) {
	if value == "" {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &raw) != nil || raw == nil || len(raw) > 2 {
		return nil, invalidQuery()
	}
	result := make([]PublishedRelationFilter, 0, len(raw))
	for key, item := range raw {
		var operators map[string]json.RawMessage
		if key == "" || json.Unmarshal(item, &operators) != nil || len(operators) != 1 {
			return nil, invalidQuery()
		}
		entryID, exists := operators["contains"]
		var id string
		if !exists || json.Unmarshal(entryID, &id) != nil || id == "" {
			return nil, invalidQuery()
		}
		result = append(result, PublishedRelationFilter{FieldKey: key, EntryID: id})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FieldKey < result[j].FieldKey })
	return result, nil
}

func parseAdminSort(value string) ([]PublishedSort, error) {
	items, err := parseAdminList(value)
	if err != nil {
		return nil, err
	}
	result := make([]PublishedSort, len(items))
	for i, item := range items {
		descending := strings.HasPrefix(item, "-")
		key := strings.TrimPrefix(item, "-")
		if key == "" {
			return nil, invalidQuery()
		}
		result[i] = PublishedSort{FieldKey: key, Descending: descending}
	}
	return result, nil
}

func parseAdminList(value string) ([]string, error) {
	if value == "" {
		return nil, nil
	}
	items, seen := strings.Split(value, ","), map[string]bool{}
	for _, item := range items {
		if item == "" || seen[item] {
			return nil, invalidQuery()
		}
		seen[item] = true
	}
	return items, nil
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
	r.Body = http.MaxBytesReader(w, r.Body, maxJSONRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, r, errRequestBodyTooLarge)
			return false
		}
		writeValidation(w, r, "", "invalid_json", "请求体不是合法 JSON")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			httpx.WriteError(w, r, errRequestBodyTooLarge)
			return false
		}
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
	userAgent := []rune(r.UserAgent())
	if len(userAgent) > 512 {
		userAgent = userAgent[:512]
	}
	return RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: httpx.ClientIPFromRequest(r), UserAgent: string(userAgent)}
}

type Module struct{ handler *Handler }

func NewModule(service *Service, principal PrincipalProvider) Module {
	return Module{handler: NewHandler(service, principal)}
}

func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
