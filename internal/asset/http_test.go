package asset

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandlerRegistersOnlyAdminRoutes(t *testing.T) {
	mux := http.NewServeMux()
	NewHandler(nil, nil).RegisterRoutes(mux)

	for _, route := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/admin/v1/assets"},
		{http.MethodPost, "/api/admin/v1/assets/uploads"},
		{http.MethodGet, "/api/admin/v1/assets/ast_1"},
		{http.MethodPost, "/api/admin/v1/assets/ast_1/confirm"},
		{http.MethodGet, "/api/admin/v1/assets/ast_1/download"},
		{http.MethodGet, "/api/admin/v1/assets/ast_1/preview"},
		{http.MethodGet, "/api/admin/v1/models/mdl_1/entries/ent_1/assets/ast_1/preview"},
		{http.MethodGet, "/api/admin/v1/models/mdl_1/entries/ent_1/assets/ast_1/download"},
	} {
		request := httptest.NewRequest(route.method, route.path, nil)
		_, pattern := mux.Handler(request)
		if pattern == "" {
			t.Fatalf("管理路由未注册: %s %s", route.method, route.path)
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/content/v1/assets/ast_1", nil)
	_, pattern := mux.Handler(request)
	if pattern != "" {
		t.Fatalf("素材管理 Handler 不应注册客户端路由: %s", pattern)
	}
}

func TestWritePreviewStreamsTextWithSafeHeaders(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/assets/ast_1/preview", nil)
	response := httptest.NewRecorder()
	writePreview(response, request, Preview{Kind: PreviewText, MimeType: "application/json", ETag: `"digest"`, Body: io.NopCloser(strings.NewReader(`{"ok":true}`))}, nil)
	if response.Code != http.StatusOK || response.Body.String() != `{"ok":true}` {
		t.Fatalf("文本预览响应错误: code=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Content-Type") != "application/json" || response.Header().Get("Content-Disposition") != "inline" || response.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("文本预览安全响应头错误: %#v", response.Header())
	}
	if response.Header().Get("Cache-Control") != "private, no-cache" || response.Header().Get("ETag") != `"digest"` {
		t.Fatalf("文本预览缓存响应头错误: %#v", response.Header())
	}
}

func TestWritePreviewSandboxesSVGAndForbidsFraming(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/assets/ast_1/preview", nil)
	response := httptest.NewRecorder()
	writePreview(response, request, Preview{Kind: PreviewImage, MimeType: "image/svg+xml", Size: 6, Body: io.NopCloser(strings.NewReader("<svg/>"))}, nil)
	if response.Code != http.StatusOK || response.Header().Get("Content-Security-Policy") != "sandbox; frame-ancestors 'none'" {
		t.Fatalf("SVG 预览 CSP 错误: code=%d headers=%#v", response.Code, response.Header())
	}
}

func TestWritePreviewStreamsImageInline(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/assets/ast_1/preview", nil)
	response := httptest.NewRecorder()
	writePreview(response, request, Preview{Kind: PreviewImage, MimeType: "image/png", Size: 3, Body: io.NopCloser(strings.NewReader("png"))}, nil)
	if response.Code != http.StatusOK || response.Body.String() != "png" || response.Header().Get("Content-Type") != "image/png" || response.Header().Get("Content-Disposition") != "inline" || response.Header().Get("Content-Length") != "3" {
		t.Fatalf("图片预览响应错误: code=%d headers=%#v body=%q", response.Code, response.Header(), response.Body.String())
	}
}

func TestWritePreviewReturnsNotModifiedWithoutBody(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/assets/ast_1/preview", nil)
	response := httptest.NewRecorder()
	writePreview(response, request, Preview{ETag: `"digest"`, NotModified: true}, nil)
	if response.Code != http.StatusNotModified || response.Body.Len() != 0 {
		t.Fatalf("条件预览响应错误: code=%d body=%q", response.Code, response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "private, no-cache" || response.Header().Get("ETag") != `"digest"` {
		t.Fatalf("条件预览缓存响应头错误: %#v", response.Header())
	}
}
