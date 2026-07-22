package app

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestSPAAndAPIIsolation(t *testing.T) {
	handler := New(fstest.MapFS{
		"index.html":    {Data: []byte("<main>CMS</main>")},
		"assets/app.js": {Data: []byte("export {}")},
	}, nil)

	tests := []struct {
		path        string
		status      int
		contentType string
	}{
		{path: "/", status: http.StatusOK, contentType: "text/html"},
		{path: "/future-route", status: http.StatusOK, contentType: "text/html"},
		{path: "/missing.js", status: http.StatusNotFound, contentType: "text/plain"},
		{path: "/api/admin/v1/missing", status: http.StatusNotFound, contentType: "application/json"},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, tt.path, nil)
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != tt.status {
				t.Fatalf("status = %d", response.Code)
			}
			if got := response.Header().Get("Content-Type"); !strings.HasPrefix(got, tt.contentType) {
				t.Fatalf("content type = %q", got)
			}
			if response.Header().Get("X-Request-ID") == "" {
				t.Fatal("missing request ID")
			}
			if response.Header().Get("Content-Security-Policy") != "frame-ancestors 'none'" || response.Header().Get("X-Frame-Options") != "DENY" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
				t.Fatalf("security headers = %#v", response.Header())
			}
		})
	}
}

func TestSPARejectsWriteMethods(t *testing.T) {
	handler := New(fstest.MapFS{"index.html": {Data: []byte("CMS")}}, nil)
	request := httptest.NewRequest(http.MethodPost, "/login", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("allow = %q", response.Header().Get("Allow"))
	}
}
