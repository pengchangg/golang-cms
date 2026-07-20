package schema

import (
	"os"
	"strings"
	"testing"
)

func TestOpenAPIContentFieldPatchConditionsAndDecimalPattern(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi/fragments/admin/models/schemas.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(data)
	for _, want := range []string{
		"pattern: '^-?(0|[1-9][0-9]*)(\\.[0-9]+)?$'",
		"dependentRequired:",
		"type: [default_value, constraints, children]",
	} {
		if !strings.Contains(schema, want) {
			t.Fatalf("OpenAPI schema missing %q", want)
		}
	}
	if strings.Contains(schema, `(\\\\.[0-9]+)`) {
		t.Fatal("decimal pattern contains a double-escaped dot")
	}
}

func TestOpenAPIRootsExposeMediaValueConstraints(t *testing.T) {
	for _, path := range []string{"../../api/openapi/admin.yaml", "../../api/openapi/content.yaml"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		schema := string(data)
		for _, name := range []string{"AssetID", "SingleMediaValue", "MultiMediaValue"} {
			if !strings.Contains(schema, "    "+name+":") || !strings.Contains(schema, "assets/schemas.yaml#/$defs/"+name) {
				t.Fatalf("%s 未聚合 %s", path, name)
			}
		}
	}

	for _, path := range []string{
		"../../api/openapi/fragments/admin/content/schemas.yaml",
		"../../api/openapi/fragments/content/schemas.yaml",
	} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		description := string(data)
		for _, name := range []string{"components.schemas.AssetID", "components.schemas.SingleMediaValue", "components.schemas.MultiMediaValue"} {
			if !strings.Contains(description, name) {
				t.Fatalf("%s 的 DynamicContent 说明未引用 %s", path, name)
			}
		}
	}
}
