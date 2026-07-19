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
