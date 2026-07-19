package schema

import (
	"encoding/json"
	"errors"
	"testing"

	"cms/internal/platform/apperror"
)

func TestValidateAllFieldTypes(t *testing.T) {
	textMin, textMax := 1, 5
	minimum, maximum := "-10", "10"
	target := "mdl_target"
	cases := []struct {
		name  string
		input ContentFieldInput
	}{
		{"single_line_text", fieldInput(FieldTypeSingleLineText, `"abc"`, FieldConstraints{MinLength: &textMin, MaxLength: &textMax})},
		{"multi_line_text", fieldInput(FieldTypeMultiLineText, `"a\nb"`, FieldConstraints{})},
		{"rich_text", fieldInput(FieldTypeRichText, `null`, FieldConstraints{})},
		{"integer", fieldInput(FieldTypeInteger, `2`, FieldConstraints{Minimum: &minimum, Maximum: &maximum})},
		{"decimal", fieldInput(FieldTypeDecimal, `"2.50"`, FieldConstraints{Minimum: &minimum, Maximum: &maximum})},
		{"boolean", fieldInput(FieldTypeBoolean, `true`, FieldConstraints{})},
		{"date", fieldInput(FieldTypeDate, `"2026-07-18"`, FieldConstraints{})},
		{"datetime", fieldInput(FieldTypeDatetime, `"2026-07-18T08:00:00+08:00"`, FieldConstraints{})},
		{"single_select", fieldInput(FieldTypeSingleSelect, `"a"`, FieldConstraints{EnumOptions: []EnumOption{{Value: "a", Label: "A"}}})},
		{"multi_select", fieldInput(FieldTypeMultiSelect, `["a","b"]`, FieldConstraints{EnumOptions: []EnumOption{{Value: "a", Label: "A"}, {Value: "b", Label: "B"}}})},
		{"json", fieldInput(FieldTypeJSON, `{"anything":true}`, FieldConstraints{})},
		{"single_media", fieldInput(FieldTypeSingleMedia, `null`, FieldConstraints{})},
		{"multi_media", fieldInput(FieldTypeMultiMedia, `null`, FieldConstraints{})},
		{"single_relation", fieldInput(FieldTypeSingleRelation, `null`, FieldConstraints{TargetModelID: &target})},
		{"multi_relation", fieldInput(FieldTypeMultiRelation, `null`, FieldConstraints{TargetModelID: &target})},
		{"object", fieldInput(FieldTypeObject, `{"nested":"ok"}`, FieldConstraints{}, ContentFieldInput{Key: "nested", DisplayName: "Nested", Type: FieldTypeSingleLineText, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{}})},
		{"repeatable_group", fieldInput(FieldTypeRepeatableGroup, `[{"nested":1}]`, FieldConstraints{}, ContentFieldInput{Key: "nested", DisplayName: "Nested", Type: FieldTypeInteger, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{}})},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateField(&test.input); err != nil {
				t.Fatalf("ValidateField() error = %v", err)
			}
		})
	}
}

func TestValidateFieldRejectsInvalidDefaultsForAllRepresentations(t *testing.T) {
	target := "mdl_target"
	cases := []ContentFieldInput{
		fieldInput(FieldTypeSingleLineText, `1`, FieldConstraints{}),
		fieldInput(FieldTypeMultiLineText, `false`, FieldConstraints{}),
		fieldInput(FieldTypeRichText, `{}`, FieldConstraints{}),
		fieldInput(FieldTypeInteger, `1.1`, FieldConstraints{}),
		fieldInput(FieldTypeDecimal, `"01.0"`, FieldConstraints{}),
		fieldInput(FieldTypeBoolean, `"true"`, FieldConstraints{}),
		fieldInput(FieldTypeDate, `"2026-02-30"`, FieldConstraints{}),
		fieldInput(FieldTypeDatetime, `"today"`, FieldConstraints{}),
		fieldInput(FieldTypeSingleSelect, `"missing"`, FieldConstraints{EnumOptions: []EnumOption{{Value: "a", Label: "A"}}}),
		fieldInput(FieldTypeMultiSelect, `["a","a"]`, FieldConstraints{EnumOptions: []EnumOption{{Value: "a", Label: "A"}}}),
		fieldInput(FieldTypeSingleMedia, `"ast_x"`, FieldConstraints{}),
		fieldInput(FieldTypeMultiMedia, `[]`, FieldConstraints{}),
		fieldInput(FieldTypeSingleRelation, `"ent_x"`, FieldConstraints{TargetModelID: &target}),
		fieldInput(FieldTypeMultiRelation, `[]`, FieldConstraints{TargetModelID: &target}),
		fieldInput(FieldTypeObject, `[]`, FieldConstraints{}, fieldInput(FieldTypeSingleLineText, `null`, FieldConstraints{})),
		fieldInput(FieldTypeRepeatableGroup, `{}`, FieldConstraints{}, fieldInput(FieldTypeSingleLineText, `null`, FieldConstraints{})),
	}
	for _, input := range cases {
		if err := ValidateField(&input); err == nil {
			t.Errorf("type %s: expected validation error", input.Type)
		}
	}
}

func TestValidateFieldConstraintsAndDepth(t *testing.T) {
	input := fieldInput(FieldTypeObject, `null`, FieldConstraints{Unique: true},
		fieldInput(FieldTypeObject, `null`, FieldConstraints{},
			fieldInput(FieldTypeObject, `null`, FieldConstraints{},
				fieldInput(FieldTypeSingleLineText, `null`, FieldConstraints{Sortable: true}))))
	err := ValidateField(&input)
	details := validationDetails(t, err)
	want := map[string]bool{
		"/constraints:index_not_allowed":                                  true,
		"/children/0/children/0/children:max_depth_exceeded":              true,
		"/children/0/children/0/children/0/constraints:index_not_allowed": true,
	}
	for _, detail := range details {
		delete(want, detail.Path+":"+detail.Code)
	}
	if len(want) != 0 {
		t.Fatalf("missing details: %#v; got %#v", want, details)
	}
}

func TestValidationDetailsAreStableAndModelKeyIsImmutableByDTO(t *testing.T) {
	request := CreateContentModelRequest{Key: "Bad-Key", DisplayName: ""}
	details := validationDetails(t, ValidateModelCreate(request))
	if len(details) != 2 || details[0].Path != "/display_name" || details[1].Path != "/key" {
		t.Fatalf("details not sorted: %#v", details)
	}
	var patch UpdateContentModelRequest
	if err := decodeStrict([]byte(`{"key":"new_key"}`), &patch); err == nil {
		t.Fatal("UpdateContentModelRequest accepted immutable key")
	}
}

func TestDecimalAndDatetimeAreNormalized(t *testing.T) {
	decimal := fieldInput(FieldTypeDecimal, `"-0.00"`, FieldConstraints{})
	if err := ValidateField(&decimal); err != nil || string(decimal.DefaultValue) != `"0"` {
		t.Fatalf("decimal = %s, err = %v", decimal.DefaultValue, err)
	}
	datetime := fieldInput(FieldTypeDatetime, `"2026-07-18T08:00:00.123456789+08:00"`, FieldConstraints{})
	if err := ValidateField(&datetime); err != nil || string(datetime.DefaultValue) != `"2026-07-18T00:00:00.123456Z"` {
		t.Fatalf("datetime = %s, err = %v", datetime.DefaultValue, err)
	}
}

func TestPatchDTORejectsNullAndUnknownProperties(t *testing.T) {
	for _, body := range []string{`{"display_name":null}`, `{"key":"immutable"}`} {
		var model UpdateContentModelRequest
		if err := json.Unmarshal([]byte(body), &model); err == nil {
			t.Errorf("model patch accepted %s", body)
		}
	}
	for _, body := range []string{`{"type":null}`, `{"constraints":null}`, `{"children":null}`, `{"key":"immutable"}`} {
		var field ContentFieldPatch
		if err := json.Unmarshal([]byte(body), &field); err == nil {
			t.Errorf("field patch accepted %s", body)
		}
	}
}

func TestValidateFieldRejectsDuplicateKeysAcrossTree(t *testing.T) {
	input := ContentFieldInput{Key: "root", DisplayName: "Root", Type: FieldTypeObject, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{
		{Key: "duplicate", DisplayName: "First", Type: FieldTypeSingleLineText, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{}},
		{Key: "group", DisplayName: "Group", Type: FieldTypeObject, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{{Key: "duplicate", DisplayName: "Second", Type: FieldTypeSingleLineText, DefaultValue: json.RawMessage(`null`), Children: []ContentFieldInput{}}}},
	}}
	details := validationDetails(t, ValidateField(&input))
	found := false
	for _, detail := range details {
		found = found || detail.Path == "/children/1/children/0/key" && detail.Code == "duplicate"
	}
	if !found {
		t.Fatalf("details = %#v", details)
	}
}

func fieldInput(fieldType FieldType, defaultValue string, constraints FieldConstraints, children ...ContentFieldInput) ContentFieldInput {
	return ContentFieldInput{Key: "child", DisplayName: "Child", Type: fieldType, DefaultValue: json.RawMessage(defaultValue), Constraints: constraints, Children: children}
}

func validationDetails(t *testing.T, err error) []ValidationDetail {
	t.Helper()
	if err == nil {
		t.Fatal("expected validation error")
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) {
		t.Fatalf("error type = %T", err)
	}
	details := make([]ValidationDetail, len(appErr.Details))
	for i, value := range appErr.Details {
		details[i] = ValidationDetail{Path: value["path"].(string), Code: value["code"].(string), Message: value["message"].(string)}
	}
	return details
}
