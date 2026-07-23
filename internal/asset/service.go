package asset

import (
	"bytes"
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
	"sync"
	"time"
	"unicode/utf8"

	"cms/internal/audit"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

const (
	permissionView     = "assets.view"
	permissionUpload   = "assets.upload"
	permissionUpdate   = "assets.update"
	permissionArchive  = "assets.archive"
	maxTextPreviewSize = int64(1 << 20)
)

type Preview struct {
	Kind        PreviewKind
	MimeType    string
	Signed      SignedRequest
	Body        io.ReadCloser
	Filename    string
	Size        int64
	ETag        string
	NotModified bool
}

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
	confirmMu  sync.Mutex
	confirms   int
	users      map[string]struct{}
}

const (
	maxAssetSize          = int64(100 << 20)
	maxConcurrentConfirms = 2
)

type RequestMeta struct{ RequestID, IP, UserAgent string }

func NewService(deps Dependencies) (*Service, error) {
	if deps.DB == nil || deps.Transactor == nil || deps.Repository == nil || deps.Store == nil || deps.Audit == nil {
		return nil, errors.New("素材服务依赖未完整装配")
	}
	if deps.Config.MaxSize < 1 || deps.Config.MaxSize > maxAssetSize {
		return nil, errors.New("素材大小上限必须在 1 字节至 100 MiB")
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
	return &Service{db: deps.DB, tx: deps.Transactor, repository: deps.Repository, store: deps.Store, audit: deps.Audit, config: deps.Config, mimeTypes: types, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, random: func(p []byte) error { _, err := rand.Read(p); return err }, users: make(map[string]struct{})}, nil
}

func (s *Service) acquireConfirm(userID string) (func(), error) {
	s.confirmMu.Lock()
	defer s.confirmMu.Unlock()
	if s.confirms >= maxConcurrentConfirms {
		return nil, appError(apperror.KindTooManyRequests, "asset_confirm_concurrency_limit", "素材确认并发数已达上限")
	}
	if _, exists := s.users[userID]; exists {
		return nil, appError(apperror.KindTooManyRequests, "asset_confirm_concurrency_limit", "素材确认并发数已达上限")
	}
	s.confirms++
	s.users[userID] = struct{}{}
	return func() {
		s.confirmMu.Lock()
		s.confirms--
		delete(s.users, userID)
		s.confirmMu.Unlock()
	}, nil
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
	decorateAsset(&value)
	signed, err := s.store.SignPut(ctx, SignPutRequest{ObjectKey: value.ObjectKey, ContentType: value.MimeType, Size: value.Size, SHA256: value.SHA256, ExpiresAt: expiresAt})
	if err != nil {
		return Upload{}, storeError(err)
	}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		if err := s.repository.Create(ctx, q, value); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "asset_upload_created", id, map[string]any{"filename": filename, "mime_type": mimeType, "size": input.Size})
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
	if value.Status == StatusQuarantined && value.Size > maxAssetSize {
		return Asset{}, appError(apperror.KindPayloadTooLarge, "asset_confirmation_too_large", "素材超过同步确认大小上限")
	}
	release, err := s.acquireConfirm(principal.UserID)
	if err != nil {
		return Asset{}, err
	}
	defer release()
	if value.Status == StatusAvailable && value.Size > maxAssetSize {
		metadata, err := s.store.Head(ctx, value.ObjectKey)
		if err != nil {
			return Asset{}, storeError(err)
		}
		if value.ETag == nil || *value.ETag != metadata.ETag || !metadataMatches(value, metadata) {
			return Asset{}, metadataMismatch()
		}
		decorateAsset(&value)
		return value, nil
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
		return s.appendAudit(ctx, q, principal, meta, "asset_upload_confirmed", id, map[string]any{"status": map[string]any{"from": StatusQuarantined, "to": StatusAvailable}, "size": value.Size, "mime_type": value.MimeType, "sha256": value.SHA256, "etag": metadata.ETag})
	})
	decorateAsset(&value)
	return value, err
}

func (s *Service) DiscardQuarantined(ctx context.Context, principal identity.Principal, meta RequestMeta, id string) error {
	if err := requirePermission(principal, permissionArchive); err != nil {
		return err
	}
	return s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		value, err := s.repository.Lock(ctx, q, id)
		if err != nil {
			return err
		}
		if value.Status != StatusQuarantined {
			return appError(apperror.KindConflict, "asset_not_quarantined", "仅待确认素材可以废弃")
		}
		if err := s.store.Delete(ctx, value.ObjectKey); err != nil && !errors.Is(err, ErrObjectNotFound) {
			return storeError(err)
		}
		if err := s.repository.DeleteQuarantined(ctx, q, id); err != nil {
			return err
		}
		return s.appendAudit(ctx, q, principal, meta, "asset_upload_discarded", id, map[string]any{"filename": value.Filename, "mime_type": value.MimeType, "size": value.Size})
	})
}

func (s *Service) Get(ctx context.Context, principal identity.Principal, id string) (Asset, error) {
	if err := requirePermission(principal, permissionView); err != nil {
		return Asset{}, err
	}
	value, err := s.repository.Get(ctx, s.db, id)
	decorateAsset(&value)
	return value, err
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
	for i := range items {
		decorateAsset(&items[i])
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
	if value.Status != StatusAvailable && value.Status != StatusArchived {
		return SignedRequest{}, appError(apperror.KindConflict, "asset_not_available", "素材不可下载")
	}
	signed, err := s.store.SignGet(ctx, SignGetRequest{ObjectKey: value.ObjectKey, DownloadFilename: value.Filename, Disposition: DispositionAttachment, ExpiresAt: s.now().Add(s.config.DownloadTTL)})
	if err != nil {
		return SignedRequest{}, storeError(err)
	}
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		return s.appendAudit(ctx, q, principal, meta, "asset_downloaded", id, map[string]any{"status": value.Status})
	})
	if err != nil {
		return SignedRequest{}, err
	}
	return signed, nil
}

func (s *Service) AdminPreview(ctx context.Context, principal identity.Principal, id, ifNoneMatch string) (Preview, error) {
	if err := requirePermission(principal, permissionView); err != nil {
		return Preview{}, err
	}
	value, err := s.repository.Get(ctx, s.db, id)
	if err != nil {
		return Preview{}, err
	}
	return s.preview(ctx, value, ifNoneMatch)
}

func (s *Service) ReferencedPreview(ctx context.Context, principal identity.Principal, modelID, entryID, id, ifNoneMatch string) (Preview, error) {
	if err := requireModelPermission(principal, modelID); err != nil {
		return Preview{}, err
	}
	value, err := s.currentDraftAsset(ctx, modelID, entryID, id)
	if err != nil {
		return Preview{}, err
	}
	return s.preview(ctx, value, ifNoneMatch)
}

func (s *Service) ReferencedDownload(ctx context.Context, principal identity.Principal, meta RequestMeta, modelID, entryID, id string) (SignedRequest, error) {
	if err := requireModelPermission(principal, modelID); err != nil {
		return SignedRequest{}, err
	}
	value, err := s.currentDraftAsset(ctx, modelID, entryID, id)
	if err != nil {
		return SignedRequest{}, err
	}
	signed, err := s.store.SignGet(ctx, SignGetRequest{ObjectKey: value.ObjectKey, DownloadFilename: value.Filename, Disposition: DispositionAttachment, ExpiresAt: s.now().Add(s.config.DownloadTTL)})
	if err != nil {
		return SignedRequest{}, storeError(err)
	}
	if err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		return s.appendAudit(ctx, q, principal, meta, "asset_downloaded", id, map[string]any{"status": value.Status, "model_id": modelID, "entry_id": entryID})
	}); err != nil {
		return SignedRequest{}, err
	}
	return signed, nil
}

func (s *Service) currentDraftAsset(ctx context.Context, modelID, entryID, id string) (Asset, error) {
	var value Asset
	err := s.db.QueryRowContext(ctx, `SELECT a.id,a.object_key,a.filename,a.mime_type,a.size,a.sha256,a.status FROM asset_references ar JOIN content_draft_pointers dp ON dp.revision_id=ar.revision_id AND dp.entry_id=ar.entry_id AND dp.model_id=ar.model_id JOIN assets a ON a.id=ar.asset_id WHERE ar.model_id=? AND ar.entry_id=? AND ar.asset_id=? AND a.status IN ('available','archived') LIMIT 1`, modelID, entryID, id).Scan(&value.ID, &value.ObjectKey, &value.Filename, &value.MimeType, &value.Size, &value.SHA256, &value.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return Asset{}, appError(apperror.KindNotFound, "resource_not_found", "素材不存在或未被当前草稿引用")
	}
	if err != nil {
		return Asset{}, fmt.Errorf("检查当前草稿素材引用: %w", err)
	}
	decorateAsset(&value)
	return value, nil
}

func (s *Service) preview(ctx context.Context, value Asset, ifNoneMatch string) (Preview, error) {
	decorateAsset(&value)
	if value.Status != StatusAvailable && value.Status != StatusArchived {
		return Preview{}, appError(apperror.KindConflict, "asset_not_available", "素材不可预览")
	}
	if value.PreviewKind == PreviewNone {
		return Preview{}, appError(apperror.KindConflict, "asset_not_previewable", "素材不支持预览")
	}
	etag := `"` + value.SHA256 + `"`
	result := Preview{Kind: value.PreviewKind, MimeType: value.MimeType, Filename: value.Filename, Size: value.Size, ETag: etag}
	if matchesPreviewETag(ifNoneMatch, etag) {
		result.NotModified = true
		return result, nil
	}
	if value.PreviewKind == PreviewText && value.Size > maxTextPreviewSize {
		return Preview{}, appError(apperror.KindInvalidArgument, "asset_preview_too_large", "文本素材超过预览大小上限")
	}
	body, metadata, err := s.store.Get(ctx, value.ObjectKey)
	if err != nil {
		return Preview{}, storeError(err)
	}
	if value.PreviewKind != PreviewText {
		if metadata.Size != value.Size || metadata.ContentType != value.MimeType {
			_ = body.Close()
			return Preview{}, metadataMismatch()
		}
		result.Body = body
		return result, nil
	}
	data, readErr := io.ReadAll(io.LimitReader(body, maxTextPreviewSize+1))
	closeErr := body.Close()
	if readErr != nil || closeErr != nil {
		return Preview{}, storeError(ErrStoreUnavailable)
	}
	if metadata.Size > maxTextPreviewSize || int64(len(data)) > maxTextPreviewSize {
		return Preview{}, appError(apperror.KindInvalidArgument, "asset_preview_too_large", "文本素材超过预览大小上限")
	}
	result.Size = int64(len(data))
	result.Body = io.NopCloser(bytes.NewReader(data))
	return result, nil
}

func matchesPreviewETag(header, etag string) bool {
	if etag == `""` {
		return false
	}
	for value := range strings.SplitSeq(header, ",") {
		value = strings.TrimSpace(value)
		if value == "*" || value == etag || strings.TrimPrefix(value, "W/") == etag {
			return true
		}
	}
	return false
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
	decorateAsset(&value)
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
		return s.appendAudit(ctx, q, principal, meta, "asset_archived", id, map[string]any{"status": map[string]any{"from": StatusAvailable, "to": StatusArchived}})
	})
}

// ResolvePublishedDownload 使用调用方事务解析已授权素材，不执行对象存储操作。
func (s *Service) ResolvePublishedDownload(ctx context.Context, q database.Querier, scope PublishedDownloadScope, id string) (PublishedDownload, error) {
	modelIDs := uniqueSorted(scope.AllowedModelIDs)
	namespaceIDs := uniqueSorted(scope.AllowedConfigNamespaceIDs)
	if len(modelIDs) == 0 && len(namespaceIDs) == 0 {
		return PublishedDownload{}, publishedAssetNotFound()
	}
	checks := []string{}
	query := `SELECT a.object_key,a.filename FROM assets a WHERE a.id=? AND a.status IN ('available','archived') AND (`
	args := []any{id}
	if len(modelIDs) > 0 {
		checks = append(checks, `EXISTS (SELECT 1 FROM asset_references ar JOIN content_published_pointers pp ON pp.revision_id=ar.revision_id AND pp.entry_id=ar.entry_id AND pp.model_id=ar.model_id WHERE ar.asset_id=a.id AND ar.model_id IN (`+placeholders(len(modelIDs))+`))`)
		for _, modelID := range modelIDs {
			args = append(args, modelID)
		}
	}
	if len(namespaceIDs) > 0 {
		checks = append(checks, `EXISTS (SELECT 1 FROM config_asset_references car JOIN config_published_pointers cpp ON cpp.revision_id=car.revision_id AND cpp.item_id=car.item_id AND cpp.namespace_id=car.namespace_id JOIN config_items ci ON ci.id=car.item_id AND ci.namespace_id=car.namespace_id AND ci.status='active' JOIN config_namespaces cn ON cn.id=car.namespace_id AND cn.status='active' WHERE car.asset_id=a.id AND car.namespace_id IN (`+placeholders(len(namespaceIDs))+`))`)
		for _, namespaceID := range namespaceIDs {
			args = append(args, namespaceID)
		}
	}
	query += strings.Join(checks, ` OR `) + `) LIMIT 1`
	var objectKey, filename string
	if err := q.QueryRowContext(ctx, query, args...).Scan(&objectKey, &filename); errors.Is(err, sql.ErrNoRows) {
		return PublishedDownload{}, publishedAssetNotFound()
	} else if err != nil {
		return PublishedDownload{}, fmt.Errorf("检查已发布素材授权: %w", err)
	}
	return PublishedDownload{ObjectKey: objectKey, Filename: filename}, nil
}

// SignPublishedDownload 在数据库事务提交后签发短时下载地址。
func (s *Service) SignPublishedDownload(ctx context.Context, download PublishedDownload) (SignedRequest, error) {
	signed, err := s.store.SignGet(ctx, SignGetRequest{ObjectKey: download.ObjectKey, DownloadFilename: download.Filename, Disposition: DispositionAttachment, ExpiresAt: s.now().Add(s.config.DownloadTTL)})
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

func (s *Service) appendAudit(ctx context.Context, q database.Querier, principal identity.Principal, meta RequestMeta, action, id string, changes map[string]any) error {
	var eventID [16]byte
	if err := s.random(eventID[:]); err != nil {
		return err
	}
	actorID, actorName := principal.UserID, principal.DisplayName
	return s.audit.Append(ctx, q, audit.Event{ID: "evt_" + hex.EncodeToString(eventID[:]), OccurredAt: s.now(), RequestID: meta.RequestID, ActorType: "user", ActorID: &actorID, ActorDisplayName: &actorName, Action: action, ResourceType: "asset", ResourceID: &id, Result: "success", IP: meta.IP, UserAgent: meta.UserAgent, Changes: changes})
}

func normalizeMime(value string) (string, error) {
	mediaType, params, err := mime.ParseMediaType(value)
	if err != nil || len(params) != 0 || mediaType == "" {
		return "", errors.New("无效 MIME")
	}
	return strings.ToLower(mediaType), nil
}

func decorateAsset(value *Asset) {
	value.PreviewKind = PreviewKindFor(value.MimeType)
}

func PreviewKindFor(mimeType string) PreviewKind {
	switch mimeType {
	case "image/jpeg", "image/png", "image/webp", "image/gif", "image/avif", "image/svg+xml":
		return PreviewImage
	case "application/pdf":
		return PreviewPDF
	case "video/mp4", "video/webm":
		return PreviewVideo
	case "audio/mpeg", "audio/mp4", "audio/ogg", "audio/wav", "audio/webm":
		return PreviewAudio
	case "text/plain", "text/csv", "application/json":
		return PreviewText
	default:
		return PreviewNone
	}
}

func requireModelPermission(principal identity.Principal, modelID string) error {
	for _, grant := range principal.ModelPermissions {
		if grant.ModelID != modelID {
			continue
		}
		for _, permission := range grant.Permissions {
			if permission == "content.view" {
				return nil
			}
		}
	}
	return appError(apperror.KindPermissionDenied, "permission_denied", "权限不足")
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
