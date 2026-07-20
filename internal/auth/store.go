package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"cms/internal/audit"
	"cms/internal/platform/database"
)

var (
	ErrNotFound     = errors.New("记录不存在")
	ErrUserDisabled = errors.New("用户已禁用")
)

type SQLStore struct {
	db    *sql.DB
	tx    database.Transactor
	audit audit.Writer
}

func NewSQLStore(db *sql.DB, writer audit.Writer) *SQLStore {
	return &SQLStore{db: db, tx: database.NewTransactor(db), audit: writer}
}

func (s *SQLStore) SaveLoginState(ctx context.Context, state LoginState) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO oidc_login_states
		(state_hash, browser_binding_hash, nonce, pkce_verifier, return_to, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		state.Hash, state.BindingHash, state.Nonce, state.PKCEVerifier, state.ReturnTo, state.ExpiresAt.UTC(), state.CreatedAt.UTC())
	return err
}

func (s *SQLStore) ConsumeLoginState(ctx context.Context, hash, bindingHash []byte, now time.Time) (LoginState, error) {
	var result LoginState
	expired := false
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		row := q.QueryRowContext(ctx, `SELECT browser_binding_hash, nonce, pkce_verifier, return_to, expires_at, created_at
			FROM oidc_login_states WHERE state_hash = ? AND browser_binding_hash = ? FOR UPDATE`, hash, bindingHash)
		if err := row.Scan(&result.BindingHash, &result.Nonce, &result.PKCEVerifier, &result.ReturnTo, &result.ExpiresAt, &result.CreatedAt); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return ErrNotFound
			}
			return err
		}
		if _, err := q.ExecContext(ctx, `DELETE FROM oidc_login_states WHERE state_hash = ?`, hash); err != nil {
			return err
		}
		if !now.Before(result.ExpiresAt) {
			expired = true
		}
		return nil
	})
	if err == nil && expired {
		err = ErrNotFound
	}
	return result, err
}

func (s *SQLStore) DeleteExpiredLoginStates(ctx context.Context, now time.Time, limit int) error {
	if limit <= 0 {
		return nil
	}
	_, err := s.db.ExecContext(ctx, `DELETE FROM oidc_login_states WHERE expires_at <= ? ORDER BY expires_at LIMIT ?`, now.UTC(), limit)
	return err
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

func (s *SQLStore) CompleteOIDCLogin(ctx context.Context, subject OIDCIdentity, userID string, now time.Time, session NewSession, event audit.Event) (User, error) {
	var result User
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		var email sql.NullString
		err := q.QueryRowContext(ctx, `SELECT u.id, u.display_name, u.email, u.enabled
			FROM oidc_identities o JOIN users u ON u.id = o.user_id
			WHERE o.issuer = ? AND o.subject = ? FOR UPDATE`, subject.Issuer, subject.Subject).
			Scan(&result.ID, &result.DisplayName, &email, &result.Enabled)
		if err == nil {
			if email.Valid {
				result.Email = &email.String
			}
			if !result.Enabled {
				return ErrUserDisabled
			}
			_, err = q.ExecContext(ctx, `UPDATE users SET display_name = ?, email = ?, updated_at = ? WHERE id = ?`, subject.DisplayName, subject.Email, now.UTC(), result.ID)
			result.DisplayName, result.Email = subject.DisplayName, subject.Email
			if err != nil {
				return err
			}
		} else {
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO users (id, display_name, email, enabled, created_at, updated_at)
				VALUES (?, ?, ?, TRUE, ?, ?)`, userID, subject.DisplayName, subject.Email, now.UTC(), now.UTC()); err != nil {
				return err
			}
			if _, err := q.ExecContext(ctx, `INSERT INTO oidc_identities (issuer, subject, user_id, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?)`, subject.Issuer, subject.Subject, userID, now.UTC(), now.UTC()); err != nil {
				return err
			}
			result = User{ID: userID, DisplayName: subject.DisplayName, Email: subject.Email, Enabled: true}
		}
		session.UserID = result.ID
		if _, err := q.ExecContext(ctx, `INSERT INTO sessions
			(id_hash, user_id, auth_method, created_at, last_seen_at, idle_expires_at, expires_at, revoked_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, NULL)`, session.Hash, session.UserID, session.AuthMethod,
			session.CreatedAt.UTC(), session.LastSeenAt.UTC(), session.IdleExpiresAt.UTC(), session.ExpiresAt.UTC()); err != nil {
			return err
		}
		event.ActorID = &result.ID
		event.ActorDisplayName = &result.DisplayName
		return s.audit.Append(ctx, q, event)
	})
	return result, err
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
