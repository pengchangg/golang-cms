package transfer

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/database"
	"cms/internal/schema"
	"github.com/go-sql-driver/mysql"
)

type TransactionRunner interface {
	WithinTx(context.Context, *sql.TxOptions, func(database.Querier) error) error
}
type ModelReader interface {
	GetModel(context.Context, database.Querier, string) (schema.ContentModel, error)
}

type Dependencies struct {
	DB          database.Querier
	Transactor  TransactionRunner
	Repository  Repository
	Models      ModelReader
	Uploads     UploadManager
	Store       ObjectStore
	UploadTTL   time.Duration
	DownloadTTL time.Duration
}

type Service struct {
	db          database.Querier
	tx          TransactionRunner
	repository  Repository
	models      ModelReader
	uploads     UploadManager
	store       ObjectStore
	uploadTTL   time.Duration
	downloadTTL time.Duration
	now         func() time.Time
	newID       func(string) (string, error)
}

func NewService(d Dependencies) *Service {
	return &Service{db: d.DB, tx: d.Transactor, repository: d.Repository, models: d.Models, uploads: d.Uploads, store: d.Store, uploadTTL: d.UploadTTL, downloadTTL: d.DownloadTTL, now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) }, newID: randomID}
}

func (s *Service) Template(ctx context.Context, p identity.Principal, modelID string, w io.Writer) error {
	if !hasSystem(p, "transfers.download") || !hasModel(p, modelID, "content.view") {
		return forbidden()
	}
	model, err := s.models.GetModel(ctx, s.db, modelID)
	if err != nil {
		return err
	}
	return WriteTemplate(w, ActiveRootFields(model.Fields))
}
func (s *Service) CreateUpload(ctx context.Context, p identity.Principal, modelID, filename string, size int64, sha string) (ImportUpload, error) {
	if !hasSystem(p, "transfers.execute") || !hasModel(p, modelID, "content.create") {
		return ImportUpload{}, forbidden()
	}
	if s.uploads == nil {
		return ImportUpload{}, fmt.Errorf("上传管理器未装配")
	}
	if strings.TrimSpace(filename) == "" || size < 1 || len(sha) != 64 {
		return ImportUpload{}, invalid("validation_failed", "上传参数无效")
	}
	if _, err := hex.DecodeString(sha); err != nil || strings.ToLower(sha) != sha {
		return ImportUpload{}, invalid("validation_failed", "sha256 必须是小写十六进制")
	}
	if _, err := s.models.GetModel(ctx, s.db, modelID); err != nil {
		return ImportUpload{}, err
	}
	return s.uploads.Create(ctx, filename, size, sha, s.now().Add(s.uploadTTL))
}
func (s *Service) CreateImport(ctx context.Context, p identity.Principal, modelID, uploadID, key string) (Job, bool, error) {
	if !hasSystem(p, "transfers.execute") || !hasModel(p, modelID, "content.create") {
		return Job{}, false, forbidden()
	}
	if s.uploads == nil {
		return Job{}, false, fmt.Errorf("上传管理器未装配")
	}
	if err := validateIdempotencyKey(key); err != nil {
		return Job{}, false, err
	}
	path := "/api/admin/v1/models/" + modelID + "/imports"
	existing, _, err := s.repository.FindIdempotent(ctx, s.db, p.UserID, "POST", path, key)
	if err != nil {
		return Job{}, false, err
	}
	if existing.ID != "" {
		var request struct {
			UploadID string `json:"upload_id"`
		}
		if json.Unmarshal(existing.RequestSnapshot, &request) != nil || request.UploadID != uploadID {
			return Job{}, false, conflict("idempotency_key_reused", "幂等键已用于不同请求")
		}
		return existing, true, nil
	}
	claims, err := s.uploads.Confirm(ctx, uploadID)
	if err != nil {
		return Job{}, false, err
	}
	request := map[string]any{"upload_id": uploadID}
	return s.createJob(ctx, p, modelID, JobCSVImport, "POST", path, key, request, claims.ObjectKey)
}
func (s *Service) CreateExport(ctx context.Context, p identity.Principal, modelID, key string, request ExportRequest) (Job, bool, error) {
	if !hasSystem(p, "transfers.execute") || !hasModel(p, modelID, "content.view") {
		return Job{}, false, forbidden()
	}
	model, err := s.models.GetModel(ctx, s.db, modelID)
	if err != nil {
		return Job{}, false, err
	}
	if _, err = ValidateExportRequest(model.Fields, request); err != nil {
		return Job{}, false, err
	}
	return s.createJob(ctx, p, modelID, JobCSVExport, "POST", "/api/admin/v1/models/"+modelID+"/exports", key, request, "")
}
func (s *Service) createJob(ctx context.Context, p identity.Principal, modelID string, kind JobType, method, path, key string, request any, source string) (Job, bool, error) {
	if err := validateIdempotencyKey(key); err != nil {
		return Job{}, false, err
	}
	model, err := s.models.GetModel(ctx, s.db, modelID)
	if err != nil {
		return Job{}, false, err
	}
	fields := ActiveRootFields(model.Fields)
	modelSnapshot, err := json.Marshal(struct {
		ID        string                `json:"id"`
		UpdatedAt time.Time             `json:"updated_at"`
		Fields    []schema.ContentField `json:"fields"`
	}{model.ID, model.UpdatedAt, fields})
	if err != nil {
		return Job{}, false, err
	}
	requestSnapshot, err := json.Marshal(request)
	if err != nil {
		return Job{}, false, invalid("validation_failed", "请求快照无效")
	}
	digest := sha256.Sum256(append(append([]byte(string(kind)+"\x00"+modelID+"\x00"), requestSnapshot...), modelSnapshot...))
	hash := hex.EncodeToString(digest[:])
	id, err := s.newID("job_")
	if err != nil {
		return Job{}, false, err
	}
	job := Job{ID: id, Type: kind, Status: JobQueued, ModelID: modelID, MaxAttempts: 3, CreatedBy: p.UserID, CreatedAt: s.now(), ModelSnapshot: modelSnapshot, RequestSnapshot: requestSnapshot, SourceObjectKey: source}
	replayed := false
	err = s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		existing, existingHash, err := s.repository.FindIdempotent(ctx, q, p.UserID, method, path, key)
		if err != nil {
			return err
		}
		if existing.ID != "" {
			if existingHash != hash {
				return conflict("idempotency_key_reused", "幂等键已用于不同请求")
			}
			job, replayed = existing, true
			return nil
		}
		return s.repository.CreateJob(ctx, q, job, method, path, key, hash)
	})
	var duplicate *mysql.MySQLError
	if errors.As(err, &duplicate) && duplicate.Number == 1062 {
		existing, existingHash, readErr := s.repository.FindIdempotent(ctx, s.db, p.UserID, method, path, key)
		if readErr != nil {
			return Job{}, false, readErr
		}
		if existing.ID != "" {
			if existingHash != hash {
				return Job{}, false, conflict("idempotency_key_reused", "幂等键已用于不同请求")
			}
			return existing, true, nil
		}
	}
	return job, replayed, err
}
func (s *Service) Get(ctx context.Context, p identity.Principal, id string) (Job, error) {
	if !hasSystem(p, "transfers.execute") {
		return Job{}, forbidden()
	}
	emergency, err := s.repository.IsEmergencyAdmin(ctx, s.db, p.UserID)
	if err != nil {
		return Job{}, err
	}
	job, err := s.repository.GetJob(ctx, s.db, p.UserID, id, emergency)
	if errors.Is(err, sql.ErrNoRows) {
		return Job{}, notFound()
	}
	return job, err
}
func (s *Service) List(ctx context.Context, p identity.Principal, filter JobFilter) (JobList, error) {
	if !hasSystem(p, "transfers.execute") {
		return JobList{}, forbidden()
	}
	if filter.Limit < 1 || filter.Limit > 100 {
		return JobList{}, invalid("invalid_query", "任务查询无效")
	}
	if filter.Type != "" && filter.Type != JobCSVImport && filter.Type != JobCSVExport || filter.Status != "" && filter.Status != JobQueued && filter.Status != JobRunning && filter.Status != JobSucceeded && filter.Status != JobFailed && filter.Status != JobCanceled {
		return JobList{}, invalid("invalid_query", "任务查询无效")
	}
	if filter.Cursor != "" {
		decoded, err := base64.RawURLEncoding.DecodeString(filter.Cursor)
		if err != nil || len(decoded) == 0 {
			return JobList{}, invalid("invalid_cursor", "分页游标无效")
		}
		filter.Cursor = string(decoded)
	}
	emergency, err := s.repository.IsEmergencyAdmin(ctx, s.db, p.UserID)
	if err != nil {
		return JobList{}, err
	}
	items, err := s.repository.ListJobs(ctx, s.db, p.UserID, filter, emergency)
	if err != nil {
		return JobList{}, err
	}
	result := JobList{Items: items}
	if len(items) > filter.Limit {
		result.Items = items[:filter.Limit]
		cursor := base64.RawURLEncoding.EncodeToString([]byte(result.Items[len(result.Items)-1].ID))
		result.NextCursor = &cursor
	}
	return result, nil
}
func (s *Service) Cancel(ctx context.Context, p identity.Principal, id string) (Job, error) {
	if !hasSystem(p, "transfers.execute") {
		return Job{}, forbidden()
	}
	var result Job
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		emergency, err := s.repository.IsEmergencyAdmin(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		job, err := s.repository.GetJob(ctx, q, p.UserID, id, emergency)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if !hasModel(p, job.ModelID, permissionForJob(job.Type)) {
			return forbidden()
		}
		result, err = s.repository.Cancel(ctx, q, p.UserID, id, s.now(), emergency)
		return err
	})
	return result, err
}
func (s *Service) Retry(ctx context.Context, p identity.Principal, id string) (Job, error) {
	if !hasSystem(p, "transfers.execute") {
		return Job{}, forbidden()
	}
	var result Job
	err := s.tx.WithinTx(ctx, nil, func(q database.Querier) error {
		emergency, err := s.repository.IsEmergencyAdmin(ctx, q, p.UserID)
		if err != nil {
			return err
		}
		job, err := s.repository.GetJob(ctx, q, p.UserID, id, emergency)
		if errors.Is(err, sql.ErrNoRows) {
			return notFound()
		}
		if err != nil {
			return err
		}
		if !hasModel(p, job.ModelID, permissionForJob(job.Type)) {
			return forbidden()
		}
		result, err = s.repository.Retry(ctx, q, p.UserID, id, s.now(), emergency)
		return err
	})
	return result, err
}

func permissionForJob(kind JobType) string {
	if kind == JobCSVExport {
		return "content.view"
	}
	return "content.create"
}

var exportInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
var exportDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

// ValidateExportRequest 按 F2 管理列表语义解析并校验导出查询。
func ValidateExportRequest(fields []schema.ContentField, request ExportRequest) (ExportQuery, error) {
	query := ExportQuery{}
	if request.WorkflowStatus != "" {
		status := content.WorkflowStatus(request.WorkflowStatus)
		if status != content.WorkflowDraft && status != content.WorkflowPendingReview && status != content.WorkflowRejected && status != content.WorkflowPublished && status != content.WorkflowUnpublished {
			return ExportQuery{}, invalid("invalid_query", "导出查询无效")
		}
		query.WorkflowStatus = &status
	}
	var err error
	if query.Filters, err = parseExportFilters(request.Filter); err != nil {
		return ExportQuery{}, err
	}
	if query.RelationFilters, err = parseExportRelationFilters(request.RelationFilter); err != nil {
		return ExportQuery{}, err
	}
	if query.Sort, err = parseExportSort(request.Sort); err != nil {
		return ExportQuery{}, err
	}
	byKey := make(map[string]schema.ContentField, len(fields))
	for _, field := range fields {
		if field.Status == schema.StatusActive {
			byKey[field.Key] = field
		}
	}
	for _, filter := range query.Filters {
		field, ok := byKey[filter.FieldKey]
		if !ok || !field.Constraints.Filterable || !validExportOperand(field.Type, filter.Operator, filter.Value) {
			return ExportQuery{}, invalid("invalid_query", "导出查询无效")
		}
	}
	seen := map[string]bool{}
	for _, filter := range query.RelationFilters {
		field, ok := byKey[filter.FieldKey]
		if !ok || seen[filter.FieldKey] || (field.Type != schema.FieldTypeSingleRelation && field.Type != schema.FieldTypeMultiRelation) {
			return ExportQuery{}, invalid("invalid_query", "导出查询无效")
		}
		seen[filter.FieldKey] = true
	}
	seen = map[string]bool{}
	for _, item := range query.Sort {
		if seen[item.FieldKey] {
			return ExportQuery{}, invalid("invalid_query", "导出查询无效")
		}
		seen[item.FieldKey] = true
		if item.FieldKey == "updated_at" || item.FieldKey == "id" {
			continue
		}
		field, ok := byKey[item.FieldKey]
		if !ok || !field.Constraints.Sortable {
			return ExportQuery{}, invalid("invalid_query", "导出查询无效")
		}
	}
	return query, nil
}

func parseExportFilters(value string) ([]content.PublishedFilter, error) {
	if value == "" {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &raw) != nil || raw == nil || len(raw) > 5 {
		return nil, invalid("invalid_query", "导出查询无效")
	}
	result := make([]content.PublishedFilter, 0, len(raw))
	for key, item := range raw {
		var operators map[string]json.RawMessage
		if key == "" || json.Unmarshal(item, &operators) != nil || len(operators) != 1 {
			return nil, invalid("invalid_query", "导出查询无效")
		}
		for operator, operand := range operators {
			result = append(result, content.PublishedFilter{FieldKey: key, Operator: operator, Value: operand})
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FieldKey < result[j].FieldKey })
	return result, nil
}

func parseExportRelationFilters(value string) ([]content.PublishedRelationFilter, error) {
	if value == "" {
		return nil, nil
	}
	var raw map[string]json.RawMessage
	if json.Unmarshal([]byte(value), &raw) != nil || raw == nil || len(raw) > 2 {
		return nil, invalid("invalid_query", "导出查询无效")
	}
	result := make([]content.PublishedRelationFilter, 0, len(raw))
	for key, item := range raw {
		var operators map[string]json.RawMessage
		var entryID string
		if key == "" || json.Unmarshal(item, &operators) != nil || len(operators) != 1 {
			return nil, invalid("invalid_query", "导出查询无效")
		}
		operand, ok := operators["contains"]
		if !ok || json.Unmarshal(operand, &entryID) != nil || entryID == "" {
			return nil, invalid("invalid_query", "导出查询无效")
		}
		result = append(result, content.PublishedRelationFilter{FieldKey: key, EntryID: entryID})
	}
	sort.Slice(result, func(i, j int) bool { return result[i].FieldKey < result[j].FieldKey })
	return result, nil
}

func parseExportSort(value string) ([]content.PublishedSort, error) {
	if value == "" {
		return nil, nil
	}
	items := strings.Split(value, ",")
	if len(items) > 3 {
		return nil, invalid("invalid_query", "导出查询无效")
	}
	result := make([]content.PublishedSort, len(items))
	for i, item := range items {
		descending := strings.HasPrefix(item, "-")
		key := strings.TrimPrefix(item, "-")
		if key == "" {
			return nil, invalid("invalid_query", "导出查询无效")
		}
		result[i] = content.PublishedSort{FieldKey: key, Descending: descending}
	}
	return result, nil
}

func validExportOperand(fieldType schema.FieldType, operator string, raw json.RawMessage) bool {
	basic := operator == "eq" || operator == "ne" || operator == "in"
	numeric := fieldType == schema.FieldTypeInteger || fieldType == schema.FieldTypeDecimal || fieldType == schema.FieldTypeDate || fieldType == schema.FieldTypeDatetime
	if !basic && !(numeric && (operator == "gt" || operator == "gte" || operator == "lt" || operator == "lte")) {
		return false
	}
	var value any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return false
	}
	if operator == "in" {
		items, ok := value.([]any)
		if !ok || len(items) < 1 || len(items) > 20 {
			return false
		}
		seen := map[string]bool{}
		for _, item := range items {
			encoded, _ := json.Marshal(item)
			if seen[string(encoded)] || !validExportValue(fieldType, item) {
				return false
			}
			seen[string(encoded)] = true
		}
		return true
	}
	return validExportValue(fieldType, value)
}

func validExportValue(fieldType schema.FieldType, value any) bool {
	switch fieldType {
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText, schema.FieldTypeSingleSelect:
		_, ok := value.(string)
		return ok
	case schema.FieldTypeInteger:
		number, ok := value.(json.Number)
		if !ok || !exportInteger.MatchString(number.String()) {
			return false
		}
		integer, ok := new(big.Int).SetString(number.String(), 10)
		return ok && integer.IsInt64()
	case schema.FieldTypeDecimal:
		text, ok := value.(string)
		if !ok || !exportDecimal.MatchString(text) {
			return false
		}
		parts := strings.Split(strings.TrimPrefix(text, "-"), ".")
		return len(parts[0]) <= 35 && (len(parts) == 1 || len(parts[1]) <= 30)
	case schema.FieldTypeBoolean:
		_, ok := value.(bool)
		return ok
	case schema.FieldTypeDate:
		text, ok := value.(string)
		parsed, err := time.Parse("2006-01-02", text)
		return ok && err == nil && parsed.Format("2006-01-02") == text
	case schema.FieldTypeDatetime:
		text, ok := value.(string)
		parsed, err := time.Parse(time.RFC3339Nano, text)
		return ok && err == nil && parsed.Nanosecond()%1000 == 0
	default:
		return false
	}
}
func (s *Service) Errors(ctx context.Context, p identity.Principal, id string, limit, after int) (ErrorList, error) {
	if !hasSystem(p, "transfers.execute") {
		return ErrorList{}, forbidden()
	}
	if limit < 1 || limit > 100 || after < 0 {
		return ErrorList{}, invalid("invalid_query", "错误详情查询无效")
	}
	emergency, err := s.repository.IsEmergencyAdmin(ctx, s.db, p.UserID)
	if err != nil {
		return ErrorList{}, err
	}
	items, truncated, err := s.repository.ListErrors(ctx, s.db, p.UserID, id, limit+1, after, emergency)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrorList{}, notFound()
	}
	if err != nil {
		return ErrorList{}, err
	}
	result := ErrorList{Items: items, ErrorsTruncated: truncated}
	if len(items) > limit {
		result.Items = items[:limit]
		cursor := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprint(after + limit)))
		result.NextCursor = &cursor
	}
	return result, nil
}
func (s *Service) Download(ctx context.Context, p identity.Principal, id, kind string) (Download, error) {
	if !hasSystem(p, "transfers.download") {
		return Download{}, forbidden()
	}
	emergency, err := s.repository.IsEmergencyAdmin(ctx, s.db, p.UserID)
	if err != nil {
		return Download{}, err
	}
	job, err := s.repository.GetJob(ctx, s.db, p.UserID, id, emergency)
	if errors.Is(err, sql.ErrNoRows) {
		return Download{}, notFound()
	}
	if err != nil {
		return Download{}, err
	}
	if !hasModel(p, job.ModelID, map[string]string{"result": "content.view", "errors": "content.create"}[kind]) {
		return Download{}, forbidden()
	}
	key, filename := job.ResultObjectKey, "export.csv"
	if kind == "errors" {
		key, filename = job.ErrorObjectKey, "errors.csv"
	}
	if key == "" {
		return Download{}, notFound()
	}
	if job.ExpiresAt == nil || !job.ExpiresAt.After(s.now()) {
		return Download{}, &jobFileExpiredError{}
	}
	expiresAt := s.now().Add(s.downloadTTL)
	if job.ExpiresAt.Before(expiresAt) {
		expiresAt = *job.ExpiresAt
	}
	location, err := s.store.SignGet(ctx, key, filename, expiresAt)
	if err != nil {
		return Download{}, objectStoreUnavailable(err)
	}
	return Download{Location: location}, nil
}
func validateIdempotencyKey(key string) error {
	if len(key) < 16 || len(key) > 128 {
		return invalid("validation_failed", "Idempotency-Key 长度必须为 16 至 128")
	}
	for _, r := range key {
		if r < '!' || r > '~' {
			return invalid("validation_failed", "Idempotency-Key 必须是可见 ASCII")
		}
	}
	return nil
}
func hasSystem(p identity.Principal, permission string) bool {
	for _, v := range p.SystemPermissions {
		if v == permission {
			return true
		}
	}
	return false
}
func hasModel(p identity.Principal, modelID, permission string) bool {
	for _, scope := range p.ModelPermissions {
		if scope.ModelID == modelID {
			for _, v := range scope.Permissions {
				if v == permission {
					return true
				}
			}
		}
	}
	return false
}
func randomID(prefix string) (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return prefix + hex.EncodeToString(value[:]), nil
}

type Importer interface {
	ImportDrafts(context.Context, identity.Principal, content.RequestMeta, string, content.DraftSource, func(database.Querier) error) error
}
