package content

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/httpx"
)

func TestRegisterRoutesExposesOnlyDraftContentOperations(t *testing.T) {
	service, _, _, _ := testService()
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) {
		return contentPrincipal(permissionCreate, permissionView), nil
	})

	request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models/mdl_1/entries", bytes.NewBufferString(`{"content":{"title":"内容"}}`))
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("创建路由状态码错误: %d, %s", response.Code, response.Body.String())
	}
	var entry Entry
	if err := json.Unmarshal(response.Body.Bytes(), &entry); err != nil || entry.CurrentDraftRevision.Number != 1 {
		t.Fatalf("创建响应不符合 DTO: %s", response.Body.String())
	}

	request = httptest.NewRequest(http.MethodPost, "/api/admin/v1/models/mdl_1/entries/ent_1/publish", nil)
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("未实现的发布路由必须保持 404，得到 %d", response.Code)
	}
}

func TestListEntriesValidatesStatusAndLimit(t *testing.T) {
	service, _, _, _ := testService()
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) {
		return contentPrincipal(permissionView), nil
	})
	for _, target := range []string{
		"/api/admin/v1/models/mdl_1/entries?status=published",
		"/api/admin/v1/models/mdl_1/entries?limit=101",
	} {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"validation_failed"`)) {
			t.Fatalf("非法列表参数响应错误: %d, %s", response.Code, response.Body.String())
		}
	}
}

func TestHandlerUsesPrincipalProviderAndMapsDomainErrors(t *testing.T) {
	service, _, _, _ := testService()
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) {
		return identity.Principal{}, &apperror.Error{Kind: apperror.KindUnauthenticated, Code: "session_invalid", Message: "管理会话无效"}
	})
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/models/mdl_1/entries", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"session_invalid"`)) {
		t.Fatalf("认证错误映射不正确: %d, %s", response.Code, response.Body.String())
	}
}

func TestUpdateRequestRequiresBaseRevisionID(t *testing.T) {
	service, repository, _, _ := testService()
	principal := contentPrincipal(permissionCreate, permissionUpdate)
	created, err := service.CreateEntry(httptest.NewRequest(http.MethodPost, "/", nil).Context(), principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"第一版"}`)})
	if err != nil {
		t.Fatal(err)
	}
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return principal, nil })
	request := httptest.NewRequest(http.MethodPatch, "/api/admin/v1/models/mdl_1/entries/"+created.ID, bytes.NewBufferString(`{"content":{"title":"第二版"}}`))
	request.RemoteAddr = "127.0.0.1:1234"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"path":"/base_revision_id"`)) {
		t.Fatalf("缺少基线 Revision 响应不正确: %d, %s", response.Code, response.Body.String())
	}
	if len(repository.revisions[created.ID]) != 1 {
		t.Fatal("缺少基线 Revision 的请求产生了新 Revision")
	}
}

func testHTTPHandler(service *Service, principal PrincipalProvider) http.Handler {
	mux := http.NewServeMux()
	NewModule(service, principal).RegisterRoutes(mux)
	return httpx.RequestID(mux)
}
