package transfer

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"

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
	return &Handler{service, principal}
}
func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/transfers/template", h.template)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/imports/uploads", h.upload)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/imports", h.createImport)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/exports", h.createExport)
	mux.HandleFunc("GET /api/admin/v1/jobs", h.list)
	mux.HandleFunc("GET /api/admin/v1/jobs/{job_id}", h.get)
	mux.HandleFunc("POST /api/admin/v1/jobs/{job_id}/cancel", h.cancel)
	mux.HandleFunc("POST /api/admin/v1/jobs/{job_id}/retry", h.retry)
	mux.HandleFunc("GET /api/admin/v1/jobs/{job_id}/errors", h.errors)
	mux.HandleFunc("GET /api/admin/v1/jobs/{job_id}/files/{file_kind}", h.download)
}
func (h *Handler) auth(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"})
		return identity.Principal{}, false
	}
	p, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return p, false
	}
	return p, true
}
func (h *Handler) template(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	var body bytes.Buffer
	if err := h.service.Template(r.Context(), p, r.PathValue("model_id"), &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="template.csv"`)
	_, _ = w.Write(body.Bytes())
}
func (h *Handler) upload(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req struct {
		Filename string `json:"filename"`
		Size     int64  `json:"size"`
		SHA256   string `json:"sha256"`
	}
	if !decode(w, r, &req) {
		return
	}
	result, err := h.service.CreateUpload(r.Context(), p, r.PathValue("model_id"), req.Filename, req.Size, req.SHA256)
	if err == nil {
		w.Header().Set("Cache-Control", "no-store")
	}
	respond(w, r, http.StatusCreated, result, err)
}
func (h *Handler) createImport(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req struct {
		UploadID string `json:"upload_id"`
	}
	if !decode(w, r, &req) {
		return
	}
	job, replayed, err := h.service.CreateImport(r.Context(), p, r.PathValue("model_id"), req.UploadID, r.Header.Get("Idempotency-Key"))
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	respond(w, r, status, job, err)
}
func (h *Handler) createExport(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	var req ExportRequest
	if !decode(w, r, &req) {
		return
	}
	job, replayed, err := h.service.CreateExport(r.Context(), p, r.PathValue("model_id"), r.Header.Get("Idempotency-Key"), req)
	status := http.StatusCreated
	if replayed {
		status = http.StatusOK
	}
	respond(w, r, status, job, err)
}
func (h *Handler) get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	result, err := h.service.Get(r.Context(), p, r.PathValue("job_id"))
	respond(w, r, http.StatusOK, result, err)
}
func (h *Handler) list(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	limit := 20
	if text := r.URL.Query().Get("limit"); text != "" {
		var err error
		limit, err = strconv.Atoi(text)
		if err != nil {
			httpx.WriteError(w, r, invalid("invalid_query", "任务查询无效"))
			return
		}
	}
	result, err := h.service.List(r.Context(), p, JobFilter{Type: JobType(r.URL.Query().Get("type")), Status: JobStatus(r.URL.Query().Get("status")), ModelID: r.URL.Query().Get("model_id"), Limit: limit, Cursor: r.URL.Query().Get("cursor")})
	respond(w, r, http.StatusOK, result, err)
}
func (h *Handler) cancel(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	result, err := h.service.Cancel(r.Context(), p, r.PathValue("job_id"))
	respond(w, r, http.StatusOK, result, err)
}
func (h *Handler) retry(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	result, err := h.service.Retry(r.Context(), p, r.PathValue("job_id"))
	respond(w, r, http.StatusOK, result, err)
}
func (h *Handler) errors(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	limit, after := 20, 0
	if text := r.URL.Query().Get("limit"); text != "" {
		var err error
		limit, err = strconv.Atoi(text)
		if err != nil {
			httpx.WriteError(w, r, invalid("invalid_query", "错误详情查询无效"))
			return
		}
	}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil {
			httpx.WriteError(w, r, invalid("invalid_cursor", "分页游标无效"))
			return
		}
		after, err = strconv.Atoi(string(raw))
		if err != nil {
			httpx.WriteError(w, r, invalid("invalid_cursor", "分页游标无效"))
			return
		}
	}
	result, err := h.service.Errors(r.Context(), p, r.PathValue("job_id"), limit, after)
	respond(w, r, http.StatusOK, result, err)
}
func (h *Handler) download(w http.ResponseWriter, r *http.Request) {
	p, ok := h.auth(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("file_kind")
	if kind != "errors" && kind != "result" {
		httpx.WriteError(w, r, notFound())
		return
	}
	result, err := h.service.Download(r.Context(), p, r.PathValue("job_id"), kind)
	if err != nil {
		var expired *jobFileExpiredError
		if errors.As(err, &expired) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusGone)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": "job_file_expired", "message": "任务文件已过期", "request_id": httpx.RequestIDFromContext(r.Context()), "details": []map[string]any{}}})
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "private, no-store")
	http.Redirect(w, r, result.Location, http.StatusFound)
}
func decode(w http.ResponseWriter, r *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		httpx.WriteError(w, r, invalid("validation_failed", "请求体不是合法 JSON"))
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		httpx.WriteError(w, r, invalid("validation_failed", "请求体只能包含一个 JSON 值"))
		return false
	}
	return true
}
func respond(w http.ResponseWriter, r *http.Request, status int, value any, err error) {
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type Module struct{ handler *Handler }

func NewModule(service *Service, principal PrincipalProvider) Module {
	return Module{NewHandler(service, principal)}
}
func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
