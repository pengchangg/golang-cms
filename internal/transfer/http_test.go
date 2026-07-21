package transfer

import (
	"bytes"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cms/internal/identity"
)

func TestImportAcceptsSingleMultipartFile(t *testing.T) {
	importer := &recordingImporter{}
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: importer})
	response := serveImport(t, service, "title\n\"\"\"标题\"\"\"\n", nil)
	if response.Code != http.StatusOK || response.Body.String() != "{\"imported\":1}\n" || importer.calls != 1 {
		t.Fatalf("response=%d %s calls=%d", response.Code, response.Body.String(), importer.calls)
	}
}

func TestImportReturnsStructuredCSVError(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: &recordingImporter{}})
	var csv strings.Builder
	csv.WriteString("title\n")
	for range MaxRows + 1 {
		csv.WriteString("\"\"\"值\"\"\"\n")
	}
	response := serveImport(t, service, csv.String(), nil)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"row_limit_exceeded"`) || !strings.Contains(response.Body.String(), `"row":1002`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestImportRejectsMoreThanTenMiB(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: &recordingImporter{}})
	response := serveImport(t, service, strings.Repeat("x", maxImportBytes+1), nil)
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(response.Body.String(), `"code":"file_too_large"`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestImportAllowsTenMiBFileBoundary(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: &recordingImporter{}})
	response := serveImport(t, service, strings.Repeat("x", maxImportBytes), nil)
	if response.Code == http.StatusRequestEntityTooLarge {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestImportRejectsAdditionalMultipartField(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: &recordingImporter{}})
	response := serveImport(t, service, "title\n", func(writer *multipart.Writer) error {
		return writer.WriteField("extra", "value")
	})
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), `"code":"invalid_multipart"`) {
		t.Fatalf("response=%d %s", response.Code, response.Body.String())
	}
}

func TestOnlySynchronousRoutesAreRegistered(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: &recordingImporter{}, Entries: &pagedEntries{}})
	mux := http.NewServeMux()
	NewHandler(service, principalProvider).RegisterRoutes(mux)
	for _, target := range []string{"/api/admin/v1/jobs", "/api/admin/v1/models/mdl_1/imports/uploads", "/api/admin/v1/models/mdl_1/exports"} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, target, nil))
		if response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d", target, response.Code)
		}
	}
}

func TestExportFailureOnLaterPageReturnsJSONError(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Entries: &pagedEntries{err: errors.New("第二页失败"), failAt: 2}})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/models/mdl_1/exports.csv", nil)
	response := httptest.NewRecorder()
	mux := http.NewServeMux()
	NewHandler(service, func(*http.Request) (identity.Principal, error) { return transferPrincipal("content.view"), nil }).RegisterRoutes(mux)
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError || !strings.Contains(response.Header().Get("Content-Type"), "application/json") || strings.HasPrefix(response.Body.String(), "\xef\xbb\xbf") {
		t.Fatalf("response=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func serveImport(t *testing.T, service *Service, csv string, extra func(*multipart.Writer) error) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "data.csv")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = file.Write([]byte(csv)); err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		if err = extra(writer); err != nil {
			t.Fatal(err)
		}
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models/mdl_1/imports", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	mux := http.NewServeMux()
	NewHandler(service, principalProvider).RegisterRoutes(mux)
	mux.ServeHTTP(response, request)
	return response
}

func principalProvider(*http.Request) (identity.Principal, error) {
	return transferPrincipal("content.create"), nil
}
