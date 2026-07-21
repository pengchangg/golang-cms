package audit

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

type captureQuerier struct{ args []any }

func (q *captureQuerier) ExecContext(_ context.Context, _ string, args ...any) (sql.Result, error) {
	q.args = args
	return fakeResult(1), nil
}
func (*captureQuerier) QueryContext(context.Context, string, ...any) (*sql.Rows, error) {
	return nil, nil
}
func (*captureQuerier) QueryRowContext(context.Context, string, ...any) *sql.Row { return nil }

type fakeResult int64

func (r fakeResult) LastInsertId() (int64, error) { return int64(r), nil }
func (r fakeResult) RowsAffected() (int64, error) { return int64(r), nil }

func TestSQLWriterAppendsValidEvent(t *testing.T) {
	q := &captureQuerier{}
	err := (SQLWriter{}).Append(context.Background(), q, Event{ID: "evt_1", OccurredAt: time.Now(), RequestID: "req", ActorType: "system", Action: "auth_local_login_failed", ResourceType: "authentication", Result: "failure", IP: "127.0.0.1", UserAgent: "", Changes: map[string]any{}, FailureCode: pointer("invalid_credentials")})
	if err != nil {
		t.Fatal(err)
	}
	if len(q.args) != 14 {
		t.Fatalf("insert args = %d", len(q.args))
	}
}

func TestSQLWriterRejectsInvalidResultShape(t *testing.T) {
	q := &captureQuerier{}
	err := (SQLWriter{}).Append(context.Background(), q, Event{ID: "evt_1", OccurredAt: time.Now(), RequestID: "req", ActorType: "user", Action: "auth_logout_succeeded", ResourceType: "authentication", Result: "success", IP: "127.0.0.1"})
	if err == nil {
		t.Fatal("缺少 actor_id 的事件被接受")
	}
}

func TestSQLWriterRequiresUserDisplayNameSnapshot(t *testing.T) {
	q := &captureQuerier{}
	actorID := "usr_1"
	err := (SQLWriter{}).Append(context.Background(), q, Event{ID: "evt_1", OccurredAt: time.Now(), RequestID: "req", ActorType: "user", ActorID: &actorID, Action: "auth_logout_succeeded", ResourceType: "authentication", Result: "success", IP: "127.0.0.1"})
	if err == nil {
		t.Fatal("缺少操作者名称快照的用户事件被接受")
	}
}

func TestSQLWriterRejectsSensitiveChanges(t *testing.T) {
	for _, changes := range []map[string]any{
		{"password_hash": "secret"},
		{"phone_e164": "+8613800138000"},
		{"otp_code": "123456"},
		{"captcha_x": 120},
	} {
		q := &captureQuerier{}
		err := (SQLWriter{}).Append(context.Background(), q, Event{ID: "evt_1", OccurredAt: time.Now(), RequestID: "req", ActorType: "system", Action: "auth_local_login_failed", ResourceType: "authentication", Result: "failure", IP: "127.0.0.1", Changes: changes, FailureCode: pointer("invalid_credentials")})
		if err == nil {
			t.Fatalf("敏感审计字段被接受: %v", changes)
		}
	}
}

func TestSQLWriterAllowsMaskedPhone(t *testing.T) {
	q := &captureQuerier{}
	err := (SQLWriter{}).Append(context.Background(), q, Event{ID: "evt_1", OccurredAt: time.Now(), RequestID: "req", ActorType: "system", Action: "user_created", ResourceType: "user", Result: "success", IP: "127.0.0.1", Changes: map[string]any{"phone_masked": "138****8000"}})
	if err != nil {
		t.Fatalf("脱敏手机号被拒绝: %v", err)
	}
}

func TestCursorBindsAllFilters(t *testing.T) {
	filter := Filter{ActorType: "user", Action: "role_created", Limit: 20}
	value, err := encodeCursor(cursor{OccurredAt: time.Now().UTC(), ID: "evt_1", Signature: filterSignature(filter)})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := decodeCursor(value)
	if err != nil {
		t.Fatal(err)
	}
	changed := filter
	changed.Action = "role_deleted"
	if decoded.Signature == filterSignature(changed) {
		t.Fatal("筛选条件变化后游标签名未变化")
	}
}

func TestAuditQueryDefaultsToDeny(t *testing.T) {
	if err := requireAuditView(Principal{}); err == nil {
		t.Fatal("无权限 Principal 被允许查询审计")
	}
	if err := requireAuditView(Principal{SystemPermissions: []string{"audit.view"}}); err != nil {
		t.Fatalf("audit.view 被拒绝: %v", err)
	}
}

func pointer(value string) *string { return &value }
