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

func TestLoadServeAssetsDisabledIgnoresOSSConfiguration(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("ALIYUN_OSS_ENDPOINT", "http://insecure.example.com")
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
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "true")
	t.Setenv("ALIYUN_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com")
	t.Setenv("ALIYUN_OSS_REGION", "cn-hangzhou")
	t.Setenv("ALIYUN_OSS_BUCKET", "private-cms")
	t.Setenv("ALIYUN_OSS_ACCESS_KEY_ID", "test-id")
	t.Setenv("ALIYUN_OSS_ACCESS_KEY_SECRET", "test-secret")
	t.Setenv("OSS_UPLOAD_URL_TTL", "15m")
	t.Setenv("OSS_DOWNLOAD_URL_TTL", "5m")
	t.Setenv("ASSET_ALLOWED_MIME_TYPES", "image/png,text/csv")
	t.Setenv("ASSET_MAX_SIZE_BYTES", "10485760")
	t.Setenv("APP_WORKER_OWNER", "cms-test")
	t.Setenv("APP_WORKER_CONCURRENCY", "2")
	t.Setenv("APP_WORKER_POLL_INTERVAL", "1s")
	t.Setenv("APP_WORKER_LEASE_DURATION", "60s")
	t.Setenv("APP_WORKER_RENEW_INTERVAL", "20s")
	cfg, err := Load("serve")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.AssetsEnabled || cfg.WorkerConcurrency != 2 || cfg.AssetMaxSize != 10485760 || len(cfg.AssetMimeTypes) != 2 {
		t.Fatalf("unexpected assets config: %+v", cfg)
	}
}

func TestLoadServeDefaultsOptionalOSSTTLs(t *testing.T) {
	setValidAssetsEnvironment(t)
	t.Setenv("OSS_UPLOAD_URL_TTL", "")
	t.Setenv("OSS_DOWNLOAD_URL_TTL", "")
	cfg, err := Load("serve")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OSSUploadTTL != 15*time.Minute || cfg.OSSDownloadTTL != 5*time.Minute {
		t.Fatalf("OSS TTL defaults = %s/%s", cfg.OSSUploadTTL, cfg.OSSDownloadTTL)
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
	t.Setenv("ALIYUN_OSS_ENDPOINT", "https://oss-cn-hangzhou.aliyuncs.com")
	t.Setenv("ALIYUN_OSS_REGION", "cn-hangzhou")
	t.Setenv("ALIYUN_OSS_BUCKET", "private-cms")
	t.Setenv("ALIYUN_OSS_ACCESS_KEY_ID", "test-id")
	t.Setenv("ALIYUN_OSS_ACCESS_KEY_SECRET", "test-secret")
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
