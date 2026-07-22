package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"

	"cms/internal/platform/apperror"
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

func TestWriteErrorMapsResourceLimitKinds(t *testing.T) {
	for _, test := range []struct {
		kind   apperror.Kind
		status int
	}{
		{apperror.KindPayloadTooLarge, http.StatusRequestEntityTooLarge},
		{apperror.KindTooManyRequests, http.StatusTooManyRequests},
	} {
		response := httptest.NewRecorder()
		WriteError(response, httptest.NewRequest(http.MethodGet, "/", nil), &apperror.Error{Kind: test.kind, Code: "limited", Message: "受限"})
		if response.Code != test.status {
			t.Fatalf("kind=%d status=%d", test.kind, response.Code)
		}
	}
}

func TestClientIPOnlyTrustsForwardedHeadersFromConfiguredProxy(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	handler := ClientIP(trusted, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(ClientIPFromRequest(r)))
	}))
	for _, test := range []struct {
		name, remote, forwarded, real, want string
	}{
		{name: "untrusted ignores spoofed header", remote: "192.0.2.10:1234", forwarded: "203.0.113.8", want: "192.0.2.10"},
		{name: "trusted uses first untrusted from right", remote: "10.0.0.2:1234", forwarded: "203.0.113.8, 10.0.0.1", want: "203.0.113.8"},
		{name: "trusted uses real ip", remote: "10.0.0.2:1234", real: "2001:db8::1", want: "2001:db8::1"},
		{name: "zoned real ip falls back direct", remote: "10.0.0.2:1234", real: "fe80::1%eth0", want: "10.0.0.2"},
		{name: "zoned forwarded ip falls back direct", remote: "10.0.0.2:1234", forwarded: "fe80::1%eth0", want: "10.0.0.2"},
		{name: "empty forwarded does not downgrade", remote: "10.0.0.2:1234", forwarded: " ", real: "203.0.113.8", want: "10.0.0.2"},
		{name: "invalid forwarded falls back direct", remote: "10.0.0.2:1234", forwarded: "invalid", want: "10.0.0.2"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.RemoteAddr = test.remote
			if test.forwarded != "" {
				request.Header.Set("X-Forwarded-For", test.forwarded)
			}
			if test.real != "" {
				request.Header.Set("X-Real-IP", test.real)
			}
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, request)
			if response.Body.String() != test.want {
				t.Fatalf("client IP = %q, want %q", response.Body.String(), test.want)
			}
		})
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "10.0.0.2:1234"
	request.Header.Add("X-Real-IP", "203.0.113.8")
	request.Header.Add("X-Real-IP", "198.51.100.20")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Body.String() != "10.0.0.2" {
		t.Fatalf("重复 X-Real-IP 不应被信任: %q", response.Body.String())
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

func TestSecurityHeadersCoverSuccessAndErrorResponses(t *testing.T) {
	for _, status := range []int{http.StatusNoContent, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			handler := SecurityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
			response := httptest.NewRecorder()
			handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
			for name, want := range map[string]string{
				"Content-Security-Policy": "frame-ancestors 'none'",
				"X-Frame-Options":         "DENY",
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "no-referrer",
			} {
				if got := response.Header().Get(name); got != want {
					t.Errorf("%s = %q, want %q", name, got, want)
				}
			}
		})
	}
}
