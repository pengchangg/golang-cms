package auth

import (
	"context"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
)

const (
	CookieName           = "cms_session"
	CaptchaBindingCookie = "cms_captcha_binding"
	CSRFHeader           = "X-CSRF-Token"
	IdleTimeout          = 30 * time.Minute
	AbsoluteExpiry       = 12 * time.Hour
	CaptchaExpiry        = 5 * time.Minute
	SMSExpiry            = 5 * time.Minute
	CaptchaMaxAttempts   = 5
	SMSMaxAttempts       = 5
)

type Clock interface {
	Now() time.Time
}

type SMSProvider interface {
	SendCode(context.Context, string, string, time.Duration) error
}

type CaptchaChallenge struct {
	Hash              []byte
	BindingHash       []byte
	TargetX           int
	TargetY           int
	AttemptsRemaining int
	ExpiresAt         time.Time
	CreatedAt         time.Time
}

type SMSChallenge struct {
	Hash              []byte
	BindingHash       []byte
	PhoneE164         string
	PhoneMasked       string
	OTPHash           []byte
	AttemptsRemaining int
	ExpiresAt         time.Time
	CreatedAt         time.Time
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
	SaveCaptchaChallenge(context.Context, CaptchaChallenge) error
	VerifyCaptchaChallenge(context.Context, []byte, []byte, int, int, int, time.Time) error
	SaveSMSChallenge(context.Context, SMSChallenge) error
	ConsumeSMSChallenge(context.Context, []byte, []byte, []byte, time.Time) (string, error)
	AllowRateLimit(context.Context, string, []byte, time.Time, time.Duration, int) (bool, error)
	FindLocalUser(context.Context, string) (User, error)
	FindPhoneUser(context.Context, string) (User, error)
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
