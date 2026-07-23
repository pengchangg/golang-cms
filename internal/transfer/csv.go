package transfer

import (
	"bufio"
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"cms/internal/schema"
)

const MaxRows = 1000
const maxFieldBytes = 10 << 20
const maxRecordBytes = 20 << 20

var csvInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
var csvDecimal = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)

var errRecordTooLarge = errors.New("CSV 记录超过 20 MiB")

type StageRow func(row int, content json.RawMessage) error

func ActiveRootFields(fields []schema.ContentField) []schema.ContentField {
	result := make([]schema.ContentField, 0, len(fields))
	for _, field := range fields {
		if field.Status == schema.StatusActive {
			result = append(result, field)
		}
	}
	return result
}

func WriteTemplate(w io.Writer, fields []schema.ContentField) error {
	if _, err := w.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	header := make([]string, len(fields))
	for i := range fields {
		header[i] = fields[i].Key
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

// ParseCSV 流式读取 RFC 4180 CSV，每次只把一条规范化记录交给暂存回调。
func ParseCSV(input io.Reader, fields []schema.ContentField, stage StageRow) error {
	reader := bufio.NewReader(input)
	prefix, _ := reader.Peek(3)
	if bytes.Equal(prefix, []byte{0xef, 0xbb, 0xbf}) {
		_, _ = reader.Discard(3)
	}
	csvReader := csv.NewReader(&limitedRecordReader{source: reader, fieldStart: true})
	csvReader.FieldsPerRecord = -1
	csvReader.ReuseRecord = true
	header, err := csvReader.Read()
	if err != nil {
		if errors.Is(err, errRecordTooLarge) {
			return csvError(1, "", "invalid_csv", errRecordTooLarge.Error())
		}
		return csvError(1, "", "invalid_csv", err.Error())
	}
	for _, cell := range header {
		if !utf8.ValidString(cell) || strings.ContainsRune(cell, '\x00') || strings.Contains(cell, "\ufeff") {
			return csvError(1, "", "invalid_encoding", "表头必须是无 NUL 的 UTF-8")
		}
	}
	for i, field := range fields {
		if len(header) != len(fields) || header[i] != field.Key {
			return csvError(1, "", "invalid_header", "表头必须与 active 根字段完全一致")
		}
	}
	csvReader.FieldsPerRecord = len(fields)
	for row := 2; ; row++ {
		record, err := csvReader.Read()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			if errors.Is(err, errRecordTooLarge) {
				return csvError(row, "", "invalid_csv", errRecordTooLarge.Error())
			}
			return csvError(row, "", "invalid_csv", "CSV 记录格式无效")
		}
		if row-1 > MaxRows {
			return csvError(row, "", "row_limit_exceeded", "CSV 最多包含 1000 行数据")
		}
		recordBytes := 0
		object := make(map[string]any, len(fields))
		for i, cell := range record {
			recordBytes += len(cell)
			if len(cell) > maxFieldBytes {
				return csvError(row, fields[i].Key, "invalid_csv", "字段超过 10 MiB")
			}
			if !utf8.ValidString(cell) || strings.ContainsRune(cell, '\x00') || strings.Contains(cell, "\ufeff") {
				return csvError(row, fields[i].Key, "invalid_encoding", "字段必须是无 NUL 的 UTF-8")
			}
			if cell == "" {
				continue
			}
			value, err := decodeCell(cell, fields[i])
			if err != nil {
				return csvError(row, fields[i].Key, "validation_failed", err.Error())
			}
			object[fields[i].Key] = value
		}
		if recordBytes > maxRecordBytes {
			return csvError(row, "", "invalid_csv", "记录超过 20 MiB")
		}
		content, err := json.Marshal(object)
		if err != nil {
			return err
		}
		if err = stage(row, content); err != nil {
			return err
		}
	}
}

// limitedRecordReader 在 encoding/csv 分配记录缓冲前限制实际读取的逻辑记录字节数。
type limitedRecordReader struct {
	source       *bufio.Reader
	bytes        int
	inQuotes     bool
	quotePending bool
	fieldStart   bool
}

func (r *limitedRecordReader) Read(p []byte) (int, error) {
	for i := range p {
		if r.bytes >= maxRecordBytes {
			next, err := r.source.Peek(1)
			if errors.Is(err, io.EOF) {
				return i, io.EOF
			} else if err != nil {
				return i, err
			}
			closed := !r.inQuotes || r.quotePending
			if !closed || next[0] != '\n' && next[0] != '\r' {
				return i, errRecordTooLarge
			}
			if next[0] == '\r' {
				ending, peekErr := r.source.Peek(2)
				if peekErr != nil || len(ending) != 2 || ending[1] != '\n' {
					return i, errRecordTooLarge
				}
			}
		}
		value, err := r.source.ReadByte()
		if err != nil {
			return i, err
		}
		p[i] = value
		r.bytes++
		r.consume(value)
		if !r.inQuotes && value == '\n' {
			r.bytes = 0
			r.fieldStart = true
		}
	}
	return len(p), nil
}

func (r *limitedRecordReader) consume(value byte) {
	if !r.inQuotes {
		if r.fieldStart && value == '"' {
			r.inQuotes = true
			r.fieldStart = false
		} else if value == ',' {
			r.fieldStart = true
		} else if value != '\r' && value != '\n' {
			r.fieldStart = false
		}
		return
	}
	if !r.quotePending {
		if value == '"' {
			r.quotePending = true
		}
		return
	}
	if value == '"' {
		r.quotePending = false
		return
	}
	r.inQuotes = false
	r.quotePending = false
	if value == ',' {
		r.fieldStart = true
	}
}

type CSVError struct{ Detail TransferError }

func (e *CSVError) Error() string { return e.Detail.Message }
func csvError(row int, field, code, message string) error {
	return &CSVError{TransferError{Row: row, Field: field, Code: code, Message: message}}
}

func decodeCell(cell string, field schema.ContentField) (any, error) {
	if len(cell) > 1 && cell[0] == '\'' && strings.ContainsRune("=+-@", rune(cell[1])) {
		cell = cell[1:]
	}
	switch field.Type {
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText:
		var value string
		if err := strictJSON(cell, &value); err != nil {
			return nil, errors.New("文本必须编码为 JSON string")
		}
		return value, nil
	case schema.FieldTypeInteger:
		if !csvInteger.MatchString(cell) {
			return nil, errors.New("integer 必须是规范十进制整数")
		}
		return json.Number(cell), nil
	case schema.FieldTypeDecimal:
		if !csvDecimal.MatchString(cell) {
			return nil, errors.New("decimal 必须是无指数规范十进制")
		}
		return cell, nil
	case schema.FieldTypeBoolean:
		if cell == "true" {
			return true, nil
		}
		if cell == "false" {
			return false, nil
		}
		return nil, errors.New("boolean 必须是 true 或 false")
	case schema.FieldTypeDate, schema.FieldTypeDatetime, schema.FieldTypeSingleSelect, schema.FieldTypeSingleMedia, schema.FieldTypeSingleRelation:
		return cell, nil
	case schema.FieldTypeMultiSelect, schema.FieldTypeMultiMedia, schema.FieldTypeMultiRelation:
		var value []any
		if err := strictJSON(cell, &value); err != nil {
			return nil, errors.New("多值字段必须是 JSON array")
		}
		return value, nil
	case schema.FieldTypeRichText:
		return cell, nil
	case schema.FieldTypeObject:
		var value map[string]any
		if err := strictJSON(cell, &value); err != nil {
			return nil, errors.New("字段必须是 JSON object")
		}
		return value, nil
	case schema.FieldTypeRepeatableGroup:
		var value []any
		if err := strictJSON(cell, &value); err != nil {
			return nil, errors.New("重复组必须是 JSON object array")
		}
		return value, nil
	case schema.FieldTypeJSON:
		var value any
		if err := strictJSON(cell, &value); err != nil {
			return nil, errors.New("json 字段值无效")
		}
		return value, nil
	default:
		return nil, fmt.Errorf("不支持字段类型 %s", field.Type)
	}
}

func strictJSON(text string, destination any) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if decoder.More() {
		return errors.New("包含多个 JSON 值")
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("包含多个 JSON 值")
	}
	return rejectDuplicateProperties(text)
}

func rejectDuplicateProperties(text string) error {
	decoder := json.NewDecoder(strings.NewReader(text))
	decoder.UseNumber()
	return consumeJSONValue(decoder)
}

func consumeJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("JSON object 属性名无效")
			}
			if seen[key] {
				return errors.New("JSON object 包含重复属性")
			}
			seen[key] = true
			if err = consumeJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		for decoder.More() {
			if err = consumeJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("JSON 容器无效")
	}
	_, err = decoder.Token()
	return err
}

func WriteCSV(w io.Writer, fields []schema.ContentField, rows func(func(json.RawMessage) error) error) error {
	if err := WriteTemplateHeader(w, fields); err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	err := rows(func(raw json.RawMessage) error {
		var object map[string]any
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		if err := decoder.Decode(&object); err != nil {
			return err
		}
		record := make([]string, len(fields))
		for i, field := range fields {
			if value, ok := object[field.Key]; ok {
				cell, err := encodeCell(value, field)
				if err != nil {
					return err
				}
				record[i] = formulaSafe(cell)
			}
		}
		return writer.Write(record)
	})
	writer.Flush()
	if err != nil {
		return err
	}
	return writer.Error()
}

func WriteTemplateHeader(w io.Writer, fields []schema.ContentField) error {
	if _, err := w.Write([]byte{0xef, 0xbb, 0xbf}); err != nil {
		return err
	}
	writer := csv.NewWriter(w)
	writer.UseCRLF = true
	header := make([]string, len(fields))
	for i := range fields {
		header[i] = fields[i].Key
	}
	if err := writer.Write(header); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func encodeCell(value any, field schema.ContentField) (string, error) {
	if value == nil {
		if field.Type == schema.FieldTypeJSON || field.Type == schema.FieldTypeObject || field.Type == schema.FieldTypeRepeatableGroup {
			return "null", nil
		}
		return "", nil
	}
	switch field.Type {
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText:
		data, err := json.Marshal(value)
		return string(data), err
	case schema.FieldTypeInteger:
		if number, ok := value.(json.Number); ok {
			return number.String(), nil
		}
		return fmt.Sprint(value), nil
	case schema.FieldTypeBoolean:
		if value.(bool) {
			return "true", nil
		}
		return "false", nil
	case schema.FieldTypeDecimal, schema.FieldTypeDate, schema.FieldTypeDatetime, schema.FieldTypeSingleSelect, schema.FieldTypeSingleMedia, schema.FieldTypeSingleRelation, schema.FieldTypeRichText:
		return value.(string), nil
	default:
		data, err := json.Marshal(value)
		if err != nil {
			return "", err
		}
		var compact bytes.Buffer
		if err = json.Compact(&compact, data); err != nil {
			return "", err
		}
		return compact.String(), nil
	}
}

func formulaSafe(value string) string {
	if value != "" && strings.ContainsRune("=+-@", rune(value[0])) {
		return "'" + value
	}
	return value
}
