package audit

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cms/internal/platform/apperror"
	"cms/internal/platform/database"
)

type Filter struct {
	ActorType, ActorID, Action, ResourceType, ResourceID, Result string
	OccurredFrom, OccurredTo                                     *time.Time
	Limit                                                        int
	Cursor                                                       string
}
type List struct {
	Items      []Event `json:"items"`
	NextCursor *string `json:"next_cursor"`
}
type Reader struct {
	db database.Querier
}

type Principal struct {
	SystemPermissions []string
}

func NewReader(db database.Querier) *Reader {
	return &Reader{db: db}
}
func (r *Reader) Get(ctx context.Context, principal Principal, id string) (Event, error) {
	if err := requireAuditView(principal); err != nil {
		return Event{}, err
	}
	return readEvent(r.db.QueryRowContext(ctx, `SELECT id, occurred_at, request_id, actor_type, actor_id, actor_display_name, action, resource_type, resource_id, result, ip, user_agent, changes, failure_code FROM audit_events WHERE id=?`, id))
}
func (r *Reader) List(ctx context.Context, principal Principal, filter Filter) (List, error) {
	if err := requireAuditView(principal); err != nil {
		return List{}, err
	}
	where, args := []string{"1=1"}, []any{}
	values := []struct{ column, value string }{{"actor_type", filter.ActorType}, {"actor_id", filter.ActorID}, {"action", filter.Action}, {"resource_type", filter.ResourceType}, {"resource_id", filter.ResourceID}, {"result", filter.Result}}
	for _, value := range values {
		if value.value != "" {
			where = append(where, value.column+"=?")
			args = append(args, value.value)
		}
	}
	if filter.OccurredFrom != nil {
		where = append(where, "occurred_at>=?")
		args = append(args, filter.OccurredFrom.UTC())
	}
	if filter.OccurredTo != nil {
		where = append(where, "occurred_at<=?")
		args = append(args, filter.OccurredTo.UTC())
	}
	if filter.Cursor != "" {
		cursor, err := decodeCursor(filter.Cursor)
		if err != nil || cursor.Signature != filterSignature(filter) {
			return List{}, invalidCursor()
		}
		where = append(where, "(occurred_at < ? OR (occurred_at = ? AND id < ?))")
		args = append(args, cursor.OccurredAt, cursor.OccurredAt, cursor.ID)
	}
	args = append(args, filter.Limit+1)
	rows, err := r.db.QueryContext(ctx, `SELECT id, occurred_at, request_id, actor_type, actor_id, actor_display_name, action, resource_type, resource_id, result, ip, user_agent, changes, failure_code FROM audit_events WHERE `+strings.Join(where, " AND ")+` ORDER BY occurred_at DESC, id DESC LIMIT ?`, args...)
	if err != nil {
		return List{}, err
	}
	defer rows.Close()
	items := make([]Event, 0, filter.Limit+1)
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return List{}, err
		}
		items = append(items, event)
	}
	if err := rows.Err(); err != nil {
		return List{}, err
	}
	var next *string
	if len(items) > filter.Limit {
		items = items[:filter.Limit]
		last := items[len(items)-1]
		value, err := encodeCursor(cursor{OccurredAt: last.OccurredAt, ID: last.ID, Signature: filterSignature(filter)})
		if err != nil {
			return List{}, err
		}
		next = &value
	}
	return List{Items: items, NextCursor: next}, nil
}

func requireAuditView(principal Principal) error {
	for _, code := range principal.SystemPermissions {
		if code == "audit.view" {
			return nil
		}
	}
	return &apperror.Error{Kind: apperror.KindPermissionDenied, Code: "permission_denied", Message: "权限不足"}
}

type scanner interface{ Scan(...any) error }

func readEvent(row *sql.Row) (Event, error) {
	event, err := scanEvent(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Event{}, &apperror.Error{Kind: apperror.KindNotFound, Code: "not_found", Message: "审计事件不存在"}
	}
	return event, err
}
func scanEvent(row scanner) (Event, error) {
	var event Event
	var changes []byte
	if err := row.Scan(&event.ID, &event.OccurredAt, &event.RequestID, &event.ActorType, &event.ActorID, &event.ActorDisplayName, &event.Action, &event.ResourceType, &event.ResourceID, &event.Result, &event.IP, &event.UserAgent, &changes, &event.FailureCode); err != nil {
		return Event{}, err
	}
	if err := json.Unmarshal(changes, &event.Changes); err != nil {
		return Event{}, fmt.Errorf("解析审计变更摘要: %w", err)
	}
	return event, nil
}

type cursor struct {
	OccurredAt time.Time `json:"occurred_at"`
	ID         string    `json:"id"`
	Signature  string    `json:"signature"`
}

func encodeCursor(value cursor) (string, error) {
	raw, err := json.Marshal(value)
	return base64.RawURLEncoding.EncodeToString(raw), err
}
func decodeCursor(value string) (cursor, error) {
	var result cursor
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err == nil {
		err = json.Unmarshal(raw, &result)
	}
	if result.ID == "" || result.OccurredAt.IsZero() {
		err = fmt.Errorf("invalid cursor")
	}
	return result, err
}
func filterSignature(f Filter) string {
	from, to := "", ""
	if f.OccurredFrom != nil {
		from = f.OccurredFrom.UTC().Format(time.RFC3339Nano)
	}
	if f.OccurredTo != nil {
		to = f.OccurredTo.UTC().Format(time.RFC3339Nano)
	}
	values := []string{f.ActorType, f.ActorID, f.Action, f.ResourceType, f.ResourceID, f.Result, from, to}
	raw, _ := json.Marshal(values)
	return base64.RawURLEncoding.EncodeToString(raw)
}
func invalidCursor() error {
	return &apperror.Error{Kind: apperror.KindInvalidArgument, Code: "invalid_cursor", Message: "游标无效"}
}
