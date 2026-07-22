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

func TestOpenAPIExposesFieldSiblingOrder(t *testing.T) {
	for _, path := range []string{"../../api/openapi/fragments/admin/models/paths.yaml", "../../api/openapi/admin.yaml"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "/models/{model_id}/fields/order") {
			t.Fatalf("%s 未聚合字段排序路径", path)
		}
	}
	data, err := os.ReadFile("../../api/openapi/fragments/admin/models/schemas.yaml")
	if err != nil {
		t.Fatal(err)
	}
	schema := string(data)
	for _, want := range []string{"UpdateFieldOrderRequest:", "required: [parent_id, base_field_ids, field_ids]", "parent_id:", "base_field_ids:", "field_ids:"} {
		if !strings.Contains(schema, want) {
			t.Fatalf("字段排序 schema 缺少 %q", want)
		}
	}
}

func TestOpenAPIExposesAtomicChildFieldCreation(t *testing.T) {
	for _, path := range []string{"../../api/openapi/fragments/admin/models/paths.yaml", "../../api/openapi/admin.yaml"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "/models/{model_id}/fields/{parent_field_id}/children") {
			t.Fatalf("%s 未聚合子字段创建路径", path)
		}
	}
	data, err := os.ReadFile("../../api/openapi/fragments/admin/models/paths.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"operationId: createAdminContentModelChildField", "$ref: ./schemas.yaml#/$defs/ContentFieldInput", `"201":`, "$ref: ./schemas.yaml#/$defs/ContentField"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("子字段创建契约缺少 %q", want)
		}
	}
}

func TestOpenAPIWorkflowActionsReturnContentEntry(t *testing.T) {
	data, err := os.ReadFile("../../api/openapi/fragments/admin/content/workflow-paths.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Count(text, "$ref: ./schemas.yaml#/$defs/ContentEntry") != 1 {
		t.Fatalf("工作流共享成功响应必须统一引用 ContentEntry: %s", text)
	}
	if strings.Contains(text, "$ref: ./workflow-schemas.yaml#/$defs/WorkflowEntry") {
		t.Fatal("工作流响应仍引用不完整的 WorkflowEntry")
	}
	for _, operation := range []string{"submitAdminContentRevision", "approveAdminContentRevision", "rejectAdminContentRevision", "unpublishAdminContentRevision"} {
		if !strings.Contains(text, "operationId: "+operation) {
			t.Fatalf("缺少工作流操作 %s", operation)
		}
	}
}
