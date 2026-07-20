package asset

import (
	"net/http"
	"net/http/httptest"
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
