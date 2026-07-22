package content

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

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

func TestListEntriesRejectsUnknownDuplicateAndInvalidF2Query(t *testing.T) {
	service, _, _, _ := testService()
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return contentPrincipal(permissionView), nil })
	targets := []string{
		"/api/admin/v1/models/mdl_1/entries?unknown=true",
		"/api/admin/v1/models/mdl_1/entries?sort=id&sort=-id",
		"/api/admin/v1/models/mdl_1/entries?workflow_status=unknown",
		"/api/admin/v1/models/mdl_1/entries?include_total=1",
		"/api/admin/v1/models/mdl_1/entries?sort=",
		"/api/admin/v1/models/mdl_1/entries?sort=id,,title",
		"/api/admin/v1/models/mdl_1/entries?filter=" + url.QueryEscape(`{"title":{"unknown":"x"}}`),
	}
	for _, target := range targets {
		request := httptest.NewRequest(http.MethodGet, target, nil)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || !bytes.Contains(response.Body.Bytes(), []byte(`"code":"invalid_query"`)) {
			t.Fatalf("非法 F2 查询响应错误 (%s): %d, %s", target, response.Code, response.Body.String())
		}
	}
}

func TestListEntriesAcceptsWorkflowFilterAndIncludesTotal(t *testing.T) {
	service, repository, _, _ := testService()
	now := time.Date(2026, 7, 18, 0, 0, 0, 0, time.UTC)
	repository.entries["ent_1"] = EntrySummary{ID: "ent_1", ModelID: "mdl_1", Status: StatusDraft, CurrentDraftRevisionID: "rev_1", WorkflowStatus: WorkflowPendingReview, CreatedAt: now, UpdatedAt: now}
	repository.revisions["ent_1"] = []Revision{{ID: "rev_1", EntryID: "ent_1", ModelID: "mdl_1", Content: json.RawMessage(`{"title":"待审核标题"}`)}}
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return contentPrincipal(permissionView), nil })
	request := httptest.NewRequest(http.MethodGet, "/api/admin/v1/models/mdl_1/entries?workflow_status=pending_review&sort=-updated_at&include_total=true", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !bytes.Contains(response.Body.Bytes(), []byte(`"workflow_status":"pending_review"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"current_draft_content":{"title":"待审核标题"}`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"fields":[{"key":"title"`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"total":1`)) || !bytes.Contains(response.Body.Bytes(), []byte(`"total_is_estimate":false`)) {
		t.Fatalf("F2 列表响应错误: %d, %s", response.Code, response.Body.String())
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

func TestHTTPJSONRequestBodyExactLimitAndOneByteOver(t *testing.T) {
	valid := `{"content":{"title":"内容"}}`
	exact := valid + strings.Repeat(" ", int(maxJSONRequestBytes)-len(valid))
	crossingSecondValue := valid + strings.Repeat(" ", int(maxJSONRequestBytes)-len(valid)-1) + `{}`
	for _, test := range []struct {
		name string
		body string
		want int
		code string
	}{
		{name: "exact limit", body: exact, want: http.StatusCreated},
		{name: "one byte over", body: exact + "x", want: http.StatusRequestEntityTooLarge, code: "request_body_too_large"},
		{name: "two values within limit", body: valid + ` {}`, want: http.StatusBadRequest, code: "validation_failed"},
		{name: "second value crosses limit", body: crossingSecondValue, want: http.StatusRequestEntityTooLarge, code: "request_body_too_large"},
	} {
		t.Run(test.name, func(t *testing.T) {
			service, _, _, _ := testService()
			handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return contentPrincipal(permissionCreate), nil })
			request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models/mdl_1/entries", strings.NewReader(test.body))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, body=%s", response.Code, response.Body.String())
			}
			if test.code != "" && !bytes.Contains(response.Body.Bytes(), []byte(`"code":"`+test.code+`"`)) {
				t.Fatalf("错误响应 = %s", response.Body.String())
			}
		})
	}
}

func TestHTTPWorkflowResponsesUseContentEntryContract(t *testing.T) {
	service, _, _, _ := testService()
	principal := contentPrincipal(permissionCreate, permissionSubmit, permissionReview, permissionPublish, permissionUnpublish)
	ctx := httptest.NewRequest(http.MethodPost, "/", nil).Context()
	created, err := service.CreateEntry(ctx, principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"待审核"}`)})
	if err != nil {
		t.Fatal(err)
	}
	handler := testHTTPHandler(service, func(*http.Request) (identity.Principal, error) { return principal, nil })
	action := func(entryID, name, body string) Entry {
		t.Helper()
		request := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models/mdl_1/entries/"+entryID+"/"+name, strings.NewReader(body))
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s 响应 = %d, body=%s", name, response.Code, response.Body.String())
		}
		var result Entry
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		for _, property := range []string{`"current_draft_content"`, `"current_draft_revision"`, `"current_published_revision_id"`, `"current_published_revision"`, `"workflow_status"`, `"referenced_assets"`} {
			if !bytes.Contains(response.Body.Bytes(), []byte(property)) {
				t.Fatalf("%s 响应缺少 %s: %s", name, property, response.Body.String())
			}
		}
		return result
	}

	submitted := action(created.ID, "submit", `{"revision_id":"`+created.CurrentDraftRevisionID+`"}`)
	approved := action(created.ID, "approve", `{"revision_id":"`+submitted.CurrentDraftRevisionID+`"}`)
	unpublished := action(created.ID, "unpublish", `{"revision_id":"`+*approved.CurrentPublishedRevisionID+`"}`)
	if submitted.WorkflowStatus != WorkflowPendingReview || approved.WorkflowStatus != WorkflowPublished || unpublished.WorkflowStatus != WorkflowUnpublished {
		t.Fatalf("工作流状态 = %s/%s/%s", submitted.WorkflowStatus, approved.WorkflowStatus, unpublished.WorkflowStatus)
	}

	rejectedEntry, err := service.CreateEntry(ctx, principal, testMeta(), "mdl_1", CreateEntryRequest{Content: json.RawMessage(`{"title":"待驳回"}`)})
	if err != nil {
		t.Fatal(err)
	}
	action(rejectedEntry.ID, "submit", `{"revision_id":"`+rejectedEntry.CurrentDraftRevisionID+`"}`)
	rejected := action(rejectedEntry.ID, "reject", `{"revision_id":"`+rejectedEntry.CurrentDraftRevisionID+`","reason":"需要修改"}`)
	if rejected.WorkflowStatus != WorkflowRejected {
		t.Fatalf("驳回状态 = %s", rejected.WorkflowStatus)
	}
}

func testHTTPHandler(service *Service, principal PrincipalProvider) http.Handler {
	mux := http.NewServeMux()
	NewModule(service, principal).RegisterRoutes(mux)
	return httpx.RequestID(mux)
}
