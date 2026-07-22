package transfer

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"cms/internal/content"
	"cms/internal/identity"
	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
	"cms/internal/schema"
)

type ModelReader interface {
	GetModel(context.Context, database.Querier, string) (schema.ContentModel, error)
}

type Dependencies struct {
	DB       database.Querier
	Models   ModelReader
	Importer Importer
	Entries  EntryLister
}

type Service struct {
	db       database.Querier
	models   ModelReader
	importer Importer
	entries  EntryLister
	exportMu sync.Mutex
	exports  int
	users    map[string]struct{}
}

const (
	maxExportRows       = 10_000
	maxExportBytes      = int64(50 << 20)
	maxConcurrentExport = 2
)

func NewService(d Dependencies) *Service {
	return &Service{db: d.DB, models: d.Models, importer: d.Importer, entries: d.Entries, users: make(map[string]struct{})}
}

func (s *Service) acquireExport(userID string) (func(), error) {
	s.exportMu.Lock()
	defer s.exportMu.Unlock()
	if s.exports >= maxConcurrentExport {
		return nil, &apperror.Error{Kind: apperror.KindTooManyRequests, Code: "export_concurrency_limit", Message: "导出并发数已达上限"}
	}
	if _, exists := s.users[userID]; exists {
		return nil, &apperror.Error{Kind: apperror.KindTooManyRequests, Code: "export_concurrency_limit", Message: "导出并发数已达上限"}
	}
	s.exports++
	s.users[userID] = struct{}{}
	return func() {
		s.exportMu.Lock()
		s.exports--
		delete(s.users, userID)
		s.exportMu.Unlock()
	}, nil
}

func (s *Service) Template(ctx context.Context, principal identity.Principal, modelID string, w io.Writer) error {
	if !hasModel(principal, modelID, "content.view") {
		return forbidden()
	}
	model, err := s.models.GetModel(ctx, s.db, modelID)
	if err != nil {
		return err
	}
	return WriteTemplate(w, ActiveRootFields(model.Fields))
}

func (s *Service) Import(ctx context.Context, principal identity.Principal, meta content.RequestMeta, modelID string, rows []json.RawMessage) (ImportResult, error) {
	if !hasModel(principal, modelID, "content.create") {
		return ImportResult{}, forbidden()
	}
	if s.importer == nil {
		return ImportResult{}, errors.New("导入服务未装配")
	}
	source := func(yield func(content.ImportDraft) error) error {
		for i, raw := range rows {
			if err := yield(content.ImportDraft{Content: raw}); err != nil {
				return &importRowError{row: i + 2, err: err}
			}
		}
		return nil
	}
	if err := s.importer.ImportDrafts(ctx, principal, meta, modelID, source, nil); err != nil {
		row := 1
		var rowError *importRowError
		if errors.As(err, &rowError) {
			row, err = rowError.row, rowError.err
		}
		return ImportResult{}, importError(row, err)
	}
	return ImportResult{Imported: len(rows)}, nil
}

type importRowError struct {
	row int
	err error
}

func (e *importRowError) Error() string { return e.err.Error() }
func (e *importRowError) Unwrap() error { return e.err }

func importError(row int, err error) error {
	var application *apperror.Error
	if !errors.As(err, &application) || application.Kind != apperror.KindInvalidArgument && application.Kind != apperror.KindConflict {
		return err
	}
	details := make([]map[string]any, 0, len(application.Details))
	for _, detail := range application.Details {
		field := strings.TrimPrefix(stringValue(detail["path"]), "/content/")
		if field == "/content" {
			field = ""
		}
		details = append(details, map[string]any{"row": row, "field": field, "code": stringValue(detail["code"]), "message": stringValue(detail["message"])})
	}
	if len(details) == 0 {
		details = append(details, map[string]any{"row": row, "field": "", "code": application.Code, "message": application.Message})
	}
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "csv_invalid", Message: "CSV 数据无效", Details: details}
}

func stringValue(value any) string {
	text, _ := value.(string)
	return text
}

func (s *Service) Export(ctx context.Context, principal identity.Principal, modelID string, request ExportRequest, w io.Writer) error {
	if !hasModel(principal, modelID, "content.view") {
		return forbidden()
	}
	model, err := s.models.GetModel(ctx, s.db, modelID)
	if err != nil {
		return err
	}
	parsed, err := ValidateExportRequest(model.Fields, request)
	if err != nil {
		return err
	}
	query := content.AdminEntryQuery{Status: content.StatusDraft, WorkflowStatus: parsed.WorkflowStatus, Limit: 100, Filters: parsed.Filters, RelationFilters: parsed.RelationFilters, Sort: parsed.Sort}
	page, err := s.entries.ListEntries(ctx, principal, modelID, query)
	if err != nil {
		return err
	}
	fields := ActiveRootFields(model.Fields)
	limited := &exportWriter{ctx: ctx, writer: w}
	rows := 0
	return WriteCSV(limited, fields, func(yield func(json.RawMessage) error) error {
		for {
			for _, entry := range page.Items {
				if err := ctx.Err(); err != nil {
					return err
				}
				rows++
				if rows > maxExportRows {
					return &apperror.Error{Kind: apperror.KindPayloadTooLarge, Code: "export_row_limit_exceeded", Message: "导出不能超过 10,000 行"}
				}
				if err := yield(entry.CurrentDraftContent); err != nil {
					return err
				}
			}
			if page.NextCursor == nil {
				return nil
			}
			query.Cursor = *page.NextCursor
			page, err = s.entries.ListEntries(ctx, principal, modelID, query)
			if err != nil {
				return err
			}
		}
	})
}

type exportWriter struct {
	ctx     context.Context
	writer  io.Writer
	written int64
}

func (w *exportWriter) Write(value []byte) (int, error) {
	if err := w.ctx.Err(); err != nil {
		return 0, err
	}
	if int64(len(value)) > maxExportBytes-w.written {
		return 0, &apperror.Error{Kind: apperror.KindPayloadTooLarge, Code: "export_size_limit_exceeded", Message: "导出文件不能超过 50 MiB"}
	}
	n, err := w.writer.Write(value)
	w.written += int64(n)
	return n, err
}

var exportInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
var exportDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

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

func hasModel(principal identity.Principal, modelID, permission string) bool {
	for _, scope := range principal.ModelPermissions {
		if scope.ModelID == modelID {
			for _, value := range scope.Permissions {
				if value == permission {
					return true
				}
			}
		}
	}
	return false
}
