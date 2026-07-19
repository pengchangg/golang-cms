package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotFoundHasStableEnvelopeAndRequestID(t *testing.T) {
	handler := RequestID(http.HandlerFunc(NotFound))
	request := httptest.NewRequest(http.MethodGet, "/api/missing", nil)
	request.Header.Set(RequestIDHeader, "test-request")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status = %d", response.Code)
	}
	if response.Header().Get(RequestIDHeader) != "test-request" {
		t.Fatalf("request id = %q", response.Header().Get(RequestIDHeader))
	}
	var body struct {
		Error struct {
			Code      string `json:"code"`
			RequestID string `json:"request_id"`
			Details   []any  `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Error.Code != "not_found" || body.Error.RequestID != "test-request" || body.Error.Details == nil {
		t.Fatalf("unexpected body: %+v", body)
	}
}

func TestInvalidRequestIDIsReplaced(t *testing.T) {
	handler := RequestID(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "invalid request id")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if got := response.Header().Get(RequestIDHeader); !validRequestID.MatchString(got) || got == "invalid request id" {
		t.Fatalf("replacement request id = %q", got)
	}
}
