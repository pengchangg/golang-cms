package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/platform/httpx"
)

func TestHTTPCreateModelContract(t *testing.T) {
	state := &transactionState{}
	service := testService(&memoryRepository{}, state)
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return testPrincipal(), nil })
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models", bytes.NewBufferString(`{"key":"articles","display_name":"Articles"}`))
	request.Header.Set(httpx.RequestIDHeader, "req_http")
	request.RemoteAddr = "192.0.2.1:1234"
	request.Header.Set("User-Agent", "contract-test")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated || response.Header().Get("Content-Type") != "application/json; charset=utf-8" {
		t.Fatalf("status/headers = %d/%v; body=%s", response.Code, response.Header(), response.Body.String())
	}
	var model ContentModel
	if err := json.Unmarshal(response.Body.Bytes(), &model); err != nil || model.ID != "mdl_1" || model.Key != "articles" || model.Fields == nil {
		t.Fatalf("model/error = %#v/%v", model, err)
	}
	if !state.authorizedInTx || !state.auditInTx {
		t.Fatalf("state = %#v", state)
	}
}

func TestHTTPGetModelNormalizesFieldArrays(t *testing.T) {
	repository := nilFieldsRepository{memoryRepository: &memoryRepository{models: map[string]ContentModelSummary{
		"mdl_empty": {ID: "mdl_empty", Key: "empty", DisplayName: "Empty", Status: StatusActive},
		"mdl_field": {ID: "mdl_field", Key: "field", DisplayName: "Field", Status: StatusActive},
	}}}
	service := testService(repository, &transactionState{})
	service.authorizer = permissiveAuthorizer{}
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return testPrincipal(), nil })
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/models/mdl_empty", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", response.Code, response.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if string(body["fields"]) != "[]" {
		t.Fatalf("fields = %s", body["fields"])
	}

	request = httptest.NewRequest(http.MethodGet, "/api/admin/v1/models/mdl_field", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	var model ContentModel
	if err := json.Unmarshal(response.Body.Bytes(), &model); err != nil {
		t.Fatal(err)
	}
	if len(model.Fields) != 1 || model.Fields[0].Children == nil {
		t.Fatalf("fields = %#v", model.Fields)
	}
}

func TestHTTPValidationEnvelopeAndUnknownFields(t *testing.T) {
	service := testService(&memoryRepository{}, &transactionState{})
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return testPrincipal(), nil })
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models", bytes.NewBufferString(`{"key":"articles","display_name":"Articles","unknown":true}`))
	request.Header.Set(httpx.RequestIDHeader, "req_validation")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, http.StatusBadRequest, "validation_failed", "req_validation")
}

func TestHTTPRejectsUnauthenticatedRequest(t *testing.T) {
	service := testService(&memoryRepository{}, &transactionState{})
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) {
		return identity.Principal{}, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"}
	})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/models", nil)
	request.Header.Set(httpx.RequestIDHeader, "req_auth")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, http.StatusUnauthorized, "session_invalid", "req_auth")
}

func TestHTTPArchiveReturns204AndSecondArchiveConflicts(t *testing.T) {
	state := &transactionState{}
	service := testService(seededRepository(), state)
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return testPrincipal(), nil })
	for index, expected := range []int{http.StatusNoContent, http.StatusConflict} {
		request := httptest.NewRequest(http.MethodDelete, "/api/admin/v1/models/mdl_1", nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != expected {
			t.Fatalf("request %d status = %d body=%s", index, response.Code, response.Body.String())
		}
	}
}

func TestHTTPStatusFilterValidation(t *testing.T) {
	service := testService(&memoryRepository{}, &transactionState{})
	service.authorizer = permissiveAuthorizer{}
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return testPrincipal(), nil })
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/models?status=deleted", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	assertHTTPError(t, response, http.StatusBadRequest, "validation_failed", response.Header().Get(httpx.RequestIDHeader))
}

func TestRequestMetaNormalizesIPAndTruncatesUserAgentByCharacters(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "[2001:0db8:0:0:0:0:0:1]:1234"
	request.Header.Set("User-Agent", strings.Repeat("界", 513))
	meta := requestMeta(request)
	if meta.IP != "2001:db8::1" || utf8.RuneCountInString(meta.UserAgent) != 512 || !utf8.ValidString(meta.UserAgent) {
		t.Fatalf("meta = %#v", meta)
	}
}

type permissiveAuthorizer struct{}

type nilFieldsRepository struct{ *memoryRepository }

func (r nilFieldsRepository) GetModel(_ context.Context, _ database.Querier, id string) (ContentModel, error) {
	model, ok := r.models[id]
	if !ok {
		return ContentModel{}, notFound("模型")
	}
	if id == "mdl_field" {
		return ContentModel{ContentModelSummary: model, Fields: []ContentField{{ID: "fld_leaf", Children: nil}}}, nil
	}
	return ContentModel{ContentModelSummary: model}, nil
}

func (permissiveAuthorizer) RequireSystemPermission(_ context.Context, _ identity.Principal, _ string) error {
	return nil
}

func testHTTPHandler(service *Service, principal PrincipalProvider) http.Handler {
	mux := http.NewServeMux()
	NewHandler(service, principal).RegisterRoutes(mux)
	return httpx.RequestID(mux)
}

func assertHTTPError(t *testing.T, response *httptest.ResponseRecorder, status int, code, requestID string) {
	t.Helper()
	if response.Code != status {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Details   []map[string]any
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != code || body.Error.RequestID != requestID || body.Error.Details == nil {
		t.Fatalf("error = %#v", body.Error)
	}
}
