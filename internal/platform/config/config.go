package config

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	ListenAddr         string
	MySQLDSN           string
	WebDistDir         string
	AllowInsecureMySQL bool
	BaseURL            string
	SessionSecret      string
	OIDCIssuerURL      string
	OIDCClientID       string
	OIDCClientSecret   string
	OIDCRedirectURL    string
	LocalLoginEnabled  bool
	OIDCEnabled        bool
}

func Load(command string) (Config, error) {
	cfg := Config{
		ListenAddr:        envOrDefault("APP_LISTEN_ADDR", ":8080"),
		MySQLDSN:          os.Getenv("MYSQL_DSN"),
		WebDistDir:        envOrDefault("WEB_DIST_DIR", "web/dist"),
		BaseURL:           os.Getenv("APP_BASE_URL"),
		SessionSecret:     os.Getenv("APP_SESSION_SECRET"),
		OIDCIssuerURL:     os.Getenv("OIDC_ISSUER_URL"),
		OIDCClientID:      os.Getenv("OIDC_CLIENT_ID"),
		OIDCClientSecret:  os.Getenv("OIDC_CLIENT_SECRET"),
		OIDCRedirectURL:   os.Getenv("OIDC_REDIRECT_URL"),
		LocalLoginEnabled: false,
		OIDCEnabled:       true,
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
	}
	return cfg, nil
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
	base, err := parseHTTPSURL("APP_BASE_URL", cfg.BaseURL)
	if err != nil {
		return err
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
		redirect, err := parseHTTPSURL("OIDC_REDIRECT_URL", cfg.OIDCRedirectURL)
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

func envOrDefault(name, fallback string) string {
	if value, ok := os.LookupEnv(name); ok {
		return value
	}
	return fallback
}
