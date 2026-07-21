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
	ListenAddr           string
	MySQLDSN             string
	WebDistDir           string
	AllowInsecureMySQL   bool
	BaseURL              string
	SessionSecret        string
	LocalLoginEnabled    bool
	Environment          string
	SMSProvider          string
	SMSFixedCode         string
	TencentSecretID      string
	TencentSecretKey     string
	TencentSMSRegion     string
	TencentSMSSDKAppID   string
	TencentSMSSignName   string
	TencentSMSTemplateID string
	AssetsEnabled        bool
	S3Endpoint           string
	S3Region             string
	S3Bucket             string
	S3AccessKeyID        string
	S3AccessKeySecret    string
	S3SessionToken       string
	S3UsePathStyle       bool
	S3BucketEndpoint     bool
	S3UploadTTL          time.Duration
	S3DownloadTTL        time.Duration
	AssetMimeTypes       []string
	AssetMaxSize         int64
}

func Load(command string) (Config, error) {
	cfg := Config{
		ListenAddr:           envOrDefault("APP_LISTEN_ADDR", ":8080"),
		MySQLDSN:             os.Getenv("MYSQL_DSN"),
		WebDistDir:           envOrDefault("WEB_DIST_DIR", "web/dist"),
		BaseURL:              os.Getenv("APP_BASE_URL"),
		SessionSecret:        os.Getenv("APP_SESSION_SECRET"),
		LocalLoginEnabled:    false,
		Environment:          envOrDefault("APP_ENV", "production"),
		SMSProvider:          envOrDefault("SMS_PROVIDER", "tencent"),
		SMSFixedCode:         os.Getenv("DEV_SMS_FIXED_CODE"),
		TencentSecretID:      os.Getenv("TENCENTCLOUD_SECRET_ID"),
		TencentSecretKey:     os.Getenv("TENCENTCLOUD_SECRET_KEY"),
		TencentSMSRegion:     os.Getenv("TENCENT_SMS_REGION"),
		TencentSMSSDKAppID:   os.Getenv("TENCENT_SMS_SDK_APP_ID"),
		TencentSMSSignName:   os.Getenv("TENCENT_SMS_SIGN_NAME"),
		TencentSMSTemplateID: os.Getenv("TENCENT_SMS_TEMPLATE_ID"),
		AssetsEnabled:        true,
		S3Endpoint:           os.Getenv("S3_ENDPOINT"),
		S3Region:             os.Getenv("S3_REGION"),
		S3Bucket:             os.Getenv("S3_BUCKET"),
		S3AccessKeyID:        os.Getenv("S3_ACCESS_KEY_ID"),
		S3AccessKeySecret:    os.Getenv("S3_ACCESS_KEY_SECRET"),
		S3SessionToken:       os.Getenv("S3_SESSION_TOKEN"),
		S3UploadTTL:          15 * time.Minute,
		S3DownloadTTL:        5 * time.Minute,
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
		{"S3_ENDPOINT", cfg.S3Endpoint}, {"S3_REGION", cfg.S3Region},
		{"S3_BUCKET", cfg.S3Bucket}, {"S3_ACCESS_KEY_ID", cfg.S3AccessKeyID},
		{"S3_ACCESS_KEY_SECRET", cfg.S3AccessKeySecret}, {"ASSET_ALLOWED_MIME_TYPES", os.Getenv("ASSET_ALLOWED_MIME_TYPES")},
		{"ASSET_MAX_SIZE_BYTES", os.Getenv("ASSET_MAX_SIZE_BYTES")},
	}
	for _, item := range required {
		if strings.TrimSpace(item.value) == "" {
			return fmt.Errorf("启用素材功能时缺少必需环境变量 %s", item.name)
		}
	}
	endpoint, err := parseHTTPSURL("S3_ENDPOINT", cfg.S3Endpoint)
	if err != nil {
		return err
	}
	if endpoint.Path != "" || endpoint.RawQuery != "" || endpoint.ForceQuery || endpoint.Fragment != "" || strings.Contains(cfg.S3Endpoint, "#") {
		return errors.New("S3_ENDPOINT 只能包含 HTTPS origin")
	}
	if err := parseBoolEnv("S3_USE_PATH_STYLE", &cfg.S3UsePathStyle); err != nil {
		return err
	}
	if err := parseBoolEnv("S3_BUCKET_ENDPOINT", &cfg.S3BucketEndpoint); err != nil {
		return err
	}
	if cfg.S3UsePathStyle && cfg.S3BucketEndpoint {
		return errors.New("S3_USE_PATH_STYLE 和 S3_BUCKET_ENDPOINT 不能同时启用")
	}
	if os.Getenv("S3_UPLOAD_URL_TTL") != "" {
		if cfg.S3UploadTTL, err = parseDurationRange("S3_UPLOAD_URL_TTL", time.Minute, 30*time.Minute); err != nil {
			return err
		}
	}
	if os.Getenv("S3_DOWNLOAD_URL_TTL") != "" {
		if cfg.S3DownloadTTL, err = parseDurationRange("S3_DOWNLOAD_URL_TTL", time.Minute, 15*time.Minute); err != nil {
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
	if cfg.Environment != "production" && cfg.Environment != "development" {
		return errors.New("APP_ENV 必须是 production 或 development")
	}
	if cfg.SMSProvider == "fixed" {
		if cfg.Environment != "development" {
			return errors.New("SMS_PROVIDER=fixed 仅允许 APP_ENV=development")
		}
		if len(cfg.SMSFixedCode) != 6 || strings.Trim(cfg.SMSFixedCode, "0123456789") != "" {
			return errors.New("DEV_SMS_FIXED_CODE 必须为 6 位")
		}
	} else if cfg.SMSProvider == "tencent" {
		required = append(required,
			struct{ name, value string }{"TENCENTCLOUD_SECRET_ID", cfg.TencentSecretID},
			struct{ name, value string }{"TENCENTCLOUD_SECRET_KEY", cfg.TencentSecretKey},
			struct{ name, value string }{"TENCENT_SMS_REGION", cfg.TencentSMSRegion},
			struct{ name, value string }{"TENCENT_SMS_SDK_APP_ID", cfg.TencentSMSSDKAppID},
			struct{ name, value string }{"TENCENT_SMS_SIGN_NAME", cfg.TencentSMSSignName},
			struct{ name, value string }{"TENCENT_SMS_TEMPLATE_ID", cfg.TencentSMSTemplateID},
		)
	} else {
		return errors.New("SMS_PROVIDER 必须是 tencent 或 fixed")
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
