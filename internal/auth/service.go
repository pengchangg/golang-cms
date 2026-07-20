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
	"net/url"
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
	oidc        OIDCClient
	clock       Clock
	random      io.Reader
	secret      []byte
	localIP     *rateLimiter
	localUser   *rateLimiter
	oidcIP      *rateLimiter
}

func NewService(store Store, permissions identity.PermissionProvider, models ModelSummaryProvider, oidcClient OIDCClient, clock Clock, random io.Reader, sessionSecret string) (*Service, error) {
	if store == nil || clock == nil || random == nil || len(sessionSecret) < 32 {
		return nil, errors.New("认证服务依赖或会话密钥不合法")
	}
	if permissions != nil && models == nil {
		return nil, errors.New("认证服务缺少模型摘要提供者")
	}
	return &Service{
		store: store, permissions: permissions, models: models, oidc: oidcClient, clock: clock, random: random, secret: []byte(sessionSecret),
		localIP: newRateLimiter(20, 4096, 5*time.Minute), localUser: newRateLimiter(10, 4096, 5*time.Minute),
		oidcIP: newRateLimiter(30, 4096, 5*time.Minute),
	}, nil
}

func (s *Service) StartOIDC(ctx context.Context, returnTo string, meta RequestMeta) (string, string, error) {
	if s.oidc == nil {
		return "", "", appError(apperror.KindUnavailable, "oidc_unavailable", "OIDC 身份源暂时不可用")
	}
	if !validReturnTo(returnTo) {
		return "", "", appError(apperror.KindInvalidArgument, "invalid_return_to", "登录返回路径不合法")
	}
	now := s.clock.Now().UTC()
	if !s.oidcIP.allow(meta.IP, now) {
		return "", "", appError(apperror.KindUnavailable, "oidc_rate_limited", "OIDC 登录请求过于频繁")
	}
	state, err := s.token(32)
	if err != nil {
		return "", "", err
	}
	binding, err := s.token(32)
	if err != nil {
		return "", "", err
	}
	nonce, err := s.token(32)
	if err != nil {
		return "", "", err
	}
	verifier, err := s.token(64)
	if err != nil {
		return "", "", err
	}
	if err := s.store.DeleteExpiredLoginStates(ctx, now, 100); err != nil {
		return "", "", fmt.Errorf("清理过期 OIDC 登录状态: %w", err)
	}
	err = s.store.SaveLoginState(ctx, LoginState{Hash: s.digest("state", state), BindingHash: s.digest("oidc_binding", binding), Nonce: nonce, PKCEVerifier: verifier, ReturnTo: returnTo, CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)})
	if err != nil {
		return "", "", fmt.Errorf("保存 OIDC 登录状态: %w", err)
	}
	challenge := sha256.Sum256([]byte(verifier))
	return s.oidc.AuthorizationURL(state, nonce, base64.RawURLEncoding.EncodeToString(challenge[:])), binding, nil
}

func (s *Service) CompleteOIDC(ctx context.Context, code, state, binding string, meta RequestMeta) (sessionResult, string, error) {
	if s.oidc == nil {
		return sessionResult{}, "", appError(apperror.KindUnavailable, "oidc_unavailable", "OIDC 身份源暂时不可用")
	}
	now := s.clock.Now().UTC()
	if code == "" || state == "" || binding == "" {
		if auditErr := s.auditFailure(ctx, "auth_oidc_login_failed", "invalid_oidc_callback", meta); auditErr != nil {
			return sessionResult{}, "", auditErr
		}
		return sessionResult{}, "", appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调无效")
	}
	loginState, err := s.store.ConsumeLoginState(ctx, s.digest("state", state), s.digest("oidc_binding", binding), now)
	if err != nil {
		if auditErr := s.auditFailure(ctx, "auth_oidc_login_failed", "invalid_oidc_callback", meta); auditErr != nil {
			return sessionResult{}, "", auditErr
		}
		return sessionResult{}, "", appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调无效")
	}
	claims, err := s.oidc.Exchange(ctx, code, loginState.PKCEVerifier, loginState.Nonce)
	if err != nil {
		if auditErr := s.auditFailure(ctx, "auth_oidc_login_failed", "oidc_verification_failed", meta); auditErr != nil {
			return sessionResult{}, "", auditErr
		}
		return sessionResult{}, "", appError(apperror.KindUnauthenticated, "oidc_verification_failed", "OIDC 身份验证失败")
	}
	userID, err := s.identifier("usr", 18)
	if err != nil {
		return sessionResult{}, "", err
	}
	raw, err := s.token(32)
	if err != nil {
		return sessionResult{}, "", err
	}
	expires := now.Add(AbsoluteExpiry)
	event, err := s.event("auth_oidc_login_succeeded", "success", nil, &userID, &claims.DisplayName, meta)
	if err != nil {
		return sessionResult{}, "", err
	}
	created := NewSession{Hash: s.digest("session", raw), UserID: userID, AuthMethod: identity.AuthMethodOIDC, CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(IdleTimeout), ExpiresAt: expires}
	user, err := s.store.CompleteOIDCLogin(ctx, claims, userID, now, created, event)
	if err != nil {
		if errors.Is(err, ErrUserDisabled) {
			if auditErr := s.auditFailure(ctx, "auth_oidc_login_failed", "user_disabled", meta); auditErr != nil {
				return sessionResult{}, "", auditErr
			}
			return sessionResult{}, "", appError(apperror.KindUnauthenticated, "invalid_credentials", "身份凭据无效")
		}
		return sessionResult{}, "", fmt.Errorf("完成 OIDC 登录: %w", err)
	}
	principal, models, err := s.principalWithModels(ctx, user.ID, user.DisplayName, user.Email, identity.AuthMethodOIDC)
	if err != nil {
		return sessionResult{}, "", fmt.Errorf("读取当前权限: %w", err)
	}
	return sessionResult{Raw: raw, Response: SessionResponse{Principal: principal, ContentModels: models, CSRFToken: s.csrf(raw), IdleExpiresAt: created.IdleExpiresAt, ExpiresAt: expires}}, loginState.ReturnTo, nil
}

func (s *Service) RejectOIDCCallback(ctx context.Context, state, binding string, meta RequestMeta) error {
	now := s.clock.Now().UTC()
	if state == "" {
		if err := s.auditFailure(ctx, "auth_oidc_login_failed", "invalid_oidc_callback", meta); err != nil {
			return err
		}
		return appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调无效")
	}
	if binding == "" {
		if err := s.auditFailure(ctx, "auth_oidc_login_failed", "invalid_oidc_callback", meta); err != nil {
			return err
		}
		return appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调无效")
	}
	_, err := s.store.ConsumeLoginState(ctx, s.digest("state", state), s.digest("oidc_binding", binding), now)
	if err != nil {
		if err := s.auditFailure(ctx, "auth_oidc_login_failed", "invalid_oidc_callback", meta); err != nil {
			return err
		}
		return appError(apperror.KindInvalidArgument, "invalid_oidc_callback", "OIDC 回调无效")
	}
	if err := s.auditFailure(ctx, "auth_oidc_login_failed", "oidc_provider_error", meta); err != nil {
		return err
	}
	return appError(apperror.KindUnauthenticated, "oidc_verification_failed", "OIDC 身份验证失败")
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
	return s.createSession(ctx, user, identity.AuthMethodLocal, "auth_local_login_succeeded", meta)
}

func (s *Service) CurrentSession(ctx context.Context, raw string) (SessionResponse, error) {
	hash := s.digest("session", raw)
	session, err := s.store.Session(ctx, hash)
	now := s.clock.Now().UTC()
	if err != nil || !session.Enabled || !now.Before(session.IdleExpiresAt) || !now.Before(session.ExpiresAt) {
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

func (s *Service) ResetEmergencyAdmin(ctx context.Context, userID, username, displayName, password string, meta RequestMeta) error {
	hash, err := HashPassword(password, s.random)
	if err != nil {
		return err
	}
	if userID == "" {
		userID, err = s.identifier("usr", 18)
		if err != nil {
			return err
		}
	}
	event, err := s.event("auth_local_password_reset", "success", nil, nil, nil, meta)
	if err != nil {
		return err
	}
	return s.store.UpsertEmergencyAdmin(ctx, userID, username, displayName, hash, s.clock.Now().UTC(), event)
}

func (s *Service) createSession(ctx context.Context, user User, method identity.AuthMethod, action string, meta RequestMeta) (sessionResult, error) {
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
	created := NewSession{Hash: s.digest("session", raw), UserID: user.ID, AuthMethod: method, CreatedAt: now, LastSeenAt: now, IdleExpiresAt: now.Add(IdleTimeout), ExpiresAt: expires}
	if err := s.store.CreateSession(ctx, created, event); err != nil {
		return sessionResult{}, fmt.Errorf("创建认证会话: %w", err)
	}
	return sessionResult{Raw: raw, Response: SessionResponse{Principal: principal, ContentModels: models, CSRFToken: s.csrf(raw), IdleExpiresAt: created.IdleExpiresAt, ExpiresAt: expires}}, nil
}

func (s *Service) principalWithModels(ctx context.Context, userID, displayName string, email *string, method identity.AuthMethod) (identity.Principal, []SessionModelSummary, error) {
	system, grants, err := s.permissionSet(ctx, userID)
	if err != nil {
		return identity.Principal{}, nil, err
	}
	principal := identity.NewPrincipal(userID, displayName, email, method, system, grants)
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

func (s *Service) permissionSet(ctx context.Context, userID string) ([]string, []identity.ModelPermissions, error) {
	if s.permissions == nil {
		return []string{}, []identity.ModelPermissions{}, nil
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

func validReturnTo(value string) bool {
	if value == "" {
		return true
	}
	if !strings.HasPrefix(value, "/") || strings.HasPrefix(value, "//") || strings.Contains(value, "\\") {
		return false
	}
	parsed, err := url.Parse(value)
	return err == nil && parsed.IsAbs() == false && parsed.Host == ""
}

func sessionInvalid() error {
	return appError(apperror.KindUnauthenticated, "session_invalid", "认证会话无效")
}
func appError(kind apperror.Kind, code, message string) error {
	return &apperror.Error{Kind: kind, Code: code, Message: message}
}
