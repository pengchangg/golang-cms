package transfer

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"
	"time"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

func TestValidateExportRequestUsesF2QueryRules(t *testing.T) {
	fields := []schema.ContentField{
		{Key: "score", Type: schema.FieldTypeInteger, Status: schema.StatusActive, Constraints: schema.FieldConstraints{Filterable: true, Sortable: true}},
		{Key: "related", Type: schema.FieldTypeSingleRelation, Status: schema.StatusActive},
	}
	request := ExportRequest{WorkflowStatus: "pending_review", Filter: `{"score":{"gte":42}}`, RelationFilter: `{"related":{"contains":"ent_1"}}`, Sort: "-score,id"}
	query, err := ValidateExportRequest(fields, request)
	if err != nil {
		t.Fatal(err)
	}
	if query.WorkflowStatus == nil || len(query.Filters) != 1 || len(query.RelationFilters) != 1 || len(query.Sort) != 2 || !query.Sort[0].Descending {
		t.Fatalf("解析后的导出查询不符合预期: %+v", query)
	}
}

func TestValidateExportRequestRejectsInvalidF2Query(t *testing.T) {
	fields := []schema.ContentField{{Key: "score", Type: schema.FieldTypeInteger, Status: schema.StatusActive, Constraints: schema.FieldConstraints{Filterable: true}}}
	for _, request := range []ExportRequest{
		{WorkflowStatus: "unknown"},
		{Filter: `{"score":{"contains":42}}`},
		{Filter: `{"score":{"gte":"42"}}`},
		{RelationFilter: `{"score":{"contains":"ent_1"}}`},
		{Sort: "score,score"},
	} {
		if _, err := ValidateExportRequest(fields, request); err == nil {
			t.Fatalf("应拒绝无效导出查询: %+v", request)
		}
	}
}

func TestCreateImportReplaysBeforeConfirmingUpload(t *testing.T) {
	job := Job{ID: "job_existing", Type: JobCSVImport, ModelID: "mdl_1", CreatedBy: "usr_1", RequestSnapshot: []byte(`{"upload_id":"upl_1"}`)}
	repository := replayRepository{job: job}
	uploads := &failingUploadManager{}
	service := NewService(Dependencies{DB: nil, Repository: repository, Uploads: uploads})
	result, replayed, err := service.CreateImport(context.Background(), transferPrincipal(), "mdl_1", "upl_1", "0123456789abcdef")
	if err != nil || !replayed || result.ID != job.ID {
		t.Fatalf("重放结果 = %+v, %t, %v", result, replayed, err)
	}
	if uploads.confirmed {
		t.Fatal("幂等重放不应重新确认 OSS 对象")
	}
}

func TestCreateImportRejectsReusedKeyBeforeConfirmingUpload(t *testing.T) {
	repository := replayRepository{job: Job{ID: "job_existing", RequestSnapshot: []byte(`{"upload_id":"upl_other"}`)}}
	uploads := &failingUploadManager{}
	service := NewService(Dependencies{Repository: repository, Uploads: uploads})
	_, _, err := service.CreateImport(context.Background(), transferPrincipal(), "mdl_1", "upl_1", "0123456789abcdef")
	if err == nil || uploads.confirmed {
		t.Fatalf("复用幂等键结果 = %v, confirmed = %t", err, uploads.confirmed)
	}
}

func TestEmergencyAdminUsesGlobalTaskScope(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*Service, identity.Principal) error
	}{
		{"get", func(s *Service, p identity.Principal) error {
			_, err := s.Get(context.Background(), p, "job_1")
			return err
		}},
		{"list", func(s *Service, p identity.Principal) error {
			_, err := s.List(context.Background(), p, JobFilter{Limit: 20})
			return err
		}},
		{"cancel", func(s *Service, p identity.Principal) error {
			_, err := s.Cancel(context.Background(), p, "job_1")
			return err
		}},
		{"retry", func(s *Service, p identity.Principal) error {
			_, err := s.Retry(context.Background(), p, "job_1")
			return err
		}},
		{"errors", func(s *Service, p identity.Principal) error {
			_, err := s.Errors(context.Background(), p, "job_1", 20, 0)
			return err
		}},
		{"download", func(s *Service, p identity.Principal) error {
			_, err := s.Download(context.Background(), p, "job_1", "result")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &scopeRepository{emergency: true}
			service := NewService(Dependencies{Repository: repository, Transactor: directTransaction{}, Store: successfulTransferStore{}})
			if err := test.call(service, transferPrincipal()); err != nil {
				t.Fatal(err)
			}
			if !repository.globalSeen {
				t.Fatal("应急管理员查询未使用全局任务作用域")
			}
		})
	}
}

func TestOrdinaryUserKeepsOwnedTaskScope(t *testing.T) {
	for _, test := range []struct {
		name string
		call func(*Service, identity.Principal) error
	}{
		{"get", func(s *Service, p identity.Principal) error {
			_, err := s.Get(context.Background(), p, "job_1")
			return err
		}},
		{"list", func(s *Service, p identity.Principal) error {
			_, err := s.List(context.Background(), p, JobFilter{Limit: 20})
			return err
		}},
		{"cancel", func(s *Service, p identity.Principal) error {
			_, err := s.Cancel(context.Background(), p, "job_1")
			return err
		}},
		{"retry", func(s *Service, p identity.Principal) error {
			_, err := s.Retry(context.Background(), p, "job_1")
			return err
		}},
		{"errors", func(s *Service, p identity.Principal) error {
			_, err := s.Errors(context.Background(), p, "job_1", 20, 0)
			return err
		}},
		{"download", func(s *Service, p identity.Principal) error {
			_, err := s.Download(context.Background(), p, "job_1", "result")
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			repository := &scopeRepository{}
			service := NewService(Dependencies{Repository: repository, Transactor: directTransaction{}, Store: successfulTransferStore{}})
			if err := test.call(service, transferPrincipal()); err != nil {
				t.Fatal(err)
			}
			if repository.globalSeen {
				t.Fatal("普通用户不应使用全局任务作用域")
			}
		})
	}
}

func TestEmergencyCancelAndRetryStillRequireTargetModelPermission(t *testing.T) {
	for _, call := range []func(*Service, identity.Principal) error{
		func(s *Service, p identity.Principal) error {
			_, err := s.Cancel(context.Background(), p, "job_1")
			return err
		},
		func(s *Service, p identity.Principal) error {
			_, err := s.Retry(context.Background(), p, "job_1")
			return err
		},
	} {
		repository := &scopeRepository{emergency: true}
		service := NewService(Dependencies{Repository: repository, Transactor: directTransaction{}})
		principal := transferPrincipal()
		principal.ModelPermissions = nil
		if err := call(service, principal); err == nil {
			t.Fatal("应急管理员缺少目标模型权限时仍执行了任务操作")
		}
		if repository.mutated {
			t.Fatal("权限检查失败后不应修改任务")
		}
	}
}

func TestDownloadMapsRawStoreFailure(t *testing.T) {
	service := NewService(Dependencies{Repository: &scopeRepository{}, Store: rawFailingTransferStore{}, DownloadTTL: 5 * time.Minute})
	_, err := service.Download(context.Background(), transferPrincipal(), "job_1", "result")
	var application *apperror.Error
	if !errors.As(err, &application) || application.Kind != apperror.KindUnavailable || application.Code != "object_store_unavailable" {
		t.Fatalf("下载 store error 映射 = %v", err)
	}
}

func TestServiceUsesInjectedTransferTTLs(t *testing.T) {
	uploads := &expiryUploadManager{}
	store := &expiryTransferStore{}
	repository := &scopeRepository{}
	service := NewService(Dependencies{Repository: repository, Models: staticTransferModelReader{}, Uploads: uploads, Store: store, UploadTTL: 7 * time.Minute, DownloadTTL: 3 * time.Minute})
	now := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	service.now = func() time.Time { return now }
	if _, err := service.CreateUpload(context.Background(), transferPrincipal(), "mdl_1", "source.csv", 1, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"); err != nil {
		t.Fatal(err)
	}
	if !uploads.expires.Equal(now.Add(7 * time.Minute)) {
		t.Fatalf("upload expires = %s", uploads.expires)
	}
	expires := now.Add(time.Hour)
	repository.expiresAt = &expires
	if _, err := service.Download(context.Background(), transferPrincipal(), "job_1", "result"); err != nil {
		t.Fatal(err)
	}
	if !store.expires.Equal(now.Add(3 * time.Minute)) {
		t.Fatalf("download expires = %s", store.expires)
	}
}

type replayRepository struct {
	Repository
	job Job
}

type scopeRepository struct {
	Repository
	emergency  bool
	globalSeen bool
	mutated    bool
	expiresAt  *time.Time
}

func (r *scopeRepository) IsEmergencyAdmin(context.Context, database.Querier, string) (bool, error) {
	return r.emergency, nil
}
func (r *scopeRepository) GetJob(_ context.Context, _ database.Querier, _, _ string, global bool) (Job, error) {
	r.globalSeen = global
	expires := time.Now().Add(time.Hour)
	if r.expiresAt != nil {
		expires = *r.expiresAt
	}
	return Job{ID: "job_1", Type: JobCSVExport, Status: JobFailed, ModelID: "mdl_1", Attempt: 1, MaxAttempts: 3, ResultObjectKey: "result.csv", ExpiresAt: &expires}, nil
}
func (r *scopeRepository) ListJobs(_ context.Context, _ database.Querier, _ string, _ JobFilter, global bool) ([]Job, error) {
	r.globalSeen = global
	return []Job{}, nil
}
func (r *scopeRepository) Cancel(_ context.Context, _ database.Querier, _, _ string, _ time.Time, global bool) (Job, error) {
	r.globalSeen, r.mutated = global, true
	return Job{}, nil
}
func (r *scopeRepository) Retry(_ context.Context, _ database.Querier, _, _ string, _ time.Time, global bool) (Job, error) {
	r.globalSeen, r.mutated = global, true
	return Job{}, nil
}
func (r *scopeRepository) ListErrors(_ context.Context, _ database.Querier, _, _ string, _, _ int, global bool) ([]TransferError, bool, error) {
	r.globalSeen = global
	return []TransferError{}, false, nil
}

type directTransaction struct{}

func (directTransaction) WithinTx(_ context.Context, _ *sql.TxOptions, fn func(database.Querier) error) error {
	return fn(nil)
}

type successfulTransferStore struct{}

func (successfulTransferStore) Get(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("unused")
}
func (successfulTransferStore) Put(context.Context, string, string, io.Reader) error {
	return errors.New("unused")
}
func (successfulTransferStore) SignGet(context.Context, string, string, time.Time) (string, error) {
	return "https://example.com/result.csv", nil
}

type rawFailingTransferStore struct{ successfulTransferStore }

func (rawFailingTransferStore) SignGet(context.Context, string, string, time.Time) (string, error) {
	return "", errors.New("secret raw store failure")
}

type expiryTransferStore struct {
	successfulTransferStore
	expires time.Time
}

func (s *expiryTransferStore) SignGet(_ context.Context, _, _ string, expires time.Time) (string, error) {
	s.expires = expires
	return "https://example.com/result.csv", nil
}

type expiryUploadManager struct{ expires time.Time }

func (m *expiryUploadManager) Create(_ context.Context, _ string, _ int64, _ string, expires time.Time) (ImportUpload, error) {
	m.expires = expires
	return ImportUpload{}, nil
}
func (*expiryUploadManager) Confirm(context.Context, string) (UploadClaims, error) {
	return UploadClaims{}, errors.New("unused")
}

type staticTransferModelReader struct{}

func (staticTransferModelReader) GetModel(context.Context, database.Querier, string) (schema.ContentModel, error) {
	return schema.ContentModel{}, nil
}

func (r replayRepository) FindIdempotent(context.Context, database.Querier, string, string, string, string) (Job, string, error) {
	return r.job, "", nil
}

type failingUploadManager struct{ confirmed bool }

func (*failingUploadManager) Create(context.Context, string, int64, string, time.Time) (ImportUpload, error) {
	return ImportUpload{}, errors.New("unused")
}
func (m *failingUploadManager) Confirm(context.Context, string) (UploadClaims, error) {
	m.confirmed = true
	return UploadClaims{}, errors.New("OSS 暂时不可用")
}

func transferPrincipal() identity.Principal {
	return identity.Principal{UserID: "usr_1", SystemPermissions: []string{"transfers.execute", "transfers.download"}, ModelPermissions: []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{"content.create", "content.view"}}}}
}
