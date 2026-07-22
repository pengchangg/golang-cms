package client

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cms/internal/platform/apperror"
)

func TestProtectionEnforcesPairAndKeyBudgets(t *testing.T) {
	p := NewProtection()
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	releases := make([]func(), 0, contentPairConcurrency)
	for range contentPairConcurrency {
		release, _, err := p.Acquire("key_a", "192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
		releases = append(releases, release)
	}
	if _, _, err := p.Acquire("key_a", "192.0.2.1"); protectionCode(err) != "content_concurrency_limited" {
		t.Fatalf("err=%v", err)
	}
	for _, release := range releases {
		release()
	}
	for range int(contentPairBurst) {
		release, _, err := p.Acquire("key_b", "192.0.2.2")
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if _, retry, err := p.Acquire("key_b", "192.0.2.2"); protectionCode(err) != "content_rate_limited" || retry <= 0 {
		t.Fatalf("err=%v retry=%s", err, retry)
	}
	if _, _, err := p.Acquire("key_c", "192.0.2.2"); err != nil {
		t.Fatal("共享 IP 的不同 Key 不应共用额度")
	}
}

func TestProtectionGlobalConcurrency(t *testing.T) {
	p := NewProtection()
	started := make(chan struct{}, contentGlobalConcurrency)
	release := make(chan struct{})
	handler := p.Global(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { started <- struct{}{}; <-release }))
	for range contentGlobalConcurrency {
		go handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
	}
	for range contentGlobalConcurrency {
		<-started
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/", nil))
	if response.Code != http.StatusServiceUnavailable || response.Header().Get("Retry-After") != "1" {
		t.Fatalf("status=%d headers=%v", response.Code, response.Header())
	}
	close(release)
}

func TestProtectionRejectsUndeclaredHeadWithoutConsumingCapacity(t *testing.T) {
	p := NewProtection()
	called := false
	handler := p.Global(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true }))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodHead, "/api/content/v1/models", nil))
	if response.Code != http.StatusMethodNotAllowed || called || p.global != 0 {
		t.Fatalf("status=%d called=%t global=%d", response.Code, called, p.global)
	}
}

func TestProtectionCapacityNeverResetsActiveRateLimit(t *testing.T) {
	p := NewProtection()
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	for range int(contentPairBurst) {
		release, _, err := p.Acquire("limited", "192.0.2.1")
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	for i := 0; len(p.buckets) < maxProtectionBuckets; i++ {
		release, _, err := p.Acquire(fmt.Sprintf("key_%d", i), fmt.Sprintf("198.51.%d.%d", i/250, i%250+1))
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	if _, _, err := p.Acquire("limited", "192.0.2.1"); protectionCode(err) != "content_rate_limited" {
		t.Fatalf("受限桶被重置: %v", err)
	}
	if _, _, err := p.Acquire("new_key", "203.0.113.1"); protectionCode(err) != "content_api_busy" {
		t.Fatalf("容量满应 fail closed: %v", err)
	}
}

func TestProtectionReleaseIsIdempotent(t *testing.T) {
	p := NewProtection()
	release, _, err := p.Acquire("key", "192.0.2.1")
	if err != nil {
		t.Fatal(err)
	}
	release()
	release()
	if p.keyActive["key"] != 0 {
		t.Fatalf("active=%d", p.keyActive["key"])
	}
}

func TestProtectionLimitsPairStatePerKey(t *testing.T) {
	p := NewProtection()
	now := time.Date(2026, 7, 22, 0, 0, 0, 0, time.UTC)
	p.now = func() time.Time { return now }
	for i := range maxPairBucketsPerKey {
		release, _, err := p.Acquire("key", fmt.Sprintf("192.0.2.%d", i))
		if err != nil {
			t.Fatal(err)
		}
		release()
		now = now.Add(time.Second)
	}
	if _, _, err := p.Acquire("key", "198.51.100.1"); protectionCode(err) != "content_rate_limited" {
		t.Fatalf("单 Key Pair 状态应受限: %v", err)
	}
	if _, _, err := p.Acquire("other", "198.51.100.1"); err != nil {
		t.Fatalf("一个 Key 不得耗尽其他 Key 的 Pair 配额: %v", err)
	}
}

func protectionCode(err error) string {
	var application *apperror.Error
	if errors.As(err, &application) {
		return application.Code
	}
	return ""
}
