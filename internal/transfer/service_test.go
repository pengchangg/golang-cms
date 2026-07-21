package transfer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

func TestImportPassesAllRowsToOneBatch(t *testing.T) {
	importer := &recordingImporter{}
	service := NewService(Dependencies{Importer: importer})
	rows := []json.RawMessage{json.RawMessage(`{"title":"一"}`), json.RawMessage(`{"title":"二"}`)}
	result, err := service.Import(context.Background(), transferPrincipal("content.create"), content.RequestMeta{}, "mdl_1", rows)
	if err != nil {
		t.Fatal(err)
	}
	if result.Imported != 2 || importer.calls != 1 || len(importer.rows) != 2 {
		t.Fatalf("result=%+v calls=%d rows=%d", result, importer.calls, len(importer.rows))
	}
}

func TestTemplateNeedsOnlyModelViewPermission(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}})
	var output bytes.Buffer
	if err := service.Template(context.Background(), transferPrincipal("content.view"), "mdl_1", &output); err != nil {
		t.Fatal(err)
	}
	if output.String() != "\xef\xbb\xbftitle\r\n" {
		t.Fatalf("template=%q", output.String())
	}
}

func TestExportUsesCurrentDraftContentAndPaginates(t *testing.T) {
	entries := &pagedEntries{}
	service := NewService(Dependencies{DB: nil, Models: staticModelReader{}, Entries: entries})
	var output bytes.Buffer
	if err := service.Export(context.Background(), transferPrincipal("content.view"), "mdl_1", ExportRequest{WorkflowStatus: "draft", Sort: "-title"}, &output); err != nil {
		t.Fatal(err)
	}
	if entries.calls != 2 || !strings.Contains(output.String(), `"一"`) || !strings.Contains(output.String(), `"二"`) {
		t.Fatalf("calls=%d output=%q", entries.calls, output.String())
	}
	if entries.queries[0].Status != content.StatusDraft || entries.queries[0].Limit != 100 || entries.queries[0].WorkflowStatus == nil {
		t.Fatalf("query=%+v", entries.queries[0])
	}
}

func TestExportValidatesBeforeWriting(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Entries: &pagedEntries{err: errors.New("查询失败")}})
	var output bytes.Buffer
	err := service.Export(context.Background(), transferPrincipal("content.view"), "mdl_1", ExportRequest{}, &output)
	if err == nil || output.Len() != 0 {
		t.Fatalf("err=%v output=%q", err, output.String())
	}
}

func TestImportAndExportRequireModelPermissions(t *testing.T) {
	service := NewService(Dependencies{Models: staticModelReader{}, Importer: &recordingImporter{}, Entries: &pagedEntries{}})
	principal := identity.Principal{UserID: "usr_1"}
	if _, err := service.Import(context.Background(), principal, content.RequestMeta{}, "mdl_1", nil); err == nil {
		t.Fatal("缺少 content.create 时导入应失败")
	}
	if err := service.Export(context.Background(), principal, "mdl_1", ExportRequest{}, &bytes.Buffer{}); err == nil {
		t.Fatal("缺少 content.view 时导出应失败")
	}
}

func TestImportPreservesRowForWriteError(t *testing.T) {
	service := NewService(Dependencies{Importer: failingImporter{duringRow: true}})
	_, err := service.Import(context.Background(), transferPrincipal("content.create"), content.RequestMeta{}, "mdl_1", []json.RawMessage{json.RawMessage(`{"title":"一"}`), json.RawMessage(`{"title":"二"}`)})
	var application *apperror.Error
	if !errors.As(err, &application) || len(application.Details) != 1 || application.Details[0]["row"] != 2 {
		t.Fatalf("err=%#v", err)
	}
}

func TestImportReportsBatchConflictAsFileError(t *testing.T) {
	service := NewService(Dependencies{Importer: failingImporter{}})
	_, err := service.Import(context.Background(), transferPrincipal("content.create"), content.RequestMeta{}, "mdl_1", []json.RawMessage{json.RawMessage(`{"title":"一"}`), json.RawMessage(`{"title":"二"}`)})
	var application *apperror.Error
	if !errors.As(err, &application) || application.Kind != apperror.KindInvalidArgument || len(application.Details) != 1 || application.Details[0]["row"] != 1 {
		t.Fatalf("err=%#v", err)
	}
}

type staticModelReader struct{}

func (staticModelReader) GetModel(context.Context, database.Querier, string) (schema.ContentModel, error) {
	return schema.ContentModel{Fields: []schema.ContentField{{Key: "title", Type: schema.FieldTypeSingleLineText, Status: schema.StatusActive, Constraints: schema.FieldConstraints{Sortable: true}}}}, nil
}

type recordingImporter struct {
	calls int
	rows  []json.RawMessage
}

type failingImporter struct{ duringRow bool }

func (i failingImporter) ImportDrafts(_ context.Context, _ identity.Principal, _ content.RequestMeta, _ string, source content.DraftSource, _ func(database.Querier) error) error {
	err := source(func(content.ImportDraft) error {
		if i.duringRow {
			return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "validation_failed", Message: "内容校验失败", Details: []map[string]any{{"path": "/content/title", "code": "required", "message": "标题必填"}}}
		}
		return nil
	})
	if err != nil {
		return err
	}
	return &apperror.Error{Kind: apperror.KindConflict, Code: "asset_not_available", Message: "素材不可用"}
}

func (i *recordingImporter) ImportDrafts(_ context.Context, _ identity.Principal, _ content.RequestMeta, _ string, source content.DraftSource, _ func(database.Querier) error) error {
	i.calls++
	return source(func(draft content.ImportDraft) error {
		i.rows = append(i.rows, append(json.RawMessage(nil), draft.Content...))
		return nil
	})
}

type pagedEntries struct {
	calls   int
	queries []content.AdminEntryQuery
	err     error
	failAt  int
}

func (e *pagedEntries) ListEntries(_ context.Context, _ identity.Principal, _ string, query content.AdminEntryQuery) (content.EntryList, error) {
	e.calls++
	e.queries = append(e.queries, query)
	if e.err != nil && (e.failAt == 0 || e.calls == e.failAt) {
		return content.EntryList{}, e.err
	}
	if query.Cursor == "" {
		next := "next"
		return content.EntryList{Items: []content.EntrySummary{{CurrentDraftContent: json.RawMessage(`{"title":"一"}`)}}, NextCursor: &next}, nil
	}
	return content.EntryList{Items: []content.EntrySummary{{CurrentDraftContent: json.RawMessage(`{"title":"二"}`)}}}, nil
}

func transferPrincipal(permission string) identity.Principal {
	return identity.Principal{UserID: "usr_1", ModelPermissions: []identity.ModelPermissions{{ModelID: "mdl_1", Permissions: []string{permission}}}}
}
