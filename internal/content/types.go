package content

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"
)

type EntryStatus string

const (
	StatusDraft    EntryStatus = "draft"
	StatusArchived EntryStatus = "archived"
)

type Revision struct {
	ID        string          `json:"id"`
	EntryID   string          `json:"entry_id"`
	ModelID   string          `json:"model_id"`
	Number    uint            `json:"number"`
	Content   json.RawMessage `json:"content"`
	CreatedBy string          `json:"created_by"`
	CreatedAt time.Time       `json:"created_at"`
}

type EntrySummary struct {
	ID                     string      `json:"id"`
	ModelID                string      `json:"model_id"`
	Status                 EntryStatus `json:"status"`
	CurrentDraftRevisionID string      `json:"current_draft_revision_id"`
	CreatedBy              string      `json:"created_by"`
	CreatedAt              time.Time   `json:"created_at"`
	UpdatedAt              time.Time   `json:"updated_at"`
}

type Entry struct {
	EntrySummary
	CurrentDraftRevision Revision `json:"current_draft_revision"`
}

type CreateEntryRequest struct {
	Content json.RawMessage `json:"content"`
}

func (r *CreateEntryRequest) UnmarshalJSON(data []byte) error {
	return decodeContentRequest(data, "", &r.Content)
}

type UpdateEntryRequest struct {
	BaseRevisionID string          `json:"base_revision_id"`
	Content        json.RawMessage `json:"content"`
}

func (r *UpdateEntryRequest) UnmarshalJSON(data []byte) error {
	return decodeContentRequest(data, "base_revision_id", &r.Content, &r.BaseRevisionID)
}

type EntryList struct {
	Items      []EntrySummary `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

type RevisionList struct {
	Items      []Revision `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

func decodeContentRequest(data []byte, stringField string, content *json.RawMessage, destination ...*string) error {
	var properties map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&properties); err != nil {
		return err
	}
	allowed := map[string]bool{"content": true}
	if stringField != "" {
		allowed[stringField] = true
	}
	for key := range properties {
		if !allowed[key] {
			return &json.UnmarshalTypeError{Value: "unknown field " + key, Type: reflect.TypeOf(properties)}
		}
	}
	if raw, ok := properties["content"]; ok {
		*content = append((*content)[:0], raw...)
	}
	if stringField != "" {
		if raw, ok := properties[stringField]; ok {
			if err := json.Unmarshal(raw, destination[0]); err != nil {
				return err
			}
		}
	}
	return nil
}
