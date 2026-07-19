package content

import (
	"encoding/json"
	"strings"
	"testing"

	"cms/internal/platform/apperror"
	"cms/internal/schema"
)

func TestValidateContentAppliesDefaultsAndNormalizesNestedValues(t *testing.T) {
	minimum := 2
	fields := []schema.ContentField{
		{Key: "title", Type: schema.FieldTypeSingleLineText, Required: true, DefaultValue: json.RawMessage(`"默认标题"`), Status: schema.StatusActive},
		{Key: "published_at", Type: schema.FieldTypeDatetime, DefaultValue: json.RawMessage("null"), Status: schema.StatusActive},
		{Key: "group", Type: schema.FieldTypeRepeatableGroup, DefaultValue: json.RawMessage("null"), Status: schema.StatusActive, Children: []schema.ContentField{
			{Key: "name", Type: schema.FieldTypeSingleLineText, Required: true, DefaultValue: json.RawMessage("null"), Constraints: schema.FieldConstraints{MinLength: &minimum}, Status: schema.StatusActive},
		}},
	}
	result, err := validateContent(json.RawMessage(`{"published_at":"2026-07-18T08:30:00+08:00","group":[{"name":"内容"}]}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	if string(result) != `{"group":[{"name":"内容"}],"published_at":"2026-07-18T00:30:00Z","title":"默认标题"}` {
		t.Fatalf("归一化内容不符合预期: %s", result)
	}
}

func TestValidateContentCollectsStableFieldErrors(t *testing.T) {
	fields := []schema.ContentField{
		{Key: "asset", Type: schema.FieldTypeSingleMedia, Required: true, DefaultValue: json.RawMessage("null"), Status: schema.StatusActive},
		{Key: "old", Type: schema.FieldTypeBoolean, DefaultValue: json.RawMessage("null"), Status: schema.StatusArchived},
	}
	_, err := validateContent(json.RawMessage(`{"unknown":1,"old":true,"asset":"ast_1"}`), fields)
	assertValidationCodes(t, err, []string{"/content/asset:unsupported_non_null_value", "/content/old:archived_field", "/content/unknown:unknown_property"})
}

func TestValidateContentRequiredRejectsExplicitNullEvenWithDefault(t *testing.T) {
	fields := []schema.ContentField{{Key: "title", Type: schema.FieldTypeSingleLineText, Required: true, DefaultValue: json.RawMessage(`"默认标题"`), Status: schema.StatusActive}}
	_, err := validateContent(json.RawMessage(`{"title":null}`), fields)
	assertValidationCodes(t, err, []string{"/content/title:required"})
}

func TestValidateContentRequiresObjectBody(t *testing.T) {
	for _, raw := range []json.RawMessage{nil, json.RawMessage("null"), json.RawMessage(`[]`)} {
		_, err := validateContent(raw, nil)
		if err == nil {
			t.Fatalf("content=%s 应被拒绝", raw)
		}
	}
}

func TestValidateContentChecksSelectDuplicatesAndNumericBounds(t *testing.T) {
	minimum, maximum := "2", "3"
	fields := []schema.ContentField{
		{Key: "count", Type: schema.FieldTypeInteger, DefaultValue: json.RawMessage("null"), Constraints: schema.FieldConstraints{Minimum: &minimum, Maximum: &maximum}, Status: schema.StatusActive},
		{Key: "tags", Type: schema.FieldTypeMultiSelect, DefaultValue: json.RawMessage("null"), Constraints: schema.FieldConstraints{EnumOptions: []schema.EnumOption{{Value: "a", Label: "A"}}}, Status: schema.StatusActive},
	}
	_, err := validateContent(json.RawMessage(`{"count":4,"tags":["a","a","x"]}`), fields)
	assertValidationCodes(t, err, []string{"/content/count:out_of_range", "/content/tags/1:duplicate", "/content/tags/2:invalid_value"})
}

func TestCanonicalUniqueValueIsTypeSafeAndNormalizesDecimals(t *testing.T) {
	integer, err := canonicalUniqueValue(schema.FieldTypeInteger, json.Number("1"))
	if err != nil {
		t.Fatal(err)
	}
	text, _ := canonicalUniqueValue(schema.FieldTypeSingleLineText, "1")
	decimalA, _ := canonicalUniqueValue(schema.FieldTypeDecimal, "1.0")
	decimalB, _ := canonicalUniqueValue(schema.FieldTypeDecimal, "1.00")
	if string(integer) == string(text) {
		t.Fatal("不同标量类型的 canonical 值不可碰撞")
	}
	if string(decimalA) != string(decimalB) {
		t.Fatal("数值相等的小数必须生成相同 canonical 值")
	}
}

func TestValidateRichTextUsesRecursiveWhitelist(t *testing.T) {
	fields := []schema.ContentField{{Key: "body", Type: schema.FieldTypeRichText, DefaultValue: json.RawMessage("null"), Status: schema.StatusActive}}
	valid := json.RawMessage(`{"body":{"type":"doc","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"标题","marks":[{"type":"bold"}]}]},{"type":"paragraph","content":[{"type":"hard_break"}]}]}}`)
	if _, err := validateContent(valid, fields); err != nil {
		t.Fatalf("白名单文档应通过: %v", err)
	}
	invalid := json.RawMessage(`{"body":{"type":"doc","content":[{"type":"paragraph","onclick":"run()","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:run()"}}]}]},{"type":"html","html":"<script>run()</script>"}]}}`)
	_, err := validateContent(invalid, fields)
	if err == nil {
		t.Fatal("HTML、事件和 URL 属性必须被递归拒绝")
	}
}

func assertValidationCodes(t *testing.T, err error, expected []string) {
	t.Helper()
	applicationError, ok := err.(*apperror.Error)
	if !ok || applicationError.Code != "validation_failed" {
		t.Fatalf("期望 validation_failed，得到 %v", err)
	}
	actual := make([]string, len(applicationError.Details))
	for i, item := range applicationError.Details {
		actual[i] = item["path"].(string) + ":" + item["code"].(string)
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("校验详情不符合预期: %v", actual)
	}
}
