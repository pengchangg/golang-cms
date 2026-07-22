package integration

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"cms/internal/client"
)

func TestClientAssetScopeUsesStrictBearerRules(t *testing.T) {
	for _, header := range []string{"", "bearer token", "Bearer", "Bearer one two", "Bearer one,Bearer two"} {
		req := httptest.NewRequest(http.MethodGet, "/api/content/v1/assets/ast_1", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		if _, err := client.ParseBearerToken(req); err == nil {
			t.Fatalf("Authorization %q should fail", header)
		}
	}
}

func TestTransferUploadsMigrationIsSingleStatement(t *testing.T) {
	data, err := os.ReadFile("../../db/migrations/000033_transfer_uploads.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := strings.TrimSpace(string(data))
	if !strings.HasSuffix(sql, ";") || strings.Count(sql, ";") != 1 {
		t.Fatal("000033 必须且只能包含一个 SQL statement")
	}
	for _, required := range []string{"created_by VARCHAR(64) NOT NULL", "model_id VARCHAR(36) NOT NULL", "fk_transfer_uploads_created_by", "fk_transfer_uploads_model"} {
		if !strings.Contains(sql, required) {
			t.Fatalf("000033 缺少上传归属约束 %q", required)
		}
	}
}
