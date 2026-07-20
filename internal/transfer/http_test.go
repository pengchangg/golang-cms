package transfer

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

func TestUploadSuccessDisablesCaching(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Uploads: staticUploadManager{}})
	response := serveTransferRequest(service, http.MethodPost, "/api/admin/v1/models/mdl_1/imports/uploads", `{"filename":"data.csv","size":1,"sha256":"`+strings.Repeat("a", 64)+`"}`)
	if response.Code != http.StatusCreated || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("上传响应 = %d, Cache-Control = %q", response.Code, response.Header().Get("Cache-Control"))
	}
}

func TestUploadTemporaryObjectStoreFailureReturns503(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Uploads: staticUploadManager{err: unavailableStoreError()}})
	response := serveTransferRequest(service, http.MethodPost, "/api/admin/v1/models/mdl_1/imports/uploads", `{"filename":"data.csv","size":1,"sha256":"`+strings.Repeat("a", 64)+`"}`)
	assertUnavailableResponse(t, response)
}

func TestDownloadTemporaryObjectStoreFailureReturns503(t *testing.T) {
	expires := time.Now().Add(time.Hour)
	repository := downloadRepository{job: Job{ID: "job_1", ModelID: "mdl_1", ResultObjectKey: "transfers/job_1/result.csv", ExpiresAt: &expires}}
	service := NewService(Dependencies{Repository: repository, Store: failingTransferStore{}})
	response := serveTransferRequest(service, http.MethodGet, "/api/admin/v1/jobs/job_1/files/result", "")
	assertUnavailableResponse(t, response)
}

func serveTransferRequest(service *Service, method, target, body string) *httptest.ResponseRecorder {
	mux := http.NewServeMux()
	NewHandler(service, func(*http.Request) (identity.Principal, error) { return transferPrincipal(), nil }).RegisterRoutes(mux)
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	return response
}

func assertUnavailableResponse(t *testing.T, response *httptest.ResponseRecorder) {
	t.Helper()
	body := response.Body.String()
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(body, `"code":"object_store_unavailable"`) || strings.Contains(body, "secret") {
		t.Fatalf("临时故障响应 = %d %s", response.Code, body)
	}
}

func unavailableStoreError() error {
	return &apperror.Error{Kind: apperror.KindUnavailable, Code: "object_store_unavailable", Message: "对象存储暂时不可用", Cause: errors.New("secret OSS failure")}
}

type staticModelReader struct{}

func (staticModelReader) GetModel(context.Context, database.Querier, string) (schema.ContentModel, error) {
	return schema.ContentModel{ContentModelSummary: schema.ContentModelSummary{ID: "mdl_1"}}, nil
}

type staticUploadManager struct{ err error }

func (m staticUploadManager) Create(context.Context, string, int64, string, time.Time) (ImportUpload, error) {
	return ImportUpload{UploadID: "upl_1"}, m.err
}
func (staticUploadManager) Confirm(context.Context, string) (UploadClaims, error) {
	return UploadClaims{}, errors.New("unused")
}

type downloadRepository struct {
	Repository
	job Job
}

func (downloadRepository) IsEmergencyAdmin(context.Context, database.Querier, string) (bool, error) {
	return false, nil
}

func (r downloadRepository) GetJob(context.Context, database.Querier, string, string, bool) (Job, error) {
	return r.job, nil
}

type failingTransferStore struct{}

func (failingTransferStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (failingTransferStore) Put(context.Context, string, string, io.Reader) error {
	return errors.New("unused")
}
func (failingTransferStore) SignGet(context.Context, string, string, time.Time) (string, error) {
	return "", unavailableStoreError()
}
