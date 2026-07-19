package content

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"time"

	"cms/internal/schema"
)

type EntryStatus string

const (
	StatusDraft    EntryStatus = "draft"
	StatusArchived EntryStatus = "archived"
)

type Revision struct {
	ID             string          `json:"id"`
	EntryID        string          `json:"entry_id"`
	ModelID        string          `json:"model_id"`
	Number         uint            `json:"number"`
	Content        json.RawMessage `json:"content"`
	WorkflowStatus WorkflowStatus  `json:"workflow_status"`
	CreatedBy      string          `json:"created_by"`
	SubmittedBy    *string         `json:"submitted_by"`
	SubmittedAt    *time.Time      `json:"submitted_at"`
	CreatedAt      time.Time       `json:"created_at"`
}

type WorkflowStatus string

const (
	WorkflowDraft         WorkflowStatus = "draft"
	WorkflowPendingReview WorkflowStatus = "pending_review"
	WorkflowRejected      WorkflowStatus = "rejected"
	WorkflowPublished     WorkflowStatus = "published"
	WorkflowUnpublished   WorkflowStatus = "unpublished"
)

type WorkflowEvent struct {
	ID         string         `json:"id"`
	EntryID    string         `json:"entry_id"`
	RevisionID string         `json:"revision_id"`
	Type       string         `json:"type"`
	FromStatus WorkflowStatus `json:"from_status"`
	ToStatus   WorkflowStatus `json:"to_status"`
	ActorID    string         `json:"actor_id"`
	Reason     *string        `json:"reason"`
	OccurredAt time.Time      `json:"occurred_at"`
}

type WorkflowEventList struct {
	Items      []WorkflowEvent `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

type EntrySummary struct {
	ID                         string         `json:"id"`
	ModelID                    string         `json:"model_id"`
	Status                     EntryStatus    `json:"status"`
	CurrentDraftRevisionID     string         `json:"current_draft_revision_id"`
	WorkflowStatus             WorkflowStatus `json:"workflow_status"`
	CurrentPublishedRevisionID *string        `json:"current_published_revision_id"`
	CreatedBy                  string         `json:"created_by"`
	CreatedAt                  time.Time      `json:"created_at"`
	UpdatedAt                  time.Time      `json:"updated_at"`
	Expanded                   map[string]any `json:"expanded,omitempty"`
}

type Entry struct {
	EntrySummary
	CurrentDraftRevision     Revision  `json:"current_draft_revision"`
	CurrentPublishedRevision *Revision `json:"current_published_revision"`
}

type RevisionConditionRequest struct {
	RevisionID string `json:"revision_id"`
}
type RejectRevisionRequest struct {
	RevisionID string `json:"revision_id"`
	Reason     string `json:"reason"`
}

type FieldValue struct {
	RevisionID, EntryID, ModelID, FieldID, ValueType string
	StringValue                                      *string
	IntegerValue                                     *int64
	DecimalValue                                     *string
	BooleanValue                                     *bool
	DateValue                                        *time.Time
	DatetimeValue                                    *time.Time
}

type Relation struct {
	RevisionID, EntryID, ModelID, FieldID, TargetEntryID, TargetModelID string
	Position                                                            int
}

type PublishedModel struct {
	ID          string           `json:"id"`
	Key         string           `json:"key"`
	DisplayName string           `json:"display_name"`
	Description string           `json:"description"`
	UpdatedAt   time.Time        `json:"updated_at"`
	Fields      []PublishedField `json:"fields"`
}

type PublishedFieldConstraints struct {
	MinLength      *int                `json:"min_length,omitempty"`
	MaxLength      *int                `json:"max_length,omitempty"`
	Minimum        *string             `json:"minimum,omitempty"`
	Maximum        *string             `json:"maximum,omitempty"`
	EnumOptions    []schema.EnumOption `json:"enum_options,omitempty"`
	TargetModelKey *string             `json:"target_model_key,omitempty"`
	Unique         bool                `json:"unique"`
	Filterable     bool                `json:"filterable"`
	Sortable       bool                `json:"sortable"`
}
type PublishedField struct {
	ID          string                    `json:"id"`
	Key         string                    `json:"key"`
	DisplayName string                    `json:"display_name"`
	Description string                    `json:"description"`
	Type        schema.FieldType          `json:"type"`
	Required    bool                      `json:"required"`
	Constraints PublishedFieldConstraints `json:"constraints"`
	Children    []PublishedField          `json:"children"`
}

type PublishedEntry struct {
	ID             string          `json:"id"`
	ModelID        string          `json:"model_id"`
	ModelKey       string          `json:"model_key"`
	RevisionID     string          `json:"revision_id"`
	RevisionNumber uint            `json:"revision_number"`
	Content        json.RawMessage `json:"content"`
	Expanded       map[string]any  `json:"expanded"`
	PublishedAt    time.Time       `json:"published_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

type ExpandedEntry struct {
	ID          string          `json:"id"`
	ModelID     string          `json:"model_id"`
	ModelKey    string          `json:"model_key"`
	RevisionID  string          `json:"revision_id"`
	Content     json.RawMessage `json:"content"`
	PublishedAt time.Time       `json:"published_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type PublishedFilter struct {
	FieldKey, Operator string
	Value              json.RawMessage
}
type PublishedRelationFilter struct{ FieldKey, EntryID string }
type PublishedSort struct {
	FieldKey   string
	Descending bool
}
type PublishedQuery struct {
	Limit           int
	Cursor          string
	Filters         []PublishedFilter
	RelationFilters []PublishedRelationFilter
	Sort            []PublishedSort
	Expand          []string
}
type PublishedEntryPage struct {
	Items      []PublishedEntry `json:"items"`
	NextCursor *string          `json:"next_cursor"`
}

type PublishedContentReader interface {
	ListPublishedModels(context.Context, []string) ([]PublishedModel, error)
	GetPublishedModel(context.Context, string, []string) (PublishedModel, error)
	ListPublishedEntries(context.Context, string, []string, PublishedQuery) (PublishedEntryPage, error)
	GetPublishedEntry(context.Context, string, string, []string, []string) (PublishedEntry, error)
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
	Items           []EntrySummary `json:"items"`
	NextCursor      *string        `json:"next_cursor"`
	Total           *int           `json:"total,omitempty"`
	TotalIsEstimate *bool          `json:"total_is_estimate,omitempty"`
}

type AdminEntryQuery struct {
	Status          EntryStatus
	WorkflowStatus  *WorkflowStatus
	Limit           int
	Cursor          string
	Filters         []PublishedFilter
	RelationFilters []PublishedRelationFilter
	Sort            []PublishedSort
	Expand          []string
	IncludeTotal    bool
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
