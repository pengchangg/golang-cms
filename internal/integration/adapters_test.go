package integration

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"cms/internal/asset"
	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

func TestTransferStoreStreamsThroughAssetStore(t *testing.T) {
	store := &recordingStore{}
	adapter := TransferStore{Store: store}
	if err := adapter.Put(context.Background(), "transfers/job/result.csv", "text/csv", strings.NewReader("value")); err != nil {
		t.Fatal(err)
	}
	if store.key != "transfers/job/result.csv" || store.size != 5 || store.body != "value" || len(store.digest) != 64 {
		t.Fatalf("unexpected put: %+v", store)
	}
}

func TestTransferStoreErrorsAreUnavailableAndRedacted(t *testing.T) {
	store := &recordingStore{err: errors.New("secret OSS endpoint failure")}
	adapter := TransferStore{Store: store}
	checks := []func() error{
		func() error { _, err := adapter.Get(context.Background(), "transfers/job/source.csv"); return err },
		func() error {
			return adapter.Put(context.Background(), "transfers/job/result.csv", "text/csv", strings.NewReader("value"))
		},
		func() error {
			_, err := adapter.SignGet(context.Background(), "transfers/job/result.csv", "result.csv", time.Now().Add(time.Minute))
			return err
		},
	}
	for _, check := range checks {
		err := check()
		var application *apperror.Error
		if !errors.As(err, &application) || application.Kind != apperror.KindUnavailable || application.Code != "object_store_unavailable" || strings.Contains(application.Message, "secret") {
			t.Fatalf("ObjectStore 错误未统一脱敏: %#v", err)
		}
	}
}

func TestAllAssetStoreErrorsUseUnavailableResponse(t *testing.T) {
	for _, storeErr := range []error{asset.ErrObjectNotFound, asset.ErrStoreConfig, asset.ErrStoreUnavailable, errors.New("secret OSS failure")} {
		err := transferStoreError(storeErr)
		var application *apperror.Error
		if !errors.As(err, &application) || application.Kind != apperror.KindUnavailable || application.Code != "object_store_unavailable" || application.Message != "对象存储暂时不可用" {
			t.Fatalf("错误 %v 映射为 %#v", storeErr, err)
		}
	}
}

func TestClientAssetScopeUsesStrictBearerRules(t *testing.T) {
	provider := ClientAssetScope(nil)
	for _, header := range []string{"", "bearer token", "Bearer", "Bearer one two", "Bearer one,Bearer two"} {
		req := httptest.NewRequest(http.MethodGet, "/api/content/v1/assets/ast_1", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		if _, err := provider(req); err == nil {
			t.Fatalf("Authorization %q should fail", header)
		}
	}
}

func TestTransferUploadsMigrationIsSingleStatement(t *testing.T) {
	data, err := os.ReadFile("../../db/migrations/000033_transfer_uploads.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.TrimSpace(string(data))
	if !strings.HasSuffix(sql, ";") || strings.Count(sql, ";") != 1 {
		t.Fatal("000033 必须且只能包含一个 SQL statement")
	}
	for _, required := range []string{"created_by VARCHAR(64) NOT NULL", "model_id VARCHAR(36) NOT NULL", "fk_transfer_uploads_created_by", "fk_transfer_uploads_model"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("000033 缺少上传归属约束 %q", required)
		}
	}
}

func TestTransferPrincipalProviderBindsActorAndModel(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/admin/v1/models/mdl_1/imports/uploads", nil)
	req.SetPathValue("model_id", "mdl_1")
	provider := TransferPrincipalProvider(func(*http.Request) (identity.Principal, error) {
		return identity.Principal{UserID: "usr_1"}, nil
	})
	if _, err := provider(req); err != nil {
		t.Fatal(err)
	}
	binding, err := bindingFromContext(req.Context())
	if err != nil || binding.actorID != "usr_1" || binding.modelID != "mdl_1" {
		t.Fatalf("binding = %+v, %v", binding, err)
	}
}

func TestDraftValidatorAlwaysRollsBackPreflight(t *testing.T) {
	importer := &recordingDraftImporter{}
	validator := DraftValidator{Content: importer}
	principal := identity.Principal{UserID: "usr_1"}
	source := func(yield func(content.ImportDraft) error) error {
		if err := yield(content.ImportDraft{}); err != nil {
			return err
		}
		return yield(content.ImportDraft{})
	}
	if failures := validator.validateDrafts(context.Background(), principal, "mdl_1", source); len(failures) != 0 {
		t.Fatalf("failures = %+v", failures)
	}
	if importer.calls != 3 || importer.committed {
		t.Fatalf("calls = %d, committed = %t", importer.calls, importer.committed)
	}
}

type recordingDraftImporter struct {
	calls     int
	committed bool
}

func (i *recordingDraftImporter) ImportDrafts(_ context.Context, _ identity.Principal, _ content.RequestMeta, _ string, source content.DraftSource, committed func(database.Querier) error) error {
	i.calls++
	if committed != nil {
		i.committed = true
	}
	err := source(func(content.ImportDraft) error { return nil })
	if !errors.Is(err, rollbackValidation) {
		return errors.New("预检未请求回滚")
	}
	return err
}

type recordingStore struct {
	key          string
	size         int64
	digest, body string
	err          error
}

func (s *recordingStore) SignPut(context.Context, asset.SignPutRequest) (asset.SignedRequest, error) {
	return asset.SignedRequest{}, s.err
}
func (s *recordingStore) Head(context.Context, string) (asset.ObjectMetadata, error) {
	return asset.ObjectMetadata{}, errors.New("unused")
}
func (s *recordingStore) SignGet(context.Context, asset.SignGetRequest) (asset.SignedRequest, error) {
	return asset.SignedRequest{}, s.err
}
func (s *recordingStore) Put(_ context.Context, request asset.PutObjectRequest, body io.Reader) (asset.ObjectMetadata, error) {
	if s.err != nil {
		return asset.ObjectMetadata{}, s.err
	}
	data, err := io.ReadAll(body)
	s.key, s.size, s.digest, s.body = request.ObjectKey, request.Size, request.SHA256, string(data)
	return asset.ObjectMetadata{}, err
}
func (s *recordingStore) Get(context.Context, string) (io.ReadCloser, asset.ObjectMetadata, error) {
	return nil, asset.ObjectMetadata{}, s.err
}
func (s *recordingStore) Delete(context.Context, string) error { return errors.New("unused") }
