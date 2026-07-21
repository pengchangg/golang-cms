package config

import (
	"testing"
	"time"
)

func TestLoadVersionDoesNotRequireDatabase(t *testing.T) {
	t.Setenv("MYSQL_DSN", "")
	cfg, err := Load("version")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" || cfg.WebDistDir != "web/dist" || cfg.LocalLoginEnabled || !cfg.AssetsEnabled {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
}

func TestLoadServeAssetsEnabledByDefaultRequiresCompleteConfiguration(t *testing.T) {
	setValidServeEnvironment(t)
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeAssetsDisabledIgnoresS3Configuration(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("S3_ENDPOINT", "http://insecure.example.com")
	if cfg, err := Load("serve"); err != nil || cfg.AssetsEnabled {
		t.Fatalf("Load() = (%+v, %v)", cfg, err)
	}
}

func TestLoadServeAssetsEnabledRequiresCompleteConfiguration(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "true")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeAssetsEnabledLoadsTypedConfiguration(t *testing.T) {
	setValidAssetsEnvironment(t)
	t.Setenv("S3_ENDPOINT", "https://minio.example.com:9000")
	t.Setenv("S3_SESSION_TOKEN", "test-token")
	t.Setenv("S3_USE_PATH_STYLE", "true")
	t.Setenv("S3_BUCKET_ENDPOINT", "false")
	t.Setenv("S3_UPLOAD_URL_TTL", "20m")
	t.Setenv("S3_DOWNLOAD_URL_TTL", "10m")
	cfg, err := Load("serve")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AssetsEnabled || cfg.S3Endpoint != "https://minio.example.com:9000" || cfg.S3Region != "cn-hangzhou" || cfg.S3Bucket != "private-cms" || cfg.S3AccessKeyID != "test-id" || cfg.S3AccessKeySecret != "test-secret" || cfg.S3SessionToken != "test-token" || !cfg.S3UsePathStyle || cfg.S3BucketEndpoint || cfg.S3UploadTTL != 20*time.Minute || cfg.S3DownloadTTL != 10*time.Minute || cfg.WorkerConcurrency != 2 || cfg.AssetMaxSize != 10485760 || len(cfg.AssetMimeTypes) != 2 {
		t.Fatalf("unexpected assets config: %+v", cfg)
	}
}

func TestLoadServeDefaultsOptionalS3Configuration(t *testing.T) {
	setValidAssetsEnvironment(t)
	t.Setenv("S3_USE_PATH_STYLE", "")
	t.Setenv("S3_BUCKET_ENDPOINT", "")
	t.Setenv("S3_UPLOAD_URL_TTL", "")
	t.Setenv("S3_DOWNLOAD_URL_TTL", "")
	cfg, err := Load("serve")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.S3UsePathStyle || cfg.S3BucketEndpoint || cfg.S3UploadTTL != 15*time.Minute || cfg.S3DownloadTTL != 5*time.Minute {
		t.Fatalf("S3 defaults = path-style %t, TTL %s/%s", cfg.S3UsePathStyle, cfg.S3UploadTTL, cfg.S3DownloadTTL)
	}
}

func TestLoadServeRejectsInvalidS3Endpoint(t *testing.T) {
	tests := []string{
		"http://minio.example.com",
		"https://user:password@minio.example.com",
		"https://minio.example.com/bucket",
		"https://minio.example.com?bucket=value",
		"https://minio.example.com#fragment",
	}
	for _, endpoint := range tests {
		t.Run(endpoint, func(t *testing.T) {
			setValidAssetsEnvironment(t)
			t.Setenv("S3_ENDPOINT", endpoint)
			if _, err := Load("serve"); err == nil {
				t.Fatal("Load() expected an error")
			}
		})
	}
}

func TestLoadServeRejectsInvalidS3UsePathStyle(t *testing.T) {
	setValidAssetsEnvironment(t)
	t.Setenv("S3_USE_PATH_STYLE", "not-a-bool")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeRejectsConflictingS3AddressingModes(t *testing.T) {
	setValidAssetsEnvironment(t)
	t.Setenv("S3_USE_PATH_STYLE", "true")
	t.Setenv("S3_BUCKET_ENDPOINT", "true")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeLegacyOSSVariablesCannotConfigureAssets(t *testing.T) {
	setValidServeEnvironment(t)
	for _, name := range []string{"S3_ENDPOINT", "S3_REGION", "S3_BUCKET", "S3_ACCESS_KEY_ID", "S3_ACCESS_KEY_SECRET"} {
		t.Setenv(name, "")
	}
	t.Setenv("ALIYUN_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com")
	t.Setenv("ALIYUN_OSS_REGION", "cn-hangzhou")
	t.Setenv("ALIYUN_OSS_BUCKET", "private-cms")
	t.Setenv("ALIYUN_OSS_ACCESS_KEY_ID", "test-id")
	t.Setenv("ALIYUN_OSS_ACCESS_KEY_SECRET", "test-secret")
	t.Setenv("ALIYUN_OSS_SECURITY_TOKEN", "test-token")
	t.Setenv("OSS_UPLOAD_URL_TTL", "15m")
	t.Setenv("OSS_DOWNLOAD_URL_TTL", "5m")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func setValidServeEnvironment(t *testing.T) {
	t.Helper()
	t.Setenv("MYSQL_DSN", "cms:cms@tcp(localhost:3306)/cms")
	t.Setenv("APP_BASE_URL", "https://cms.example.com")
	t.Setenv("APP_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("APP_OIDC_ENABLED", "false")
}

func setValidAssetsEnvironment(t *testing.T) {
	t.Helper()
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "true")
	t.Setenv("S3_ENDPOINT", "https://s3.oss-cn-hangzhou.aliyuncs.com")
	t.Setenv("S3_REGION", "cn-hangzhou")
	t.Setenv("S3_BUCKET", "private-cms")
	t.Setenv("S3_ACCESS_KEY_ID", "test-id")
	t.Setenv("S3_ACCESS_KEY_SECRET", "test-secret")
	t.Setenv("ASSET_ALLOWED_MIME_TYPES", "image/png,text/csv")
	t.Setenv("ASSET_MAX_SIZE_BYTES", "10485760")
	t.Setenv("APP_WORKER_OWNER", "cms-test")
	t.Setenv("APP_WORKER_CONCURRENCY", "2")
	t.Setenv("APP_WORKER_POLL_INTERVAL", "1s")
	t.Setenv("APP_WORKER_LEASE_DURATION", "60s")
	t.Setenv("APP_WORKER_RENEW_INTERVAL", "20s")
}

func TestLoadServeRequiresDatabase(t *testing.T) {
	t.Setenv("MYSQL_DSN", "")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeRejectsInvalidListenAddress(t *testing.T) {
	t.Setenv("MYSQL_DSN", "cms:cms@tcp(localhost:3306)/cms")
	t.Setenv("APP_LISTEN_ADDR", "8080")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeValidatesAuthenticationConfiguration(t *testing.T) {
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("MYSQL_DSN", "cms:cms@tcp(localhost:3306)/cms")
	t.Setenv("APP_BASE_URL", "https://cms.example.com")
	t.Setenv("APP_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("OIDC_ISSUER_URL", "https://id.example.com")
	t.Setenv("OIDC_CLIENT_ID", "cms")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	t.Setenv("OIDC_REDIRECT_URL", "https://cms.example.com/api/admin/v1/auth/oidc/callback")
	if _, err := Load("serve"); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadServeAllowsHTTPBaseURL(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("APP_BASE_URL", "http://localhost:8080")
	if _, err := Load("serve"); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
}

func TestLoadServeRejectsRemoteHTTPBaseURL(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("APP_BASE_URL", "http://cms.internal.example")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeRejectsMismatchedOIDCRedirect(t *testing.T) {
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("MYSQL_DSN", "cms:cms@tcp(localhost:3306)/cms")
	t.Setenv("APP_BASE_URL", "https://cms.example.com")
	t.Setenv("APP_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("OIDC_ISSUER_URL", "https://id.example.com")
	t.Setenv("OIDC_CLIENT_ID", "cms")
	t.Setenv("OIDC_CLIENT_SECRET", "secret")
	t.Setenv("OIDC_REDIRECT_URL", "https://evil.example.com/callback")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}
