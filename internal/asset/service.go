package asset

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

const (
	permissionView    = "assets.view"
	permissionUpload  = "assets.upload"
	permissionUpdate  = "assets.update"
	permissionArchive = "assets.archive"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}

type Config struct {
	AllowedMimeTypes []string
	MaxSize          int64
	UploadTTL        time.Duration
	DownloadTTL      time.Duration
}

type Dependencies struct {
	DB         database.Querier
	Transactor TransactionRunner
	Repository Repository
	Store      ObjectStore
	Audit      audit.Writer
	Config     Config
}

type Service struct {
	db         database.Querier
	tx         TransactionRunner
	repository Repository
	store      ObjectStore
	audit      audit.Writer
	config     Config
	mimeTypes  map[string]struct{}
	now        func() time.Time
	random     func([]byte) error
}

type RequestMeta struct{ RequestID, IP, UserAgent string }

func NewService(deps Dependencies) (*Service, error) {
	if deps.DB == nil || deps.Transactor == nil || deps.Repository == nil || deps.Store == nil || deps.Audit == nil {
		return nil, errors.New("素材服务依赖未完整装配")
	}
	if deps.Config.MaxSize < 1 || deps.Config.MaxSize > 5*1024*1024*1024 {
		return nil, errors.New("素材大小上限必须在 1 字节至 5 GiB")
	}
	if deps.Config.UploadTTL < time.Minute || deps.Config.UploadTTL > 30*time.Minute || deps.Config.DownloadTTL < time.Minute || deps.Config.DownloadTTL > 15*time.Minute {
		return nil, errors.New("素材签名有效期超出冻结范围")
	}
	types := map[string]struct{}{}
	for _, value := range deps.Config.AllowedMimeTypes {
		normalized, err := normalizeMime(value)
		if err != nil || normalized == "application/octet-stream" || normalized != value {
			return nil, fmt.Errorf("素材 MIME 允许列表包含无效值 %q", value)
		}
		types[value] = struct{}{}
	}
	if len(types) == 0 {
		return nil, errors.New("素材 MIME 允许列表不能为空")
	}
	return &Service{db: deps.DB, tx: deps.Transactor, repository: deps.Repository, store: deps.Store, audit: deps.Audit, config: deps.Config, mimeTypes: types, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, random: func(p []byte) error { _, err := rand.Read(p); return err }}, nil
}

func (s *Service) CreateUpload(ctx context.Context, principal identity.Principal, meta RequestMeta, input CreateUploadRequest) (Upload, error) {
	if err := requirePermission(principal, permissionUpload); err != nil {
		return Upload{}, err
	}
	filename, mimeType, err := s.validateUpload(input)
	if err != nil {
		return Upload{}, err
	}
	id, suffix, err := s.identifiers()
	if err != nil {
		return Upload{}, err
	}
	now := s.now()
	expiresAt := now.Add(s.config.UploadTTL)
	value := Asset{ID: id, Filename: filename, MimeType: mimeType, Size: input.Size, SHA256: input.SHA256, Status: StatusQuarantined, CreatedBy: principal.UserID, CreatedAt: now, ObjectKey: "assets/" + id + "/" + suffix, UploadUntil: expiresAt}
	signed, err := s.store.SignPut(ctx, SignPutRequest{ObjectKey: value.ObjectKey, ContentType: value.MimeType, Size: value.Size, SHA256: value.SHA256, ExpiresAt: expiresAt})
	if err != nil {
		return Upload{}, storeError(err)
	}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.repository.Create(ctx, q, value); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal.UserID, meta, "asset_upload_created", id, map[string]any{"filename": filename, "mime_type": mimeType, "size": input.Size})
	})
	if err != nil {
		return Upload{}, err
	}
	return Upload{Asset: value, Upload: signed}, nil
}

func (s *Service) Confirm(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) (Asset, error) {
	if err := requirePermission(principal, permissionUpload); err != nil {
		return Asset{}, err
	}
	value, err := s.repository.Get(ctx, s.db, id)
	if err != nil {
		return Asset{}, err
	}
	if value.Status == StatusArchived {
		return Asset{}, appError(apperror.KindConflict, "asset_not_available", "归档素材不能确认")
	}
	if value.Status == StatusQuarantined && !s.now().Before(value.UploadUntil) {
		return Asset{}, appError(apperror.KindConflict, "asset_upload_expired", "素材上传申请已过期")
	}
	metadata, err := s.store.Head(ctx, value.ObjectKey)
	if err != nil {
		return Asset{}, storeError(err)
	}
	if !metadataMatches(value, metadata) {
		return Asset{}, metadataMismatch()
	}
	body, getMetadata, err := s.store.Get(ctx, value.ObjectKey)
	if err != nil {
		return Asset{}, storeError(err)
	}
	digest := sha256.New()
	read, copyErr := io.Copy(digest, io.LimitReader(body, value.Size+1))
	closeErr := body.Close()
	if copyErr != nil || closeErr != nil {
		return Asset{}, storeError(ErrStoreUnavailable)
	}
	if read != value.Size || !sameMetadata(metadata, getMetadata) || hex.EncodeToString(digest.Sum(nil)) != value.SHA256 {
		return Asset{}, metadataMismatch()
	}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		locked, err := s.repository.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		if locked.Status == StatusAvailable {
			if locked.ETag == nil || *locked.ETag != metadata.ETag || !metadataMatches(locked, metadata) {
				return metadataMismatch()
			}
			value = locked
			return nil
		}
		if locked.Status != StatusQuarantined {
			return appError(apperror.KindConflict, "asset_not_available", "素材不能确认")
		}
		confirmedAt := s.now()
		if !confirmedAt.Before(locked.UploadUntil) {
			return appError(apperror.KindConflict, "asset_upload_expired", "素材上传申请已过期")
		}
		if err := s.repository.Confirm(ctx, q, id, metadata.ETag, confirmedAt); err != nil {
			return err
		}
		locked.Status, locked.ETag, locked.ConfirmedAt = StatusAvailable, &metadata.ETag, &confirmedAt
		value = locked
		return s.appendAudit(ctx, q, principal.UserID, meta, "asset_upload_confirmed", id, map[string]any{"status": map[string]any{"from": StatusQuarantined, "to": StatusAvailable}, "size": value.Size, "mime_type": value.MimeType, "sha256": value.SHA256, "etag": metadata.ETag})
	})
	return value, err
}

func (s *Service) Get(ctx context.Context, principal identity.Principal, id string) (Asset, error) {
	if err := requirePermission(principal, permissionView); err != nil {
		return Asset{}, err
	}
	return s.repository.Get(ctx, s.db, id)
}

func (s *Service) List(ctx context.Context, principal identity.Principal, input ListQuery) (List, error) {
	if err := requirePermission(principal, permissionView); err != nil {
		return List{}, err
	}
	if input.Limit < 1 || input.Limit > 100 || input.Status != nil && !validStatus(*input.Status) {
		return List{}, appError(apperror.KindInvalidArgument, "invalid_query", "素材查询无效")
	}
	if input.MimeType != "" {
		normalized, err := normalizeMime(input.MimeType)
		if err != nil || normalized != input.MimeType {
			return List{}, appError(apperror.KindInvalidArgument, "invalid_query", "素材查询无效")
		}
	}
	cursor, err := decodeCursor(input.Cursor)
	if err != nil {
		return List{}, err
	}
	items, err := s.repository.List(ctx, s.db, input, input.Limit+1, cursor)
	if err != nil {
		return List{}, err
	}
	result := List{Items: items}
	if len(items) > input.Limit {
		result.Items = items[:input.Limit]
		last := result.Items[len(result.Items)-1]
		encoded := base64.RawURLEncoding.EncodeToString([]byte(last.CreatedAt.Format(time.RFC3339Nano) + "\x00" + last.ID))
		result.NextCursor = &encoded
	}
	return result, nil
}

func (s *Service) AdminDownload(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) (SignedRequest, error) {
	if err := requirePermission(principal, permissionView); err != nil {
		return SignedRequest{}, err
	}
	value, err := s.repository.Get(ctx, s.db, id)
	if err != nil {
		return SignedRequest{}, err
	}
	if value.Status == StatusQuarantined {
		return SignedRequest{}, appError(apperror.KindConflict, "asset_not_available", "素材不可下载")
	}
	signed, err := s.store.SignGet(ctx, SignGetRequest{ObjectKey: value.ObjectKey, DownloadFilename: value.Filename, ExpiresAt: s.now().Add(s.config.DownloadTTL)})
	if err != nil {
		return SignedRequest{}, storeError(err)
	}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		return s.appendAudit(ctx, q, principal.UserID, meta, "asset_downloaded", id, map[string]any{"status": value.Status})
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return signed, nil
}

func (s *Service) Rename(ctx context.Context, principal identity.Principal, id, filename string) (Asset, error) {
	if err := requirePermission(principal, permissionUpdate); err != nil {
		return Asset{}, err
	}
	validated, _, err := s.validateUpload(CreateUploadRequest{Filename: filename, MimeType: firstMime(s.mimeTypes), Size: 1, SHA256: strings.Repeat("0", 64)})
	if err != nil {
		return Asset{}, err
	}
	var value Asset
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		value, err = s.repository.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		if value.Status == StatusArchived {
			return appError(apperror.KindConflict, "resource_archived", "归档素材不可修改")
		}
		if err = s.repository.Rename(ctx, q, id, validated); err != nil {
			return err
		}
		value.Filename = validated
		return nil
	})
	return value, err
}

func (s *Service) Archive(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	if err := requirePermission(principal, permissionArchive); err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		value, err := s.repository.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		if value.Status == StatusArchived {
			return appError(apperror.KindConflict, "resource_archived", "素材已归档")
		}
		if value.Status != StatusAvailable {
			return appError(apperror.KindConflict, "asset_not_available", "素材不可归档")
		}
		if err := s.repository.Archive(ctx, q, id, s.now()); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal.UserID, meta, "asset_archived", id, map[string]any{"status": map[string]any{"from": StatusAvailable, "to": StatusArchived}})
	})
}

func (s *Service) PublishedDownload(ctx context.Context, scope PublishedDownloadScope, id string) (SignedRequest, error) {
	modelIDs := uniqueSorted(scope.AllowedModelIDs)
	if len(modelIDs) == 0 {
		return SignedRequest{}, publishedAssetNotFound()
	}
	query := `SELECT a.object_key,a.filename FROM assets a JOIN asset_references ar ON ar.asset_id=a.id JOIN content_published_pointers pp ON pp.revision_id=ar.revision_id AND pp.entry_id=ar.entry_id AND pp.model_id=ar.model_id WHERE a.id=? AND a.status IN ('available','archived') AND ar.model_id IN (` + placeholders(len(modelIDs)) + `) LIMIT 1`
	args := []any{id}
	for _, modelID := range modelIDs {
		args = append(args, modelID)
	}
	var objectKey, filename string
	if err := s.db.QueryRowContext(ctx, query, args...).Scan(&objectKey, &filename); errors.Is(err, sql.ErrNoRows) {
		return SignedRequest{}, publishedAssetNotFound()
	} else if err != nil {
		return SignedRequest{}, fmt.Errorf("检查已发布素材授权: %w", err)
	}
	signed, err := s.store.SignGet(ctx, SignGetRequest{ObjectKey: objectKey, DownloadFilename: filename, ExpiresAt: s.now().Add(s.config.DownloadTTL)})
	if err != nil {
		return SignedRequest{}, storeError(err)
	}
	return signed, nil
}

func (s *Service) validateUpload(input CreateUploadRequest) (string, string, error) {
	failures := []map[string]any{}
	filename := input.Filename
	if n := utf8.RuneCountInString(filename); n < 1 || n > 255 || strings.ContainsAny(filename, "\x00/\\") {
		failures = append(failures, detail("/filename", "invalid_format", "filename 必须是 1 至 255 个字符且不能包含路径分隔符或 NUL"))
	}
	mimeType, mimeErr := normalizeMime(input.MimeType)
	if mimeErr != nil || len(mimeType) > 255 {
		failures = append(failures, detail("/mime_type", "invalid_format", "mime_type 无效"))
	} else if _, ok := s.mimeTypes[mimeType]; !ok {
		failures = append(failures, detail("/mime_type", "not_allowed", "mime_type 不在允许列表"))
	}
	if input.Size < 1 {
		failures = append(failures, detail("/size", "out_of_range", "size 必须大于 0"))
	}
	if input.Size > s.config.MaxSize {
		return "", "", appError(apperror.KindInvalidArgument, "file_too_large", "文件超过大小上限")
	}
	if len(input.SHA256) != 64 {
		failures = append(failures, detail("/sha256", "invalid_format", "sha256 必须是 64 位小写十六进制"))
	} else if _, err := hex.DecodeString(input.SHA256); err != nil || strings.ToLower(input.SHA256) != input.SHA256 {
		failures = append(failures, detail("/sha256", "invalid_format", "sha256 必须是 64 位小写十六进制"))
	}
	if len(failures) > 0 {
		sort.Slice(failures, func(i, j int) bool { return failures[i]["path"].(string) < failures[j]["path"].(string) })
		return "", "", &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "请求数据校验失败", Details: failures}
	}
	return filename, mimeType, nil
}

func (s *Service) identifiers() (string, string, error) {
	var id, suffix [16]byte
	if err := s.random(id[:]); err != nil {
		return "", "", fmt.Errorf("生成素材 ID: %w", err)
	}
	if err := s.random(suffix[:]); err != nil {
		return "", "", fmt.Errorf("生成素材对象 Key: %w", err)
	}
	return "ast_" + hex.EncodeToString(id[:]), hex.EncodeToString(suffix[:]), nil
}

func (s *Service) appendAudit(ctx context.Context, q database.Querier, actorID string, meta RequestMeta, action, id string, changes map[string]any) error {
	var eventID [16]byte
	if err := s.random(eventID[:]); err != nil {
		return err
	}
	return s.audit.Append(ctx, q, audit.Event{ID: "evt_" + hex.EncodeToString(eventID[:]), OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, Action: action, ResourceType: "asset", ResourceID: &id, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}

func normalizeMime(value string) (string, error) {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || len(params) != 0 || mediaType == "" {
		return "", errors.New("无效 MIME")
	}
	return strings.ToLower(mediaType), nil
}
func validStatus(value Status) bool {
	return value == StatusQuarantined || value == StatusAvailable || value == StatusArchived
}
func requirePermission(principal identity.Principal, required string) error {
	for _, value := range principal.SystemPermissions {
		if value == required {
			return nil
		}
	}
	return appError(apperror.KindPermissionDenied, "permission_denied", "权限不足")
}
func metadataMatches(value Asset, metadata ObjectMetadata) bool {
	return metadata.ObjectKey == value.ObjectKey && metadata.Size == value.Size && metadata.ContentType == value.MimeType && metadata.SHA256 == value.SHA256 && strings.Trim(metadata.ETag, `"`) != ""
}
func sameMetadata(a, b ObjectMetadata) bool {
	return a.ObjectKey == b.ObjectKey && a.Size == b.Size && a.ContentType == b.ContentType && a.SHA256 == b.SHA256 && strings.Trim(a.ETag, `"`) == strings.Trim(b.ETag, `"`)
}
func metadataMismatch() error {
	return appError(apperror.KindConflict, "asset_metadata_mismatch", "上传对象元数据不匹配")
}
func publishedAssetNotFound() error {
	return appError(apperror.KindNotFound, "published_asset_not_found", "已发布素材不存在")
}
func detail(path, code, message string) map[string]any {
	return map[string]any{"path": path, "code": code, "message": message}
}

func firstMime(values map[string]struct{}) string {
	for value := range values {
		return value
	}
	return ""
}

func decodeCursor(value string) (*Cursor, error) {
	if value == "" {
		return nil, nil
	}
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return nil, appError(apperror.KindInvalidArgument, "invalid_cursor", "分页游标无效")
	}
	parts := strings.Split(string(data), "\x00")
	if len(parts) != 2 || parts[1] == "" {
		return nil, appError(apperror.KindInvalidArgument, "invalid_cursor", "分页游标无效")
	}
	at, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, appError(apperror.KindInvalidArgument, "invalid_cursor", "分页游标无效")
	}
	return &Cursor{CreatedAt: at.UTC(), ID: parts[1]}, nil
}
