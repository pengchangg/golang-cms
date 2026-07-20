package client

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type Authorizer interface {
	RequireSystemPermission(context.Context, identity.Principal, string) error
}

type Dependencies struct {
	DB         database.Querier
	Transactor TransactionRunner
	Repository Repository
	Authorizer Authorizer
	Audit      audit.Writer
}

type Service struct {
	db         database.Querier
	tx         TransactionRunner
	repository Repository
	authorizer Authorizer
	audit      audit.Writer
	now        func() time.Time
	random     func([]byte) error
	newID      func(string) (string, error)
}

func NewService(dependencies Dependencies) *Service {
	return &Service{db: dependencies.DB, tx: dependencies.Transactor, repository: dependencies.Repository, authorizer: dependencies.Authorizer, audit: dependencies.Audit, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, random: func(value []byte) error { _, err := rand.Read(value); return err }, newID: randomID}
}

func (s *Service) List(ctx context.Context, principal identity.Principal, status APIKeyStatus, limit int, encodedCursor string) (APIKeyList, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "api_keys.view"); err != nil {
		return APIKeyList{}, err
	}
	if status != "" && status != APIKeyActive && status != APIKeyExpired && status != APIKeyRevoked {
		return APIKeyList{}, invalidQuery()
	}
	cursor, err := decodeAPIKeyCursor(encodedCursor, status)
	if err != nil {
		return APIKeyList{}, err
	}
	items, err := s.repository.List(ctx, s.db, status, limit+1, cursor, s.now())
	if err != nil {
		return APIKeyList{}, err
	}
	result := APIKeyList{Items: items}
	if len(items) > limit {
		result.Items = items[:limit]
		last := result.Items[limit-1]
		value, err := encodeAPIKeyCursor(last, status)
		if err != nil {
			return APIKeyList{}, err
		}
		result.NextCursor = &value
	}
	return result, nil
}

func (s *Service) Get(ctx context.Context, principal identity.Principal, id string) (APIKey, error) {
	if err := s.authorizer.RequireSystemPermission(ctx, principal, "api_keys.view"); err != nil {
		return APIKey{}, err
	}
	return s.repository.Get(ctx, s.db, id, false, s.now())
}

func (s *Service) Create(ctx context.Context, principal identity.Principal, meta RequestMeta, request CreateAPIKeyRequest) (APIKeySecret, error) {
	name, ids, expires, err := validateKeyInput(request.Name, request.ModelIDs, request.ExpiresAt, s.now())
	if err != nil {
		return APIKeySecret{}, err
	}
	generated, err := s.generate(name, ids, expires, principal.UserID, nil)
	if err != nil {
		return APIKeySecret{}, err
	}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "api_keys.create"); err != nil {
			return err
		}
		if err := s.repository.ValidateActiveModels(ctx, q, ids); err != nil {
			return err
		}
		if err := s.repository.Create(ctx, q, generated.APIKey); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "api_key_created", generated.APIKey)
	})
	return generated, err
}

func (s *Service) Revoke(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "api_keys.revoke"); err != nil {
			return err
		}
		key, err := s.repository.Get(ctx, q, id, true, s.now())
		if err != nil {
			return err
		}
		if key.RevokedAt != nil {
			return appError(apperror.KindConflict, "api_key_already_revoked", "API Key 已撤销")
		}
		now := s.now()
		if err := s.repository.Revoke(ctx, q, id, "", now); err != nil {
			return err
		}
		key.RevokedAt = &now
		key.Status = APIKeyRevoked
		return s.appendAudit(ctx, q, principal, meta, "api_key_revoked", key)
	})
}

func (s *Service) Rotate(ctx context.Context, principal identity.Principal, meta RequestMeta, id string, request RotateAPIKeyRequest) (APIKeySecret, error) {
	var result APIKeySecret
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.authorizer.RequireSystemPermission(ctx, principal, "api_keys.create"); err != nil {
			return err
		}
		old, err := s.repository.Get(ctx, q, id, true, s.now())
		if err != nil {
			return err
		}
		if old.RevokedAt != nil {
			return appError(apperror.KindConflict, "api_key_already_revoked", "API Key 已撤销")
		}
		if old.ExpiresAt != nil && !s.now().Before(*old.ExpiresAt) {
			return appError(apperror.KindConflict, "api_key_expired", "API Key 已过期")
		}
		name, ids, expires := old.Name, old.ModelIDs, old.ExpiresAt
		if request.Name != nil {
			name = *request.Name
		}
		if request.ModelIDs != nil {
			ids = *request.ModelIDs
		}
		if request.ExpiresAt.Set {
			expires = request.ExpiresAt.Value
		}
		name, ids, expires, err = validateKeyInput(name, ids, expires, s.now())
		if err != nil {
			return err
		}
		if err = s.repository.ValidateActiveModels(ctx, q, ids); err != nil {
			return err
		}
		result, err = s.generate(name, ids, expires, principal.UserID, &old.ID)
		if err != nil {
			return err
		}
		if err = s.repository.Create(ctx, q, result.APIKey); err != nil {
			return err
		}
		if err = s.repository.Revoke(ctx, q, old.ID, result.ID, s.now()); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "api_key_rotated", result.APIKey)
	})
	return result, err
}

func (s *Service) Authenticate(ctx context.Context, raw string) (AuthenticatedKey, error) {
	prefix, secret, err := parseRawKey(raw)
	if err != nil {
		return AuthenticatedKey{}, invalidAPIKey()
	}
	key, err := s.repository.FindByPrefix(ctx, s.db, prefix, s.now())
	if errors.Is(err, sql.ErrNoRows) {
		return AuthenticatedKey{}, invalidAPIKey()
	}
	if err != nil {
		return AuthenticatedKey{}, err
	}
	if key.RevokedAt != nil {
		return AuthenticatedKey{}, appError(apperror.KindUnauthenticated, "api_key_revoked", "API Key 已撤销")
	}
	if key.ExpiresAt != nil && !s.now().Before(*key.ExpiresAt) {
		return AuthenticatedKey{}, appError(apperror.KindUnauthenticated, "api_key_expired", "API Key 已过期")
	}
	digest := sha256.Sum256(append(append([]byte(nil), key.Salt...), secret...))
	if len(key.Hash) != sha256.Size || subtle.ConstantTimeCompare(digest[:], key.Hash) != 1 {
		return AuthenticatedKey{}, invalidAPIKey()
	}
	// 节流条件由数据库原子判断；写入失败不能改变本次鉴权结果。
	if key.LastUsedAt == nil || !key.LastUsedAt.After(s.now().Add(-5*time.Minute)) {
		_ = s.repository.TouchLastUsed(ctx, s.db, key.ID, s.now())
	}
	return AuthenticatedKey{ID: key.ID, Prefix: key.Prefix, ModelIDs: append([]string(nil), key.ModelIDs...)}, nil
}

func (s *Service) generate(name string, ids []string, expires *time.Time, createdBy string, rotatedFrom *string) (APIKeySecret, error) {
	prefixBytes := make([]byte, 8)
	secret := make([]byte, 32)
	salt := make([]byte, 16)
	for _, value := range [][]byte{prefixBytes, secret, salt} {
		if err := s.random(value); err != nil {
			return APIKeySecret{}, fmt.Errorf("生成 API Key: %w", err)
		}
	}
	prefix := strings.ToLower(base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(prefixBytes))[:12]
	digest := sha256.Sum256(append(append([]byte(nil), salt...), secret...))
	id, err := s.newID("key_")
	if err != nil {
		return APIKeySecret{}, err
	}
	key := APIKey{ID: id, Name: name, Prefix: prefix, ModelIDs: ids, Status: APIKeyActive, ExpiresAt: expires, RotatedFromID: rotatedFrom, CreatedBy: createdBy, CreatedAt: s.now(), Salt: salt, Hash: digest[:]}
	return APIKeySecret{APIKey: key, Key: "cmsk_" + prefix + "_" + base64.RawURLEncoding.EncodeToString(secret)}, nil
}

func validateKeyInput(name string, ids []string, expires *time.Time, now time.Time) (string, []string, *time.Time, error) {
	name = strings.TrimSpace(name)
	details := []map[string]any{}
	if count := utf8.RuneCountInString(name); count < 1 || count > 120 {
		details = append(details, map[string]any{"path": "/name", "code": "invalid_length", "message": "name 去除首尾空白后长度必须为 1 到 120"})
	}
	seen := map[string]bool{}
	normalized := []string{}
	for _, id := range ids {
		if id == "" {
			details = append(details, map[string]any{"path": "/model_ids", "code": "invalid_value", "message": "model_ids 不能包含空值"})
			continue
		}
		if !seen[id] {
			seen[id] = true
			normalized = append(normalized, id)
		}
	}
	if len(normalized) == 0 {
		details = append(details, map[string]any{"path": "/model_ids", "code": "required", "message": "至少需要一个模型范围"})
	}
	sort.Strings(normalized)
	if expires != nil {
		value := expires.UTC()
		expires = &value
		if !now.Before(value) {
			details = append(details, map[string]any{"path": "/expires_at", "code": "must_be_future", "message": "expires_at 必须晚于当前时间"})
		}
	}
	if len(details) > 0 {
		return "", nil, nil, validation(details...)
	}
	return name, normalized, expires, nil
}

func parseRawKey(raw string) (string, []byte, error) {
	parts := strings.SplitN(raw, "_", 3)
	if len(parts) != 3 || parts[0] != "cmsk" || len(parts[1]) != 12 {
		return "", nil, errors.New("格式无效")
	}
	for _, r := range parts[1] {
		if !(r >= 'a' && r <= 'z' || r >= '2' && r <= '7') {
			return "", nil, errors.New("prefix 无效")
		}
	}
	secret, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(secret) != 32 {
		return "", nil, errors.New("secret 无效")
	}
	return parts[1], secret, nil
}

func (s *Service) appendAudit(ctx context.Context, q database.Querier, principal identity.Principal, meta RequestMeta, action string, key APIKey) error {
	id, err := s.newID("evt_")
	if err != nil {
		return err
	}
	actor := principal.UserID
	actorName := principal.DisplayName
	resource := key.ID
	changes := map[string]any{"id": key.ID, "prefix": key.Prefix, "name": key.Name, "model_ids": key.ModelIDs, "expires_at": key.ExpiresAt}
	if key.RotatedFromID != nil {
		changes["rotated_from_id"] = *key.RotatedFromID
	}
	if key.ReplacedByID != nil {
		changes["replaced_by_id"] = *key.ReplacedByID
	}
	return s.audit.Append(ctx, q, audit.Event{ID: id, OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actor, ActorDisplayName: &actorName, Action: action, ResourceType: "api_key", ResourceID: &resource, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}

type apiKeyCursorEnvelope struct {
	Status    APIKeyStatus `json:"status"`
	CreatedAt string       `json:"created_at"`
	ID        string       `json:"id"`
}

func encodeAPIKeyCursor(key APIKey, status APIKeyStatus) (string, error) {
	data, err := json.Marshal(apiKeyCursorEnvelope{status, key.CreatedAt.Format(time.RFC3339Nano), key.ID})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}
func decodeAPIKeyCursor(value string, status APIKeyStatus) (*APIKeyCursor, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	var item apiKeyCursorEnvelope
	if err != nil || json.Unmarshal(data, &item) != nil || item.Status != status || item.ID == "" {
		return nil, invalidCursor()
	}
	created, err := time.Parse(time.RFC3339Nano, item.CreatedAt)
	if err != nil {
		return nil, invalidCursor()
	}
	return &APIKeyCursor{created.UTC(), item.ID}, nil
}

func randomID(prefix string) (string, error) {
	value := make([]byte, 16)
	if _, err := rand.Read(value); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value), nil
}
