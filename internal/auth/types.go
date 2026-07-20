package auth

import (
	"context"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
)

const (
	CookieName     = "cms_session"
	OIDCCookieName = "cms_oidc_binding"
	CSRFHeader     = "X-CSRF-Token"
	IdleTimeout    = 30 * time.Minute
	AbsoluteExpiry = 12 * time.Hour
)

type Clock interface {
	Now() time.Time
}

type OIDCIdentity struct {
	Issuer      string
	Subject     string
	DisplayName string
	Email       *string
}

type OIDCClient interface {
	AuthorizationURL(state, nonce, challenge string) string
	Exchange(context.Context, string, string, string) (OIDCIdentity, error)
}

type LoginState struct {
	Hash         []byte
	BindingHash  []byte
	Nonce        string
	PKCEVerifier string
	ReturnTo     string
	ExpiresAt    time.Time
	CreatedAt    time.Time
}

type User struct {
	ID           string
	DisplayName  string
	Email        *string
	Enabled      bool
	PasswordHash string
}

type Session struct {
	UserID        string
	DisplayName   string
	Email         *string
	Enabled       bool
	AuthMethod    identity.AuthMethod
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
}

type NewSession struct {
	Hash          []byte
	UserID        string
	AuthMethod    identity.AuthMethod
	CreatedAt     time.Time
	LastSeenAt    time.Time
	IdleExpiresAt time.Time
	ExpiresAt     time.Time
}

type Store interface {
	SaveLoginState(context.Context, LoginState) error
	ConsumeLoginState(context.Context, []byte, []byte, time.Time) (LoginState, error)
	DeleteExpiredLoginStates(context.Context, time.Time, int) error
	FindLocalUser(context.Context, string) (User, error)
	CompleteOIDCLogin(context.Context, OIDCIdentity, string, time.Time, NewSession, audit.Event) (User, error)
	CreateSession(context.Context, NewSession, audit.Event) error
	Session(context.Context, []byte) (Session, error)
	TouchSession(context.Context, []byte, time.Time, time.Time) error
	RevokeSession(context.Context, []byte, time.Time, audit.Event) error
	AppendFailure(context.Context, audit.Event) error
	UpsertEmergencyAdmin(context.Context, string, string, string, string, time.Time, audit.Event) error
}

type RequestMeta struct {
	RequestID string
	IP        string
	UserAgent string
}

type SessionModelSummary struct {
	ID          string `json:"id"`
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
}

type ModelSummaryProvider interface {
	ActiveModelSummaries(context.Context, []string) ([]SessionModelSummary, error)
}

type SessionResponse struct {
	Principal     identity.Principal    `json:"principal"`
	ContentModels []SessionModelSummary `json:"content_models"`
	CSRFToken     string                `json:"csrf_token"`
	IdleExpiresAt time.Time             `json:"idle_expires_at"`
	ExpiresAt     time.Time             `json:"expires_at"`
}

type sessionResult struct {
	Raw      string
	Response SessionResponse
}
