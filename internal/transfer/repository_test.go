package transfer

import (
	"os"
	"strings"
	"testing"
)

func TestTransferMutationSQLUsesLeaseFencing(t *testing.T) {
	data, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	for _, mutation := range []string{"transfer_job_rows", "transfer_job_errors", "result_object_key", "error_object_key", "committed_at"} {
		if !strings.Contains(source, mutation) {
			t.Errorf("Repository 缺少 %s 副作用", mutation)
		}
	}
	if strings.Count(source, "b.status='running' AND b.lease_token=? AND b.lease_expires_at>=NOW(6)") < 7 {
		t.Fatal("transfer 副作用 SQL 未全部限制 running、lease token 和未过期租约")
	}
}

func TestTransferMigrationsAvoidMySQLRowNumberKeyword(t *testing.T) {
	for _, path := range []string{"../../db/migrations/000031_transfer_job_rows.up.sql", "../../db/migrations/000032_transfer_job_errors.up.sql"} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "row_number") || !strings.Contains(string(data), "source_row") {
			t.Errorf("迁移 %s 未使用 source_row", path)
		}
	}
}

func TestTaskScopeSQLDoesNotAcceptRawSQL(t *testing.T) {
	data, err := os.ReadFile("repository.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(data)
	if strings.Contains(source, "scope string") || strings.Contains(source, "+actor+") || strings.Contains(source, "+id+") || strings.Contains(source, "+userID+") {
		t.Fatal("任务作用域 SQL 不应接收或拼接用户输入")
	}
	if !strings.Contains(source, "u.enabled=TRUE AND lc.emergency_admin=TRUE") {
		t.Fatal("应急管理员检查必须同时验证用户启用状态和本地凭据标记")
	}
}
