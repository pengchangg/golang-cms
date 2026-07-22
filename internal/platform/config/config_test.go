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
	if !cfg.AssetsEnabled || cfg.S3Endpoint != "https://minio.example.com:9000" || cfg.S3Region != "cn-hangzhou" || cfg.S3Bucket != "private-cms" || cfg.S3AccessKeyID != "test-id" || cfg.S3AccessKeySecret != "test-secret" || cfg.S3SessionToken != "test-token" || !cfg.S3UsePathStyle || cfg.S3BucketEndpoint || cfg.S3UploadTTL != 20*time.Minute || cfg.S3DownloadTTL != 10*time.Minute || cfg.AssetMaxSize != 10485760 || len(cfg.AssetMimeTypes) != 2 {
		t.Fatalf("unexpected assets config: %+v", cfg)
	}
}

func TestLoadServeRejectsAssetSizeAboveSynchronousConfirmationLimit(t *testing.T) {
	setValidAssetsEnvironment(t)
	t.Setenv("ASSET_MAX_SIZE_BYTES", "104857601")
	if _, err := Load("serve"); err == nil {
		t.Fatal("超过 100 MiB 的素材配置应被拒绝")
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
	t.Setenv("APP_LISTEN_ADDR", "127.0.0.1:8080")
	t.Setenv("APP_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("APP_SESSION_SECRET", "01234567890123456789012345678901")
	t.Setenv("APP_ENV", "development")
	t.Setenv("SMS_PROVIDER", "fixed")
	t.Setenv("DEV_SMS_FIXED_CODE", "123456")
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
	t.Setenv("APP_ENV", "production")
	t.Setenv("SMS_PROVIDER", "tencent")
	t.Setenv("TENCENTCLOUD_SECRET_ID", "id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "secret")
	t.Setenv("TENCENT_SMS_REGION", "ap-guangzhou")
	t.Setenv("TENCENT_SMS_SDK_APP_ID", "1400000000")
	t.Setenv("TENCENT_SMS_SIGN_NAME", "测试签名")
	t.Setenv("TENCENT_SMS_TEMPLATE_ID", "123456")
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

func TestLoadServeRejectsFixedSMSOutsideDevelopment(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("APP_ENV", "production")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestLoadServeRejectsFixedSMSOnNonLoopbackEndpoints(t *testing.T) {
	for _, modify := range []func(*testing.T){
		func(t *testing.T) { t.Setenv("APP_BASE_URL", "https://cms.example.com") },
		func(t *testing.T) {
			t.Setenv("APP_BASE_URL", "http://127.0.0.1:8080")
			t.Setenv("APP_LISTEN_ADDR", ":8080")
		},
	} {
		setValidServeEnvironment(t)
		t.Setenv("APP_ASSETS_ENABLED", "false")
		modify(t)
		if _, err := Load("serve"); err == nil {
			t.Fatal("Load() expected an error")
		}
	}
}

func TestLoadServeRejectsInsecureMySQLInProduction(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("APP_ENV", "production")
	t.Setenv("SMS_PROVIDER", "tencent")
	t.Setenv("TENCENTCLOUD_SECRET_ID", "id")
	t.Setenv("TENCENTCLOUD_SECRET_KEY", "secret")
	t.Setenv("TENCENT_SMS_REGION", "ap-guangzhou")
	t.Setenv("TENCENT_SMS_SDK_APP_ID", "1400000000")
	t.Setenv("TENCENT_SMS_SIGN_NAME", "测试签名")
	t.Setenv("TENCENT_SMS_TEMPLATE_ID", "123456")
	t.Setenv("MYSQL_ALLOW_INSECURE", "true")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}

func TestDatabaseCommandsRejectInsecureMySQLInProduction(t *testing.T) {
	for _, command := range []string{"migrate", "admin"} {
		t.Run(command, func(t *testing.T) {
			t.Setenv("MYSQL_DSN", "cms:cms@tcp(db.example.com:3306)/cms")
			t.Setenv("APP_ENV", "production")
			t.Setenv("MYSQL_ALLOW_INSECURE", "true")
			if _, err := Load(command); err == nil {
				t.Fatal("Load() expected an error")
			}
		})
	}
}

func TestLoadServeParsesCanonicalTrustedProxyCIDRs(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("APP_BASE_URL", "http://127.0.0.1:8080")
	t.Setenv("APP_LISTEN_ADDR", "127.0.0.1:8080")
	t.Setenv("APP_TRUSTED_PROXY_CIDRS", "10.0.0.0/8,2001:db8::/32")
	cfg, err := Load("serve")
	if err != nil || len(cfg.TrustedProxyCIDRs) != 2 {
		t.Fatalf("Load() = (%+v, %v)", cfg, err)
	}
	t.Setenv("APP_TRUSTED_PROXY_CIDRS", "10.0.0.1/8")
	if _, err := Load("serve"); err == nil {
		t.Fatal("非规范 CIDR 应被拒绝")
	}
	t.Setenv("APP_TRUSTED_PROXY_CIDRS", "::ffff:a00:0/104")
	if _, err := Load("serve"); err == nil {
		t.Fatal("IPv4-mapped IPv6 CIDR 应被拒绝")
	}
}

func TestLoadServeRejectsUnknownSMSProvider(t *testing.T) {
	setValidServeEnvironment(t)
	t.Setenv("APP_ASSETS_ENABLED", "false")
	t.Setenv("SMS_PROVIDER", "console")
	if _, err := Load("serve"); err == nil {
		t.Fatal("Load() expected an error")
	}
}
