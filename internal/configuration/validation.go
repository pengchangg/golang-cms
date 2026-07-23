package configuration

import (
	"bytes"
	"encoding/json"
	"math/big"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const maxValueJSONBytes = 64 << 10
const maxMultiValues = 50
const maxPatternBytes = 512
const maxAssetSize = int64(5 << 30)

var (
	keyPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)
	integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	decimalPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)(\.[0-9]+)?$`)
	mimePattern    = regexp.MustCompile(`^[a-z0-9!#$&^_.+%*-]+/[a-z0-9!#$&^_.+%*-]+$`)
)

func validateNamespaceCreate(input CreateNamespaceRequest) error {
	var failures validationErrors
	validateKey(input.Key, "/namespace_key", &failures)
	validateText(input.DisplayName, 1, 120, "/display_name", &failures)
	validateText(input.Description, 0, 1000, "/description", &failures)
	return failures.err()
}

func validateNamespacePatch(input NamespacePatch) error {
	var failures validationErrors
	if input.DisplayName == nil && input.Description == nil {
		failures.add("", "required", "至少提交一个可修改属性")
	}
	if input.DisplayName != nil {
		validateText(*input.DisplayName, 1, 120, "/display_name", &failures)
	}
	if input.Description != nil {
		validateText(*input.Description, 0, 1000, "/description", &failures)
	}
	return failures.err()
}

func validateItemCreate(input *CreateItemRequest) error {
	var failures validationErrors
	validateKey(input.Key, "/item_key", &failures)
	validateText(input.DisplayName, 1, 120, "/display_name", &failures)
	validateText(input.Description, 0, 1000, "/description", &failures)
	normalizeConstraints(&input.Constraints)
	validateConstraints(input.ValueType, input.Constraints, &failures)
	return failures.err()
}

func validateItemPatch(input ItemPatch, current Item) (Item, error) {
	var failures validationErrors
	if input.DisplayName == nil && input.Description == nil && input.Constraints == nil {
		failures.add("", "required", "至少提交一个可修改属性")
	}
	if input.DisplayName != nil {
		validateText(*input.DisplayName, 1, 120, "/display_name", &failures)
		current.DisplayName = *input.DisplayName
	}
	if input.Description != nil {
		validateText(*input.Description, 0, 1000, "/description", &failures)
		current.Description = *input.Description
	}
	if input.Constraints != nil {
		current.Constraints = *input.Constraints
		normalizeConstraints(&current.Constraints)
	}
	validateConstraints(current.ValueType, current.Constraints, &failures)
	return current, failures.err()
}

func validateConstraints(valueType ValueType, constraints Constraints, failures *validationErrors) {
	if !validValueType(valueType) {
		failures.add("/value_type", "invalid_value", "配置值类型无效")
		return
	}
	if valueType != TypeString && (constraints.MinLength != nil || constraints.MaxLength != nil) {
		failures.add("/constraints", "constraint_not_allowed", "min_length 和 max_length 仅适用于 string")
	}
	if constraints.MinLength != nil && *constraints.MinLength < 0 {
		failures.add("/constraints/min_length", "out_of_range", "min_length 不能小于 0")
	}
	if constraints.MaxLength != nil && *constraints.MaxLength < 0 {
		failures.add("/constraints/max_length", "out_of_range", "max_length 不能小于 0")
	}
	if constraints.MinLength != nil && constraints.MaxLength != nil && *constraints.MinLength > *constraints.MaxLength {
		failures.add("/constraints/max_length", "out_of_range", "max_length 必须大于或等于 min_length")
	}
	if valueType != TypeString && (constraints.Pattern != nil || constraints.StringEnum != nil) {
		failures.add("/constraints", "constraint_not_allowed", "pattern 和 string_enum 仅适用于 string")
	}
	if constraints.Pattern != nil {
		if len(*constraints.Pattern) > maxPatternBytes {
			failures.add("/constraints/pattern", "too_long", "pattern 不能超过 512 字节")
		} else if _, err := regexp.Compile(*constraints.Pattern); err != nil {
			failures.add("/constraints/pattern", "invalid_format", "pattern 必须是合法 RE2 表达式")
		}
	}
	validateEnum(constraints.StringEnum, "/constraints/string_enum", func(string) bool { return true }, failures)
	for index, value := range constraints.StringEnum {
		path := "/constraints/string_enum/" + strconv.Itoa(index)
		length := utf8.RuneCountInString(value)
		if constraints.MinLength != nil && length < *constraints.MinLength || constraints.MaxLength != nil && length > *constraints.MaxLength {
			failures.add(path, "out_of_range", "string_enum 值不满足长度约束")
		}
		if constraints.Pattern != nil {
			pattern, err := regexp.Compile(*constraints.Pattern)
			if err == nil && !pattern.MatchString(value) {
				failures.add(path, "pattern_mismatch", "string_enum 值不匹配 pattern")
			}
		}
	}
	numeric := valueType == TypeInteger || valueType == TypeDecimal
	if !numeric && (constraints.Minimum != nil || constraints.Maximum != nil) {
		failures.add("/constraints", "constraint_not_allowed", "minimum 和 maximum 仅适用于 integer 和 decimal")
	}
	if constraints.Minimum != nil && !validBound(*constraints.Minimum, valueType) {
		failures.add("/constraints/minimum", "invalid_format", "数值下界格式无效")
	}
	if constraints.Maximum != nil && !validBound(*constraints.Maximum, valueType) {
		failures.add("/constraints/maximum", "invalid_format", "数值上界格式无效")
	}
	if constraints.Minimum != nil && constraints.Maximum != nil {
		minimum, ok1 := decimalRat(*constraints.Minimum)
		maximum, ok2 := decimalRat(*constraints.Maximum)
		if ok1 && ok2 && minimum.Cmp(maximum) > 0 {
			failures.add("/constraints/maximum", "out_of_range", "maximum 必须大于或等于 minimum")
		}
	}
	if valueType != TypeInteger && constraints.IntegerEnum != nil {
		failures.add("/constraints/integer_enum", "constraint_not_allowed", "integer_enum 仅适用于 integer")
	}
	validateEnum(constraints.IntegerEnum, "/constraints/integer_enum", validInteger, failures)
	validateNumericEnum(constraints.IntegerEnum, "/constraints/integer_enum", constraints, nil, failures)
	validateNumericEnumDuplicates(constraints.IntegerEnum, "/constraints/integer_enum", failures)
	if valueType != TypeDecimal && (constraints.Scale != nil || constraints.DecimalEnum != nil) {
		failures.add("/constraints", "constraint_not_allowed", "scale 和 decimal_enum 仅适用于 decimal")
	}
	if constraints.Scale != nil && (*constraints.Scale < 0 || *constraints.Scale > 30) {
		failures.add("/constraints/scale", "out_of_range", "scale 必须在 0 到 30 之间")
	}
	validateEnum(constraints.DecimalEnum, "/constraints/decimal_enum", validDecimal, failures)
	validateNumericEnum(constraints.DecimalEnum, "/constraints/decimal_enum", constraints, constraints.Scale, failures)
	validateNumericEnumDuplicates(constraints.DecimalEnum, "/constraints/decimal_enum", failures)
	if valueType != TypeJSON && constraints.MaxBytes != nil {
		failures.add("/constraints/max_bytes", "constraint_not_allowed", "max_bytes 仅适用于 json")
	}
	if constraints.MaxBytes != nil && (*constraints.MaxBytes < 1 || *constraints.MaxBytes > maxValueJSONBytes) {
		failures.add("/constraints/max_bytes", "out_of_range", "max_bytes 必须在 1 到 65536 之间")
	}
	asset := valueType == TypeSingleAsset || valueType == TypeMultiAsset
	if !asset && (constraints.AllowedMimeTypes != nil || constraints.MaxSize != nil) {
		failures.add("/constraints", "constraint_not_allowed", "allowed_mime_types 和 max_size 仅适用于素材配置")
	}
	if constraints.AllowedMimeTypes != nil {
		if len(constraints.AllowedMimeTypes) == 0 {
			failures.add("/constraints/allowed_mime_types", "too_short", "allowed_mime_types 不能为空")
		}
		for index, mimeType := range constraints.AllowedMimeTypes {
			if !mimePattern.MatchString(mimeType) {
				failures.add("/constraints/allowed_mime_types/"+strconv.Itoa(index), "invalid_format", "MIME 类型必须是精确小写 type/subtype")
			}
		}
	}
	if constraints.MaxSize != nil && (*constraints.MaxSize < 1 || *constraints.MaxSize > maxAssetSize) {
		failures.add("/constraints/max_size", "out_of_range", "max_size 必须是 1 到 5 GiB 之间的整数")
	}
	relation := valueType == TypeSingleRelation || valueType == TypeMultiRelation
	if relation && (constraints.TargetModelID == nil || *constraints.TargetModelID == "") {
		failures.add("/constraints/target_model_id", "required", "关系配置必须指定目标模型")
	}
	if !relation && constraints.TargetModelID != nil {
		failures.add("/constraints/target_model_id", "constraint_not_allowed", "target_model_id 仅适用于关系配置")
	}
}

func validateAndNormalizeValue(raw json.RawMessage, valueType ValueType, constraints Constraints) (json.RawMessage, []string, []string, error) {
	var failures validationErrors
	if len(raw) == 0 {
		failures.add("/value", "required", "value 为必填项")
		return nil, nil, nil, failures.err()
	}
	if len(raw) > maxValueJSONBytes {
		failures.add("/value", "too_large", "value 的 JSON 编码不能超过 64 KiB")
		return nil, nil, nil, failures.err()
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		failures.add("/value", "invalid_json", "value 不是合法 JSON")
		return nil, nil, nil, failures.err()
	}
	if value == nil {
		if constraints.Required {
			failures.add("/value", "required", "配置值不能为空")
		}
		return json.RawMessage("null"), nil, nil, failures.err()
	}
	var ids []string
	switch valueType {
	case TypeString:
		text, ok := value.(string)
		if !ok {
			failures.add("/value", "invalid_type", "value 必须是字符串")
		} else {
			length := utf8.RuneCountInString(text)
			if constraints.MinLength != nil && length < *constraints.MinLength {
				failures.add("/value", "out_of_range", "字符串短于 min_length")
			}
			if constraints.MaxLength != nil && length > *constraints.MaxLength {
				failures.add("/value", "out_of_range", "字符串长于 max_length")
			}
			if constraints.Pattern != nil {
				pattern, err := regexp.Compile(*constraints.Pattern)
				if err == nil && !pattern.MatchString(text) {
					failures.add("/value", "pattern_mismatch", "字符串不匹配 pattern")
				}
			}
			if len(constraints.StringEnum) > 0 && !slices.Contains(constraints.StringEnum, text) {
				failures.add("/value", "not_in_enum", "字符串不在 string_enum 中")
			}
		}
	case TypeInteger:
		number, ok := value.(json.Number)
		if !ok || !integerPattern.MatchString(number.String()) {
			failures.add("/value", "invalid_type", "value 必须是 JSON integer")
		} else if integer, valid := new(big.Int).SetString(number.String(), 10); !valid || !integer.IsInt64() {
			failures.add("/value", "out_of_range", "integer 超出 int64 范围")
		} else {
			validateNumeric(number.String(), constraints, &failures)
			if len(constraints.IntegerEnum) > 0 && !decimalEnumContains(constraints.IntegerEnum, number.String()) {
				failures.add("/value", "not_in_enum", "integer 不在 integer_enum 中")
			}
		}
	case TypeDecimal:
		text, ok := value.(string)
		if !ok || !validDecimal(text) {
			failures.add("/value", "invalid_format", "decimal 必须是无指数十进制字符串")
		} else {
			if decimal, _ := decimalRat(text); decimal.Sign() == 0 {
				value = "0"
			}
			validateNumeric(text, constraints, &failures)
			if constraints.Scale != nil && decimalScale(text) > *constraints.Scale {
				failures.add("/value", "out_of_range", "decimal 小数位数超过 scale")
			}
			if len(constraints.DecimalEnum) > 0 && !decimalEnumContains(constraints.DecimalEnum, text) {
				failures.add("/value", "not_in_enum", "decimal 不在 decimal_enum 中")
			}
		}
	case TypeBoolean:
		if _, ok := value.(bool); !ok {
			failures.add("/value", "invalid_type", "value 必须是 boolean")
		}
	case TypeJSON:
		// 任意非空合法 JSON 均可保存，大小在规范化后统一检查。
	case TypeSingleAsset, TypeSingleRelation:
		id, ok := value.(string)
		if !ok || id == "" {
			failures.add("/value", "invalid_type", "value 必须是非空 ID")
		} else {
			ids = []string{id}
		}
	case TypeMultiAsset, TypeMultiRelation:
		items, ok := value.([]any)
		if !ok {
			failures.add("/value", "invalid_type", "value 必须是 ID 数组")
			break
		}
		seen := map[string]bool{}
		for _, item := range items {
			id, ok := item.(string)
			if !ok || id == "" {
				failures.add("/value", "invalid_type", "多值配置只能包含非空 ID")
				continue
			}
			if !seen[id] {
				seen[id] = true
				ids = append(ids, id)
			}
		}
		if len(ids) > maxMultiValues {
			failures.add("/value", "too_many_items", "多值配置最多包含 50 个不同值")
		}
		value = ids
	}
	if err := failures.err(); err != nil {
		return nil, nil, nil, err
	}
	normalized, err := json.Marshal(value)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(normalized) > maxValueJSONBytes {
		failures.add("/value", "too_large", "value 的 JSON 编码不能超过 64 KiB")
		return nil, nil, nil, failures.err()
	}
	if valueType == TypeJSON && constraints.MaxBytes != nil && len(normalized) > *constraints.MaxBytes {
		failures.add("/value", "too_large", "value 的 JSON 编码超过 max_bytes")
		return nil, nil, nil, failures.err()
	}
	if valueType == TypeSingleAsset || valueType == TypeMultiAsset {
		return normalized, ids, nil, nil
	}
	if valueType == TypeSingleRelation || valueType == TypeMultiRelation {
		return normalized, nil, ids, nil
	}
	return normalized, nil, nil, nil
}

func validateNumeric(value string, constraints Constraints, failures *validationErrors) {
	number, ok := decimalRat(value)
	if !ok {
		return
	}
	if constraints.Minimum != nil {
		minimum, valid := decimalRat(*constraints.Minimum)
		if valid && number.Cmp(minimum) < 0 {
			failures.add("/value", "out_of_range", "value 小于 minimum")
		}
	}
	if constraints.Maximum != nil {
		maximum, valid := decimalRat(*constraints.Maximum)
		if valid && number.Cmp(maximum) > 0 {
			failures.add("/value", "out_of_range", "value 大于 maximum")
		}
	}
}

func validBound(value string, valueType ValueType) bool {
	if valueType == TypeInteger {
		return integerPattern.MatchString(value)
	}
	return valueType == TypeDecimal && validDecimal(value)
}

func validInteger(value string) bool {
	integer, ok := new(big.Int).SetString(value, 10)
	return integerPattern.MatchString(value) && ok && integer.IsInt64()
}

func decimalScale(value string) int {
	if index := strings.IndexByte(value, '.'); index >= 0 {
		return len(value) - index - 1
	}
	return 0
}

func decimalEnumContains(values []string, value string) bool {
	number, ok := decimalRat(value)
	if !ok {
		return false
	}
	for _, candidate := range values {
		if enumValue, valid := decimalRat(candidate); valid && number.Cmp(enumValue) == 0 {
			return true
		}
	}
	return false
}

func validateEnum(values []string, path string, valid func(string) bool, failures *validationErrors) {
	if values == nil {
		return
	}
	if len(values) == 0 {
		failures.add(path, "too_short", "enum 不能为空")
		return
	}
	seen := make(map[string]bool, len(values))
	for index, value := range values {
		itemPath := path + "/" + strconv.Itoa(index)
		if !valid(value) {
			failures.add(itemPath, "invalid_format", "enum 值格式无效")
		}
		if seen[value] {
			failures.add(itemPath, "duplicate", "enum 值不能重复")
		}
		seen[value] = true
	}
}

func validateNumericEnum(values []string, path string, constraints Constraints, scale *int, failures *validationErrors) {
	for index, value := range values {
		if !validDecimal(value) {
			continue
		}
		itemPath := path + "/" + strconv.Itoa(index)
		number, _ := decimalRat(value)
		if constraints.Minimum != nil {
			minimum, valid := decimalRat(*constraints.Minimum)
			if valid && number.Cmp(minimum) < 0 {
				failures.add(itemPath, "out_of_range", "enum 值小于 minimum")
			}
		}
		if constraints.Maximum != nil {
			maximum, valid := decimalRat(*constraints.Maximum)
			if valid && number.Cmp(maximum) > 0 {
				failures.add(itemPath, "out_of_range", "enum 值大于 maximum")
			}
		}
		if scale != nil && decimalScale(value) > *scale {
			failures.add(itemPath, "out_of_range", "decimal_enum 值小数位数超过 scale")
		}
	}
}

func validateNumericEnumDuplicates(values []string, path string, failures *validationErrors) {
	for i, value := range values {
		for j := 0; j < i; j++ {
			left, leftOK := decimalRat(value)
			right, rightOK := decimalRat(values[j])
			if leftOK && rightOK && left.Cmp(right) == 0 {
				failures.add(path+"/"+strconv.Itoa(i), "duplicate", "enum 数值不能重复")
				break
			}
		}
	}
}

func normalizeConstraints(constraints *Constraints) {
	if constraints.AllowedMimeTypes == nil {
		return
	}
	seen := make(map[string]bool, len(constraints.AllowedMimeTypes))
	values := constraints.AllowedMimeTypes[:0]
	for _, value := range constraints.AllowedMimeTypes {
		if !seen[value] {
			seen[value] = true
			values = append(values, value)
		}
	}
	sort.Strings(values)
	constraints.AllowedMimeTypes = values
}

func validDecimal(value string) bool {
	if !decimalPattern.MatchString(value) {
		return false
	}
	parts := strings.Split(strings.TrimPrefix(value, "-"), ".")
	return len(parts[0]) <= 35 && (len(parts) == 1 || len(parts[1]) <= 30)
}

func decimalRat(value string) (*big.Rat, bool) {
	if !decimalPattern.MatchString(value) {
		return nil, false
	}
	result, ok := new(big.Rat).SetString(value)
	return result, ok
}

func validValueType(value ValueType) bool {
	switch value {
	case TypeString, TypeInteger, TypeDecimal, TypeBoolean, TypeJSON, TypeSingleAsset, TypeMultiAsset, TypeSingleRelation, TypeMultiRelation:
		return true
	default:
		return false
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
