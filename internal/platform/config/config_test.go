package config

import "testing"

func TestLoadVersionDoesNotRequireDatabase(t *testing.T) {
	t.Setenv("MYSQL_DSN", "")
	cfg, err := Load("version")
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.ListenAddr != ":8080" || cfg.WebDistDir != "web/dist" || cfg.LocalLoginEnabled {
		t.Fatalf("unexpected defaults: %+v", cfg)
	}
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

func TestLoadServeRejectsMismatchedOIDCRedirect(t *testing.T) {
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
