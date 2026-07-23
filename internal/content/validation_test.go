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
	assertValidationCodes(t, err, []string{"/content/old:archived_field", "/content/unknown:unknown_property"})
}

func TestMediaReferencesRecursesObjectsAndGroups(t *testing.T) {
	fields := []schema.ContentField{{ID: "fld_gallery", Key: "gallery", Type: schema.FieldTypeRepeatableGroup, Status: schema.StatusActive, Children: []schema.ContentField{{ID: "fld_image", Key: "image", Type: schema.FieldTypeSingleMedia, Status: schema.StatusActive}, {ID: "fld_assets", Key: "assets", Type: schema.FieldTypeMultiMedia, Status: schema.StatusActive}}}}
	content, err := validateContent(json.RawMessage(`{"gallery":[{"image":"ast_1","assets":["ast_2","ast_3"]}]}`), fields)
	if err != nil {
		t.Fatal(err)
	}
	refs, err := mediaReferences(content, Revision{ID: "rev_1", EntryID: "ent_1", ModelID: "mdl_1"}, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 || refs[0].JSONPointer != "/gallery/0/image" || refs[1].JSONPointer != "/gallery/0/assets" || refs[1].Position != 0 || refs[2].Position != 1 {
		t.Fatalf("媒体引用不符合预期: %#v", refs)
	}
}

func TestValidateMediaRejectsDuplicateAndMoreThanFifty(t *testing.T) {
	fields := []schema.ContentField{{Key: "assets", Type: schema.FieldTypeMultiMedia, Status: schema.StatusActive}}
	_, err := validateContent(json.RawMessage(`{"assets":["ast_1","ast_1"]}`), fields)
	assertValidationCodes(t, err, []string{"/content/assets/1:duplicate"})
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

func TestValidateRichTextAllowsOnlyAtomicBlockMediaNodes(t *testing.T) {
	fields := []schema.ContentField{{ID: "fld_body", Key: "body", Type: schema.FieldTypeRichText, Status: schema.StatusActive}}
	valid := json.RawMessage(`{"body":{"type":"doc","content":[{"type":"image","attrs":{"asset_id":"ast_image","alt":"封面"}},{"type":"audio","attrs":{"asset_id":"ast_audio"}},{"type":"video","attrs":{"asset_id":"ast_video"}}]}}`)
	content, err := validateContent(valid, fields)
	if err != nil {
		t.Fatalf("合法媒体块应通过: %v", err)
	}
	refs, err := mediaReferences(content, Revision{ID: "rev_1", EntryID: "ent_1", ModelID: "mdl_1"}, fields)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 3 || refs[0].AssetID != "ast_image" || refs[0].Kind != "image" || refs[0].JSONPointer != "/body/content/0/attrs/asset_id" || refs[0].Position != 0 || refs[2].JSONPointer != "/body/content/2/attrs/asset_id" {
		t.Fatalf("富文本媒体引用不符合预期: %#v", refs)
	}

	invalid := json.RawMessage(`{"body":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"image","attrs":{"asset_id":"ast_inline","alt":""}}]},{"type":"image","attrs":{"asset_id":"ast_extra","alt":"","title":"x"},"content":[]},{"type":"audio","attrs":{"asset_id":"ast_audio","alt":"x"}}]}}`)
	_, err = validateContent(invalid, fields)
	assertValidationCodes(t, err, []string{
		"/content/body/content/0/content/0/type:invalid_value",
		"/content/body/content/1/attrs/title:unknown_property",
		"/content/body/content/1/content:unknown_property",
		"/content/body/content/2/attrs/alt:unknown_property",
	})
}

func TestValidateRichTextLimitsImageAlt(t *testing.T) {
	fields := []schema.ContentField{{Key: "body", Type: schema.FieldTypeRichText, Status: schema.StatusActive}}
	raw, err := json.Marshal(map[string]any{"body": map[string]any{"type": "doc", "content": []any{map[string]any{"type": "image", "attrs": map[string]any{"asset_id": "ast_image", "alt": strings.Repeat("图", 1001)}}}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = validateContent(raw, fields)
	assertValidationCodes(t, err, []string{"/content/body/content/0/attrs/alt:too_long"})
}

func TestRichTextMediaReferencesStayInDraftAndPublishedScans(t *testing.T) {
	fields := []schema.ContentField{{ID: "fld_body", Key: "body", Type: schema.FieldTypeRichText, Status: schema.StatusActive}}
	raw := json.RawMessage(`{"body":{"type":"doc","content":[{"type":"blockquote","content":[{"type":"image","attrs":{"asset_id":"ast_1","alt":"说明"}}]}]}}`)
	relations, draftRefs := draftIdentifiers(raw, "mdl_1", fields)
	if len(relations) != 0 || len(draftRefs) != 1 || draftRefs[0].AssetID != "ast_1" || draftRefs[0].Kind != "image" {
		t.Fatalf("CSV 草稿标识扫描不符合预期: relations=%#v refs=%#v", relations, draftRefs)
	}
	ids, err := publishedAssetIDs(raw, fields)
	if err != nil || len(ids) != 1 || ids[0] != "ast_1" {
		t.Fatalf("发布素材扫描不符合预期: ids=%#v err=%v", ids, err)
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
