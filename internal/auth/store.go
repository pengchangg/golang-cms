package auth

import (
	"context"
	"crypto/hmac"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"cms/internal/audit"
	"cms/internal/platform/database"
)

var (
	ErrNotFound         = errors.New("记录不存在")
	ErrUserDisabled     = errors.New("用户已禁用")
	ErrInvalidChallenge = errors.New("挑战验证失败")
)

type SQLStore struct {
	db    *sql.DB
	tx    database.Transactor
	audit audit.Writer
}

func NewSQLStore(db *sql.DB, writer audit.Writer) *SQLStore {
	return &SQLStore{db: db, tx: database.NewTransactor(db), audit: writer}
}

func (s *SQLStore) SaveCaptchaChallenge(ctx context.Context, challenge CaptchaChallenge) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO captcha_challenges
		(id_hash, browser_binding_hash, target_x, target_y, attempts_remaining, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, NULL, ?)`, challenge.Hash, challenge.BindingHash, challenge.TargetX, challenge.TargetY,
		challenge.AttemptsRemaining, challenge.ExpiresAt.UTC(), challenge.CreatedAt.UTC())
	return err
}

func (s *SQLStore) VerifyCaptchaChallenge(ctx context.Context, hash, bindingHash []byte, x, y, padding int, now time.Time) error {
	validationErr := error(nil)
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		var targetX, targetY, attempts int
		var expires time.Time
		err := q.QueryRowContext(ctx, `SELECT target_x, target_y, attempts_remaining, expires_at FROM captcha_challenges
			WHERE id_hash = ? AND browser_binding_hash = ? AND consumed_at IS NULL FOR UPDATE`, hash, bindingHash).
			Scan(&targetX, &targetY, &attempts, &expires)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (!now.Before(expires) || attempts <= 0) {
			validationErr = ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		valid := x >= targetX-padding && x <= targetX+padding && y >= targetY-padding && y <= targetY+padding
		if valid {
			_, err = q.ExecContext(ctx, `UPDATE captcha_challenges SET consumed_at = ? WHERE id_hash = ?`, now.UTC(), hash)
			return err
		}
		_, err = q.ExecContext(ctx, `UPDATE captcha_challenges SET attempts_remaining = attempts_remaining - 1,
			consumed_at = CASE WHEN attempts_remaining = 1 THEN ? ELSE NULL END WHERE id_hash = ?`, now.UTC(), hash)
		if err != nil {
			return err
		}
		validationErr = ErrInvalidChallenge
		return nil
	})
	if err != nil {
		return err
	}
	return validationErr
}

func (s *SQLStore) SaveSMSChallenge(ctx context.Context, challenge SMSChallenge) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sms_challenges
		(id_hash, browser_binding_hash, phone_e164, phone_masked, otp_hash, attempts_remaining, expires_at, consumed_at, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, NULL, ?)`, challenge.Hash, challenge.BindingHash, challenge.PhoneE164,
		challenge.PhoneMasked, challenge.OTPHash, challenge.AttemptsRemaining, challenge.ExpiresAt.UTC(), challenge.CreatedAt.UTC())
	return err
}

func (s *SQLStore) ConsumeSMSChallenge(ctx context.Context, hash, bindingHash, otpHash []byte, now time.Time) (string, error) {
	var phone string
	validationErr := error(nil)
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		var storedHash []byte
		var attempts int
		var expires time.Time
		err := q.QueryRowContext(ctx, `SELECT phone_e164, otp_hash, attempts_remaining, expires_at FROM sms_challenges
			WHERE id_hash = ? AND browser_binding_hash = ? AND consumed_at IS NULL FOR UPDATE`, hash, bindingHash).
			Scan(&phone, &storedHash, &attempts, &expires)
		if errors.Is(err, sql.ErrNoRows) || err == nil && (!now.Before(expires) || attempts <= 0) {
			validationErr = ErrNotFound
			return nil
		}
		if err != nil {
			return err
		}
		if !hmac.Equal(storedHash, otpHash) {
			_, err = q.ExecContext(ctx, `UPDATE sms_challenges SET attempts_remaining = attempts_remaining - 1,
				consumed_at = CASE WHEN attempts_remaining = 1 THEN ? ELSE NULL END WHERE id_hash = ?`, now.UTC(), hash)
			if err != nil {
				return err
			}
			validationErr = ErrInvalidChallenge
			return nil
		}
		_, err = q.ExecContext(ctx, `UPDATE sms_challenges SET consumed_at = ? WHERE id_hash = ?`, now.UTC(), hash)
		return err
	})
	if err == nil && validationErr != nil {
		err = validationErr
	}
	return phone, err
}

func (s *SQLStore) AllowRateLimit(ctx context.Context, scope string, keyHash []byte, now time.Time, window time.Duration, limit int) (bool, error) {
	allowed := false
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		var started time.Time
		var count int
		err := q.QueryRowContext(ctx, `SELECT window_started_at, request_count FROM auth_rate_limits
			WHERE scope = ? AND key_hash = ? FOR UPDATE`, scope, keyHash).Scan(&started, &count)
		if errors.Is(err, sql.ErrNoRows) {
			_, err = q.ExecContext(ctx, `INSERT INTO auth_rate_limits
				(scope, key_hash, window_started_at, request_count, expires_at) VALUES (?, ?, ?, 1, ?)`,
				scope, keyHash, now.UTC(), now.Add(window).UTC())
			allowed = err == nil
			return err
		}
		if err != nil {
			return err
		}
		if !now.Before(started.Add(window)) {
			_, err = q.ExecContext(ctx, `UPDATE auth_rate_limits SET window_started_at = ?, request_count = 1, expires_at = ?
				WHERE scope = ? AND key_hash = ?`, now.UTC(), now.Add(window).UTC(), scope, keyHash)
			allowed = err == nil
			return err
		}
		if count >= limit {
			return nil
		}
		_, err = q.ExecContext(ctx, `UPDATE auth_rate_limits SET request_count = request_count + 1
			WHERE scope = ? AND key_hash = ?`, scope, keyHash)
		allowed = err == nil
		return err
	})
	return allowed, err
}

func (s *SQLStore) FindLocalUser(ctx context.Context, username string) (User, error) {
	var user User
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.display_name, u.email, u.enabled, c.password_hash
		FROM local_credentials c JOIN users u ON u.id = c.user_id
		WHERE c.username = ? AND c.emergency_admin = TRUE`, username).
		Scan(&user.ID, &user.DisplayName, &email, &user.Enabled, &user.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if email.Valid {
		user.Email = &email.String
	}
	return user, err
}

func (s *SQLStore) FindPhoneUser(ctx context.Context, phone string) (User, error) {
	var user User
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT u.id, u.display_name, u.email, u.enabled
		FROM sms_credentials c JOIN users u ON u.id = c.user_id WHERE c.phone_e164 = ?`, phone).
		Scan(&user.ID, &user.DisplayName, &email, &user.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if email.Valid {
		user.Email = &email.String
	}
	return user, err
}

func (s *SQLStore) CreateSession(ctx context.Context, session NewSession, event audit.Event) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		_, err := q.ExecContext(ctx, `INSERT INTO sessions
			(id_hash, user_id, auth_method, created_at, last_seen_at, idle_expires_at, expires_at, revoked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`, session.Hash, session.UserID, session.AuthMethod,
			session.CreatedAt.UTC(), session.LastSeenAt.UTC(), session.IdleExpiresAt.UTC(), session.ExpiresAt.UTC())
		if err != nil {
			return err
		}
		return s.audit.Append(ctx, q, event)
	})
}

func (s *SQLStore) Session(ctx context.Context, hash []byte) (Session, error) {
	var result Session
	var email sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT s.user_id, u.display_name, u.email, u.enabled, s.auth_method,
		s.idle_expires_at, s.expires_at FROM sessions s JOIN users u ON u.id = s.user_id
		WHERE s.id_hash = ? AND s.revoked_at IS NULL`, hash).
		Scan(&result.UserID, &result.DisplayName, &email, &result.Enabled, &result.AuthMethod, &result.IdleExpiresAt, &result.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if email.Valid {
		result.Email = &email.String
	}
	return result, err
}

func (s *SQLStore) TouchSession(ctx context.Context, hash []byte, now, idleExpiry time.Time) error {
	result, err := s.db.ExecContext(ctx, `UPDATE sessions SET last_seen_at = ?, idle_expires_at = ?
		WHERE id_hash = ? AND revoked_at IS NULL AND idle_expires_at > ? AND expires_at > ?
		AND EXISTS (SELECT 1 FROM users WHERE users.id = sessions.user_id AND users.enabled = TRUE)`,
		now.UTC(), idleExpiry.UTC(), hash, now.UTC(), now.UTC())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *SQLStore) RevokeSession(ctx context.Context, hash []byte, now time.Time, event audit.Event) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		result, err := q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ?
			WHERE id_hash = ? AND revoked_at IS NULL AND idle_expires_at > ? AND expires_at > ?
			AND EXISTS (SELECT 1 FROM users WHERE users.id = sessions.user_id AND users.enabled = TRUE)`, now.UTC(), hash, now.UTC(), now.UTC())
		if err != nil {
			return err
		}
		count, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if count != 1 {
			return ErrNotFound
		}
		return s.audit.Append(ctx, q, event)
	})
}

func (s *SQLStore) AppendFailure(ctx context.Context, event audit.Event) error {
	return s.audit.Append(ctx, s.db, event)
}

func (s *SQLStore) UpsertEmergencyAdmin(ctx context.Context, userID, username, displayName, passwordHash string, now time.Time, event audit.Event) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		var existingID string
		err := q.QueryRowContext(ctx, `SELECT user_id FROM local_credentials WHERE username = ? FOR UPDATE`, username).Scan(&existingID)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if errors.Is(err, sql.ErrNoRows) {
			if _, err := q.ExecContext(ctx, `INSERT INTO users (id, display_name, enabled, created_at, updated_at) VALUES (?, ?, TRUE, ?, ?)`, userID, displayName, now.UTC(), now.UTC()); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO local_credentials (user_id, username, password_hash, emergency_admin, created_at, updated_at) VALUES (?, ?, ?, TRUE, ?, ?)`, userID, username, passwordHash, now.UTC(), now.UTC()); err != nil {
				return err
			}
			event.ResourceID = &userID
		} else {
			if _, err := q.ExecContext(ctx, `UPDATE users SET display_name = ?, enabled = TRUE, updated_at = ? WHERE id = ?`, displayName, now.UTC(), existingID); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `UPDATE local_credentials SET password_hash = ?, emergency_admin = TRUE, updated_at = ? WHERE user_id = ?`, passwordHash, now.UTC(), existingID); err != nil {
				return err
			}
			event.ResourceID = &existingID
		}
		targetID := userID
		if existingID != "" {
			targetID = existingID
		}
		if _, err := q.ExecContext(ctx, `UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`, now.UTC(), targetID); err != nil {
			return err
		}
		return s.audit.Append(ctx, q, event)
	})
}

func (s *SQLStore) String() string { return fmt.Sprintf("SQLStore(%p)", s.db) }
