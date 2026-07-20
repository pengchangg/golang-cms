package asset

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestOSSStoreSignsBoundPrivateRequests(t *testing.T) {
	now := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	store, err := NewOSSStore(OSSConfig{Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou", Bucket: "private-bucket", AccessKeyID: "id", AccessKeySecret: "secret", SecurityToken: "token", UploadMaxTTL: 15 * time.Minute, DownloadMaxTTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	key := "assets/ast_0123456789abcdef0123456789abcdef/0123456789abcdef0123456789abcdef"
	put, err := store.SignPut(context.Background(), SignPutRequest{ObjectKey: key, ContentType: "image/png", Size: 12, SHA256: strings.Repeat("a", 64), ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(put.URL)
	if parsed.Scheme != "https" || parsed.Host != "private-bucket.oss-cn-hangzhou.aliyuncs.com" || put.Method != "PUT" {
		t.Fatalf("上传签名地址错误: %+v", put)
	}
	if _, exists := put.Headers["Content-Length"]; exists {
		t.Fatalf("浏览器上传不得要求 Content-Length: %#v", put.Headers)
	}
	if put.Headers["Content-Type"] != "image/png" || put.Headers["x-oss-meta-sha256"] == "" || put.Headers["x-oss-forbid-overwrite"] != "true" {
		t.Fatalf("上传签名未绑定元数据: %#v", put.Headers)
	}
	if additional := parsed.Query().Get("x-oss-additional-headers"); strings.Contains(additional, "content-length") {
		t.Fatalf("上传签名不应包含 Content-Length: %q", additional)
	}
	for _, key := range []string{"x-oss-signature", "x-oss-credential", "x-oss-security-token", "x-oss-expires"} {
		if parsed.Query().Get(key) == "" {
			t.Errorf("签名缺少 %s", key)
		}
	}
	get, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: key, DownloadFilename: "报告\r\n.png", ExpiresAt: now.Add(4 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = url.Parse(get.URL)
	if get.Method != "GET" || strings.Contains(parsed.Query().Get("response-content-disposition"), "\r") || strings.Contains(parsed.Query().Get("response-content-disposition"), "\n") {
		t.Fatalf("下载文件名未安全处理: %s", get.URL)
	}
	if _, exists := parsed.Query()["x-oss-additional-headers"]; exists {
		t.Fatalf("无附加 Header 的 GET 不应包含 x-oss-additional-headers: %s", get.URL)
	}
}

// 固定向量来自阿里云 OSS V4 文档的 CanonicalQuery 规则：先 URI 编码名称和值，再按编码结果排序。
func TestCanonicalQueryMatchesOSSV4Rules(t *testing.T) {
	values := url.Values{
		"a":   {"z"},
		"a-":  {"x"},
		"z 空": {"2", "1"},
		"~":   {"a/b"},
		"é":   {"值"},
	}
	const want = "%C3%A9=%E5%80%BC&a=z&a-=x&z%20%E7%A9%BA=1&z%20%E7%A9%BA=2&~=a%2Fb"
	if got := canonicalQuery(values); got != want {
		t.Fatalf("canonical query = %q, want %q", got, want)
	}
}

func TestOSSStoreGetSignatureWithoutAdditionalHeadersIsFixed(t *testing.T) {
	now := time.Date(2023, 12, 3, 12, 12, 12, 0, time.UTC)
	store, err := NewOSSStore(OSSConfig{Endpoint: "https://oss-cn-hangzhou.aliyuncs.com", Region: "cn-hangzhou", Bucket: "examplebucket", AccessKeyID: "AKIDEXAMPLE", AccessKeySecret: "example-secret", UploadMaxTTL: 15 * time.Minute, DownloadMaxTTL: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	store.now = func() time.Time { return now }
	signed, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: "exampleobject", ExpiresAt: now.Add(5 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := parsed.Query()["x-oss-additional-headers"]; exists {
		t.Fatal("空附加 Header 被编码进查询参数")
	}
	if got, want := parsed.Query().Get("x-oss-signature"), "a9b601c1292e4d975788ec2596af5d70d85987b5bf505e691474e20b5510a9a2"; got != want {
		t.Fatalf("signature = %q, want %q", got, want)
	}
}

func TestOSSStoreRejectsInsecureConfiguration(t *testing.T) {
	cases := []string{
		"http://oss-cn-hangzhou.aliyuncs.com",
		"https://127.0.0.1",
		"https://user@oss-cn-hangzhou.aliyuncs.com",
		"https://oss-cn-hangzhou.aliyuncs.com.attacker.example",
		"https://attacker-oss-cn-hangzhou.aliyuncs.com",
		"https://oss-cn-hangzhou.aliyuncs.com:443",
		"https://oss-cn-hangzhou.aliyuncs.com/path",
		"https://oss-cn-shanghai.aliyuncs.com",
	}
	for _, endpoint := range cases {
		if _, err := NewOSSStore(testOSSConfig(endpoint, nil)); err == nil {
			t.Errorf("不安全 Endpoint 应被拒绝: %s", endpoint)
		}
	}
}

func TestOSSStoreAcceptsOfficialRegionHosts(t *testing.T) {
	for _, endpoint := range []string{"https://oss-cn-hangzhou.aliyuncs.com", "https://oss-cn-hangzhou-internal.aliyuncs.com"} {
		if _, err := NewOSSStore(testOSSConfig(endpoint, nil)); err != nil {
			t.Errorf("官方 Endpoint 应被接受: %s: %v", endpoint, err)
		}
	}
}

func TestOSSStoreNormalizesOfficialRegionName(t *testing.T) {
	config := testOSSConfig("https://oss-cn-hangzhou.aliyuncs.com", nil)
	config.Region = "oss-cn-hangzhou"
	store, err := NewOSSStore(config)
	if err != nil {
		t.Fatal(err)
	}
	if store.region != "cn-hangzhou" {
		t.Fatalf("region = %q", store.region)
	}
	now := time.Date(2026, 7, 19, 8, 0, 0, 0, time.UTC)
	store.now = func() time.Time { return now }
	signed, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: "assets/ast_x/key", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(signed.URL)
	if err != nil {
		t.Fatal(err)
	}
	if credential := parsed.Query().Get("x-oss-credential"); !strings.Contains(credential, "/cn-hangzhou/oss/") {
		t.Fatalf("签名 credential 未使用规范化地域: %q", credential)
	}
}

func TestOSSStoreCredentialRequestDoesNotFollowRedirect(t *testing.T) {
	redirected := false
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "attacker.example" {
			redirected = true
		}
		return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"https://attacker.example/stolen"}}, Body: io.NopCloser(strings.NewReader("")), Request: req}, nil
	})}
	store, err := NewOSSStore(testOSSConfig("https://oss-cn-hangzhou.aliyuncs.com", client))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Head(context.Background(), "assets/ast_x/key"); err == nil {
		t.Fatal("重定向响应不应视为成功")
	}
	if redirected {
		t.Fatal("携带 OSS 凭证的请求不应跟随重定向")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func testOSSConfig(endpoint string, client *http.Client) OSSConfig {
	return OSSConfig{Endpoint: endpoint, Region: "cn-hangzhou", Bucket: "b", AccessKeyID: "id", AccessKeySecret: "secret", UploadMaxTTL: 15 * time.Minute, DownloadMaxTTL: 5 * time.Minute, HTTPClient: client}
}
