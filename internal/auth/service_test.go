package auth

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"cms/internal/audit"
	"cms/internal/identity"
)

type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time { return c.now }

type fakePermissions struct{}

func (fakePermissions) Permissions(context.Context, string) (identity.PermissionSet, error) {
	return identity.PermissionSet{System: []string{"models.view", "models.view"}, Models: []identity.ModelPermissions{{ModelID: "a", Permissions: []string{"content.view"}}}}, nil
}

type fakeModels struct{}

func (fakeModels) ActiveModelSummaries(context.Context, []string) ([]SessionModelSummary, error) {
	return []SessionModelSummary{{ID: "a", Key: "articles", DisplayName: "文章"}}, nil
}

type fakeCaptcha struct{}

func (fakeCaptcha) Generate() (CaptchaData, error) {
	return CaptchaData{BackgroundImage: "data:image/jpeg;base64,bg", TileImage: "data:image/png;base64,tile", TileX: 5, TileY: 40, TargetX: 180, TargetY: 40}, nil
}

type recordingSMSProvider struct {
	sent int
}

func (p *recordingSMSProvider) SendCode(context.Context, string, string, time.Duration) error {
	p.sent++
	return nil
}

type memoryStore struct {
	local      User
	phones     map[string]User
	captchas   map[string]CaptchaChallenge
	sms        map[string]SMSChallenge
	sessions   map[string]Session
	rates      map[string]int
	failures   []audit.Event
	failureErr error
}

func newMemoryStore() *memoryStore {
	return &memoryStore{phones: map[string]User{}, captchas: map[string]CaptchaChallenge{}, sms: map[string]SMSChallenge{}, sessions: map[string]Session{}, rates: map[string]int{}}
}

func key(value []byte) string { return string(value) }

func (m *memoryStore) SaveCaptchaChallenge(_ context.Context, value CaptchaChallenge) error {
	m.captchas[key(value.Hash)] = value
	return nil
}

func (m *memoryStore) VerifyCaptchaChallenge(_ context.Context, hash, binding []byte, x, y, padding int, now time.Time) error {
	value, ok := m.captchas[key(hash)]
	if !ok || !bytes.Equal(value.BindingHash, binding) || !now.Before(value.ExpiresAt) || value.AttemptsRemaining <= 0 {
		return ErrNotFound
	}
	if x < value.TargetX-padding || x > value.TargetX+padding || y < value.TargetY-padding || y > value.TargetY+padding {
		value.AttemptsRemaining--
		m.captchas[key(hash)] = value
		return ErrInvalidChallenge
	}
	delete(m.captchas, key(hash))
	return nil
}

func (m *memoryStore) SaveSMSChallenge(_ context.Context, value SMSChallenge) error {
	m.sms[key(value.Hash)] = value
	return nil
}

func (m *memoryStore) ConsumeSMSChallenge(_ context.Context, hash, binding, otp []byte, now time.Time) (string, *string, error) {
	value, ok := m.sms[key(hash)]
	if !ok || !bytes.Equal(value.BindingHash, binding) || !now.Before(value.ExpiresAt) || value.AttemptsRemaining <= 0 {
		return "", nil, ErrNotFound
	}
	if !bytes.Equal(value.OTPHash, otp) {
		value.AttemptsRemaining--
		m.sms[key(hash)] = value
		return "", nil, ErrInvalidChallenge
	}
	delete(m.sms, key(hash))
	return value.PhoneE164, value.UserID, nil
}

func (m *memoryStore) AllowRateLimit(_ context.Context, scope string, hash []byte, _ time.Time, _ time.Duration, limit int) (bool, error) {
	k := scope + key(hash)
	m.rates[k]++
	return m.rates[k] <= limit, nil
}

func (m *memoryStore) FindLocalUser(_ context.Context, username string) (User, error) {
	if username != "admin" {
		return User{}, ErrNotFound
	}
	return m.local, nil
}

func (m *memoryStore) FindPhoneUser(_ context.Context, phone string) (User, error) {
	user, ok := m.phones[phone]
	if !ok {
		return User{}, ErrNotFound
	}
	return user, nil
}

func (m *memoryStore) CreateSession(_ context.Context, value NewSession, _ audit.Event) error {
	user := m.local
	if value.AuthMethod == identity.AuthMethodSMS {
		candidate, ok := m.phones[value.PhoneE164]
		if !ok || candidate.ID != value.UserID || !candidate.Enabled {
			return ErrInvalidChallenge
		}
		for _, candidate := range m.phones {
			if candidate.ID == value.UserID {
				user = candidate
			}
		}
	} else if value.AuthMethod == identity.AuthMethodLocal && (!m.local.Enabled || m.local.PasswordHash != value.PasswordHash) {
		return ErrInvalidChallenge
	}
	m.sessions[key(value.Hash)] = Session{UserID: value.UserID, DisplayName: user.DisplayName, Email: user.Email, Enabled: user.Enabled, AuthMethod: value.AuthMethod, IdleExpiresAt: value.IdleExpiresAt, ExpiresAt: value.ExpiresAt}
	return nil
}

func TestSMSLoginRejectsChallengeAfterPhoneChanges(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 21, 1, 2, 3, 0, time.UTC)}
	store := newMemoryStore()
	service := testService(t, store, clock)
	challengeID, binding := "challenge", "binding"
	challengeHash := service.digest("sms_challenge", challengeID)
	store.sms[key(challengeHash)] = SMSChallenge{
		Hash: challengeHash, BindingHash: service.digest("captcha_binding", binding), PhoneE164: "+8613800138000",
		UserID: stringPointer("usr_sms"), OTPHash: service.digest("sms_otp", challengeID+"\x00"+"123456"), AttemptsRemaining: 1, ExpiresAt: clock.now.Add(time.Minute),
	}
	store.phones["+8613800138000"] = User{ID: "usr_other", DisplayName: "另一用户", Enabled: true}

	if _, err := service.VerifySMSChallenge(context.Background(), challengeID, "123456", binding, RequestMeta{IP: "127.0.0.1"}); err == nil {
		t.Fatal("号码转绑后旧挑战登录了另一用户")
	}
	if len(store.sessions) != 0 {
		t.Fatalf("旧挑战创建了会话: %v", store.sessions)
	}
}

func stringPointer(value string) *string { return &value }

func (m *memoryStore) Session(_ context.Context, hash []byte) (Session, error) {
	value, ok := m.sessions[key(hash)]
	if !ok {
		return Session{}, ErrNotFound
	}
	return value, nil
}

func (m *memoryStore) TouchSession(_ context.Context, hash []byte, _ time.Time, idle time.Time) error {
	value := m.sessions[key(hash)]
	value.IdleExpiresAt = idle
	m.sessions[key(hash)] = value
	return nil
}

func (m *memoryStore) RevokeSession(_ context.Context, hash []byte, _ time.Time, _ audit.Event) error {
	delete(m.sessions, key(hash))
	return nil
}

func (m *memoryStore) AppendFailure(_ context.Context, event audit.Event) error {
	m.failures = append(m.failures, event)
	return m.failureErr
}

func (m *memoryStore) UpsertEmergencyAdmin(context.Context, string, string, string, string, bool, time.Time, audit.Event) (bool, error) {
	return true, nil
}

func testService(t *testing.T, store *memoryStore, clock *fakeClock) *Service {
	t.Helper()
	service, err := NewService(store, fakePermissions{}, fakeModels{}, FixedSMSProvider{Code: "123456"}, fakeCaptcha{}, clock, bytes.NewReader(bytes.Repeat([]byte{7}, 8192)), "01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSMSLoginTwoStagesCreatesSessionAndConsumesChallenge(t *testing.T) {
	clock := &fakeClock{now: time.Date(2026, 7, 21, 1, 2, 3, 0, time.UTC)}
	store := newMemoryStore()
	store.phones["+8613800138000"] = User{ID: "usr_sms", DisplayName: "短信用户", Enabled: true}
	service := testService(t, store, clock)
	captcha, binding, err := service.CreateCaptcha(context.Background(), "", RequestMeta{IP: "127.0.0.1"})
	if err != nil || binding == "" || captcha.BackgroundImage == "" {
		t.Fatalf("CreateCaptcha() = (%+v, %q, %v)", captcha, binding, err)
	}
	challenge, err := service.CreateSMSChallenge(context.Background(), "13800138000", captcha.ChallengeID, binding, 183, 42, RequestMeta{IP: "127.0.0.1"})
	if err != nil || challenge.PhoneMasked != "138****8000" || challenge.RetryAfterSeconds != 60 {
		t.Fatalf("CreateSMSChallenge() = (%+v, %v)", challenge, err)
	}
	result, err := service.VerifySMSChallenge(context.Background(), challenge.ChallengeID, "123456", binding, RequestMeta{RequestID: "req", IP: "127.0.0.1"})
	if err != nil || result.Response.Principal.UserID != "usr_sms" || result.Response.Principal.AuthMethod != identity.AuthMethodSMS || result.Response.CSRFToken == "" {
		t.Fatalf("VerifySMSChallenge() = (%+v, %v)", result, err)
	}
	if _, err := service.VerifySMSChallenge(context.Background(), challenge.ChallengeID, "123456", binding, RequestMeta{IP: "127.0.0.1"}); err == nil {
		t.Fatal("短信挑战被重复消费")
	}
}

func TestSMSChallengeRejectsWrongBindingAndExhaustsAttempts(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	service := testService(t, store, clock)
	captcha, binding, _ := service.CreateCaptcha(context.Background(), "", RequestMeta{IP: "1"})
	if _, err := service.CreateSMSChallenge(context.Background(), "13800138000", captcha.ChallengeID, "wrong", 180, 40, RequestMeta{IP: "1"}); err == nil {
		t.Fatal("错误浏览器绑定通过行为验证")
	}
	for i := 0; i < CaptchaMaxAttempts; i++ {
		_, _ = service.CreateSMSChallenge(context.Background(), "13800138000", captcha.ChallengeID, binding, 1, 1, RequestMeta{IP: "1"})
	}
	if _, err := service.CreateSMSChallenge(context.Background(), "13800138000", captcha.ChallengeID, binding, 180, 40, RequestMeta{IP: "1"}); err == nil {
		t.Fatal("耗尽尝试次数后仍可通过行为验证")
	}
}

func TestSMSLoginDoesNotEnumeratePhoneCredential(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	service := testService(t, store, clock)
	captcha, binding, _ := service.CreateCaptcha(context.Background(), "", RequestMeta{IP: "1"})
	challenge, err := service.CreateSMSChallenge(context.Background(), "+8613800138000", captcha.ChallengeID, binding, 180, 40, RequestMeta{IP: "1"})
	if err != nil || challenge.PhoneMasked != "138****8000" {
		t.Fatalf("未绑定手机号发送阶段泄漏状态: %+v %v", challenge, err)
	}
	_, err = service.VerifySMSChallenge(context.Background(), challenge.ChallengeID, "123456", binding, RequestMeta{IP: "1"})
	if err == nil || len(store.failures) != 1 || store.failures[0].FailureCode == nil || *store.failures[0].FailureCode != "invalid_credentials" {
		t.Fatalf("未绑定手机号验证结果不统一: %v %+v", err, store.failures)
	}
}

func TestSMSChallengeOnlySendsToEnabledProvisionedUser(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	provider := &recordingSMSProvider{}
	service, err := NewService(store, fakePermissions{}, fakeModels{}, provider, fakeCaptcha{}, clock, bytes.NewReader(bytes.Repeat([]byte{7}, 8192)), "01234567890123456789012345678901")
	if err != nil {
		t.Fatal(err)
	}

	create := func(phone, ip string) {
		captcha, binding, captchaErr := service.CreateCaptcha(context.Background(), "", RequestMeta{IP: ip})
		if captchaErr != nil {
			t.Fatal(captchaErr)
		}
		if _, challengeErr := service.CreateSMSChallenge(context.Background(), phone, captcha.ChallengeID, binding, 180, 40, RequestMeta{IP: ip}); challengeErr != nil {
			t.Fatal(challengeErr)
		}
	}
	create("13800138000", "1")
	store.phones["+8613900139000"] = User{ID: "disabled", Enabled: false}
	create("13900139000", "2")
	store.phones["+8613700137000"] = User{ID: "enabled", Enabled: true}
	create("13700137000", "3")
	if provider.sent != 1 {
		t.Fatalf("短信发送次数 = %d，期望 1", provider.sent)
	}
}

func TestSMSChallengeEnforcesResendInterval(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	store.phones["+8613800138000"] = User{ID: "usr_sms", Enabled: true}
	service := testService(t, store, clock)
	create := func() error {
		captcha, binding, err := service.CreateCaptcha(context.Background(), "", RequestMeta{IP: "1"})
		if err != nil {
			return err
		}
		_, err = service.CreateSMSChallenge(context.Background(), "13800138000", captcha.ChallengeID, binding, 180, 40, RequestMeta{IP: "1"})
		return err
	}
	if err := create(); err != nil {
		t.Fatal(err)
	}
	if err := create(); err == nil {
		t.Fatal("重发间隔未生效")
	}
}

func TestSMSChallengeExhaustsOTPAttempts(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	store.phones["+8613800138000"] = User{ID: "usr_sms", DisplayName: "短信用户", Enabled: true}
	service := testService(t, store, clock)
	captcha, binding, _ := service.CreateCaptcha(context.Background(), "", RequestMeta{IP: "1"})
	challenge, err := service.CreateSMSChallenge(context.Background(), "13800138000", captcha.ChallengeID, binding, 180, 40, RequestMeta{IP: "1"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < SMSMaxAttempts; i++ {
		_, _ = service.VerifySMSChallenge(context.Background(), challenge.ChallengeID, "000000", binding, RequestMeta{IP: "1"})
	}
	if _, err := service.VerifySMSChallenge(context.Background(), challenge.ChallengeID, "123456", binding, RequestMeta{IP: "1"}); err == nil {
		t.Fatal("耗尽尝试次数后仍接受正确验证码")
	}
}

func TestGoCaptchaGeneratorProducesSlideImages(t *testing.T) {
	data, err := NewGoCaptchaGenerator().Generate()
	if err != nil {
		t.Fatal(err)
	}
	if data.BackgroundImage == "" || data.TileImage == "" || data.TargetX <= data.TileX || data.TargetY != data.TileY {
		t.Fatalf("滑动拼图数据不完整: %+v", data)
	}
}

func TestLocalLoginRemainsAvailable(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	hash, _ := HashPassword("correct horse", bytes.NewReader(bytes.Repeat([]byte{1}, 16)))
	store.local = User{ID: "usr_local", DisplayName: "Admin", Enabled: true, PasswordHash: hash}
	service := testService(t, store, clock)
	result, err := service.LocalLogin(context.Background(), "admin", "correct horse", RequestMeta{IP: "127.0.0.1"})
	if err != nil || result.Response.Principal.AuthMethod != identity.AuthMethodLocal {
		t.Fatalf("LocalLogin() = (%+v, %v)", result, err)
	}
	if _, err := service.LocalLogin(context.Background(), "admin", "wrong", RequestMeta{IP: "127.0.0.2"}); err == nil {
		t.Fatal("错误本地密码被接受")
	}
}

func TestLocalSessionRejectsPasswordChangedAfterVerification(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	oldHash, _ := HashPassword("old password", bytes.NewReader(bytes.Repeat([]byte{1}, 16)))
	newHash, _ := HashPassword("new password", bytes.NewReader(bytes.Repeat([]byte{2}, 16)))
	store.local = User{ID: "usr_local", DisplayName: "Admin", Enabled: true, PasswordHash: newHash}
	service := testService(t, store, clock)

	_, err := service.createSession(context.Background(), User{ID: "usr_local", DisplayName: "Admin", Enabled: true, PasswordHash: oldHash}, identity.AuthMethodLocal, "", "auth_local_login_succeeded", RequestMeta{IP: "127.0.0.1"})
	if !errors.Is(err, ErrInvalidChallenge) || len(store.sessions) != 0 {
		t.Fatalf("createSession() error = %v, sessions=%v", err, store.sessions)
	}
}

func TestUnknownAuthMethodFailsClosed(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	service := testService(t, store, clock)
	store.sessions[key(service.digest("session", "unknown"))] = Session{UserID: "usr_legacy", DisplayName: "旧用户", Enabled: true, AuthMethod: identity.AuthMethod("oidc"), IdleExpiresAt: clock.now.Add(time.Minute), ExpiresAt: clock.now.Add(time.Hour)}

	if _, err := service.CurrentSession(context.Background(), "unknown"); err == nil {
		t.Fatal("未知认证方式的历史会话仍然有效")
	}
	if _, err := service.createSession(context.Background(), User{ID: "usr_legacy", Enabled: true}, identity.AuthMethod("oidc"), "", "auth_unknown", RequestMeta{}); !errors.Is(err, ErrInvalidChallenge) {
		t.Fatalf("createSession() error = %v", err)
	}
}

func TestPersistentRateLimit(t *testing.T) {
	clock := &fakeClock{now: time.Now().UTC()}
	store := newMemoryStore()
	service := testService(t, store, clock)
	for i := 0; i < 30; i++ {
		if _, _, err := service.CreateCaptcha(context.Background(), "binding", RequestMeta{IP: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := service.CreateCaptcha(context.Background(), "binding", RequestMeta{IP: "1"}); err == nil {
		t.Fatal("持久限流未生效")
	}
}

func TestFailureAuditErrorPropagates(t *testing.T) {
	store := newMemoryStore()
	store.failureErr = errors.New("audit unavailable")
	service := testService(t, store, &fakeClock{now: time.Now().UTC()})
	if _, err := service.VerifySMSChallenge(context.Background(), "missing", "000000", "binding", RequestMeta{IP: "1"}); err == nil || !errors.Is(err, store.failureErr) {
		t.Fatalf("审计错误被静默丢弃: %v", err)
	}
}
