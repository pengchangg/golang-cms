package integration

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"strings"
	"time"

	"cms/internal/asset"
	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
	"cms/internal/transfer"
)

type MediaReferenceManager struct{ Manager asset.ReferenceManager }

func (a MediaReferenceManager) ValidateAvailable(ctx context.Context, q database.Querier, values []content.MediaReference) error {
	ids := make([]string, len(values))
	for i, value := range values {
		ids[i] = value.AssetID
	}
	return a.Manager.ValidateAvailable(ctx, q, ids)
}

func (a MediaReferenceManager) InsertRevisionReferences(ctx context.Context, q database.Querier, values []content.MediaReference) error {
	references := make([]asset.Reference, len(values))
	for i, value := range values {
		references[i] = asset.Reference{RevisionID: value.RevisionID, EntryID: value.EntryID, ModelID: value.ModelID, FieldID: value.FieldID, AssetID: value.AssetID, JSONPointer: value.JSONPointer, Position: value.Position}
	}
	return a.Manager.InsertRevisionReferences(ctx, q, references)
}

func (a MediaReferenceManager) ValidatePublishableRevision(ctx context.Context, q database.Querier, id string) error {
	return a.Manager.ValidatePublishableRevision(ctx, q, id)
}

type TransferStore struct{ Store asset.ObjectStore }

func (a TransferStore) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	body, _, err := a.Store.Get(ctx, key)
	if err != nil {
		return nil, transferStoreError(err)
	}
	return body, nil
}

func (a TransferStore) Put(ctx context.Context, key, contentType string, source io.Reader) error {
	file, err := os.CreateTemp("", "cms-transfer-*")
	if err != nil {
		return err
	}
	defer func() { name := file.Name(); _ = file.Close(); _ = os.Remove(name) }()
	digest := sha256.New()
	size, err := io.Copy(io.MultiWriter(file, digest), source)
	if err != nil {
		return err
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return err
	}
	_, err = a.Store.Put(ctx, asset.PutObjectRequest{ObjectKey: key, ContentType: contentType, Size: size, SHA256: hex.EncodeToString(digest.Sum(nil))}, file)
	if err != nil {
		return transferStoreError(err)
	}
	return nil
}

func (a TransferStore) SignGet(ctx context.Context, key, filename string, expires time.Time) (string, error) {
	signed, err := a.Store.SignGet(ctx, asset.SignGetRequest{ObjectKey: key, DownloadFilename: filename, ExpiresAt: expires})
	if err != nil {
		return "", transferStoreError(err)
	}
	return signed.URL, nil
}

type UploadManager struct {
	DB      *sql.DB
	Store   asset.ObjectStore
	MaxSize int64
}

type uploadBinding struct {
	actorID string
	modelID string
}

type uploadBindingKey struct{}

// TransferPrincipalProvider 将 transfer 接口未显式携带的上传归属绑定到请求上下文。
func TransferPrincipalProvider(next func(*http.Request) (identity.Principal, error)) func(*http.Request) (identity.Principal, error) {
	return func(r *http.Request) (identity.Principal, error) {
		principal, err := next(r)
		if err == nil {
			ctx := context.WithValue(r.Context(), uploadBindingKey{}, uploadBinding{actorID: principal.UserID, modelID: r.PathValue("model_id")})
			*r = *r.WithContext(ctx)
		}
		return principal, err
	}
}

func bindingFromContext(ctx context.Context) (uploadBinding, error) {
	binding, ok := ctx.Value(uploadBindingKey{}).(uploadBinding)
	if !ok || binding.actorID == "" || binding.modelID == "" {
		return uploadBinding{}, appError(apperror.KindInternal, "internal_error", "上传请求归属未装配")
	}
	return binding, nil
}

type TransferRepository struct{ *transfer.SQLRepository }

func (r TransferRepository) CreateJob(ctx context.Context, q database.Querier, job transfer.Job, method, path, key, hash string) error {
	if err := r.SQLRepository.CreateJob(ctx, q, job, method, path, key, hash); err != nil {
		return err
	}
	if job.Type != transfer.JobCSVImport {
		return nil
	}
	result, err := q.ExecContext(ctx, `UPDATE transfer_uploads SET bound_job_id=? WHERE object_key=? AND created_by=? AND model_id=? AND confirmed_at IS NOT NULL AND bound_job_id IS NULL`, job.ID, job.SourceObjectKey, job.CreatedBy, job.ModelID)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed != 1 {
		return appError(apperror.KindConflict, "upload_already_used", "上传申请已绑定其他导入任务")
	}
	return nil
}

func (m UploadManager) Create(ctx context.Context, filename string, size int64, digest string, expires time.Time) (transfer.ImportUpload, error) {
	binding, err := bindingFromContext(ctx)
	if err != nil {
		return transfer.ImportUpload{}, err
	}
	if size > m.MaxSize {
		return transfer.ImportUpload{}, appError(apperror.KindInvalidArgument, "file_too_large", "文件超过大小上限")
	}
	var idBytes, suffix [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return transfer.ImportUpload{}, err
	}
	if _, err := rand.Read(suffix[:]); err != nil {
		return transfer.ImportUpload{}, err
	}
	id := "upl_" + hex.EncodeToString(idBytes[:])
	key := "transfers/uploads/" + id + "/" + hex.EncodeToString(suffix[:]) + ".csv"
	signed, err := m.Store.SignPut(ctx, asset.SignPutRequest{ObjectKey: key, ContentType: "text/csv", Size: size, SHA256: digest, ExpiresAt: expires})
	if err != nil {
		return transfer.ImportUpload{}, transferStoreError(err)
	}
	_, err = m.DB.ExecContext(ctx, `INSERT INTO transfer_uploads(id,object_key,filename,size,sha256,expires_at,created_by,model_id,created_at) VALUES(?,?,?,?,?,?,?,?,NOW(6))`, id, key, filename, size, digest, expires, binding.actorID, binding.modelID)
	if err != nil {
		return transfer.ImportUpload{}, fmt.Errorf("保存 CSV 上传申请: %w", err)
	}
	return transfer.ImportUpload{UploadID: id, Method: signed.Method, URL: signed.URL, Headers: signed.Headers, ExpiresAt: signed.ExpiresAt}, nil
}

func (m UploadManager) Confirm(ctx context.Context, id string) (transfer.UploadClaims, error) {
	binding, err := bindingFromContext(ctx)
	if err != nil {
		return transfer.UploadClaims{}, err
	}
	var claims transfer.UploadClaims
	var confirmed sql.NullTime
	err = m.DB.QueryRowContext(ctx, `SELECT id,object_key,filename,size,sha256,expires_at,confirmed_at FROM transfer_uploads WHERE id=? AND created_by=? AND model_id=?`, id, binding.actorID, binding.modelID).Scan(&claims.ID, &claims.ObjectKey, &claims.Filename, &claims.Size, &claims.SHA256, &claims.ExpiresAt, &confirmed)
	if errors.Is(err, sql.ErrNoRows) {
		return claims, appError(apperror.KindNotFound, "resource_not_found", "上传申请不存在")
	}
	if err != nil {
		return claims, err
	}
	if !confirmed.Valid && !time.Now().UTC().Before(claims.ExpiresAt) {
		return claims, appError(apperror.KindConflict, "asset_upload_expired", "上传申请已过期")
	}
	metadata, err := m.Store.Head(ctx, claims.ObjectKey)
	if err != nil {
		return claims, transferStoreError(err)
	}
	if metadata.Size != claims.Size || metadata.ContentType != "text/csv" || metadata.SHA256 != claims.SHA256 || metadata.ETag == "" {
		return claims, metadataMismatch()
	}
	body, fetched, err := m.Store.Get(ctx, claims.ObjectKey)
	if err != nil {
		return claims, transferStoreError(err)
	}
	digest := sha256.New()
	read, copyErr := io.Copy(digest, io.LimitReader(body, claims.Size+1))
	closeErr := body.Close()
	if copyErr != nil || closeErr != nil || read != claims.Size || fetched.Size != metadata.Size || fetched.ETag != metadata.ETag || hex.EncodeToString(digest.Sum(nil)) != claims.SHA256 {
		return claims, metadataMismatch()
	}
	if !confirmed.Valid {
		result, err := m.DB.ExecContext(ctx, `UPDATE transfer_uploads SET confirmed_at=NOW(6) WHERE id=? AND created_by=? AND model_id=? AND confirmed_at IS NULL AND expires_at>=NOW(6)`, id, binding.actorID, binding.modelID)
		if err != nil {
			return claims, err
		}
		changed, _ := result.RowsAffected()
		if changed != 1 {
			if err := m.DB.QueryRowContext(ctx, `SELECT confirmed_at FROM transfer_uploads WHERE id=? AND created_by=? AND model_id=?`, id, binding.actorID, binding.modelID).Scan(&confirmed); err != nil || !confirmed.Valid {
				return claims, appError(apperror.KindConflict, "asset_upload_expired", "上传申请已过期")
			}
		}
	}
	return claims, nil
}

type PermissionProvider interface {
	Permissions(context.Context, string) ([]string, []identity.ModelPermissions, error)
}

type PrincipalSnapshot struct {
	DB          *sql.DB
	Permissions PermissionProvider
}

func (s PrincipalSnapshot) Principal(ctx context.Context, id string) (identity.Principal, error) {
	var display string
	var email sql.NullString
	var enabled bool
	if err := s.DB.QueryRowContext(ctx, `SELECT display_name,email,enabled FROM users WHERE id=?`, id).Scan(&display, &email, &enabled); err != nil {
		return identity.Principal{}, err
	}
	if !enabled {
		return identity.Principal{}, errors.New("用户已禁用")
	}
	system, models, err := s.Permissions.Permissions(ctx, id)
	if err != nil {
		return identity.Principal{}, err
	}
	var emailPointer *string
	if email.Valid {
		emailPointer = &email.String
	}
	return identity.NewPrincipal(id, display, emailPointer, identity.AuthMethodLocal, system, models), nil
}

var rollbackValidation = errors.New("仅校验回滚")

type DraftValidator struct {
	Content DraftImporter
	DB      database.Querier
}

type DraftImporter interface {
	ImportDrafts(context.Context, identity.Principal, content.RequestMeta, string, content.DraftSource, func(database.Querier) error) error
}

func (v DraftValidator) ValidateDrafts(ctx context.Context, modelID string, source content.DraftSource) []transfer.TransferError {
	var actorID string
	if err := v.DB.QueryRowContext(ctx, `SELECT id FROM users WHERE enabled=TRUE ORDER BY id LIMIT 1`).Scan(&actorID); err != nil {
		return []transfer.TransferError{{Row: 1, Code: "validation_failed", Message: "导入校验用户不可用"}}
	}
	principal := identity.NewPrincipal(actorID, "transfer_validator", nil, identity.AuthMethodLocal, nil, []identity.ModelPermissions{{ModelID: modelID, Permissions: []string{"content.create"}}})
	return v.validateDrafts(ctx, principal, modelID, source)
}

func (v DraftValidator) validateDrafts(ctx context.Context, principal identity.Principal, modelID string, source content.DraftSource) []transfer.TransferError {
	result := []transfer.TransferError{}
	row := 1
	sourceErr := source(func(draft content.ImportDraft) error {
		row++
		err := v.Content.ImportDrafts(ctx, principal, content.RequestMeta{}, modelID, func(yield func(content.ImportDraft) error) error {
			if err := yield(draft); err != nil {
				return err
			}
			return rollbackValidation
		}, nil)
		if !errors.Is(err, rollbackValidation) {
			result = append(result, validationErrors(row, err)...)
		}
		return ctx.Err()
	})
	if sourceErr != nil {
		return append(result, validationErrors(row, sourceErr)...)
	}
	if len(result) != 0 {
		return result
	}
	row = 1
	wrapped := func(yield func(content.ImportDraft) error) error {
		if err := source(func(draft content.ImportDraft) error { row++; return yield(draft) }); err != nil {
			return err
		}
		return rollbackValidation
	}
	err := v.Content.ImportDrafts(ctx, principal, content.RequestMeta{}, modelID, wrapped, nil)
	if errors.Is(err, rollbackValidation) {
		return nil
	}
	return validationErrors(row, err)
}

func validationErrors(row int, err error) []transfer.TransferError {
	result := []transfer.TransferError{}
	var application *apperror.Error
	if errors.As(err, &application) {
		for _, detail := range application.Details {
			result = append(result, transfer.TransferError{Row: row, Field: strings.TrimPrefix(fmt.Sprint(detail["path"]), "/content/"), Code: fmt.Sprint(detail["code"]), Message: fmt.Sprint(detail["message"])})
		}
	}
	if len(result) == 0 {
		result = append(result, transfer.TransferError{Row: row, Code: "validation_failed", Message: "内容校验失败"})
	}
	return result
}

type ExportSource struct {
	Content    *content.Service
	Principals PrincipalSnapshot
	Models     ModelReader
}

func (s ExportSource) Stream(ctx context.Context, job transfer.Job, yield func(json.RawMessage) error) error {
	principal, err := s.Principals.Principal(ctx, job.CreatedBy)
	if err != nil {
		return err
	}
	var request struct {
		WorkflowStatus string `json:"workflow_status"`
		Filter         string `json:"filter"`
		RelationFilter string `json:"relation_filter"`
		Sort           string `json:"sort"`
	}
	if err = json.Unmarshal(job.RequestSnapshot, &request); err != nil {
		return err
	}
	var snapshot struct {
		Fields []schema.ContentField `json:"fields"`
	}
	if err = json.Unmarshal(job.ModelSnapshot, &snapshot); err != nil {
		return err
	}
	model, err := s.Models.GetModel(ctx, nil, job.ModelID)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(transfer.ActiveRootFields(model.Fields), snapshot.Fields) {
		return errors.New("模型结构与任务快照不一致")
	}
	query := url.Values{"status": {"draft"}, "limit": {"100"}}
	if request.WorkflowStatus != "" {
		query.Set("workflow_status", request.WorkflowStatus)
	}
	if request.Filter != "" {
		query.Set("filter", request.Filter)
	}
	if request.RelationFilter != "" {
		query.Set("relation_filter", request.RelationFilter)
	}
	if request.Sort != "" {
		query.Set("sort", request.Sort)
	}
	mux := http.NewServeMux()
	content.NewModule(s.Content, func(*http.Request) (identity.Principal, error) { return principal, nil }).RegisterRoutes(mux)
	for {
		req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/admin/v1/models/"+job.ModelID+"/entries?"+query.Encode(), nil)
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, req)
		if response.Code != http.StatusOK {
			return fmt.Errorf("导出查询失败: HTTP %d", response.Code)
		}
		var page content.EntryList
		if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil {
			return err
		}
		for _, summary := range page.Items {
			entry, err := s.Content.GetEntry(ctx, principal, job.ModelID, summary.ID)
			if err != nil {
				return err
			}
			if err = yield(entry.CurrentDraftRevision.Content); err != nil {
				return err
			}
		}
		if page.NextCursor == nil {
			return nil
		}
		query.Set("cursor", *page.NextCursor)
	}
}

type ModelReader struct {
	DB         database.Querier
	Repository schema.SQLRepository
}

func (r ModelReader) GetModel(ctx context.Context, q database.Querier, id string) (schema.ContentModel, error) {
	if q == nil {
		q = r.DB
	}
	return r.Repository.GetModel(ctx, q, id)
}

func appError(kind apperror.Kind, code, message string) error {
	return &apperror.Error{Kind: kind, Code: code, Message: message}
}
func metadataMismatch() error {
	return appError(apperror.KindConflict, "asset_metadata_mismatch", "上传对象元数据不匹配")
}

func transferStoreError(err error) error {
	return appError(apperror.KindUnavailable, "object_store_unavailable", "对象存储暂时不可用")
}
