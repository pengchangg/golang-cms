package configuration

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"cms/internal/identity"
	"cms/internal/platform/apperror"
)

func TestValidateAndNormalizeMultiValueDeduplicatesInOrder(t *testing.T) {
	value, assets, relations, err := validateAndNormalizeValue(json.RawMessage(`["a","b","a","c","b"]`), TypeMultiAsset, Constraints{})
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != `["a","b","c"]` {
		t.Fatalf("value = %s", value)
	}
	if !reflect.DeepEqual(assets, []string{"a", "b", "c"}) || relations != nil {
		t.Fatalf("assets = %v, relations = %v", assets, relations)
	}
}

func TestValidateAndNormalizeMultiValueLimitAfterDeduplication(t *testing.T) {
	values := make([]string, 51)
	for i := range values {
		values[i] = strings.Repeat("x", i+1)
	}
	raw, _ := json.Marshal(values)
	_, _, _, err := validateAndNormalizeValue(raw, TypeMultiRelation, Constraints{TargetModelID: pointer("mdl")})
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")
}

func TestValidateAndNormalizeDecimalCanonicalizesNegativeZero(t *testing.T) {
	value, _, _, err := validateAndNormalizeValue(json.RawMessage(`"-0.00"`), TypeDecimal, Constraints{})
	if err != nil {
		t.Fatal(err)
	}
	if string(value) != `"0"` {
		t.Fatalf("value = %s", value)
	}
}

func TestValidateValueConstraintsAndJSONLimit(t *testing.T) {
	minimum, maximum := 2, 3
	_, _, _, err := validateAndNormalizeValue(json.RawMessage(`"a"`), TypeString, Constraints{MinLength: &minimum, MaxLength: &maximum})
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")

	oversized := json.RawMessage(`"` + strings.Repeat("x", maxValueJSONBytes) + `"`)
	_, _, _, err = validateAndNormalizeValue(oversized, TypeJSON, Constraints{})
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")
}

func TestValidateConstraintsRejectsWrongTypeAndMissingRelationModel(t *testing.T) {
	minimum := "1"
	input := CreateItemRequest{Key: "title", DisplayName: "标题", ValueType: TypeString, Constraints: Constraints{Minimum: &minimum}}
	assertApplicationError(t, validateItemCreate(&input), apperror.KindInvalidArgument, "validation_failed")

	input.ValueType = TypeSingleRelation
	input.Constraints = Constraints{}
	assertApplicationError(t, validateItemCreate(&input), apperror.KindInvalidArgument, "validation_failed")
}

func TestConstraintsRejectUnknownFields(t *testing.T) {
	var input CreateItemRequest
	err := json.Unmarshal([]byte(`{"item_key":"title","display_name":"标题","value_type":"string","constraints":{"required":true,"unknown":1}}`), &input)
	if err == nil {
		t.Fatal("未知 constraints 字段未被拒绝")
	}
	err = json.Unmarshal([]byte(`{"item_key":"title","display_name":"标题","value_type":"string","constraints":null}`), &input)
	if err == nil {
		t.Fatal("null constraints 未被拒绝")
	}
}

func TestValidateStringPatternAndEnum(t *testing.T) {
	pattern := `^[a-z]+$`
	constraints := Constraints{Pattern: &pattern, StringEnum: []string{"foo", "bar"}}
	if _, _, _, err := validateAndNormalizeValue(json.RawMessage(`"foo"`), TypeString, constraints); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := validateAndNormalizeValue(json.RawMessage(`"FOO"`), TypeString, constraints)
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")

	invalid := "("
	input := CreateItemRequest{Key: "title", DisplayName: "标题", ValueType: TypeString, Constraints: Constraints{Pattern: &invalid}}
	assertApplicationError(t, validateItemCreate(&input), apperror.KindInvalidArgument, "validation_failed")
	tooLong := strings.Repeat("a", maxPatternBytes+1)
	input.Constraints.Pattern = &tooLong
	assertApplicationError(t, validateItemCreate(&input), apperror.KindInvalidArgument, "validation_failed")
}

func TestValidateIntegerEnumPreservesInt64Precision(t *testing.T) {
	constraints := Constraints{IntegerEnum: []string{"9007199254740993", "9223372036854775807"}}
	if _, _, _, err := validateAndNormalizeValue(json.RawMessage(`9007199254740993`), TypeInteger, constraints); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := validateAndNormalizeValue(json.RawMessage(`9007199254740992`), TypeInteger, constraints)
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")

	input := CreateItemRequest{Key: "limit", DisplayName: "上限", ValueType: TypeInteger, Constraints: Constraints{IntegerEnum: []string{"9223372036854775808"}}}
	assertApplicationError(t, validateItemCreate(&input), apperror.KindInvalidArgument, "validation_failed")
}

func TestValidateDecimalScaleAndEnum(t *testing.T) {
	scale := 2
	constraints := Constraints{Scale: &scale, DecimalEnum: []string{"1.20", "2.50"}}
	if _, _, _, err := validateAndNormalizeValue(json.RawMessage(`"1.2"`), TypeDecimal, constraints); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := validateAndNormalizeValue(json.RawMessage(`"1.200"`), TypeDecimal, constraints)
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")
	_, _, _, err = validateAndNormalizeValue(json.RawMessage(`"1.21"`), TypeDecimal, constraints)
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")
}

func TestValidateJSONMaxBytesUsesNormalizedEncoding(t *testing.T) {
	limit := 7
	if _, _, _, err := validateAndNormalizeValue(json.RawMessage("  {\"a\":1}  "), TypeJSON, Constraints{MaxBytes: &limit}); err != nil {
		t.Fatal(err)
	}
	limit = 6
	_, _, _, err := validateAndNormalizeValue(json.RawMessage(`{"a":1}`), TypeJSON, Constraints{MaxBytes: &limit})
	assertApplicationError(t, err, apperror.KindInvalidArgument, "validation_failed")
}

func TestValidateAssetConstraintsNormalizeMIMETypes(t *testing.T) {
	maxSize := int64(1024)
	input := CreateItemRequest{Key: "hero", DisplayName: "主图", ValueType: TypeSingleAsset, Constraints: Constraints{AllowedMimeTypes: []string{"image/webp", "image/png", "image/webp"}, MaxSize: &maxSize}}
	if err := validateItemCreate(&input); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(input.Constraints.AllowedMimeTypes, []string{"image/png", "image/webp"}) {
		t.Fatalf("allowed_mime_types = %v", input.Constraints.AllowedMimeTypes)
	}
	if !assetMatchesConstraints("image/png", 1024, input.Constraints) || assetMatchesConstraints("image/jpeg", 10, input.Constraints) || assetMatchesConstraints("image/png", 1025, input.Constraints) {
		t.Fatal("素材约束匹配结果错误")
	}

	input.Constraints.AllowedMimeTypes = []string{"Image/PNG"}
	assertApplicationError(t, validateItemCreate(&input), apperror.KindInvalidArgument, "validation_failed")
}

func TestRequireNamespacePermissionUsesNamespaceID(t *testing.T) {
	principal := principalWithConfig("cns_site", permissionView)
	if err := requireNamespacePermission(principal, "cns_site", permissionView); err != nil {
		t.Fatal(err)
	}
	assertApplicationError(t, requireNamespacePermission(principal, "other", permissionView), apperror.KindPermissionDenied, "permission_denied")
}

func pointer[T any](value T) *T { return &value }

func assertApplicationError(t *testing.T, err error, kind apperror.Kind, code string) {
	t.Helper()
	var applicationError *apperror.Error
	if !errors.As(err, &applicationError) || applicationError.Kind != kind || applicationError.Code != code {
		t.Fatalf("error = %#v", err)
	}
}

func principalWithConfig(namespaceID string, permissions ...string) identity.Principal {
	return identity.Principal{ConfigNamespacePermissions: []identity.ConfigNamespacePermissions{{ConfigNamespaceID: namespaceID, Permissions: permissions}}}
}
