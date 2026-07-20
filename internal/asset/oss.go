package asset

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var ossRegion = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type OSSConfig struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
	SecurityToken   string
	UploadMaxTTL    time.Duration
	DownloadMaxTTL  time.Duration
	HTTPClient      *http.Client
}

type OSSStore struct {
	endpoint       *url.URL
	region         string
	bucket         string
	accessKeyID    string
	accessSecret   string
	securityToken  string
	uploadMaxTTL   time.Duration
	downloadMaxTTL time.Duration
	client         *http.Client
	now            func() time.Time
}

func NewOSSStore(config OSSConfig) (*OSSStore, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Port() != "" || endpoint.Path != "" {
		return nil, errors.New("OSS Endpoint 必须是无凭证和查询参数的 HTTPS URL")
	}
	if config.Region == "" || config.Bucket == "" || config.AccessKeyID == "" || config.AccessKeySecret == "" {
		return nil, errors.New("OSS 配置缺少必填项")
	}
	region := normalizeRegion(config.Region)
	host := strings.ToLower(endpoint.Hostname())
	if !ossRegion.MatchString(region) || host != "oss-"+region+".aliyuncs.com" && host != "oss-"+region+"-internal.aliyuncs.com" {
		return nil, errors.New("OSS Endpoint 与 Region 不匹配")
	}
	if config.UploadMaxTTL < time.Minute || config.UploadMaxTTL > 30*time.Minute || config.DownloadMaxTTL < time.Minute || config.DownloadMaxTTL > 15*time.Minute {
		return nil, errors.New("OSS 签名有效期上限超出冻结范围")
	}
	client := config.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *client
		client = &clone
	}
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && (via[len(via)-1].Header.Get("Authorization") != "" || via[len(via)-1].Header.Get("x-oss-security-token") != "") {
			return http.ErrUseLastResponse
		}
		if len(via) > 0 && (req.URL.Scheme != "https" || !strings.EqualFold(req.URL.Host, via[0].URL.Host)) {
			return http.ErrUseLastResponse
		}
		if previousRedirect != nil {
			return previousRedirect(req, via)
		}
		return nil
	}
	endpoint.Path = strings.TrimSuffix(endpoint.Path, "/")
	return &OSSStore{endpoint: endpoint, region: region, bucket: config.Bucket, accessKeyID: config.AccessKeyID, accessSecret: config.AccessKeySecret, securityToken: config.SecurityToken, uploadMaxTTL: config.UploadMaxTTL, downloadMaxTTL: config.DownloadMaxTTL, client: client, now: func() time.Time { return time.Now().UTC() }}, nil
}

func (s *OSSStore) SignPut(_ context.Context, input SignPutRequest) (SignedRequest, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil {
		return SignedRequest{}, err
	}
	if input.Size < 0 || input.ContentType == "" || len(input.SHA256) != 64 {
		return SignedRequest{}, ErrStoreConfig
	}
	headers := map[string]string{"Content-Type": input.ContentType, "x-oss-meta-sha256": input.SHA256, "x-oss-forbid-overwrite": "true"}
	return s.sign("PUT", input.ObjectKey, input.ExpiresAt, s.uploadMaxTTL, headers, nil)
}

func (s *OSSStore) SignGet(_ context.Context, input SignGetRequest) (SignedRequest, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil {
		return SignedRequest{}, err
	}
	query := url.Values{}
	if input.DownloadFilename != "" {
		query.Set("response-content-disposition", contentDisposition(input.DownloadFilename))
	}
	return s.sign("GET", input.ObjectKey, input.ExpiresAt, s.downloadMaxTTL, map[string]string{}, query)
}

func (s *OSSStore) sign(method, objectKey string, expiresAt time.Time, maxTTL time.Duration, headers map[string]string, query url.Values) (SignedRequest, error) {
	now := s.now().UTC()
	ttl := expiresAt.Sub(now)
	if ttl <= 0 || ttl > maxTTL {
		return SignedRequest{}, ErrStoreConfig
	}
	seconds := int64(ttl / time.Second)
	if seconds < 1 {
		return SignedRequest{}, ErrStoreConfig
	}
	requestURL := s.objectURL(objectKey)
	if query == nil {
		query = url.Values{}
	}
	date := now.Format("20060102")
	credential := s.accessKeyID + "/" + date + "/" + s.region + "/oss/aliyun_v4_request"
	query.Set("x-oss-signature-version", "OSS4-HMAC-SHA256")
	query.Set("x-oss-credential", credential)
	query.Set("x-oss-date", now.Format("20060102T150405Z"))
	query.Set("x-oss-expires", strconv.FormatInt(seconds, 10))
	if s.securityToken != "" {
		query.Set("x-oss-security-token", s.securityToken)
	}
	canonicalHeaders, signedHeaders := canonicalHeaders(headers)
	if signedHeaders != "" {
		query.Set("x-oss-additional-headers", signedHeaders)
	}
	canonicalRequest := method + "\n" + escapePath("/"+s.bucket+requestURL.Path) + "\n" + canonicalQuery(query) + "\n" + canonicalHeaders + "\n" + signedHeaders + "\nUNSIGNED-PAYLOAD"
	scope := date + "/" + s.region + "/oss/aliyun_v4_request"
	requestHash := sha256.Sum256([]byte(canonicalRequest))
	stringToSign := "OSS4-HMAC-SHA256\n" + now.Format("20060102T150405Z") + "\n" + scope + "\n" + hex.EncodeToString(requestHash[:])
	key := hmacDigest([]byte("aliyun_v4"+s.accessSecret), date)
	key = hmacDigest(key, s.region)
	key = hmacDigest(key, "oss")
	key = hmacDigest(key, "aliyun_v4_request")
	query.Set("x-oss-signature", hex.EncodeToString(hmacDigest(key, stringToSign)))
	requestURL.RawQuery = query.Encode()
	return SignedRequest{Method: method, URL: requestURL.String(), Headers: headers, ExpiresAt: expiresAt.UTC()}, nil
}

func (s *OSSStore) Head(ctx context.Context, objectKey string) (ObjectMetadata, error) {
	response, err := s.do(ctx, "HEAD", objectKey, nil, nil, 0)
	if err != nil {
		return ObjectMetadata{}, err
	}
	defer response.Body.Close()
	return responseMetadata(objectKey, response)
}

func (s *OSSStore) Get(ctx context.Context, objectKey string) (io.ReadCloser, ObjectMetadata, error) {
	response, err := s.do(ctx, "GET", objectKey, nil, nil, 0)
	if err != nil {
		return nil, ObjectMetadata{}, err
	}
	metadata, err := responseMetadata(objectKey, response)
	if err != nil {
		response.Body.Close()
		return nil, ObjectMetadata{}, err
	}
	return response.Body, metadata, nil
}

func (s *OSSStore) Put(ctx context.Context, input PutObjectRequest, body io.Reader) (ObjectMetadata, error) {
	if input.Size < 0 || len(input.SHA256) != 64 {
		return ObjectMetadata{}, ErrStoreConfig
	}
	temporary, err := os.CreateTemp("", "cms-oss-put-*")
	if err != nil {
		return ObjectMetadata{}, ErrStoreUnavailable
	}
	defer func() {
		name := temporary.Name()
		_ = temporary.Close()
		_ = os.Remove(name)
	}()
	digest := sha256.New()
	written, err := io.Copy(io.MultiWriter(temporary, digest), io.LimitReader(body, input.Size+1))
	if err != nil {
		return ObjectMetadata{}, ErrStoreUnavailable
	}
	if written != input.Size || hex.EncodeToString(digest.Sum(nil)) != input.SHA256 {
		return ObjectMetadata{}, ErrStoreConfig
	}
	if _, err := temporary.Seek(0, io.SeekStart); err != nil {
		return ObjectMetadata{}, ErrStoreUnavailable
	}
	headers := http.Header{"Content-Type": {input.ContentType}, "x-oss-meta-sha256": {input.SHA256}}
	response, err := s.do(ctx, "PUT", input.ObjectKey, headers, temporary, input.Size)
	if err != nil {
		return ObjectMetadata{}, err
	}
	if err := response.Body.Close(); err != nil {
		return ObjectMetadata{}, ErrStoreUnavailable
	}
	return s.Head(ctx, input.ObjectKey)
}

func (s *OSSStore) Delete(ctx context.Context, objectKey string) error {
	response, err := s.do(ctx, "DELETE", objectKey, nil, nil, 0)
	if err != nil {
		return err
	}
	return response.Body.Close()
}

// CheckPrivateBucket 在启动阶段校验地域和 ACL；适配器不会创建 Bucket 或修改 ACL。
func (s *OSSStore) CheckPrivateBucket(ctx context.Context) error {
	response, err := s.doBucket(ctx, "GET", url.Values{"acl": {""}})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var acl struct {
		Grant string `xml:"AccessControlList>Grant"`
	}
	if err := xml.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&acl); err != nil || acl.Grant != "private" {
		return ErrStoreConfig
	}
	response, err = s.doBucket(ctx, "GET", url.Values{"location": {""}})
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var location string
	if err := xml.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&location); err != nil || normalizeRegion(location) != normalizeRegion(s.region) {
		return ErrStoreConfig
	}
	const probeBody = "cms-object-store-probe"
	digest := sha256.Sum256([]byte(probeBody))
	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrStoreConfig
	}
	probeKey := "healthchecks/" + hex.EncodeToString(random[:])
	metadata, err := s.Put(ctx, PutObjectRequest{ObjectKey: probeKey, ContentType: "text/plain", Size: int64(len(probeBody)), SHA256: hex.EncodeToString(digest[:])}, strings.NewReader(probeBody))
	if err != nil {
		return err
	}
	defer s.Delete(context.WithoutCancel(ctx), probeKey)
	if metadata.Size != int64(len(probeBody)) || metadata.SHA256 != hex.EncodeToString(digest[:]) {
		return ErrStoreConfig
	}
	body, getMetadata, err := s.Get(ctx, probeKey)
	if err != nil {
		return err
	}
	actual, readErr := io.ReadAll(io.LimitReader(body, int64(len(probeBody))+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(actual) != probeBody || getMetadata.SHA256 != metadata.SHA256 {
		return ErrStoreConfig
	}
	if err := s.Delete(ctx, probeKey); err != nil {
		return err
	}
	return nil
}

func (s *OSSStore) do(ctx context.Context, method, objectKey string, headers http.Header, body io.Reader, size int64) (*http.Response, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return nil, err
	}
	return s.doURL(ctx, method, s.objectURL(objectKey), headers, body, size)
}

func (s *OSSStore) doBucket(ctx context.Context, method string, query url.Values) (*http.Response, error) {
	u := *s.endpoint
	u.Host = s.bucket + "." + s.endpoint.Host
	u.RawQuery = query.Encode()
	return s.doURL(ctx, method, &u, nil, nil, 0)
}

func (s *OSSStore) doURL(ctx context.Context, method string, u *url.URL, headers http.Header, body io.Reader, size int64) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, u.String(), body)
	if err != nil {
		return nil, ErrStoreConfig
	}
	for key, values := range headers {
		req.Header[key] = append([]string(nil), values...)
	}
	if body != nil {
		req.ContentLength = size
	}
	s.authorize(req)
	response, err := s.client.Do(req)
	if err != nil {
		return nil, ErrStoreUnavailable
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return response, nil
	}
	response.Body.Close()
	return nil, classifyStatus(response.StatusCode)
}

func (s *OSSStore) authorize(req *http.Request) {
	now := s.now().UTC()
	req.Header.Set("Date", now.Format(http.TimeFormat))
	if s.securityToken != "" {
		req.Header.Set("x-oss-security-token", s.securityToken)
	}
	resource := "/" + s.bucket + req.URL.EscapedPath()
	if req.URL.RawQuery != "" {
		resource += "?" + strings.TrimSuffix(req.URL.RawQuery, "=")
	}
	canonical := req.Method + "\n\n" + req.Header.Get("Content-Type") + "\n" + req.Header.Get("Date") + "\n" + canonicalOSSHeaders(req.Header) + resource
	mac := hmac.New(sha1.New, []byte(s.accessSecret))
	_, _ = mac.Write([]byte(canonical))
	signature := mac.Sum(nil)
	req.Header.Set("Authorization", "OSS "+s.accessKeyID+":"+encodeBase64(signature))
}

func (s *OSSStore) objectURL(objectKey string) *url.URL {
	u := *s.endpoint
	u.Host = s.bucket + "." + s.endpoint.Host
	u.Path = strings.TrimSuffix(s.endpoint.Path, "/") + "/" + objectKey
	return &u
}
func validateObjectKey(value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, "\x00\r\n") {
		return ErrStoreConfig
	}
	return nil
}
func classifyStatus(status int) error {
	if status == http.StatusNotFound {
		return ErrObjectNotFound
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return ErrStoreUnavailable
	}
	return ErrStoreConfig
}
func responseMetadata(key string, response *http.Response) (ObjectMetadata, error) {
	size, err := strconv.ParseInt(response.Header.Get("Content-Length"), 10, 64)
	if err != nil || size < 0 {
		return ObjectMetadata{}, ErrStoreConfig
	}
	modified, _ := http.ParseTime(response.Header.Get("Last-Modified"))
	etag := strings.Trim(response.Header.Get("ETag"), `"`)
	if etag == "" {
		return ObjectMetadata{}, ErrStoreConfig
	}
	return ObjectMetadata{ObjectKey: key, Size: size, ContentType: strings.ToLower(response.Header.Get("Content-Type")), SHA256: response.Header.Get("x-oss-meta-sha256"), ETag: etag, LastModified: modified.UTC()}, nil
}
func contentDisposition(filename string) string {
	cleaned := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, filename)
	return `attachment; filename="download"; filename*=UTF-8''` + url.PathEscape(cleaned)
}
func escapePath(value string) string {
	escaped := url.PathEscape(value)
	return strings.ReplaceAll(escaped, "%2F", "/")
}
func canonicalQuery(values url.Values) string {
	keys := make([]string, 0, len(values))
	encoded := make(map[string]string, len(values))
	for key := range values {
		encoded[key] = queryEscape(key)
		keys = append(keys, key)
	}
	sortStringsBy(keys, func(value string) string { return encoded[value] })
	parts := make([]string, 0, len(values))
	for _, key := range keys {
		items := make([]string, len(values[key]))
		for i, value := range values[key] {
			items[i] = queryEscape(value)
		}
		sortStrings(items)
		for _, value := range items {
			parts = append(parts, encoded[key]+"="+value)
		}
	}
	return strings.Join(parts, "&")
}
func queryEscape(value string) string {
	return strings.ReplaceAll(url.QueryEscape(value), "+", "%20")
}
func canonicalHeaders(headers map[string]string) (string, string) {
	keys := make([]string, 0, len(headers))
	normalized := map[string]string{}
	for key, value := range headers {
		lower := strings.ToLower(key)
		keys = append(keys, lower)
		normalized[lower] = strings.TrimSpace(value)
	}
	sortStrings(keys)
	lines := make([]string, len(keys))
	for i, key := range keys {
		lines[i] = key + ":" + normalized[key]
	}
	return strings.Join(lines, "\n") + boolSuffix(len(lines) > 0, "\n"), strings.Join(keys, ";")
}
func canonicalOSSHeaders(headers http.Header) string {
	keys := []string{}
	for key := range headers {
		if strings.HasPrefix(strings.ToLower(key), "x-oss-") {
			keys = append(keys, strings.ToLower(key))
		}
	}
	sortStrings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(key)
		b.WriteByte(':')
		b.WriteString(strings.TrimSpace(headers.Get(key)))
		b.WriteByte('\n')
	}
	return b.String()
}
func hmacDigest(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}
func encodeBase64(value []byte) string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789+/"
	result := make([]byte, (len(value)+2)/3*4)
	for i, j := 0, 0; i < len(value); i, j = i+3, j+4 {
		n := uint(value[i]) << 16
		remain := len(value) - i
		if remain > 1 {
			n |= uint(value[i+1]) << 8
		}
		if remain > 2 {
			n |= uint(value[i+2])
		}
		result[j], result[j+1] = chars[(n>>18)&63], chars[(n>>12)&63]
		result[j+2], result[j+3] = '=', '='
		if remain > 1 {
			result[j+2] = chars[(n>>6)&63]
		}
		if remain > 2 {
			result[j+3] = chars[n&63]
		}
	}
	return string(result)
}
func sortStrings(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
func sortStringsBy(values []string, key func(string) string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && key(values[j]) < key(values[j-1]); j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
func boolSuffix(ok bool, value string) string {
	if ok {
		return value
	}
	return ""
}
func normalizeRegion(value string) string {
	return strings.TrimPrefix(strings.TrimSpace(value), "oss-")
}
