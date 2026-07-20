package task

import (
	"os"
	"strings"
	"testing"
)

func TestClaimSQLUsesDatabaseTimeAndSkipLocked(t *testing.T) {
	checks := []string{
		"LEFT JOIN transfer_jobs t ON t.job_id=b.id",
		"b.status='queued' AND b.available_at<=NOW(6)",
		"b.status='running' AND b.lease_expires_at<NOW(6)",
		"ORDER BY b.available_at ASC,b.id ASC",
		"FOR UPDATE SKIP LOCKED",
	}
	for _, check := range checks {
		if !strings.Contains(claimSQL, check) {
			t.Errorf("领取 SQL 缺少 %q", check)
		}
	}
}

func TestClaimSQLReadsAttemptAndCommittedStateInOrder(t *testing.T) {
	columns := "b.id,b.type,b.payload,b.attempt,b.max_attempts,b.cancel_requested_at,t.committed_at"
	if !strings.Contains(claimSQL, columns) {
		t.Fatalf("领取 SQL 字段顺序错误: %s", claimSQL)
	}
	if !strings.Contains(reconcileCommittedSQL, "type='csv_import'") || !strings.Contains(reconcileCommittedSQL, "status='succeeded',progress=100") || !strings.Contains(reconcileCommittedSQL, "committed_at IS NOT NULL") {
		t.Fatalf("已提交导入未原子收敛成功: %s", reconcileCommittedSQL)
	}
}

func TestLeaseMutationSQLMatchesTokenAndUnexpiredLease(t *testing.T) {
	for name, query := range map[string]string{
		"renew": renewSQL, "progress": progressSQL, "succeed": succeedSQL, "fail": failSQL, "cancel": cancelSQL,
	} {
		if !strings.Contains(query, "status='running'") || !strings.Contains(query, "lease_token=?") || !strings.Contains(query, "lease_expires_at>=NOW(6)") {
			t.Errorf("%s SQL 未完整限制有效租约: %s", name, query)
		}
	}
}

func TestSucceedSQLFencesCancellationUnlessTransferCommitted(t *testing.T) {
	if !strings.Contains(succeedSQL, "cancel_requested_at IS NULL") || !strings.Contains(succeedSQL, "committed_at IS NOT NULL") {
		t.Fatalf("成功 SQL 未定义取消与已提交导入的优先级: %s", succeedSQL)
	}
}

func TestFailureSQLDefinesFixedBackoff(t *testing.T) {
	if !strings.Contains(claimUpdateSQL, "NOW(6)") {
		t.Fatal("领取更新必须使用数据库时间")
	}
	if !strings.Contains(failSQL, "IF(attempt=1,30,120) SECOND") || !strings.Contains(failSQL, "attempt<max_attempts") {
		t.Fatal("失败 SQL 未定义 F3 固定退避和最大次数")
	}
}

func TestMigrationIsSingleStatement(t *testing.T) {
	data, err := os.ReadFile("../../../db/migrations/000029_background_jobs.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), ";") != 1 {
		t.Fatalf("迁移必须恰好包含一个 SQL 语句")
	}
}
