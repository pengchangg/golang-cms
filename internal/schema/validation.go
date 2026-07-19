package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

var (
	keyPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
)

func ValidateModelCreate(request CreateContentModelRequest) error {
	var failures validationErrors
	validateKey(request.Key, "/key", &failures)
	validateText(request.DisplayName, 1, 120, "/display_name", &failures)
	validateText(request.Description, 0, 1000, "/description", &failures)
	return failures.err()
}

func ValidateModelPatch(request UpdateContentModelRequest) error {
	var failures validationErrors
	if request.Empty() {
		failures.add("", "required", "至少提交一个可修改属性")
	}
	if request.DisplayName != nil {
		validateText(*request.DisplayName, 1, 120, "/display_name", &failures)
	}
	if request.Description != nil {
		validateText(*request.Description, 0, 1000, "/description", &failures)
	}
	return failures.err()
}

func ValidateField(input *ContentFieldInput) error {
	var failures validationErrors
	validateField(input, 0, "", map[string]bool{}, &failures)
	return failures.err()
}

func validateField(input *ContentFieldInput, depth int, path string, seenKeys map[string]bool, failures *validationErrors) {
	validateKey(input.Key, path+"/key", failures)
	if seenKeys[input.Key] {
		failures.add(path+"/key", "duplicate", "字段 key 在模型内必须唯一")
	}
	seenKeys[input.Key] = true
	validateText(input.DisplayName, 1, 120, path+"/display_name", failures)
	validateText(input.Description, 0, 1000, path+"/description", failures)
	if _, ok := fieldTypes[input.Type]; !ok {
		failures.add(path+"/type", "invalid_value", "字段类型无效")
	}
	validateConstraints(input, depth, path, failures)
	validateDefault(input, path+"/default_value", failures)
	isContainer := input.Type == FieldTypeObject || input.Type == FieldTypeRepeatableGroup
	if !isContainer && len(input.Children) > 0 {
		failures.add(path+"/children", "not_allowed", "该字段类型不允许 children")
	}
	if isContainer && len(input.Children) == 0 {
		failures.add(path+"/children", "required", "对象和重复组至少需要一个子字段")
	}
	if depth >= 2 && len(input.Children) > 0 {
		failures.add(path+"/children", "max_depth_exceeded", "字段嵌套深度不能超过两层")
	}
	for i := range input.Children {
		childPath := path + "/children/" + strconv.Itoa(i)
		validateField(&input.Children[i], depth+1, childPath, seenKeys, failures)
	}
}

func validateConstraints(input *ContentFieldInput, depth int, path string, failures *validationErrors) {
	c := input.Constraints
	text := input.Type == FieldTypeSingleLineText || input.Type == FieldTypeMultiLineText
	numeric := input.Type == FieldTypeInteger || input.Type == FieldTypeDecimal
	selectType := input.Type == FieldTypeSingleSelect || input.Type == FieldTypeMultiSelect
	relation := input.Type == FieldTypeSingleRelation || input.Type == FieldTypeMultiRelation
	if !text && (c.MinLength != nil || c.MaxLength != nil) {
		failures.add(path+"/constraints", "constraint_not_allowed", "min_length 和 max_length 仅适用于文本字段")
	}
	if c.MinLength != nil && *c.MinLength < 0 {
		failures.add(path+"/constraints/min_length", "out_of_range", "min_length 不能小于 0")
	}
	if c.MaxLength != nil && *c.MaxLength < 0 {
		failures.add(path+"/constraints/max_length", "out_of_range", "max_length 不能小于 0")
	}
	if c.MinLength != nil && c.MaxLength != nil && *c.MaxLength < *c.MinLength {
		failures.add(path+"/constraints/max_length", "out_of_range", "max_length 必须大于或等于 min_length")
	}
	if !numeric && (c.Minimum != nil || c.Maximum != nil) {
		failures.add(path+"/constraints", "constraint_not_allowed", "minimum 和 maximum 仅适用于整数和小数字段")
	}
	if c.Minimum != nil {
		validateBound(*c.Minimum, input.Type, path+"/constraints/minimum", failures)
	}
	if c.Maximum != nil {
		validateBound(*c.Maximum, input.Type, path+"/constraints/maximum", failures)
	}
	if c.Minimum != nil && c.Maximum != nil {
		minimum, ok1 := decimalRat(*c.Minimum)
		maximum, ok2 := decimalRat(*c.Maximum)
		if ok1 && ok2 && minimum.Cmp(maximum) > 0 {
			failures.add(path+"/constraints/maximum", "out_of_range", "maximum 必须大于或等于 minimum")
		}
	}
	if !selectType && len(c.EnumOptions) > 0 {
		failures.add(path+"/constraints/enum_options", "constraint_not_allowed", "enum_options 仅适用于选择字段")
	}
	if selectType && len(c.EnumOptions) == 0 {
		failures.add(path+"/constraints/enum_options", "required", "选择字段至少需要一个枚举选项")
	}
	seen := map[string]bool{}
	for i, option := range c.EnumOptions {
		base := fmt.Sprintf("%s/constraints/enum_options/%d", path, i)
		validateText(option.Value, 1, 120, base+"/value", failures)
		validateText(option.Label, 1, 120, base+"/label", failures)
		if seen[option.Value] {
			failures.add(base+"/value", "duplicate", "枚举 value 必须唯一")
		}
		seen[option.Value] = true
	}
	if !relation && c.TargetModelID != nil {
		failures.add(path+"/constraints/target_model_id", "constraint_not_allowed", "target_model_id 仅适用于关联字段")
	}
	if relation && (c.TargetModelID == nil || *c.TargetModelID == "") {
		failures.add(path+"/constraints/target_model_id", "required", "关联字段必须指定目标模型")
	}
	if (c.Unique || c.Filterable || c.Sortable) && (depth != 0 || !isRootScalar(input.Type)) {
		failures.add(path+"/constraints", "index_not_allowed", "unique、filterable 和 sortable 仅适用于根级标量字段")
	}
}

func validateDefault(input *ContentFieldInput, path string, failures *validationErrors) {
	raw := input.DefaultValue
	if len(raw) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		input.DefaultValue = json.RawMessage("null")
		return
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		failures.add(path, "invalid_json", "默认值不是合法 JSON")
		return
	}
	switch input.Type {
	case FieldTypeSingleLineText, FieldTypeMultiLineText:
		text, ok := value.(string)
		if !ok {
			failures.add(path, "invalid_type", "默认值必须是字符串")
			return
		}
		if input.Constraints.MinLength != nil && utf8.RuneCountInString(text) < *input.Constraints.MinLength {
			failures.add(path, "out_of_range", "默认值短于 min_length")
		}
		if input.Constraints.MaxLength != nil && utf8.RuneCountInString(text) > *input.Constraints.MaxLength {
			failures.add(path, "out_of_range", "默认值长于 max_length")
		}
	case FieldTypeRichText, FieldTypeSingleMedia, FieldTypeMultiMedia, FieldTypeSingleRelation, FieldTypeMultiRelation:
		failures.add(path, "default_not_allowed", "该字段类型在当前版本不接受非空默认值")
	case FieldTypeInteger:
		number, ok := value.(json.Number)
		if !ok || !regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`).MatchString(number.String()) {
			failures.add(path, "invalid_type", "默认值必须是 JSON integer")
			return
		}
		validateNumericValue(number.String(), input, path, failures)
	case FieldTypeDecimal:
		text, ok := value.(string)
		if !ok || !decimalPattern.MatchString(text) {
			failures.add(path, "invalid_format", "默认值必须是无指数规范十进制字符串")
			return
		}
		if isZeroDecimal(text) && strings.HasPrefix(text, "-") {
			input.DefaultValue = json.RawMessage(`"0"`)
		}
		validateNumericValue(text, input, path, failures)
	case FieldTypeBoolean:
		if _, ok := value.(bool); !ok {
			failures.add(path, "invalid_type", "默认值必须是 boolean")
		}
	case FieldTypeDate:
		text, ok := value.(string)
		if !ok || !validDate(text) {
			failures.add(path, "invalid_format", "默认值必须是 YYYY-MM-DD")
		}
	case FieldTypeDatetime:
		text, ok := value.(string)
		if !ok {
			failures.add(path, "invalid_type", "默认值必须是 RFC 3339 字符串")
			return
		}
		parsed, err := time.Parse(time.RFC3339Nano, text)
		if err != nil {
			failures.add(path, "invalid_format", "默认值必须是 RFC 3339 字符串")
			return
		}
		input.DefaultValue, _ = json.Marshal(parsed.UTC().Truncate(time.Microsecond).Format(time.RFC3339Nano))
	case FieldTypeSingleSelect:
		text, ok := value.(string)
		if !ok || !enumContains(input.Constraints.EnumOptions, text) {
			failures.add(path, "invalid_value", "默认值必须是已有枚举 value")
		}
	case FieldTypeMultiSelect:
		values, ok := value.([]any)
		if !ok {
			failures.add(path, "invalid_type", "默认值必须是枚举 value 数组")
			return
		}
		seen := map[string]bool{}
		for _, item := range values {
			text, ok := item.(string)
			if !ok || !enumContains(input.Constraints.EnumOptions, text) {
				failures.add(path, "invalid_value", "默认值包含未知枚举 value")
				continue
			}
			if seen[text] {
				failures.add(path, "duplicate", "默认值不能包含重复枚举 value")
			}
			seen[text] = true
		}
	case FieldTypeJSON:
		// 任意合法 JSON 均可作为默认值。
	case FieldTypeObject:
		object, ok := value.(map[string]any)
		if !ok {
			failures.add(path, "invalid_type", "对象字段默认值必须是 object")
			return
		}
		validateCompositeDefault(object, input.Children, path, failures)
	case FieldTypeRepeatableGroup:
		items, ok := value.([]any)
		if !ok {
			failures.add(path, "invalid_type", "重复组默认值必须是 object array")
			return
		}
		for i, item := range items {
			object, ok := item.(map[string]any)
			if !ok {
				failures.add(fmt.Sprintf("%s/%d", path, i), "invalid_type", "重复组元素必须是 object")
				continue
			}
			validateCompositeDefault(object, input.Children, fmt.Sprintf("%s/%d", path, i), failures)
		}
	}
}

func validateCompositeDefault(value map[string]any, children []ContentFieldInput, path string, failures *validationErrors) {
	byKey := make(map[string]*ContentFieldInput, len(children))
	for i := range children {
		byKey[children[i].Key] = &children[i]
	}
	keys := make([]string, 0, len(value))
	for key := range value {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		child, ok := byKey[key]
		if !ok {
			failures.add(path+"/"+escapePointer(key), "unknown_property", "默认值包含未知子字段")
			continue
		}
		raw, _ := json.Marshal(value[key])
		copy := *child
		copy.DefaultValue = raw
		validateDefault(&copy, path+"/"+escapePointer(key), failures)
	}
	for _, child := range children {
		if child.Required {
			if _, ok := value[child.Key]; !ok {
				failures.add(path+"/"+escapePointer(child.Key), "required", "缺少必填子字段")
			}
		}
	}
}

func validateNumericValue(value string, input *ContentFieldInput, path string, failures *validationErrors) {
	number, ok := decimalRat(value)
	if !ok {
		return
	}
	if input.Constraints.Minimum != nil {
		minimum, ok := decimalRat(*input.Constraints.Minimum)
		if ok && number.Cmp(minimum) < 0 {
			failures.add(path, "out_of_range", "默认值小于 minimum")
		}
	}
	if input.Constraints.Maximum != nil {
		maximum, ok := decimalRat(*input.Constraints.Maximum)
		if ok && number.Cmp(maximum) > 0 {
			failures.add(path, "out_of_range", "默认值大于 maximum")
		}
	}
}

func validateBound(value string, fieldType FieldType, path string, failures *validationErrors) {
	valid := decimalPattern.MatchString(value)
	if fieldType == FieldTypeInteger {
		valid = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`).MatchString(value)
	}
	if !valid {
		failures.add(path, "invalid_format", "数值边界格式无效")
	}
}

func validateKey(value, path string, failures *validationErrors) {
	if !keyPattern.MatchString(value) {
		failures.add(path, "invalid_format", "key 必须以小写字母开头且只含小写字母、数字和下划线，长度不超过 64")
	}
}
func validateText(value string, minimum, maximum int, path string, failures *validationErrors) {
	length := utf8.RuneCountInString(value)
	if length < minimum {
		failures.add(path, "too_short", "文本长度不足")
	}
	if length > maximum {
		failures.add(path, "too_long", "文本长度超限")
	}
}
func isRootScalar(value FieldType) bool {
	switch value {
	case FieldTypeSingleLineText, FieldTypeMultiLineText, FieldTypeInteger, FieldTypeDecimal, FieldTypeBoolean, FieldTypeDate, FieldTypeDatetime, FieldTypeSingleSelect:
		return true
	}
	return false
}
func decimalRat(value string) (*big.Rat, bool) {
	if !decimalPattern.MatchString(value) {
		return nil, false
	}
	result, ok := new(big.Rat).SetString(value)
	return result, ok
}
func isZeroDecimal(value string) bool {
	number, ok := decimalRat(value)
	return ok && number.Sign() == 0
}
func enumContains(options []EnumOption, value string) bool {
	for _, option := range options {
		if option.Value == value {
			return true
		}
	}
	return false
}
func validDate(value string) bool {
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}
func escapePointer(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "~", "~0"), "/", "~1")
}
