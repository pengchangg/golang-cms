package asset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/aws/smithy-go"
	smithyendpoints "github.com/aws/smithy-go/endpoints"
	"github.com/aws/smithy-go/middleware"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

var s3Region = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	AccessKeyID     string
	AccessKeySecret string
	SessionToken    string
	UsePathStyle    bool
	BucketEndpoint  bool
	UploadMaxTTL    time.Duration
	DownloadMaxTTL  time.Duration
	HTTPClient      *http.Client
}

type S3Store struct {
	bucket           string
	region           string
	uploadMaxTTL     time.Duration
	downloadMaxTTL   time.Duration
	preventOverwrite bool
	client           *s3.Client
	presigner        *s3.PresignClient
	httpClient       *http.Client
	now              func() time.Time
}

type bucketEndpointResolver struct {
	endpoint url.URL
	fallback s3.EndpointResolverV2
}

func (r bucketEndpointResolver) ResolveEndpoint(ctx context.Context, params s3.EndpointParameters) (smithyendpoints.Endpoint, error) {
	resolved, err := r.fallback.ResolveEndpoint(ctx, params)
	if err != nil {
		return smithyendpoints.Endpoint{}, err
	}
	resolved.URI.Scheme = r.endpoint.Scheme
	resolved.URI.Host = r.endpoint.Host
	resolved.URI.Path = r.endpoint.Path
	return resolved, nil
}

func NewS3Store(config S3Config) (*S3Store, error) {
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.Host == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Path != "" {
		return nil, ErrStoreConfig
	}
	region := strings.TrimSpace(config.Region)
	if !s3Region.MatchString(region) || config.Bucket == "" || strings.ContainsAny(config.Bucket, "\x00\r\n/") || config.AccessKeyID == "" || config.AccessKeySecret == "" {
		return nil, ErrStoreConfig
	}
	if config.UploadMaxTTL <= 0 || config.UploadMaxTTL > 7*24*time.Hour || config.DownloadMaxTTL <= 0 || config.DownloadMaxTTL > 7*24*time.Hour {
		return nil, ErrStoreConfig
	}

	httpClient := config.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	} else {
		clone := *httpClient
		httpClient = &clone
	}
	httpClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	loadOptions := []func(*awsconfig.LoadOptions) error{
		awsconfig.WithRegion(region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(config.AccessKeyID, config.AccessKeySecret, config.SessionToken)),
		awsconfig.WithHTTPClient(httpClient),
		awsconfig.WithRequestChecksumCalculation(aws.RequestChecksumCalculationWhenRequired),
	}
	awsConfig, err := awsconfig.LoadDefaultConfig(context.Background(), loadOptions...)
	if err != nil {
		return nil, ErrStoreConfig
	}
	client := s3.NewFromConfig(awsConfig, func(options *s3.Options) {
		options.BaseEndpoint = aws.String(config.Endpoint)
		options.UsePathStyle = config.UsePathStyle
		if config.BucketEndpoint {
			options.EndpointResolverV2 = bucketEndpointResolver{endpoint: *endpoint, fallback: s3.NewDefaultEndpointResolverV2()}
		}
	})
	return &S3Store{
		bucket:           config.Bucket,
		region:           region,
		uploadMaxTTL:     config.UploadMaxTTL,
		downloadMaxTTL:   config.DownloadMaxTTL,
		preventOverwrite: true,
		client:           client,
		presigner:        s3.NewPresignClient(client),
		httpClient:       httpClient,
		now:              func() time.Time { return time.Now().UTC() },
	}, nil
}

func (s *S3Store) SignPut(ctx context.Context, input SignPutRequest) (SignedRequest, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil {
		return SignedRequest{}, err
	}
	if input.Size < 0 || input.ContentType == "" || len(input.SHA256) != 64 {
		return SignedRequest{}, ErrStoreConfig
	}
	ttl, err := s.presignTTL(input.ExpiresAt, s.uploadMaxTTL)
	if err != nil {
		return SignedRequest{}, err
	}
	request, err := s.presigner.PresignPutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(input.ObjectKey),
		ContentType: aws.String(input.ContentType),
		Metadata:    map[string]string{"sha256": input.SHA256},
	}, func(options *s3.PresignOptions) {
		options.Expires = ttl
		options.ClientOptions = append(options.ClientOptions, func(clientOptions *s3.Options) {
			clientOptions.APIOptions = append(clientOptions.APIOptions, bindPresignedPutHeaders(input.ContentType, s.preventOverwrite))
		})
	})
	if err != nil {
		return SignedRequest{}, classifyS3Error(err)
	}
	return signedRequest(request.Method, request.URL, request.SignedHeader, input.ExpiresAt), nil
}

func bindPresignedPutHeaders(contentType string, preventOverwrite bool) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Finalize.Add(middleware.FinalizeMiddlewareFunc("BindPresignedContentType", func(ctx context.Context, input middleware.FinalizeInput, next middleware.FinalizeHandler) (middleware.FinalizeOutput, middleware.Metadata, error) {
			request, ok := input.Request.(*smithyhttp.Request)
			if !ok {
				return middleware.FinalizeOutput{}, middleware.Metadata{}, ErrStoreConfig
			}
			request.Header.Set("Content-Type", contentType)
			if preventOverwrite {
				request.Header.Set("If-None-Match", "*")
			}
			return next.HandleFinalize(ctx, input)
		}), middleware.Before)
	}
}

func (s *S3Store) SignGet(ctx context.Context, input SignGetRequest) (SignedRequest, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil {
		return SignedRequest{}, err
	}
	ttl, err := s.presignTTL(input.ExpiresAt, s.downloadMaxTTL)
	if err != nil {
		return SignedRequest{}, err
	}
	requestInput := &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(input.ObjectKey)}
	if input.DownloadFilename != "" && (input.Disposition == "" || input.Disposition == DispositionAttachment) {
		requestInput.ResponseContentDisposition = aws.String(contentDisposition(input.DownloadFilename))
	}
	request, err := s.presigner.PresignGetObject(ctx, requestInput, func(options *s3.PresignOptions) { options.Expires = ttl })
	if err != nil {
		return SignedRequest{}, classifyS3Error(err)
	}
	return signedRequest(request.Method, request.URL, request.SignedHeader, input.ExpiresAt), nil
}

func (s *S3Store) presignTTL(expiresAt time.Time, maxTTL time.Duration) (time.Duration, error) {
	ttl := expiresAt.Sub(s.now().UTC())
	if ttl < time.Second || ttl > maxTTL {
		return 0, ErrStoreConfig
	}
	return ttl, nil
}

func signedRequest(method, requestURL string, signedHeaders http.Header, expiresAt time.Time) SignedRequest {
	headers := make(map[string]string, len(signedHeaders))
	for key, values := range signedHeaders {
		headers[key] = strings.Join(values, ",")
	}
	return SignedRequest{Method: method, URL: requestURL, Headers: headers, ExpiresAt: expiresAt.UTC()}
}

func (s *S3Store) Head(ctx context.Context, objectKey string) (ObjectMetadata, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return ObjectMetadata{}, err
	}
	output, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	if err != nil {
		return ObjectMetadata{}, classifyS3Error(err)
	}
	return objectMetadata(objectKey, output.ContentLength, output.ContentType, output.Metadata, output.ETag, output.LastModified)
}

func (s *S3Store) Get(ctx context.Context, objectKey string) (io.ReadCloser, ObjectMetadata, error) {
	if err := validateObjectKey(objectKey); err != nil {
		return nil, ObjectMetadata{}, err
	}
	output, err := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	if err != nil {
		return nil, ObjectMetadata{}, classifyS3Error(err)
	}
	metadata, err := objectMetadata(objectKey, output.ContentLength, output.ContentType, output.Metadata, output.ETag, output.LastModified)
	if err != nil {
		output.Body.Close()
		return nil, ObjectMetadata{}, err
	}
	return output.Body, metadata, nil
}

func (s *S3Store) Put(ctx context.Context, input PutObjectRequest, body io.Reader) (ObjectMetadata, error) {
	if err := validateObjectKey(input.ObjectKey); err != nil {
		return ObjectMetadata{}, err
	}
	if input.Size < 0 || input.ContentType == "" || len(input.SHA256) != 64 {
		return ObjectMetadata{}, ErrStoreConfig
	}
	temporary, err := os.CreateTemp("", "cms-s3-put-*")
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
	_, err = s.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:        aws.String(s.bucket),
		Key:           aws.String(input.ObjectKey),
		Body:          temporary,
		ContentLength: aws.Int64(input.Size),
		ContentType:   aws.String(input.ContentType),
		Metadata:      map[string]string{"sha256": input.SHA256},
	})
	if err != nil {
		return ObjectMetadata{}, classifyS3Error(err)
	}
	return s.Head(ctx, input.ObjectKey)
}

func (s *S3Store) Delete(ctx context.Context, objectKey string) error {
	if err := validateObjectKey(objectKey); err != nil {
		return err
	}
	_, err := s.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: aws.String(s.bucket), Key: aws.String(objectKey)})
	return classifyS3Error(err)
}

// CheckPrivateBucket 在启动阶段校验 Bucket 地域、ACL 和对象读写能力，不修改 Bucket 配置。
func (s *S3Store) CheckPrivateBucket(ctx context.Context) error {
	if _, err := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(s.bucket)}); err != nil {
		return storeOperationError("检查 Bucket 访问", err)
	}
	location, err := s.client.GetBucketLocation(ctx, &s3.GetBucketLocationInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		if !regionsMatch(s.region, string(location.LocationConstraint)) {
			return fmt.Errorf("检查 Bucket Region: %w", ErrStoreConfig)
		}
	} else if !bucketMetadataCheckUnsupported(err) {
		return storeOperationError("检查 Bucket Region", err)
	}
	acl, err := s.client.GetBucketAcl(ctx, &s3.GetBucketAclInput{Bucket: aws.String(s.bucket)})
	if err == nil {
		if hasPublicGrant(acl.Grants) {
			return ErrStoreConfig
		}
	} else if !aclCheckUnsupported(err) {
		return storeOperationError("检查 Bucket ACL", err)
	}

	var random [16]byte
	if _, err := rand.Read(random[:]); err != nil {
		return ErrStoreConfig
	}
	suffix := hex.EncodeToString(random[:])
	probeKeys := []string{
		"assets/healthchecks/" + suffix,
		"transfers/uploads/healthchecks/" + suffix + ".csv",
		"transfers/healthchecks/attempt-1-" + suffix + "/result.csv",
	}
	for index, probeKey := range probeKeys {
		if err := s.checkObjectPrefix(ctx, probeKey, index == 0); err != nil {
			return err
		}
	}
	return nil
}

func (s *S3Store) checkObjectPrefix(ctx context.Context, probeKey string, verifyNoOverwrite bool) error {
	const probeBody = "cms-object-store-probe"
	digest := sha256.Sum256([]byte(probeBody))
	sha := hex.EncodeToString(digest[:])
	cleanupPending := true
	defer func() {
		if cleanupPending {
			_ = s.Delete(context.WithoutCancel(ctx), probeKey)
		}
	}()
	metadata, err := s.Put(ctx, PutObjectRequest{ObjectKey: probeKey, ContentType: "text/plain", Size: int64(len(probeBody)), SHA256: sha}, strings.NewReader(probeBody))
	if err != nil {
		return fmt.Errorf("写入对象存储探针: %w", err)
	}
	if metadata.Size != int64(len(probeBody)) || metadata.SHA256 != sha {
		return ErrStoreConfig
	}
	if err := s.checkAnonymousReadDenied(ctx, probeKey); err != nil {
		return fmt.Errorf("检查匿名读取: %w", err)
	}
	if err := s.checkAnonymousWriteDenied(ctx, probeKey+"-anonymous"); err != nil {
		return fmt.Errorf("检查匿名写入: %w", err)
	}
	if verifyNoOverwrite {
		if err := s.checkPresignedOverwriteDenied(ctx, probeKey); err != nil {
			return fmt.Errorf("检查预签名不可覆盖: %w", err)
		}
	}
	body, getMetadata, err := s.Get(ctx, probeKey)
	if err != nil {
		return fmt.Errorf("读取对象存储探针: %w", err)
	}
	actual, readErr := io.ReadAll(io.LimitReader(body, int64(len(probeBody))+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil || string(actual) != probeBody || getMetadata.SHA256 != metadata.SHA256 {
		return ErrStoreConfig
	}
	cleanupPending = false
	if err := s.Delete(ctx, probeKey); err != nil {
		return fmt.Errorf("删除对象存储探针: %w", err)
	}
	return nil
}

func (s *S3Store) checkPresignedOverwriteDenied(ctx context.Context, objectKey string) error {
	const replacement = "cms-overwrite-must-be-denied"
	digest := sha256.Sum256([]byte(replacement))
	signed, err := s.SignPut(ctx, SignPutRequest{ObjectKey: objectKey, ContentType: "text/plain", Size: int64(len(replacement)), SHA256: hex.EncodeToString(digest[:]), ExpiresAt: s.now().Add(time.Minute)})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, signed.Method, signed.URL, strings.NewReader(replacement))
	if err != nil {
		return ErrStoreConfig
	}
	for key, value := range signed.Headers {
		request.Header.Set(key, value)
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return ErrStoreUnavailable
	}
	response.Body.Close()
	if response.StatusCode == http.StatusConflict || response.StatusCode == http.StatusPreconditionFailed {
		return nil
	}
	if response.StatusCode == http.StatusBadRequest || response.StatusCode == http.StatusMethodNotAllowed || response.StatusCode == http.StatusNotImplemented {
		s.preventOverwrite = false
		return nil
	}
	if response.StatusCode >= 500 || response.StatusCode == http.StatusTooManyRequests || response.StatusCode == http.StatusRequestTimeout {
		return ErrStoreUnavailable
	}
	return ErrStoreConfig
}

func storeOperationError(operation string, err error) error {
	var apiError smithy.APIError
	if errors.As(err, &apiError) {
		code := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-' {
				return r
			}
			return -1
		}, apiError.ErrorCode())
		if code != "" {
			return fmt.Errorf("%s（%s）: %w", operation, code, classifyS3Error(err))
		}
	}
	return fmt.Errorf("%s: %w", operation, classifyS3Error(err))
}

func bucketMetadataCheckUnsupported(err error) bool {
	return aclCheckUnsupported(err)
}

func aclCheckUnsupported(err error) bool {
	var apiError smithy.APIError
	if !errors.As(err, &apiError) {
		return false
	}
	switch strings.ToLower(apiError.ErrorCode()) {
	case "accessdenied", "methodnotallowed", "notimplemented", "unsupportedoperation", "xnotimplemented":
		return true
	default:
		return false
	}
}

func (s *S3Store) checkAnonymousReadDenied(ctx context.Context, objectKey string) error {
	signed, err := s.SignGet(ctx, SignGetRequest{ObjectKey: objectKey, ExpiresAt: s.now().Add(time.Minute)})
	if err != nil {
		return err
	}
	requestURL, err := url.Parse(signed.URL)
	if err != nil {
		return ErrStoreConfig
	}
	requestURL.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL.String(), nil)
	if err != nil {
		return ErrStoreConfig
	}
	response, err := s.httpClient.Do(request)
	if err != nil {
		return ErrStoreUnavailable
	}
	response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return ErrStoreConfig
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return nil
	}
	return classifyS3Status(response.StatusCode)
}

func (s *S3Store) checkAnonymousWriteDenied(ctx context.Context, objectKey string) error {
	const body = "cms-anonymous-write-probe"
	digest := sha256.Sum256([]byte(body))
	signed, err := s.SignPut(ctx, SignPutRequest{ObjectKey: objectKey, ContentType: "text/plain", Size: int64(len(body)), SHA256: hex.EncodeToString(digest[:]), ExpiresAt: s.now().Add(time.Minute)})
	if err != nil {
		return err
	}
	requestURL, err := url.Parse(signed.URL)
	if err != nil {
		return ErrStoreConfig
	}
	requestURL.RawQuery = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodPut, requestURL.String(), strings.NewReader(body))
	if err != nil {
		return ErrStoreConfig
	}
	request.Header.Set("Content-Type", "text/plain")
	response, err := s.httpClient.Do(request)
	if err != nil {
		return ErrStoreUnavailable
	}
	response.Body.Close()
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		_ = s.Delete(context.WithoutCancel(ctx), objectKey)
		return ErrStoreConfig
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return nil
	}
	return classifyS3Status(response.StatusCode)
}

func objectMetadata(key string, size *int64, contentType *string, metadata map[string]string, etag *string, modified *time.Time) (ObjectMetadata, error) {
	if size == nil || *size < 0 || etag == nil || strings.Trim(*etag, `"`) == "" {
		return ObjectMetadata{}, ErrStoreConfig
	}
	lastModified := time.Time{}
	if modified != nil {
		lastModified = modified.UTC()
	}
	return ObjectMetadata{
		ObjectKey:    key,
		Size:         *size,
		ContentType:  strings.ToLower(aws.ToString(contentType)),
		SHA256:       metadata["sha256"],
		ETag:         strings.Trim(*etag, `"`),
		LastModified: lastModified,
	}, nil
}

func hasPublicGrant(grants []types.Grant) bool {
	for _, grant := range grants {
		if grant.Grantee == nil || grant.Grantee.URI == nil {
			continue
		}
		uri := strings.ToLower(*grant.Grantee.URI)
		if strings.Contains(uri, "allusers") || strings.Contains(uri, "authenticatedusers") {
			return true
		}
	}
	return false
}

func regionsMatch(configured, actual string) bool {
	actual = strings.TrimSpace(actual)
	if actual == "" {
		actual = "us-east-1"
	} else if strings.EqualFold(actual, "EU") {
		actual = "eu-west-1"
	}
	return strings.EqualFold(strings.TrimPrefix(configured, "oss-"), strings.TrimPrefix(actual, "oss-"))
}

func classifyS3Error(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ErrStoreUnavailable
	}
	var networkError net.Error
	if errors.As(err, &networkError) {
		return ErrStoreUnavailable
	}

	status := 0
	var responseError *smithyhttp.ResponseError
	if errors.As(err, &responseError) {
		status = responseError.HTTPStatusCode()
	}
	var apiError smithy.APIError
	code := ""
	if errors.As(err, &apiError) {
		code = strings.ToLower(apiError.ErrorCode())
	}
	switch code {
	case "nosuchbucket", "invalidbucketname", "accessdenied", "invalidaccesskeyid", "signaturedoesnotmatch", "authorizationheadermalformed", "expiredtoken", "invalidtoken", "permanentredirect":
		return ErrStoreConfig
	}
	if status == http.StatusNotFound || code == "nosuchkey" || code == "notfound" {
		return ErrObjectNotFound
	}
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 || code == "requesttimeout" || code == "slowdown" || code == "throttling" || code == "throttlingexception" {
		return ErrStoreUnavailable
	}
	return ErrStoreConfig
}

func classifyS3Status(status int) error {
	if status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500 {
		return ErrStoreUnavailable
	}
	return ErrStoreConfig
}

func validateObjectKey(value string) error {
	if value == "" || strings.HasPrefix(value, "/") || strings.Contains(value, "..") || strings.ContainsAny(value, "\x00\r\n") {
		return ErrStoreConfig
	}
	return nil
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
