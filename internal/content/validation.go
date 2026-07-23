package content

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"cms/internal/schema"
)

var (
	integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

func validateContent(raw json.RawMessage, fields []schema.ContentField) (json.RawMessage, error) {
	if len(raw) == 0 {
		var failures validationErrors
		failures.add("/content", "required", "content 为必填项")
		return nil, failures.err()
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		var failures validationErrors
		failures.add("/content", "invalid_json", "content 不是合法 JSON")
		return nil, failures.err()
	}
	object, ok := value.(map[string]any)
	if !ok {
		var failures validationErrors
		failures.add("/content", "invalid_type", "content 必须是 object")
		return nil, failures.err()
	}
	var failures validationErrors
	normalized := validateObject(object, fields, "/content", &failures)
	if err := failures.err(); err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return nil, fmt.Errorf("编码动态内容: %w", err)
	}
	return encoded, nil
}

func revisionDerivatives(content json.RawMessage, revision Revision, fields []schema.ContentField) ([]FieldValue, []Relation, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, nil, err
	}
	values := []FieldValue{}
	relations := []Relation{}
	for _, field := range fields {
		value, exists := object[field.Key]
		if !exists || value == nil || field.Status == schema.StatusArchived {
			continue
		}
		if field.Type == schema.FieldTypeSingleRelation || field.Type == schema.FieldTypeMultiRelation {
			ids := []string{}
			if field.Type == schema.FieldTypeSingleRelation {
				ids = append(ids, value.(string))
			} else {
				for _, item := range value.([]any) {
					ids = append(ids, item.(string))
				}
			}
			for position, id := range ids {
				relations = append(relations, Relation{RevisionID: revision.ID, EntryID: revision.EntryID, ModelID: revision.ModelID, FieldID: field.ID, TargetEntryID: id, TargetModelID: *field.Constraints.TargetModelID, Position: position})
			}
			continue
		}
		if !field.Constraints.Unique && !field.Constraints.Filterable && !field.Constraints.Sortable {
			continue
		}
		projected, err := projectFieldValue(revision, field, value)
		if err != nil {
			var failures validationErrors
			failures.add("/content/"+escapePointer(field.Key), "projection_out_of_range", "字段值超出投影范围")
			return nil, nil, failures.err()
		}
		values = append(values, projected)
	}
	return values, relations, nil
}

func projectFieldValue(revision Revision, field schema.ContentField, value any) (FieldValue, error) {
	result := FieldValue{RevisionID: revision.ID, EntryID: revision.EntryID, ModelID: revision.ModelID, FieldID: field.ID}
	switch field.Type {
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText, schema.FieldTypeSingleSelect:
		v := value.(string)
		result.ValueType, result.StringValue = "string", &v
	case schema.FieldTypeInteger:
		integer, ok := new(big.Int).SetString(value.(json.Number).String(), 10)
		if !ok || !integer.IsInt64() {
			return result, errors.New("integer 超出 BIGINT")
		}
		v := integer.Int64()
		result.ValueType, result.IntegerValue = "integer", &v
	case schema.FieldTypeDecimal:
		text := value.(string)
		parts := strings.Split(strings.TrimPrefix(text, "-"), ".")
		if len(parts[0]) > 35 || len(parts) == 2 && len(parts[1]) > 30 {
			return result, errors.New("decimal 超出 DECIMAL(65,30)")
		}
		result.ValueType, result.DecimalValue = "decimal", &text
	case schema.FieldTypeBoolean:
		v := value.(bool)
		result.ValueType, result.BooleanValue = "boolean", &v
	case schema.FieldTypeDate:
		v, _ := time.Parse("2006-01-02", value.(string))
		result.ValueType, result.DateValue = "date", &v
	case schema.FieldTypeDatetime:
		v, _ := time.Parse(time.RFC3339Nano, value.(string))
		v = v.UTC()
		result.ValueType, result.DatetimeValue = "datetime", &v
	default:
		return result, errors.New("字段不可投影")
	}
	return result, nil
}

func uniqueValues(content json.RawMessage, fields []schema.ContentField) ([]UniqueValue, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("读取唯一字段值: %w", err)
	}
	values := make([]UniqueValue, 0)
	for _, field := range fields {
		value, exists := object[field.Key]
		if !exists || value == nil || field.Status == schema.StatusArchived || !field.Constraints.Unique {
			continue
		}
		canonical, err := canonicalUniqueValue(field.Type, value)
		if err != nil {
			return nil, fmt.Errorf("生成字段 %s 的唯一值: %w", field.Key, err)
		}
		values = append(values, UniqueValue{FieldID: field.ID, CanonicalValue: canonical})
	}
	return values, nil
}

func canonicalUniqueValue(fieldType schema.FieldType, value any) ([]byte, error) {
	var canonical string
	switch fieldType {
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText:
		canonical = value.(string)
	case schema.FieldTypeInteger:
		integer, ok := new(big.Int).SetString(value.(json.Number).String(), 10)
		if !ok {
			return nil, fmt.Errorf("无效 integer")
		}
		canonical = integer.String()
	case schema.FieldTypeDecimal:
		number, ok := new(big.Rat).SetString(value.(string))
		if !ok {
			return nil, fmt.Errorf("无效 decimal")
		}
		canonical = number.RatString()
	case schema.FieldTypeBoolean:
		canonical = fmt.Sprintf("%t", value.(bool))
	case schema.FieldTypeDate, schema.FieldTypeDatetime, schema.FieldTypeSingleSelect:
		canonical = value.(string)
	default:
		return nil, fmt.Errorf("非根级标量类型 %s", fieldType)
	}
	digest := sha256.Sum256(append([]byte(string(fieldType)+"\x00"), canonical...))
	return digest[:], nil
}

func validateObject(value map[string]any, fields []schema.ContentField, path string, failures *validationErrors) map[string]any {
	byKey := make(map[string]schema.ContentField, len(fields))
	for _, field := range fields {
		byKey[field.Key] = field
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make(map[string]any, len(value))
	for _, key := range keys {
		field, ok := byKey[key]
		fieldPath := path + "/" + escapePointer(key)
		if !ok {
			failures.add(fieldPath, "unknown_property", "包含未知字段")
			continue
		}
		if field.Status == schema.StatusArchived {
			failures.add(fieldPath, "archived_field", "归档字段不可写入")
			continue
		}
		result[key] = validateValue(value[key], field, fieldPath, failures)
	}
	for _, field := range fields {
		if field.Status == schema.StatusArchived {
			continue
		}
		value, exists := value[field.Key]
		if !exists && !isNullJSON(field.DefaultValue) {
			var defaultValue any
			decoder := json.NewDecoder(bytes.NewReader(field.DefaultValue))
			decoder.UseNumber()
			if decoder.Decode(&defaultValue) == nil {
				result[field.Key] = validateValue(defaultValue, field, path+"/"+escapePointer(field.Key), failures)
				exists, value = true, defaultValue
			}
		}
		if field.Required && (!exists || value == nil) {
			failures.add(path+"/"+escapePointer(field.Key), "required", "缺少必填字段")
		}
	}
	return result
}

func validateValue(value any, field schema.ContentField, path string, failures *validationErrors) any {
	if value == nil {
		return nil
	}
	switch field.Type {
	case schema.FieldTypeSingleMedia:
		if text, ok := value.(string); !ok || text == "" {
			failures.add(path, "invalid_type", "单媒体必须是非空素材 ID")
		}
	case schema.FieldTypeMultiMedia:
		items, ok := value.([]any)
		if !ok {
			failures.add(path, "invalid_type", "多媒体必须是素材 ID 数组")
			return value
		}
		if len(items) > 50 {
			failures.add(path, "out_of_range", "多媒体最多包含 50 项")
		}
		seen := map[string]bool{}
		for i, item := range items {
			text, ok := item.(string)
			itemPath := fmt.Sprintf("%s/%d", path, i)
			if !ok || text == "" {
				failures.add(itemPath, "invalid_type", "媒体项必须是非空素材 ID")
			} else if seen[text] {
				failures.add(itemPath, "duplicate", "素材 ID 不可重复")
			}
			seen[text] = true
		}
	case schema.FieldTypeSingleRelation:
		if text, ok := value.(string); !ok || text == "" {
			failures.add(path, "invalid_type", "单关联必须是非空条目 ID")
		}
	case schema.FieldTypeMultiRelation:
		items, ok := value.([]any)
		if !ok {
			failures.add(path, "invalid_type", "多关联必须是条目 ID 数组")
			return value
		}
		if len(items) > 50 {
			failures.add(path, "out_of_range", "多关联最多包含 50 项")
		}
		seen := map[string]bool{}
		for i, item := range items {
			text, ok := item.(string)
			itemPath := fmt.Sprintf("%s/%d", path, i)
			if !ok || text == "" {
				failures.add(itemPath, "invalid_type", "关联项必须是非空条目 ID")
			} else if seen[text] {
				failures.add(itemPath, "duplicate", "关联条目不可重复")
			}
			seen[text] = true
		}
	case schema.FieldTypeSingleLineText, schema.FieldTypeMultiLineText:
		text, ok := value.(string)
		if !ok {
			failures.add(path, "invalid_type", "字段值必须是字符串")
			return value
		}
		if field.Constraints.MinLength != nil && utf8.RuneCountInString(text) < *field.Constraints.MinLength || field.Constraints.MaxLength != nil && utf8.RuneCountInString(text) > *field.Constraints.MaxLength {
			failures.add(path, "out_of_range", "字段值不满足长度约束")
		}
	case schema.FieldTypeRichText:
		text, ok := value.(string)
		if !ok {
			failures.add(path, "invalid_type", "富文本字段值必须是 HTML 字符串")
			return value
		}
		return validateRichTextHTML(text, path, failures)
	case schema.FieldTypeInteger:
		number, ok := value.(json.Number)
		if !ok || !integerPattern.MatchString(number.String()) {
			failures.add(path, "invalid_type", "字段值必须是 JSON integer")
			return value
		}
		validateNumber(number.String(), field, path, failures)
	case schema.FieldTypeDecimal:
		text, ok := value.(string)
		if !ok || !decimalPattern.MatchString(text) {
			failures.add(path, "invalid_format", "字段值必须是无指数规范十进制字符串")
			return value
		}
		validateNumber(text, field, path, failures)
		if number, _ := new(big.Rat).SetString(text); number.Sign() == 0 {
			return "0"
		}
	case schema.FieldTypeBoolean:
		if _, ok := value.(bool); !ok {
			failures.add(path, "invalid_type", "字段值必须是 boolean")
		}
	case schema.FieldTypeDate:
		text, ok := value.(string)
		parsed, err := time.Parse("2006-01-02", text)
		if !ok || err != nil || parsed.Format("2006-01-02") != text {
			failures.add(path, "invalid_format", "字段值必须是 YYYY-MM-DD")
		}
	case schema.FieldTypeDatetime:
		text, ok := value.(string)
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if !ok || err != nil || parsed.Nanosecond()%1000 != 0 {
			failures.add(path, "invalid_format", "字段值必须是 RFC 3339 字符串")
		} else {
			return parsed.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano)
		}
	case schema.FieldTypeSingleSelect:
		text, ok := value.(string)
		if !ok || !hasOption(field, text) {
			failures.add(path, "invalid_value", "字段值必须是已有枚举 value")
		}
	case schema.FieldTypeMultiSelect:
		items, ok := value.([]any)
		if !ok {
			failures.add(path, "invalid_type", "字段值必须是枚举 value 数组")
			return value
		}
		seen := map[string]bool{}
		for i, item := range items {
			text, ok := item.(string)
			itemPath := fmt.Sprintf("%s/%d", path, i)
			if !ok || !hasOption(field, text) {
				failures.add(itemPath, "invalid_value", "数组包含未知枚举 value")
			} else if seen[text] {
				failures.add(itemPath, "duplicate", "枚举 value 不可重复")
			}
			seen[text] = true
		}
	case schema.FieldTypeJSON:
		return value
	case schema.FieldTypeObject:
		object, ok := value.(map[string]any)
		if !ok {
			failures.add(path, "invalid_type", "对象字段值必须是 object")
			return value
		}
		return validateObject(object, field.Children, path, failures)
	case schema.FieldTypeRepeatableGroup:
		items, ok := value.([]any)
		if !ok {
			failures.add(path, "invalid_type", "重复组字段值必须是 object array")
			return value
		}
		result := make([]any, len(items))
		for i, item := range items {
			object, ok := item.(map[string]any)
			itemPath := fmt.Sprintf("%s/%d", path, i)
			if !ok {
				failures.add(itemPath, "invalid_type", "重复组元素必须是 object")
				result[i] = item
				continue
			}
			result[i] = validateObject(object, field.Children, itemPath, failures)
		}
		return result
	}
	return value
}

func mediaReferences(content json.RawMessage, revision Revision, fields []schema.ContentField) ([]MediaReference, error) {
	var object map[string]any
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("读取媒体引用: %w", err)
	}
	result := []MediaReference{}
	var walk func(map[string]any, []schema.ContentField, string)
	walk = func(value map[string]any, fields []schema.ContentField, parent string) {
		for _, field := range fields {
			item, exists := value[field.Key]
			if !exists || item == nil || field.Status == schema.StatusArchived {
				continue
			}
			pointer := parent + "/" + escapePointer(field.Key)
			switch field.Type {
			case schema.FieldTypeSingleMedia:
				result = append(result, MediaReference{RevisionID: revision.ID, EntryID: revision.EntryID, ModelID: revision.ModelID, FieldID: field.ID, AssetID: item.(string), JSONPointer: pointer, Position: 0})
			case schema.FieldTypeMultiMedia:
				for position, assetID := range item.([]any) {
					result = append(result, MediaReference{RevisionID: revision.ID, EntryID: revision.EntryID, ModelID: revision.ModelID, FieldID: field.ID, AssetID: assetID.(string), JSONPointer: pointer, Position: position})
				}
			case schema.FieldTypeRichText:
				appendRichTextMediaReferences(&result, item, revision, field.ID, pointer)
			case schema.FieldTypeObject:
				if child, ok := item.(map[string]any); ok {
					walk(child, field.Children, pointer)
				}
			case schema.FieldTypeRepeatableGroup:
				groups, _ := item.([]any)
				for position, group := range groups {
					if child, ok := group.(map[string]any); ok {
						walk(child, field.Children, fmt.Sprintf("%s/%d", pointer, position))
					}
				}
			}
		}
	}
	walk(object, fields, "")
	return result, nil
}

func appendRichTextMediaReferences(result *[]MediaReference, value any, revision Revision, fieldID, pointer string) {
	text, ok := value.(string)
	if !ok || text == "" {
		return
	}
	appendRichTextHTMLMediaReferences(result, text, revision, fieldID, pointer)
}

func validateNumber(value string, field schema.ContentField, path string, failures *validationErrors) {
	number, ok := new(big.Rat).SetString(value)
	if !ok {
		return
	}
	if field.Constraints.Minimum != nil {
		minimum, valid := new(big.Rat).SetString(*field.Constraints.Minimum)
		if valid && number.Cmp(minimum) < 0 {
			failures.add(path, "out_of_range", "字段值小于 minimum")
		}
	}
	if field.Constraints.Maximum != nil {
		maximum, valid := new(big.Rat).SetString(*field.Constraints.Maximum)
		if valid && number.Cmp(maximum) > 0 {
			failures.add(path, "out_of_range", "字段值大于 maximum")
		}
	}
}

func hasOption(field schema.ContentField, value string) bool {
	for _, option := range field.Constraints.EnumOptions {
		if option.Value == value {
			return true
		}
	}
	return false
}

func isNullJSON(value json.RawMessage) bool {
	return len(value) == 0 || bytes.Equal(bytes.TrimSpace(value), []byte("null"))
}

func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
