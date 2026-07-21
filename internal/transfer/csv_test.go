package transfer

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"cms/internal/schema"
)

type repeatedByteReader struct {
	remaining int
	value     byte
}

func (r *repeatedByteReader) Read(p []byte) (int, error) {
	if r.remaining == 0 {
		return 0, io.EOF
	}
	if len(p) > r.remaining {
		p = p[:r.remaining]
	}
	for i := range p {
		p[i] = r.value
	}
	r.remaining -= len(p)
	return len(p), nil
}

func TestTemplateUsesBOMStableHeaderAndCRLF(t *testing.T) {
	fields := []schema.ContentField{{Key: "title", Status: schema.StatusActive}, {Key: "old", Status: schema.StatusArchived}, {Key: "count", Status: schema.StatusActive}}
	var output bytes.Buffer
	if err := WriteTemplate(&output, ActiveRootFields(fields)); err != nil {
		t.Fatal(err)
	}
	if got := output.String(); got != "\xef\xbb\xbftitle,count\r\n" {
		t.Fatalf("模板不符合预期: %q", got)
	}
}

func TestParseCSVHandlesRFC4180BOMAndFormulaEscaping(t *testing.T) {
	fields := []schema.ContentField{{Key: "title", Type: schema.FieldTypeSingleLineText, Status: schema.StatusActive}, {Key: "asset", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive}}
	input := "\xef\xbb\xbftitle,asset\r\n\"\"\"一,二\"\"\",'=ast_1\r\n"
	var staged json.RawMessage
	if err := ParseCSV(strings.NewReader(input), fields, func(row int, content json.RawMessage) error { staged = append(staged[:0], content...); return nil }); err != nil {
		t.Fatal(err)
	}
	if string(staged) != `{"asset":"=ast_1","title":"一,二"}` {
		t.Fatalf("规范化结果不符合预期: %s", staged)
	}
}

func TestParseCSVAcceptsQuotedRecordAcrossPhysicalLines(t *testing.T) {
	fields := []schema.ContentField{{Key: "title", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive}}
	var staged json.RawMessage
	err := ParseCSV(strings.NewReader("title\r\n\"第一行\r\n第二行\"\r\n"), fields, func(_ int, raw json.RawMessage) error {
		staged = append(staged[:0], raw...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(staged) != `{"title":"第一行\n第二行"}` {
		t.Fatalf("跨行记录解析结果=%s", staged)
	}
}

func TestParseCSVRejectsUnclosedRecordBeforeReadingBeyondLimit(t *testing.T) {
	fields := []schema.ContentField{{Key: "title", Type: schema.FieldTypeSingleLineText, Status: schema.StatusActive}}
	input := io.MultiReader(strings.NewReader("title\n\""), &repeatedByteReader{remaining: maxRecordBytes + 1024, value: 'x'})
	err := ParseCSV(input, fields, func(int, json.RawMessage) error { return nil })
	csvErr, ok := err.(*CSVError)
	if !ok || csvErr.Detail.Code != "invalid_csv" || !strings.Contains(csvErr.Detail.Message, "20 MiB") {
		t.Fatalf("应在记录读取边界拒绝超大未闭合字段: %v", err)
	}
}

func TestParseCSVRejectsHeaderAndDuplicateJSONProperties(t *testing.T) {
	fields := []schema.ContentField{{Key: "object", Type: schema.FieldTypeObject, Status: schema.StatusActive}}
	for _, test := range []struct{ input, code string }{{"extra\n", "invalid_header"}, {"object\n\"{\"\"a\"\":1,\"\"a\"\":2}\"\n", "validation_failed"}} {
		err := ParseCSV(strings.NewReader(test.input), fields, func(int, json.RawMessage) error { return nil })
		csvErr, ok := err.(*CSVError)
		if !ok || csvErr.Detail.Code != test.code {
			t.Fatalf("input=%q: 得到 %v", test.input, err)
		}
	}
}

func TestParseCSVAcceptsExactlyOneThousandRows(t *testing.T) {
	fields := []schema.ContentField{{Key: "count", Type: schema.FieldTypeInteger, Status: schema.StatusActive}}
	var input strings.Builder
	input.Grow(700000)
	input.WriteString("count\n")
	for i := 0; i < MaxRows; i++ {
		fmt.Fprintf(&input, "%d\n", i)
	}
	rows := 0
	if err := ParseCSV(strings.NewReader(input.String()), fields, func(int, json.RawMessage) error { rows++; return nil }); err != nil {
		t.Fatal(err)
	}
	if rows != MaxRows {
		t.Fatalf("暂存行数=%d", rows)
	}
	input.WriteString("1\n")
	err := ParseCSV(strings.NewReader(input.String()), fields, func(int, json.RawMessage) error { return nil })
	if csvErr, ok := err.(*CSVError); !ok || csvErr.Detail.Code != "row_limit_exceeded" {
		t.Fatalf("应拒绝超限文件: %v", err)
	}
}

func TestWriteCSVEscapesFormulaAndRoundTrips(t *testing.T) {
	fields := []schema.ContentField{{Key: "asset", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive}}
	var output bytes.Buffer
	err := WriteCSV(&output, fields, func(yield func(json.RawMessage) error) error { return yield(json.RawMessage(`{"asset":"=2+2"}`)) })
	if err != nil {
		t.Fatal(err)
	}
	reader := csv.NewReader(strings.NewReader(strings.TrimPrefix(output.String(), "\xef\xbb\xbf")))
	_, _ = reader.Read()
	record, err := reader.Read()
	if err != nil {
		t.Fatal(err)
	}
	if record[0] != `'=2+2` {
		t.Fatalf("公式防护单元格=%q", record[0])
	}
	var staged json.RawMessage
	if err = ParseCSV(bytes.NewReader(output.Bytes()), fields, func(_ int, raw json.RawMessage) error { staged = append(staged[:0], raw...); return nil }); err != nil {
		t.Fatal(err)
	}
	if string(staged) != `{"asset":"=2+2"}` {
		t.Fatalf("往返内容=%s", staged)
	}
}
