package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"cms/internal/platform/database"
)

type Event struct {
	ID               string         `json:"id"`
	OccurredAt       time.Time      `json:"occurred_at"`
	RequestID        string         `json:"request_id"`
	ActorType        string         `json:"actor_type"`
	ActorID          *string        `json:"actor_id"`
	ActorDisplayName *string        `json:"actor_display_name"`
	Action           string         `json:"action"`
	ResourceType     string         `json:"resource_type"`
	ResourceID       *string        `json:"resource_id"`
	Result           string         `json:"result"`
	IP               string         `json:"ip"`
	UserAgent        string         `json:"user_agent"`
	Changes          map[string]any `json:"changes"`
	FailureCode      *string        `json:"failure_code"`
}

type Writer interface {
	Append(context.Context, database.Querier, Event) error
}

type SQLWriter struct{}

var auditCode = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func (SQLWriter) Append(ctx context.Context, q database.Querier, event Event) error {
	if q == nil {
		return errors.New("审计写入器缺少数据库执行器")
	}
	if err := validate(event); err != nil {
		return err
	}
	changes := event.Changes
	if changes == nil {
		changes = map[string]any{}
	}
	encoded, err := json.Marshal(changes)
	if err != nil {
		return fmt.Errorf("编码审计变更摘要: %w", err)
	}
	_, err = q.ExecContext(ctx, `INSERT INTO audit_events
		(id, occurred_at, request_id, actor_type, actor_id, actor_display_name, action, resource_type, resource_id, result, ip, user_agent, changes, failure_code)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, event.ID, event.OccurredAt.UTC(), event.RequestID,
		event.ActorType, event.ActorID, event.ActorDisplayName, event.Action, event.ResourceType, event.ResourceID, event.Result,
		event.IP, event.UserAgent, encoded, event.FailureCode)
	if err != nil {
		return fmt.Errorf("追加审计事件: %w", err)
	}
	return nil
}

func validate(event Event) error {
	if event.ID == "" || event.OccurredAt.IsZero() || event.RequestID == "" || event.IP == "" {
		return errors.New("审计事件缺少必填字段")
	}
	if event.ActorType != "user" && event.ActorType != "system" {
		return errors.New("审计操作者类型不合法")
	}
	if event.ActorType == "user" && (event.ActorID == nil || *event.ActorID == "") {
		return errors.New("用户审计事件缺少操作者 ID")
	}
	if event.ActorType == "user" && (event.ActorDisplayName == nil || strings.TrimSpace(*event.ActorDisplayName) == "") {
		return errors.New("用户审计事件缺少操作者名称")
	}
	if event.ActorType == "system" && event.ActorDisplayName != nil {
		return errors.New("系统审计事件不能包含操作者名称")
	}
	if !auditCode.MatchString(event.Action) || !auditCode.MatchString(event.ResourceType) {
		return errors.New("审计动作或资源类型不合法")
	}
	if event.Result == "success" && event.FailureCode != nil {
		return errors.New("成功审计事件不能包含失败码")
	}
	if event.Result == "failure" && (event.FailureCode == nil || !auditCode.MatchString(*event.FailureCode)) {
		return errors.New("失败审计事件缺少合法失败码")
	}
	if event.Result != "success" && event.Result != "failure" {
		return errors.New("审计结果不合法")
	}
	if containsSensitiveKey(event.Changes) {
		return errors.New("审计变更摘要包含敏感字段")
	}
	return nil
}

func containsSensitiveKey(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.ReplaceAll(key, "-", "_"))
			for _, sensitive := range []string{"password", "secret", "token", "session", "pkce", "nonce", "authorization", "credential", "signed_url", "otp", "captcha", "slider"} {
				if strings.Contains(normalized, sensitive) {
					return true
				}
			}
			if strings.Contains(normalized, "phone") && !strings.Contains(normalized, "masked") {
				return true
			}
			if containsSensitiveKey(child) {
				return true
			}
		}
	case []any:
		for _, child := range typed {
			if containsSensitiveKey(child) {
				return true
			}
		}
	}
	return false
}
