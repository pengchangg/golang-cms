package configuration

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"
)

type ResourceStatus string

const (
	StatusActive   ResourceStatus = "active"
	StatusArchived ResourceStatus = "archived"
)

type ValueType string

const (
	TypeString         ValueType = "string"
	TypeInteger        ValueType = "integer"
	TypeDecimal        ValueType = "decimal"
	TypeBoolean        ValueType = "boolean"
	TypeJSON           ValueType = "json"
	TypeSingleAsset    ValueType = "single_asset"
	TypeMultiAsset     ValueType = "multi_asset"
	TypeSingleRelation ValueType = "single_relation"
	TypeMultiRelation  ValueType = "multi_relation"
)

type WorkflowStatus string

const (
	WorkflowDraft         WorkflowStatus = "draft"
	WorkflowPendingReview WorkflowStatus = "pending_review"
	WorkflowRejected      WorkflowStatus = "rejected"
	WorkflowPublished     WorkflowStatus = "published"
	WorkflowUnpublished   WorkflowStatus = "unpublished"
)

type Constraints struct {
	Required         bool     `json:"required"`
	MinLength        *int     `json:"min_length,omitempty"`
	MaxLength        *int     `json:"max_length,omitempty"`
	Pattern          *string  `json:"pattern,omitempty"`
	StringEnum       []string `json:"string_enum,omitempty"`
	Minimum          *string  `json:"minimum,omitempty"`
	Maximum          *string  `json:"maximum,omitempty"`
	IntegerEnum      []string `json:"integer_enum,omitempty"`
	Scale            *int     `json:"scale,omitempty"`
	DecimalEnum      []string `json:"decimal_enum,omitempty"`
	MaxBytes         *int     `json:"max_bytes,omitempty"`
	AllowedMimeTypes []string `json:"allowed_mime_types,omitempty"`
	MaxSize          *int64   `json:"max_size,omitempty"`
	TargetModelID    *string  `json:"target_model_id,omitempty"`
}

func (c *Constraints) UnmarshalJSON(data []byte) error {
	if bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(Constraints{})}
	}
	type constraints Constraints
	var value constraints
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*c = Constraints(value)
	return nil
}

type Namespace struct {
	ID          string         `json:"id"`
	Key         string         `json:"namespace_key"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Status      ResourceStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type Item struct {
	ID          string         `json:"id"`
	NamespaceID string         `json:"namespace_id"`
	Key         string         `json:"item_key"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	ValueType   ValueType      `json:"value_type"`
	Constraints Constraints    `json:"constraints"`
	Status      ResourceStatus `json:"status"`
	CreatedBy   string         `json:"created_by"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type Revision struct {
	ID             string          `json:"id"`
	ItemID         string          `json:"item_id"`
	NamespaceID    string          `json:"namespace_id"`
	Number         uint            `json:"revision_number"`
	ValueType      ValueType       `json:"value_type"`
	Constraints    Constraints     `json:"constraints"`
	Value          json.RawMessage `json:"value"`
	WorkflowStatus WorkflowStatus  `json:"workflow_status"`
	CreatedBy      string          `json:"created_by"`
	SubmittedBy    *string         `json:"submitted_by"`
	SubmittedAt    *time.Time      `json:"submitted_at"`
	CreatedAt      time.Time       `json:"created_at"`
}

type RevisionList struct {
	Items      []Revision `json:"items"`
	NextCursor *string    `json:"next_cursor"`
}

type ItemValue struct {
	Item                       Item      `json:"item"`
	CurrentDraftRevision       Revision  `json:"current_draft_revision"`
	CurrentPublishedRevisionID *string   `json:"current_published_revision_id"`
	CurrentPublishedRevision   *Revision `json:"current_published_revision,omitempty"`
}

type WorkflowEvent struct {
	ID          string         `json:"id"`
	ItemID      string         `json:"item_id"`
	NamespaceID string         `json:"namespace_id"`
	RevisionID  string         `json:"revision_id"`
	Type        string         `json:"type"`
	FromStatus  WorkflowStatus `json:"from_status"`
	ToStatus    WorkflowStatus `json:"to_status"`
	ActorID     string         `json:"actor_id"`
	Reason      *string        `json:"reason"`
	OccurredAt  time.Time      `json:"occurred_at"`
}

type WorkflowEventList struct {
	Items      []WorkflowEvent `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

type WorkflowEventCursor struct {
	OccurredAt time.Time
	ID         string
}

type AssetReference struct {
	RevisionID, ItemID, NamespaceID, AssetID string
	Position                                 int
}

type Relation struct {
	RevisionID, ItemID, NamespaceID, TargetEntryID, TargetModelID string
	Position                                                      int
}

type CreateNamespaceRequest struct {
	Key         string `json:"namespace_key"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type NamespacePatch struct {
	DisplayName *string
	Description *string
}

func (p *NamespacePatch) UnmarshalJSON(data []byte) error {
	return decodePatch(data, map[string]func(json.RawMessage) error{
		"display_name": func(raw json.RawMessage) error { return decodeNonNull(raw, &p.DisplayName, "") },
		"description":  func(raw json.RawMessage) error { return decodeNonNull(raw, &p.Description, "") },
	})
}

type CreateItemRequest struct {
	Key         string      `json:"item_key"`
	DisplayName string      `json:"display_name"`
	Description string      `json:"description"`
	ValueType   ValueType   `json:"value_type"`
	Constraints Constraints `json:"constraints"`
}

func (r *CreateItemRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key         string          `json:"item_key"`
		DisplayName string          `json:"display_name"`
		Description string          `json:"description"`
		ValueType   ValueType       `json:"value_type"`
		Constraints json.RawMessage `json:"constraints"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	*r = CreateItemRequest{Key: raw.Key, DisplayName: raw.DisplayName, Description: raw.Description, ValueType: raw.ValueType}
	if len(raw.Constraints) == 0 {
		return nil
	}
	var constraints Constraints
	decoder = json.NewDecoder(bytes.NewReader(raw.Constraints))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&constraints); err != nil {
		return err
	}
	r.Constraints = constraints
	return nil
}

type ItemPatch struct {
	DisplayName *string
	Description *string
	Constraints *Constraints
}

func (p *ItemPatch) UnmarshalJSON(data []byte) error {
	return decodePatch(data, map[string]func(json.RawMessage) error{
		"display_name": func(raw json.RawMessage) error { return decodeNonNull(raw, &p.DisplayName, "") },
		"description":  func(raw json.RawMessage) error { return decodeNonNull(raw, &p.Description, "") },
		"constraints":  func(raw json.RawMessage) error { return decodeNonNullStrict(raw, &p.Constraints, Constraints{}) },
	})
}

type CreateDraftRequest struct {
	Value json.RawMessage `json:"value"`
}

type UpdateDraftRequest struct {
	BaseRevisionID string          `json:"base_revision_id"`
	Value          json.RawMessage `json:"value"`
}

type RevisionConditionRequest struct {
	RevisionID string `json:"revision_id"`
}

type RejectRevisionRequest struct {
	RevisionID string `json:"revision_id"`
	Reason     string `json:"reason"`
}

func decodePatch(data []byte, fields map[string]func(json.RawMessage) error) error {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	for key, value := range raw {
		decode, ok := fields[key]
		if !ok {
			return &json.UnmarshalTypeError{Value: "unknown field " + key}
		}
		if err := decode(value); err != nil {
			return err
		}
	}
	return nil
}

func decodeNonNull[T any](raw json.RawMessage, destination **T, example T) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(example)}
	}
	var value T
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*destination = &value
	return nil
}

func decodeNonNullStrict[T any](raw json.RawMessage, destination **T, example T) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(example)}
	}
	var value T
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	*destination = &value
	return nil
}

type PublishedAsset struct {
	ID        string `json:"id"`
	ObjectKey string `json:"object_key"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256"`
	ETag      string `json:"etag"`
}

type PublishedRelation struct {
	ID               string          `json:"id"`
	ModelID          string          `json:"model_id"`
	ModelKey         string          `json:"model_key"`
	RevisionID       string          `json:"revision_id"`
	RevisionNumber   uint            `json:"revision_number"`
	Content          json.RawMessage `json:"content"`
	ReferencedAssets map[string]any  `json:"referenced_assets"`
	PublishedAt      time.Time       `json:"published_at"`
	UpdatedAt        time.Time       `json:"updated_at"`
}

type PublishedItem struct {
	Key            string    `json:"item_key"`
	ValueType      ValueType `json:"value_type"`
	Value          any       `json:"value"`
	RevisionID     string    `json:"revision_id"`
	RevisionNumber uint      `json:"revision_number"`
	PublishedAt    time.Time `json:"published_at"`
}

type PublishedNamespace map[string]any

type RequestMeta struct{ RequestID, IP, UserAgent string }
