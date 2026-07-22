package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
)

type Service struct {
	store       Store
	permissions identity.PermissionProvider
	models      ModelSummaryProvider
	sms         SMSProvider
	captcha     CaptchaGenerator
	clock       Clock
	random      io.Reader
	secret      []byte
	localIP     *rateLimiter
	localUser   *rateLimiter
}

func NewService(store Store, permissions identity.PermissionProvider, models ModelSummaryProvider, sms SMSProvider, captcha CaptchaGenerator, clock Clock, random io.Reader, sessionSecret string) (*Service, error) {
	if store == nil || clock == nil || random == nil || len(sessionSecret) < 32 {
		return nil, errors.New("认证服务依赖或会话密钥不合法")
	}
	if permissions != nil && models == nil {
		return nil, errors.New("认证服务缺少模型摘要提供者")
	}
	return &Service{
		store: store, permissions: permissions, models: models, sms: sms, captcha: captcha, clock: clock, random: random, secret: []byte(sessionSecret),
		localIP: newRateLimiter(20, 4096, 5*time.Minute), localUser: newRateLimiter(10, 4096, 5*time.Minute),
	}, nil
}

var mainlandPhonePattern = regexp.MustCompile(`^1[3-9][0-9]{9}$`)

func (s *Service) CreateCaptcha(ctx context.Context, binding string, meta RequestMeta) (CaptchaResponse, string, error) {
	if s.captcha == nil {
		return CaptchaResponse{}, "", appError(apperror.KindUnavailable, "captcha_unavailable", "行为验证暂时不可用")
	}
	now := s.clock.Now().UTC()
	if err := s.requireRateLimit(ctx, "captcha_ip", meta.IP, now, 5*time.Minute, 30); err != nil {
		return CaptchaResponse{}, "", err
	}
	if binding == "" {
		var err error
		binding, err = s.token(32)
		if err != nil {
			return CaptchaResponse{}, "", err
		}
	}
	data, err := s.captcha.Generate()
	if err != nil {
		return CaptchaResponse{}, "", fmt.Errorf("生成滑动拼图: %w", err)
	}
	id, err := s.token(24)
	if err != nil {
		return CaptchaResponse{}, "", err
	}
	expires := now.Add(CaptchaExpiry)
	challenge := CaptchaChallenge{Hash: s.digest("captcha", id), BindingHash: s.digest("captcha_binding", binding), TargetX: data.TargetX, TargetY: data.TargetY, AttemptsRemaining: CaptchaMaxAttempts, ExpiresAt: expires, CreatedAt: now}
	if err := s.store.SaveCaptchaChallenge(ctx, challenge); err != nil {
		return CaptchaResponse{}, "", fmt.Errorf("保存滑动拼图挑战: %w", err)
	}
	return CaptchaResponse{ChallengeID: id, BackgroundImage: data.BackgroundImage, TileImage: data.TileImage, TileX: data.TileX, TileY: data.TileY, ExpiresAt: expires}, binding, nil
}

func (s *Service) CreateSMSChallenge(ctx context.Context, phone, captchaID, binding string, x, y int, meta RequestMeta) (SMSChallengeResponse, error) {
	normalized, masked, err := normalizeMainlandPhone(phone)
	if err != nil || captchaID == "" || binding == "" || x < 0 || y < 0 {
		return SMSChallengeResponse{}, appError(apperror.KindInvalidArgument, "validation_failed", "请求数据校验失败")
	}
	now := s.clock.Now().UTC()
	if err := s.store.VerifyCaptchaChallenge(ctx, s.digest("captcha", captchaID), s.digest("captcha_binding", binding), x, y, 5, now); err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidChallenge) {
			return SMSChallengeResponse{}, appError(apperror.KindUnauthenticated, "captcha_invalid", "行为验证无效或已过期")
		}
		return SMSChallengeResponse{}, fmt.Errorf("校验滑动拼图挑战: %w", err)
	}
	if err := s.requireRateLimit(ctx, "sms_ip", meta.IP, now, 10*time.Minute, 10); err != nil {
		return SMSChallengeResponse{}, err
	}
	if err := s.requireRateLimit(ctx, "sms_phone", normalized, now, 10*time.Minute, 5); err != nil {
		return SMSChallengeResponse{}, err
	}
	if err := s.requireRateLimit(ctx, "sms_resend", normalized, now, time.Minute, 1); err != nil {
		return SMSChallengeResponse{}, err
	}
	if err := s.requireRateLimit(ctx, "sms_global", "global", now, time.Minute, 100); err != nil {
		return SMSChallengeResponse{}, err
	}
	id, err := s.token(24)
	if err != nil {
		return SMSChallengeResponse{}, err
	}
	code, err := s.numericCode(6)
	if err != nil {
		return SMSChallengeResponse{}, err
	}
	if fixed, ok := s.sms.(interface{ FixedCode() string }); ok {
		code = fixed.FixedCode()
	}
	expires := now.Add(SMSExpiry)
	challenge := SMSChallenge{Hash: s.digest("sms_challenge", id), BindingHash: s.digest("captcha_binding", binding), PhoneE164: normalized, PhoneMasked: masked, OTPHash: s.digest("sms_otp", id+"\x00"+code), AttemptsRemaining: SMSMaxAttempts, ExpiresAt: expires, CreatedAt: now}
	user, findErr := s.store.FindPhoneUser(ctx, normalized)
	if findErr == nil && user.Enabled {
		challenge.UserID = &user.ID
	}
	if err := s.store.SaveSMSChallenge(ctx, challenge); err != nil {
		return SMSChallengeResponse{}, fmt.Errorf("保存短信挑战: %w", err)
	}
	if findErr == nil && user.Enabled {
		if s.sms == nil {
			return SMSChallengeResponse{}, appError(apperror.KindUnavailable, "sms_unavailable", "短信服务暂时不可用")
		}
		if err := s.sms.SendCode(ctx, normalized, code, SMSExpiry); err != nil {
			return SMSChallengeResponse{}, fmt.Errorf("发送登录短信: %w", err)
		}
	} else if findErr != nil && !errors.Is(findErr, ErrNotFound) {
		return SMSChallengeResponse{}, fmt.Errorf("读取手机号用户: %w", findErr)
	}
	return SMSChallengeResponse{ChallengeID: id, PhoneMasked: masked, ExpiresAt: expires, RetryAfterSeconds: 60}, nil
}

func (s *Service) VerifySMSChallenge(ctx context.Context, challengeID, code, binding string, meta RequestMeta) (sessionResult, error) {
	if challengeID == "" || binding == "" || len(code) != 6 || strings.Trim(code, "0123456789") != "" {
		return sessionResult{}, s.smsFailure(ctx, meta)
	}
	now := s.clock.Now().UTC()
	if err := s.requireRateLimit(ctx, "sms_verify_ip", meta.IP, now, 10*time.Minute, 30); err != nil {
		return sessionResult{}, err
	}
	phone, challengeUserID, err := s.store.ConsumeSMSChallenge(ctx, s.digest("sms_challenge", challengeID), s.digest("captcha_binding", binding), s.digest("sms_otp", challengeID+"\x00"+code), now)
	if err != nil {
		if errors.Is(err, ErrNotFound) || errors.Is(err, ErrInvalidChallenge) {
			return sessionResult{}, s.smsFailure(ctx, meta)
		}
		return sessionResult{}, fmt.Errorf("校验短信挑战: %w", err)
	}
	user, err := s.store.FindPhoneUser(ctx, phone)
	if err != nil || !user.Enabled || challengeUserID == nil || user.ID != *challengeUserID {
		if err != nil && !errors.Is(err, ErrNotFound) {
			return sessionResult{}, fmt.Errorf("读取手机号用户: %w", err)
		}
		return sessionResult{}, s.smsFailure(ctx, meta)
	}
	result, err := s.createSession(ctx, user, identity.AuthMethodSMS, phone, "auth_sms_login_succeeded", meta)
	if errors.Is(err, ErrInvalidChallenge) {
		return sessionResult{}, s.smsFailure(ctx, meta)
	}
	return result, err
}

func (s *Service) LocalLogin(ctx context.Context, username, password string, meta RequestMeta) (sessionResult, error) {
	now := s.clock.Now().UTC()
	userKey := base64.RawURLEncoding.EncodeToString(s.digest("login_user", strings.ToLower(username)))
	if !s.localIP.allow(meta.IP, now) || !s.localUser.allow(userKey, now) {
		if err := s.auditFailure(ctx, "auth_local_login_failed", "rate_limited", meta); err != nil {
			return sessionResult{}, err
		}
		return sessionResult{}, appError(apperror.KindUnavailable, "local_login_rate_limited", "登录请求过于频繁")
	}
	user, err := s.store.FindLocalUser(ctx, username)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return sessionResult{}, fmt.Errorf("读取本地应急用户: %w", err)
	}
	passwordValid := false
	if err == nil {
		passwordValid, err = verifyPasswordContext(ctx, user.PasswordHash, password)
		if err != nil {
			return sessionResult{}, err
		}
	}
	valid := passwordValid && user.Enabled
	if !valid {
		// 对不存在用户执行同等成本的 Argon2id，避免账号枚举时序差异。
		if errors.Is(err, ErrNotFound) {
			_, _ = verifyPasswordContext(ctx, "$argon2id$v=19$m=65536,t=3,p=2$MDEyMzQ1Njc4OWFiY2RlZg$zDqT3Vt9pNq8+BV6m8t+zUqzjqBnUSI4oT7U6ZQeEtY", password)
		}
		if err := s.auditFailure(ctx, "auth_local_login_failed", "invalid_credentials", meta); err != nil {
			return sessionResult{}, err
		}
		return sessionResult{}, appError(apperror.KindUnauthenticated, "invalid_credentials", "用户名或密码无效")
	}
	result, err := s.createSession(ctx, user, identity.AuthMethodLocal, "", "auth_local_login_succeeded", meta)
	if errors.Is(err, ErrInvalidChallenge) || errors.Is(err, ErrUserDisabled) {
		if auditErr := s.auditFailure(ctx, "auth_local_login_failed", "invalid_credentials", meta); auditErr != nil {
			return sessionResult{}, auditErr
		}
		return sessionResult{}, appError(apperror.KindUnauthenticated, "invalid_credentials", "用户名或密码无效")
	}
	return result, err
}

func (s *Service) CurrentSession(ctx context.Context, raw string) (SessionResponse, error) {
	hash := s.digest("session", raw)
	session, err := s.store.Session(ctx, hash)
	now := s.clock.Now().UTC()
	if err != nil || !identity.ValidAuthMethod(session.AuthMethod) || !session.Enabled || !now.Before(session.IdleExpiresAt) || !now.Before(session.ExpiresAt) {
		return SessionResponse{}, sessionInvalid()
	}
	idle := now.Add(IdleTimeout)
	if idle.After(session.ExpiresAt) {
		idle = session.ExpiresAt
	}
	if err := s.store.TouchSession(ctx, hash, now, idle); err != nil {
		return SessionResponse{}, sessionInvalid()
	}
	principal, models, err := s.principalWithModels(ctx, session.UserID, session.DisplayName, session.Email, session.AuthMethod)
	if err != nil {
		return SessionResponse{}, fmt.Errorf("读取当前权限: %w", err)
	}
	return SessionResponse{
		Principal: principal, ContentModels: models, CSRFToken: s.csrf(raw), IdleExpiresAt: idle, ExpiresAt: session.ExpiresAt.UTC(),
	}, nil
}

func (s *Service) Logout(ctx context.Context, raw string, meta RequestMeta) error {
	hash := s.digest("session", raw)
	session, err := s.store.Session(ctx, hash)
	now := s.clock.Now().UTC()
	if err != nil || !session.Enabled || !now.Before(session.IdleExpiresAt) || !now.Before(session.ExpiresAt) {
		return sessionInvalid()
	}
	event, err := s.event("auth_logout_succeeded", "success", nil, &session.UserID, &session.DisplayName, meta)
	if err != nil {
		return err
	}
	if err := s.store.RevokeSession(ctx, hash, now, event); err != nil {
		return sessionInvalid()
	}
	return nil
}

func (s *Service) CheckCSRF(raw, supplied string) error {
	if supplied == "" {
		return appError(apperror.KindPermissionDenied, "csrf_token_required", "缺少 CSRF Token")
	}
	want := s.csrf(raw)
	if subtle.ConstantTimeCompare([]byte(want), []byte(supplied)) != 1 {
		return appError(apperror.KindPermissionDenied, "csrf_token_invalid", "CSRF Token 无效")
	}
	return nil
}

func (s *Service) ResetEmergencyAdmin(ctx context.Context, userID, username, displayName, password string, ensure bool, meta RequestMeta) (bool, error) {
	hash, err := HashPassword(password, s.random)
	if err != nil {
		return false, err
	}
	if userID == "" {
		userID, err = s.identifier("usr", 18)
		if err != nil {
			return false, err
		}
	}
	event, err := s.event("auth_local_password_reset", "success", nil, nil, nil, meta)
	if err != nil {
		return false, err
	}
	return s.store.UpsertEmergencyAdmin(ctx, userID, username, displayName, hash, ensure, s.clock.Now().UTC(), event)
}

func (s *Service) createSession(ctx context.Context, user User, method identity.AuthMethod, phoneE164, action string, meta RequestMeta) (sessionResult, error) {
	if !identity.ValidAuthMethod(method) {
		return sessionResult{}, ErrInvalidChallenge
	}
	principal, models, err := s.principalWithModels(ctx, user.ID, user.DisplayName, user.Email, method)
	if err != nil {
		return sessionResult{}, fmt.Errorf("读取当前权限: %w", err)
	}
	raw, err := s.token(32)
	if err != nil {
		return sessionResult{}, err
	}
	now := s.clock.Now().UTC()
	expires := now.Add(AbsoluteExpiry)
	event, err := s.event(action, "success", nil, &user.ID, &user.DisplayName, meta)
	if err != nil {
		return sessionResult{}, err
	}
	created := NewSession{Hash: s.digest("session", raw), UserID: user.ID, AuthMethod: method, CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(IdleTimeout), ExpiresAt: expires, PhoneE164: phoneE164, PasswordHash: user.PasswordHash}
	if err := s.store.CreateSession(ctx, created, event); err != nil {
		return sessionResult{}, fmt.Errorf("创建认证会话: %w", err)
	}
	return sessionResult{Raw: raw, Response: SessionResponse{Principal: principal, ContentModels: models, CSRFToken: s.csrf(raw), IdleExpiresAt: created.IdleExpiresAt, ExpiresAt: expires}}, nil
}

func (s *Service) principalWithModels(ctx context.Context, userID, displayName string, email *string, method identity.AuthMethod) (identity.Principal, []SessionModelSummary, error) {
	permissions, err := s.permissionSet(ctx, userID)
	if err != nil {
		return identity.Principal{}, nil, err
	}
	principal := identity.NewPrincipal(userID, displayName, email, method, permissions)
	ids := make([]string, len(principal.ModelPermissions))
	for i, grant := range principal.ModelPermissions {
		ids[i] = grant.ModelID
	}
	if s.models == nil {
		return principal, []SessionModelSummary{}, nil
	}
	models, err := s.models.ActiveModelSummaries(ctx, ids)
	if err != nil {
		return identity.Principal{}, nil, err
	}
	if models == nil {
		models = []SessionModelSummary{}
	}
	return principal, models, nil
}

func (s *Service) permissionSet(ctx context.Context, userID string) (identity.PermissionSet, error) {
	if s.permissions == nil {
		return identity.PermissionSet{}, nil
	}
	return s.permissions.Permissions(ctx, userID)
}

func (s *Service) auditFailure(ctx context.Context, action, code string, meta RequestMeta) error {
	event, err := s.event(action, "failure", &code, nil, nil, meta)
	if err != nil {
		return fmt.Errorf("创建失败审计事件: %w", err)
	}
	auditCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
	defer cancel()
	if err := s.store.AppendFailure(auditCtx, event); err != nil {
		return fmt.Errorf("追加失败审计事件: %w", err)
	}
	return nil
}

func (s *Service) event(action, result string, failure, actorID, actorDisplayName *string, meta RequestMeta) (audit.Event, error) {
	id, err := s.identifier("evt", 18)
	if err != nil {
		return audit.Event{}, err
	}
	actorType := "system"
	if actorID != nil {
		actorType = "user"
	}
	return audit.Event{ID: id, OccurredAt: s.clock.Now().UTC(), RequestID: meta.RequestID, ActorType: actorType, ActorID: actorID, ActorDisplayName: actorDisplayName, Action: action, ResourceType: "authentication", Result: result, IP: meta.IP, UserAgent: meta.UserAgent, Changes: map[string]any{}, FailureCode: failure}, nil
}

func (s *Service) token(size int) (string, error) {
	data := make([]byte, size)
	if _, err := io.ReadFull(s.random, data); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

func (s *Service) identifier(prefix string, size int) (string, error) {
	value, err := s.token(size)
	if err != nil {
		return "", err
	}
	return prefix + "_" + value, nil
}

func (s *Service) digest(purpose, raw string) []byte {
	mac := hmac.New(sha256.New, s.secret)
	_, _ = mac.Write([]byte(purpose + "\x00" + raw))
	return mac.Sum(nil)
}

func (s *Service) csrf(raw string) string {
	return base64.RawURLEncoding.EncodeToString(s.digest("csrf", raw))
}

func (s *Service) numericCode(size int) (string, error) {
	result := make([]byte, size)
	for i := range result {
		var value [1]byte
		for {
			if _, err := io.ReadFull(s.random, value[:]); err != nil {
				return "", err
			}
			if value[0] < 250 {
				break
			}
		}
		result[i] = '0' + value[0]%10
	}
	return string(result), nil
}

func (s *Service) requireRateLimit(ctx context.Context, scope, key string, now time.Time, window time.Duration, limit int) error {
	allowed, err := s.store.AllowRateLimit(ctx, scope, s.digest("rate_limit", scope+"\x00"+key), now, window, limit)
	if err != nil {
		return fmt.Errorf("检查认证限流: %w", err)
	}
	if !allowed {
		return appError(apperror.KindUnavailable, "auth_rate_limited", "认证请求过于频繁")
	}
	return nil
}

func (s *Service) smsFailure(ctx context.Context, meta RequestMeta) error {
	if err := s.auditFailure(ctx, "auth_sms_login_failed", "invalid_credentials", meta); err != nil {
		return err
	}
	return appError(apperror.KindUnauthenticated, "invalid_credentials", "手机号或验证码无效")
}

func normalizeMainlandPhone(value string) (string, string, error) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "+86") {
		value = strings.TrimPrefix(value, "+86")
	}
	if !mainlandPhonePattern.MatchString(value) {
		return "", "", errors.New("大陆手机号格式不合法")
	}
	return "+86" + value, value[:3] + "****" + value[7:], nil
}

func sessionInvalid() error {
	return appError(apperror.KindUnauthenticated, "session_invalid", "认证会话无效")
}
func appError(kind apperror.Kind, code, message string) error {
	return &apperror.Error{Kind: kind, Code: code, Message: message}
}
