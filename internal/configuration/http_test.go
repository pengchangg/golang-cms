package configuration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
)

type staticPublishedReader struct {
	namespace PublishedNamespace
	item      PublishedItem
	err       error
}

func (r staticPublishedReader) GetPublishedNamespace(context.Context, string, []string, []string) (PublishedNamespace, error) {
	return r.namespace, r.err
}

func (r staticPublishedReader) GetPublishedItem(context.Context, string, string, []string, []string) (PublishedItem, error) {
	return r.item, r.err
}

type staticAuthenticate struct{ principal ContentPrincipal }

func (a staticAuthenticate) Authenticate(context.Context, string) (ContentPrincipal, error) {
	return a.principal, nil
}

func TestContentHandlerETagAndConditionalRequest(t *testing.T) {
	reader := staticPublishedReader{item: PublishedItem{Key: "title", ValueType: TypeString, Value: "首页", RevisionID: "rev", RevisionNumber: 1}}
	handler := NewContentHandler(reader, staticAuthenticate{ContentPrincipal{ID: "key"}}, nil)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	request := httptest.NewRequest(http.MethodGet, "/api/content/v1/configurations/site/title", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("ETag") == "" {
		t.Fatalf("status = %d, etag = %q, body = %s", response.Code, response.Header().Get("ETag"), response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/api/content/v1/configurations/site/title", nil)
	request.Header.Set("Authorization", "Bearer secret")
	request.Header.Set("If-None-Match", response.Header().Get("ETag"))
	conditional := httptest.NewRecorder()
	mux.ServeHTTP(conditional, request)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 {
		t.Fatalf("status = %d, body = %s", conditional.Code, conditional.Body.String())
	}
}

func TestContentHandlerReturnsReaderFailureAtomically(t *testing.T) {
	reader := staticPublishedReader{err: publishedNotFound()}
	handler := NewContentHandler(reader, staticAuthenticate{ContentPrincipal{ID: "key"}}, nil)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodGet, "/api/content/v1/configurations/site", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusNotFound || strings.Contains(response.Body.String(), `"items"`) {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestContentHandlerRejectsMissingBearerAndQuery(t *testing.T) {
	handler := NewContentHandler(staticPublishedReader{}, staticAuthenticate{}, nil)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/content/v1/configurations/site", nil))
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("missing bearer status = %d", response.Code)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/content/v1/configurations/site?expand=x", nil)
	request.Header.Set("Authorization", "Bearer secret")
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("query status = %d", response.Code)
	}
}

func TestDecodeRequestIsStrictAndSingleJSON(t *testing.T) {
	for _, body := range []string{
		`{"namespace_key":"site","display_name":"Site","description":"","unknown":true}`,
		`{"namespace_key":"site","display_name":"Site","description":""} {}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		response := httptest.NewRecorder()
		var value CreateNamespaceRequest
		if decodeRequest(response, request, &value) {
			t.Fatalf("accepted body %s", body)
		}
		if response.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
		}
	}
}

func TestContentHandlerResponseLimit(t *testing.T) {
	handler := NewContentHandler(staticPublishedReader{}, staticAuthenticate{}, nil)
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	response := httptest.NewRecorder()
	handler.respond(response, request, strings.Repeat("x", 100), nil, 10)
	if response.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d", response.Code)
	}
}

func TestAssertApplicationErrorHelper(t *testing.T) {
	err := &apperror.Error{Kind: apperror.KindConflict, Code: "conflict"}
	var target *apperror.Error
	if !errors.As(err, &target) {
		t.Fatal("application error should unwrap")
	}
}

func TestParsePaginationQueryIsStrict(t *testing.T) {
	tests := []struct {
		query string
		limit int
		ok    bool
	}{
		{"", 20, true},
		{"?limit=100&cursor=next", 100, true},
		{"?limit=0", 0, false},
		{"?limit=101", 0, false},
		{"?limit=20&limit=30", 0, false},
		{"?cursor=", 0, false},
		{"?unknown=value", 0, false},
	}
	for _, test := range tests {
		t.Run(test.query, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/"+test.query, nil)
			response := httptest.NewRecorder()
			limit, ok := parsePaginationQuery(response, request)
			if ok != test.ok || limit != test.limit {
				t.Fatalf("limit = %d, ok = %v", limit, ok)
			}
			if !test.ok && response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d", response.Code)
			}
		})
	}
}

func TestConfigurationCursorsAreBoundToItem(t *testing.T) {
	revisionCursor, err := encodeConfigurationCursor(configurationCursor{Kind: "revisions", NamespaceID: "cns_1", ItemID: "cit_1", Number: 3})
	if err != nil {
		t.Fatal(err)
	}
	before, err := decodeRevisionCursor(revisionCursor, "cns_1", "cit_1")
	if err != nil || before == nil || *before != 3 {
		t.Fatalf("before = %v, err = %v", before, err)
	}
	if _, err := decodeRevisionCursor(revisionCursor, "cns_1", "cit_2"); err == nil {
		t.Fatal("跨配置项 Revision 游标应被拒绝")
	}

	eventCursor, err := encodeConfigurationCursor(configurationCursor{Kind: "workflow_events", NamespaceID: "cns_1", ItemID: "cit_1", OccurredAt: "2026-07-23T01:02:03.123456Z", ID: "cwe_1"})
	if err != nil {
		t.Fatal(err)
	}
	event, err := decodeWorkflowEventCursor(eventCursor, "cns_1", "cit_1")
	if err != nil || event == nil || event.ID != "cwe_1" || event.OccurredAt.Nanosecond() != 123456000 {
		t.Fatalf("event = %#v, err = %v", event, err)
	}
	if _, err := decodeWorkflowEventCursor(eventCursor, "cns_2", "cit_1"); err == nil {
		t.Fatal("跨配置命名空间事件游标应被拒绝")
	}
}

func TestAdminHistoryRoutesRejectUnknownQueries(t *testing.T) {
	handler := NewAdminHandler(nil, func(*http.Request) (identity.Principal, error) { return identity.Principal{}, nil })
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, path := range []string{
		"/api/admin/v1/configurations/cns_1/items/cit_1/revisions?unknown=value",
		"/api/admin/v1/configurations/cns_1/items/cit_1/revisions/crv_1?unknown=value",
		"/api/admin/v1/configurations/cns_1/items/cit_1/workflow-events?cursor=",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("path = %s, status = %d, body = %s", path, response.Code, response.Body.String())
		}
	}
}
