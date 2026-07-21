package asset

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/smithy-go"
	"github.com/aws/smithy-go/logging"
)

func TestSmithyLoggerUsesApplicationJSONFormat(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&output, &slog.HandlerOptions{Level: slog.LevelDebug}))
	config := testS3Config("https://storage.example.com", true, nil)
	config.Logger = logger
	store, err := NewS3Store(config)
	if err != nil {
		t.Fatal(err)
	}
	store.client.Options().Logger.Logf(logging.Warn, "checksum %s", "missing")
	line := output.String()
	if !strings.Contains(line, `"level":"WARN"`) || !strings.Contains(line, `"msg":"checksum missing"`) || !strings.Contains(line, `"component":"aws_sdk"`) {
		t.Fatalf("SDK 日志未使用统一 JSON 格式: %s", line)
	}
}

func TestS3StorePresignBindsStandardHeaders(t *testing.T) {
	store := newTestS3Store(t, "https://s3.example.com", false, nil)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	key := "assets/ast_0123456789abcdef0123456789abcdef/object"

	put, err := store.SignPut(context.Background(), SignPutRequest{ObjectKey: key, ContentType: "image/png", Size: 12, SHA256: strings.Repeat("a", 64), ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(put.URL)
	if err != nil {
		t.Fatal(err)
	}
	if put.Method != http.MethodPut || parsed.Host != "private-bucket.s3.example.com" || parsed.Path != "/"+key {
		t.Fatalf("virtual-hosted 预签名地址错误: %+v", put)
	}
	if put.Headers["Content-Type"] != "image/png" || put.Headers["X-Amz-Meta-Sha256"] != strings.Repeat("a", 64) {
		t.Fatalf("PUT 未绑定标准元数据 Header: %#v", put.Headers)
	}
	if put.Headers["If-None-Match"] != "*" {
		t.Fatalf("PUT 未绑定不可覆盖条件: %#v", put.Headers)
	}
	if _, exists := put.Headers["Content-Length"]; exists {
		t.Fatalf("浏览器 PUT 不应绑定 Content-Length: %#v", put.Headers)
	}
	if signed := strings.ToLower(parsed.Query().Get("X-Amz-SignedHeaders")); !strings.Contains(signed, "content-type") || !strings.Contains(signed, "if-none-match") || !strings.Contains(signed, "x-amz-meta-sha256") {
		t.Fatalf("预签名未包含必要 Header: %q", signed)
	}
	if strings.Contains(strings.ToLower(put.URL), "x-oss-") {
		t.Fatalf("不得添加供应商私有参数: %s", put.URL)
	}

	get, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: key, DownloadFilename: "报告\r\n.png", ExpiresAt: now.Add(4 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, err = url.Parse(get.URL)
	if err != nil {
		t.Fatal(err)
	}
	disposition := parsed.Query().Get("response-content-disposition")
	if get.Method != http.MethodGet || strings.ContainsAny(disposition, "\r\n") || !strings.Contains(disposition, "filename*=UTF-8''") {
		t.Fatalf("下载文件名未安全处理: %s", get.URL)
	}
	preview, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: key, Disposition: "inline", ContentType: "image/png", ExpiresAt: now.Add(4 * time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ = url.Parse(preview.URL)
	if parsed.Query().Get("response-content-disposition") != "" || parsed.Query().Get("response-content-type") != "" {
		t.Fatalf("预览签名不应包含不兼容的响应头覆盖参数: %s", preview.URL)
	}
}

func TestS3StoreUsesPathStyle(t *testing.T) {
	store := newTestS3Store(t, "https://storage.example.com", true, nil)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	signed, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: "a/b", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signed.URL)
	if parsed.Host != "storage.example.com" || parsed.Path != "/private-bucket/a/b" {
		t.Fatalf("path-style 地址错误: %s", signed.URL)
	}
}

func TestS3StoreUsesAliyunVirtualHost(t *testing.T) {
	store := newTestS3Store(t, "https://s3.oss-cn-hangzhou.aliyuncs.com", false, nil)
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	signed, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: "objects/key", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signed.URL)
	if parsed.Host != "private-bucket.s3.oss-cn-hangzhou.aliyuncs.com" || parsed.Path != "/objects/key" {
		t.Fatalf("阿里云 virtual-hosted 地址错误: %s", signed.URL)
	}
}

func TestS3StoreUsesBucketEndpoint(t *testing.T) {
	config := testS3Config("https://assets.example.com", false, nil)
	config.BucketEndpoint = true
	store, err := NewS3Store(config)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	signed, err := store.SignGet(context.Background(), SignGetRequest{ObjectKey: "objects/key", ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signed.URL)
	if parsed.Host != "assets.example.com" || parsed.Path != "/objects/key" {
		t.Fatalf("Bucket Endpoint 地址错误: %s", signed.URL)
	}
}

func TestS3StorePutUsesSeekableFixedLengthBodyAndReadsMetadata(t *testing.T) {
	body := "probe-body"
	digest := "bef9cc721e8ab1900858d0ae8f635d4ead757c6c61708bc70b6826429f600b17"
	putCalls := 0
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch req.Method {
		case http.MethodPut:
			putCalls++
			if req.ContentLength != int64(len(body)) || len(req.TransferEncoding) != 0 {
				t.Fatalf("PUT 未使用固定 Content-Length: length=%d encoding=%v", req.ContentLength, req.TransferEncoding)
			}
			if req.Header.Get("X-Amz-Meta-Sha256") != digest || req.Header.Get("Content-Type") != "text/plain" {
				t.Fatalf("PUT 元数据错误: %#v", req.Header)
			}
			actual, _ := io.ReadAll(req.Body)
			if string(actual) != body {
				t.Fatalf("PUT body = %q", actual)
			}
			return response(req, http.StatusOK, "", nil), nil
		case http.MethodHead:
			return response(req, http.StatusOK, "", http.Header{
				"Content-Length":    {"10"},
				"Content-Type":      {"text/plain"},
				"ETag":              {`"etag-value"`},
				"Last-Modified":     {"Tue, 21 Jul 2026 10:00:00 GMT"},
				"X-Amz-Meta-Sha256": {digest},
			}), nil
		default:
			t.Fatalf("意外请求: %s %s", req.Method, req.URL)
			return nil, nil
		}
	})}
	store := newTestS3Store(t, "https://storage.example.com", true, client)
	metadata, err := store.Put(context.Background(), PutObjectRequest{ObjectKey: "objects/key", ContentType: "text/plain", Size: int64(len(body)), SHA256: digest}, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if putCalls != 1 || metadata.SHA256 != digest || metadata.ETag != "etag-value" || metadata.Size != int64(len(body)) {
		t.Fatalf("metadata = %+v, putCalls = %d", metadata, putCalls)
	}
}

func TestS3StoreMapsErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{name: "NoSuchKey", err: &testAPIError{code: "NoSuchKey"}, want: ErrObjectNotFound},
		{name: "SlowDown", err: &testAPIError{code: "SlowDown"}, want: ErrStoreUnavailable},
		{name: "RequestTimeout", err: &testAPIError{code: "RequestTimeout"}, want: ErrStoreUnavailable},
		{name: "AccessDenied", err: &testAPIError{code: "AccessDenied"}, want: ErrStoreConfig},
		{name: "NoSuchBucket", err: &testAPIError{code: "NoSuchBucket"}, want: ErrStoreConfig},
		{name: "network timeout", err: timeoutError{}, want: ErrStoreUnavailable},
		{name: "context deadline", err: context.DeadlineExceeded, want: ErrStoreUnavailable},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyS3Error(tc.err); !errors.Is(got, tc.want) {
				t.Fatalf("classifyS3Error() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestS3StoreCheckPrivateBucketProbe(t *testing.T) {
	const probeBody = "cms-object-store-probe"
	digest := "4291c2f9a817f75cb51372195337bb8b2f0b4d7c126c53be16374ef4d212ca0f"
	requests := []string{}
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, req.Method+" "+req.URL.EscapedPath()+"?"+req.URL.RawQuery)
		query := req.URL.Query()
		switch {
		case req.Method == http.MethodHead && req.URL.Path == "/private-bucket":
			return response(req, http.StatusOK, "", nil), nil
		case req.Method == http.MethodGet && query.Has("location"):
			return response(req, http.StatusOK, `<LocationConstraint>cn-hangzhou</LocationConstraint>`, xmlHeader()), nil
		case req.Method == http.MethodGet && query.Has("acl"):
			return response(req, http.StatusOK, `<AccessControlPolicy><Owner><ID>owner</ID></Owner><AccessControlList><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="CanonicalUser"><ID>owner</ID></Grantee><Permission>FULL_CONTROL</Permission></Grant></AccessControlList></AccessControlPolicy>`, xmlHeader()), nil
		case req.Method == http.MethodPut && req.URL.RawQuery != "" && req.Header.Get("If-None-Match") == "*":
			return response(req, http.StatusPreconditionFailed, "", nil), nil
		case req.Method == http.MethodPut && req.URL.RawQuery != "":
			return response(req, http.StatusOK, "", nil), nil
		case req.Method == http.MethodGet && req.URL.RawQuery == "":
			return response(req, http.StatusForbidden, "", nil), nil
		case req.Method == http.MethodPut && req.URL.RawQuery == "":
			return response(req, http.StatusForbidden, "", nil), nil
		case req.Method == http.MethodHead:
			return response(req, http.StatusOK, "", http.Header{"Content-Length": {"22"}, "Content-Type": {"text/plain"}, "ETag": {`"probe-etag"`}, "X-Amz-Meta-Sha256": {digest}}), nil
		case req.Method == http.MethodGet:
			return response(req, http.StatusOK, probeBody, http.Header{"Content-Length": {"22"}, "Content-Type": {"text/plain"}, "ETag": {`"probe-etag"`}, "X-Amz-Meta-Sha256": {digest}}), nil
		case req.Method == http.MethodDelete:
			return response(req, http.StatusNoContent, "", nil), nil
		default:
			t.Fatalf("意外请求: %s", requests[len(requests)-1])
			return nil, nil
		}
	})}
	store := newTestS3Store(t, "https://storage.example.com", true, client)
	if err := store.CheckPrivateBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
	deleteCount := 0
	for _, request := range requests {
		if strings.HasPrefix(request, http.MethodDelete+" ") {
			deleteCount++
		}
	}
	if deleteCount != 3 {
		t.Fatalf("probe 删除次数 = %d，请求=%v", deleteCount, requests)
	}
}

func TestS3StoreCheckPrivateBucketRejectsPublicACL(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		switch {
		case req.Method == http.MethodHead:
			return response(req, http.StatusOK, "", nil), nil
		case req.URL.Query().Has("location"):
			return response(req, http.StatusOK, `<LocationConstraint>cn-hangzhou</LocationConstraint>`, xmlHeader()), nil
		case req.URL.Query().Has("acl"):
			return response(req, http.StatusOK, `<AccessControlPolicy><Owner><ID>owner</ID></Owner><AccessControlList><Grant><Grantee xmlns:xsi="http://www.w3.org/2001/XMLSchema-instance" xsi:type="Group"><URI>http://acs.amazonaws.com/groups/global/AllUsers</URI></Grantee><Permission>READ</Permission></Grant></AccessControlList></AccessControlPolicy>`, xmlHeader()), nil
		default:
			t.Fatalf("公共 ACL 后不应执行 probe: %s", req.URL)
			return nil, nil
		}
	})}
	store := newTestS3Store(t, "https://storage.example.com", true, client)
	if err := store.CheckPrivateBucket(context.Background()); !errors.Is(err, ErrStoreConfig) {
		t.Fatalf("公开 Bucket 应拒绝，得到 %v", err)
	}
}

func TestS3StoreAnonymousWriteProbeUsesMinimalHeaders(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPut || req.URL.RawQuery != "" {
			t.Fatalf("匿名写探针请求错误: %s %s", req.Method, req.URL)
		}
		if req.Header.Get("Content-Type") != "text/plain" || req.Header.Get("If-None-Match") != "" || req.Header.Get("X-Amz-Meta-Sha256") != "" {
			t.Fatalf("匿名写探针携带了预签名 Header: %#v", req.Header)
		}
		return response(req, http.StatusForbidden, "", nil), nil
	})}
	store := newTestS3Store(t, "https://storage.example.com", true, client)
	if err := store.checkAnonymousWriteDenied(context.Background(), "assets/healthchecks/anonymous"); err != nil {
		t.Fatal(err)
	}
}

func TestS3StoreDisablesUnsupportedConditionalWrites(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if req.Method != http.MethodPut || req.Header.Get("If-None-Match") != "*" {
			t.Fatalf("条件写探针错误: %s %#v", req.Method, req.Header)
		}
		return response(req, http.StatusBadRequest, "", nil), nil
	})}
	store := newTestS3Store(t, "https://storage.example.com", true, client)
	if err := store.checkPresignedOverwriteDenied(context.Background(), "assets/healthchecks/existing"); err != nil {
		t.Fatal(err)
	}
	if store.preventOverwrite {
		t.Fatal("不支持条件写时应关闭不可覆盖 Header")
	}
	now := time.Now().UTC()
	store.now = func() time.Time { return now }
	signed, err := store.SignPut(context.Background(), SignPutRequest{ObjectKey: "assets/new", ContentType: "text/plain", Size: 1, SHA256: strings.Repeat("a", 64), ExpiresAt: now.Add(time.Minute)})
	if err != nil {
		t.Fatal(err)
	}
	if signed.Headers["If-None-Match"] != "" {
		t.Fatalf("降级后不应继续返回条件写 Header: %#v", signed.Headers)
	}
}

func TestS3StoreRejectsInvalidConfiguration(t *testing.T) {
	for _, endpoint := range []string{"http://s3.example.com", "https://user@s3.example.com", "https://s3.example.com/path", "https://s3.example.com?token=secret"} {
		config := testS3Config(endpoint, false, nil)
		if _, err := NewS3Store(config); !errors.Is(err, ErrStoreConfig) {
			t.Errorf("不安全 Endpoint 应被拒绝: %s: %v", endpoint, err)
		}
	}
}

func TestS3StoreIntegration(t *testing.T) {
	if os.Getenv("S3_INTEGRATION") != "true" {
		t.Skip("设置 S3_INTEGRATION=true 后执行真实对象存储探测")
	}
	usePathStyle := os.Getenv("S3_USE_PATH_STYLE") == "true"
	bucketEndpoint := os.Getenv("S3_BUCKET_ENDPOINT") == "true"
	bucket := os.Getenv("S3_BUCKET")
	createBucket := os.Getenv("S3_INTEGRATION_CREATE_BUCKET") == "true"
	if createBucket {
		var suffix [8]byte
		if _, err := rand.Read(suffix[:]); err != nil {
			t.Fatal(err)
		}
		bucket = "cms-s3-test-" + hex.EncodeToString(suffix[:])
	}
	store, err := NewS3Store(S3Config{
		Endpoint:        os.Getenv("S3_ENDPOINT"),
		Region:          os.Getenv("S3_REGION"),
		Bucket:          bucket,
		AccessKeyID:     os.Getenv("S3_ACCESS_KEY_ID"),
		AccessKeySecret: os.Getenv("S3_ACCESS_KEY_SECRET"),
		SessionToken:    os.Getenv("S3_SESSION_TOKEN"),
		UsePathStyle:    usePathStyle,
		BucketEndpoint:  bucketEndpoint,
		UploadMaxTTL:    15 * time.Minute,
		DownloadMaxTTL:  5 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if createBucket {
		if _, err := store.client.CreateBucket(context.Background(), &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if _, err := store.client.DeleteBucket(context.Background(), &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
				t.Errorf("清理集成测试 Bucket: %v", classifyS3Error(err))
			}
		}()
	}
	if err := store.CheckPrivateBucket(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func newTestS3Store(t *testing.T, endpoint string, pathStyle bool, client *http.Client) *S3Store {
	t.Helper()
	store, err := NewS3Store(testS3Config(endpoint, pathStyle, client))
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func testS3Config(endpoint string, pathStyle bool, client *http.Client) S3Config {
	return S3Config{Endpoint: endpoint, Region: "cn-hangzhou", Bucket: "private-bucket", AccessKeyID: "id", AccessKeySecret: "secret", SessionToken: "token", UsePathStyle: pathStyle, UploadMaxTTL: 15 * time.Minute, DownloadMaxTTL: 5 * time.Minute, HTTPClient: client}
}

func response(req *http.Request, status int, body string, header http.Header) *http.Response {
	if header == nil {
		header = http.Header{}
	}
	canonical := http.Header{}
	for key, values := range header {
		for _, value := range values {
			canonical.Add(key, value)
		}
	}
	header = canonical
	contentLength := int64(len(body))
	if value := header.Get("Content-Length"); value != "" {
		switch value {
		case "10":
			contentLength = 10
		case "22":
			contentLength = 22
		}
	}
	return &http.Response{StatusCode: status, Header: header, Body: io.NopCloser(strings.NewReader(body)), ContentLength: contentLength, Request: req}
}

func xmlHeader() http.Header { return http.Header{"Content-Type": {"application/xml"}} }

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

type testAPIError struct{ code string }

func (e *testAPIError) Error() string                 { return e.code }
func (e *testAPIError) ErrorCode() string             { return e.code }
func (e *testAPIError) ErrorMessage() string          { return "redacted" }
func (e *testAPIError) ErrorFault() smithy.ErrorFault { return smithy.FaultClient }

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

var _ net.Error = timeoutError{}
