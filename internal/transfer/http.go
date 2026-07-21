package transfer

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"strings"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

const maxImportBytes = 10 << 20
const maxImportRequestBytes = maxImportBytes + 1<<20

type PrincipalProvider func(*http.Request) (identity.Principal, error)

type Handler struct {
	service   *Service
	principal PrincipalProvider
}

func NewHandler(service *Service, principal PrincipalProvider) *Handler {
	return &Handler{service: service, principal: principal}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/transfers/template", h.template)
	mux.HandleFunc("POST /api/admin/v1/models/{model_id}/imports", h.importCSV)
	mux.HandleFunc("GET /api/admin/v1/models/{model_id}/exports.csv", h.exportCSV)
}

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (identity.Principal, bool) {
	if h.principal == nil {
		httpx.WriteError(w, r, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"})
		return identity.Principal{}, false
	}
	principal, err := h.principal(r)
	if err != nil {
		httpx.WriteError(w, r, err)
		return principal, false
	}
	return principal, true
}

func (h *Handler) template(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	var body bytes.Buffer
	if err := h.service.Template(r.Context(), principal, r.PathValue("model_id"), &body); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="template.csv"`)
	_, _ = w.Write(body.Bytes())
}

func (h *Handler) importCSV(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	if !hasModel(principal, r.PathValue("model_id"), "content.create") {
		httpx.WriteError(w, r, forbidden())
		return
	}
	// multipart 自身有边界和头部开销，文件大小在解析后单独按 10 MiB 校验。
	r.Body = http.MaxBytesReader(w, r.Body, maxImportRequestBytes)
	if err := r.ParseMultipartForm(maxImportBytes); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeStatusError(w, r, http.StatusRequestEntityTooLarge, "file_too_large", "CSV 文件不能超过 10 MiB")
			return
		}
		httpx.WriteError(w, r, invalid("invalid_multipart", "请求必须是 multipart/form-data"))
		return
	}
	defer r.MultipartForm.RemoveAll()
	file, err := singleImportFile(r.MultipartForm)
	if err != nil {
		var tooLarge *fileTooLargeError
		if errors.As(err, &tooLarge) {
			writeStatusError(w, r, http.StatusRequestEntityTooLarge, "file_too_large", tooLarge.Error())
			return
		}
		httpx.WriteError(w, r, err)
		return
	}
	defer file.Close()
	model, err := h.service.models.GetModel(r.Context(), h.service.db, r.PathValue("model_id"))
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	rows := make([]json.RawMessage, 0)
	err = ParseCSV(file, ActiveRootFields(model.Fields), func(_ int, raw json.RawMessage) error {
		rows = append(rows, append(json.RawMessage(nil), raw...))
		return nil
	})
	if err != nil {
		var csvErr *CSVError
		if errors.As(err, &csvErr) {
			httpx.WriteError(w, r, invalidDetail(csvErr.Detail))
		} else {
			httpx.WriteError(w, r, err)
		}
		return
	}
	result, err := h.service.Import(r.Context(), principal, requestMeta(r), r.PathValue("model_id"), rows)
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(result)
}

func singleImportFile(form *multipart.Form) (multipart.File, error) {
	if len(form.Value) != 0 || len(form.File) != 1 || len(form.File["file"]) != 1 {
		return nil, invalid("invalid_multipart", "必须且只能提供一个 file 文件字段")
	}
	header := form.File["file"][0]
	if header.Size > maxImportBytes {
		return nil, &fileTooLargeError{}
	}
	file, err := header.Open()
	if err != nil {
		return nil, invalid("invalid_multipart", "无法读取 CSV 文件")
	}
	return file, nil
}

type fileTooLargeError struct{}

func (*fileTooLargeError) Error() string { return "CSV 文件不能超过 10 MiB" }

func (h *Handler) exportCSV(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.authenticate(w, r)
	if !ok {
		return
	}
	values := r.URL.Query()
	for key := range values {
		if key != "workflow_status" && key != "filter" && key != "relation_filter" && key != "sort" || len(values[key]) != 1 {
			httpx.WriteError(w, r, invalid("invalid_query", "导出查询无效"))
			return
		}
	}
	file, err := os.CreateTemp("", "cms-export-*.csv")
	if err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	defer func() { name := file.Name(); _ = file.Close(); _ = os.Remove(name) }()
	if err = h.service.Export(r.Context(), principal, r.PathValue("model_id"), ExportRequest{WorkflowStatus: values.Get("workflow_status"), Filter: values.Get("filter"), RelationFilter: values.Get("relation_filter"), Sort: values.Get("sort")}, file); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		httpx.WriteError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="export.csv"`)
	_, _ = io.Copy(w, file)
}

func requestMeta(r *http.Request) content.RequestMeta {
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
	return content.RequestMeta{RequestID: httpx.RequestIDFromContext(r.Context()), IP: ip, UserAgent: string(userAgent)}
}

func writeStatusError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": code, "message": message, "request_id": httpx.RequestIDFromContext(r.Context()), "details": []map[string]any{}}})
}

type Module struct{ handler *Handler }

func NewModule(service *Service, principal PrincipalProvider) Module {
	return Module{handler: NewHandler(service, principal)}
}

func (m Module) RegisterRoutes(mux *http.ServeMux) { m.handler.RegisterRoutes(mux) }
