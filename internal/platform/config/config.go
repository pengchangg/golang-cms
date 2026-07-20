package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ListenAddr          string
	MySQLDSN            string
	WebDistDir          string
	AllowInsecureMySQL  bool
	BaseURL             string
	SessionSecret       string
	OIDCIssuerURL       string
	OIDCClientID        string
	OIDCClientSecret    string
	OIDCRedirectURL     string
	LocalLoginEnabled   bool
	OIDCEnabled         bool
	AssetsEnabled       bool
	OSSEndpoint         string
	OSSRegion           string
	OSSBucket           string
	OSSAccessKeyID      string
	OSSAccessKeySecret  string
	OSSSecurityToken    string
	OSSUploadTTL        time.Duration
	OSSDownloadTTL      time.Duration
	AssetMimeTypes      []string
	AssetMaxSize        int64
	WorkerOwner         string
	WorkerConcurrency   int
	WorkerPollInterval  time.Duration
	WorkerLeaseDuration time.Duration
	WorkerRenewInterval time.Duration
}

func Load(command string) (Config, error) {
	cfg := Config{
		ListenAddr:         envOrDefault("APP_LISTEN_ADDR", ":8080"),
		MySQLDSN:           os.Getenv("MYSQL_DSN"),
		WebDistDir:         envOrDefault("WEB_DIST_DIR", "web/dist"),
		BaseURL:            os.Getenv("APP_BASE_URL"),
		SessionSecret:      os.Getenv("APP_SESSION_SECRET"),
		OIDCIssuerURL:      os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:       os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:   os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:    os.Getenv("OIDC_REDIRECT_URL"),
		LocalLoginEnabled:  false,
		OIDCEnabled:        true,
		AssetsEnabled:      true,
		OSSEndpoint:        os.Getenv("ALIYUN_OSS_ENDPOINT"),
		OSSRegion:          os.Getenv("ALIYUN_OSS_REGION"),
		OSSBucket:          os.Getenv("ALIYUN_OSS_BUCKET"),
		OSSAccessKeyID:     os.Getenv("ALIYUN_OSS_ACCESS_KEY_ID"),
		OSSAccessKeySecret: os.Getenv("ALIYUN_OSS_ACCESS_KEY_SECRET"),
		OSSSecurityToken:   os.Getenv("ALIYUN_OSS_SECURITY_TOKEN"),
		OSSUploadTTL:       15 * time.Minute,
		OSSDownloadTTL:     5 * time.Minute,
		WorkerOwner:        os.Getenv("APP_WORKER_OWNER"),
	}
	if err := parseBoolEnv("APP_ASSETS_ENABLED", &cfg.AssetsEnabled); err != nil {
		return Config{}, err
	}
	if value := os.Getenv("APP_LOCAL_LOGIN_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("APP_LOCAL_LOGIN_ENABLED 格式不合法: %w", err)
		}
		cfg.LocalLoginEnabled = enabled
	}
	if value := os.Getenv("APP_OIDC_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("APP_OIDC_ENABLED 格式不合法: %w", err)
		}
		cfg.OIDCEnabled = enabled
	}
	if value := os.Getenv("MYSQL_ALLOW_INSECURE"); value != "" {
		allow, err := strconv.ParseBool(value)
		if err != nil {
			return Config{}, fmt.Errorf("MYSQL_ALLOW_INSECURE 格式不合法: %w", err)
		}
		cfg.AllowInsecureMySQL = allow
	}

	if command == "version" {
		return cfg, nil
	}
	if cfg.MySQLDSN == "" {
		return Config{}, errors.New("缺少必需环境变量 MYSQL_DSN")
	}
	if command == "serve" {
		if cfg.ListenAddr == "" {
			return Config{}, fmt.Errorf("APP_LISTEN_ADDR 不能为空")
		}
		if _, _, err := net.SplitHostPort(cfg.ListenAddr); err != nil {
			return Config{}, fmt.Errorf("APP_LISTEN_ADDR 格式不合法: %w", err)
		}
		if err := validateSecurityConfig(cfg); err != nil {
			return Config{}, err
		}
		if cfg.AssetsEnabled {
			if err := loadAssetsConfig(&cfg); err != nil {
				return Config{}, err
			}
		}
	}
	return cfg, nil
}

func loadAssetsConfig(cfg *Config) error {
	required := []struct{ name, value string }{
		{"ALIYUN_OSS_ENDPOINT", cfg.OSSEndpoint}, {"ALIYUN_OSS_REGION", cfg.OSSRegion},
		{"ALIYUN_OSS_BUCKET", cfg.OSSBucket}, {"ALIYUN_OSS_ACCESS_KEY_ID", cfg.OSSAccessKeyID},
		{"ALIYUN_OSS_ACCESS_KEY_SECRET", cfg.OSSAccessKeySecret}, {"ASSET_ALLOWED_MIME_TYPES", os.Getenv("ASSET_ALLOWED_MIME_TYPES")},
		{"ASSET_MAX_SIZE_BYTES", os.Getenv("ASSET_MAX_SIZE_BYTES")}, {"APP_WORKER_OWNER", cfg.WorkerOwner},
		{"APP_WORKER_CONCURRENCY", os.Getenv("APP_WORKER_CONCURRENCY")}, {"APP_WORKER_POLL_INTERVAL", os.Getenv("APP_WORKER_POLL_INTERVAL")},
		{"APP_WORKER_LEASE_DURATION", os.Getenv("APP_WORKER_LEASE_DURATION")}, {"APP_WORKER_RENEW_INTERVAL", os.Getenv("APP_WORKER_RENEW_INTERVAL")},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("启用素材功能时缺少必需环境变量 %s", item.name)
		}
	}
	if _, err := parseHTTPSURL("ALIYUN_OSS_ENDPOINT", cfg.OSSEndpoint); err != nil {
		return err
	}
	var err error
	if os.Getenv("OSS_UPLOAD_URL_TTL") != "" {
		if cfg.OSSUploadTTL, err = parseDurationRange("OSS_UPLOAD_URL_TTL", time.Minute, 30*time.Minute); err != nil {
			return err
		}
	}
	if os.Getenv("OSS_DOWNLOAD_URL_TTL") != "" {
		if cfg.OSSDownloadTTL, err = parseDurationRange("OSS_DOWNLOAD_URL_TTL", time.Minute, 15*time.Minute); err != nil {
			return err
		}
	}
	for _, value := range strings.Split(os.Getenv("ASSET_ALLOWED_MIME_TYPES"), ",") {
		value = strings.TrimSpace(value)
		if value == "" || value != strings.ToLower(value) || strings.ContainsAny(value, "; \t\r\n") || !strings.Contains(value, "/") || value == "application/octet-stream" {
			return errors.New("ASSET_ALLOWED_MIME_TYPES 必须是逗号分隔的小写规范 MIME 列表")
		}
		cfg.AssetMimeTypes = append(cfg.AssetMimeTypes, value)
	}
	if cfg.AssetMaxSize, err = strconv.ParseInt(os.Getenv("ASSET_MAX_SIZE_BYTES"), 10, 64); err != nil || cfg.AssetMaxSize < 1 || cfg.AssetMaxSize > 5*1024*1024*1024 {
		return errors.New("ASSET_MAX_SIZE_BYTES 必须在 1 至 5368709120 之间")
	}
	if cfg.WorkerConcurrency, err = strconv.Atoi(os.Getenv("APP_WORKER_CONCURRENCY")); err != nil || cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 64 {
		return errors.New("APP_WORKER_CONCURRENCY 必须是 1 至 64")
	}
	if cfg.WorkerPollInterval, err = parseDurationRange("APP_WORKER_POLL_INTERVAL", 100*time.Millisecond, time.Minute); err != nil {
		return err
	}
	if cfg.WorkerLeaseDuration, err = parseDurationRange("APP_WORKER_LEASE_DURATION", 10*time.Second, 10*time.Minute); err != nil {
		return err
	}
	if cfg.WorkerRenewInterval, err = parseDurationRange("APP_WORKER_RENEW_INTERVAL", time.Second, cfg.WorkerLeaseDuration/2); err != nil {
		return err
	}
	if len(cfg.WorkerOwner) > 128 {
		return errors.New("APP_WORKER_OWNER 不能超过 128 字节")
	}
	return nil
}

func parseDurationRange(name string, minimum, maximum time.Duration) (time.Duration, error) {
	value, err := time.ParseDuration(os.Getenv(name))
	if err != nil || value < minimum || value > maximum {
		return 0, fmt.Errorf("%s 必须是 %s 至 %s 的时长", name, minimum, maximum)
	}
	return value, nil
}

func parseBoolEnv(name string, destination *bool) error {
	value := os.Getenv(name)
	if value == "" {
		return nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fmt.Errorf("%s 格式不合法: %w", name, err)
	}
	*destination = parsed
	return nil
}

func validateSecurityConfig(cfg Config) error {
	required := []struct{ name, value string }{
		{"APP_BASE_URL", cfg.BaseURL}, {"APP_SESSION_SECRET", cfg.SessionSecret},
	}
	if cfg.OIDCEnabled {
		required = append(required,
			struct{ name, value string }{"OIDC_ISSUER_URL", cfg.OIDCIssuerURL},
			struct{ name, value string }{"OIDC_CLIENT_ID", cfg.OIDCClientID},
			struct{ name, value string }{"OIDC_CLIENT_SECRET", cfg.OIDCClientSecret},
			struct{ name, value string }{"OIDC_REDIRECT_URL", cfg.OIDCRedirectURL},
		)
	}
	for _, item := range required {
		if item.value == "" {
			return fmt.Errorf("缺少必需环境变量 %s", item.name)
		}
	}
	if len(cfg.SessionSecret) < 32 {
		return errors.New("APP_SESSION_SECRET 长度不能少于 32 字节")
	}
	base, err := parseHTTPURL("APP_BASE_URL", cfg.BaseURL)
	if err != nil {
		return err
	}
	if base.Scheme == "http" && !isLoopbackHost(base.Hostname()) {
		return errors.New("APP_BASE_URL 仅回环地址允许使用 HTTP")
	}
	if base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return errors.New("APP_BASE_URL 只能包含 origin")
	}
	if cfg.OIDCEnabled {
		issuer, err := parseHTTPSURL("OIDC_ISSUER_URL", cfg.OIDCIssuerURL)
		if err != nil {
			return err
		}
		if issuer.Fragment != "" {
			return errors.New("OIDC_ISSUER_URL 不能包含 fragment")
		}
		redirect, err := parseHTTPURL("OIDC_REDIRECT_URL", cfg.OIDCRedirectURL)
		if err != nil {
			return err
		}
		expected := strings.TrimSuffix(cfg.BaseURL, "/") + "/api/admin/v1/auth/oidc/callback"
		if redirect.String() != expected {
			return fmt.Errorf("OIDC_REDIRECT_URL 必须为 %s", expected)
		}
	}
	return nil
}

func parseHTTPSURL(name, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%s 必须是合法 HTTPS URL", name)
	}
	return parsed, nil
}

func parseHTTPURL(name, value string) (*url.URL, error) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil {
		return nil, fmt.Errorf("%s 必须是合法 HTTP 或 HTTPS URL", name)
	}
	return parsed, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func envOrDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
