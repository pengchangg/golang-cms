package schema

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

type FieldType string

const (
	FieldTypeSingleLineText  FieldType = "single_line_text"
	FieldTypeMultiLineText   FieldType = "multi_line_text"
	FieldTypeRichText        FieldType = "rich_text"
	FieldTypeInteger         FieldType = "integer"
	FieldTypeDecimal         FieldType = "decimal"
	FieldTypeBoolean         FieldType = "boolean"
	FieldTypeDate            FieldType = "date"
	FieldTypeDatetime        FieldType = "datetime"
	FieldTypeSingleSelect    FieldType = "single_select"
	FieldTypeMultiSelect     FieldType = "multi_select"
	FieldTypeJSON            FieldType = "json"
	FieldTypeSingleMedia     FieldType = "single_media"
	FieldTypeMultiMedia      FieldType = "multi_media"
	FieldTypeSingleRelation  FieldType = "single_relation"
	FieldTypeMultiRelation   FieldType = "multi_relation"
	FieldTypeObject          FieldType = "object"
	FieldTypeRepeatableGroup FieldType = "repeatable_group"
)

var fieldTypes = map[FieldType]struct{}{
	FieldTypeSingleLineText: {}, FieldTypeMultiLineText: {}, FieldTypeRichText: {},
	FieldTypeInteger: {}, FieldTypeDecimal: {}, FieldTypeBoolean: {}, FieldTypeDate: {},
	FieldTypeDatetime: {}, FieldTypeSingleSelect: {}, FieldTypeMultiSelect: {}, FieldTypeJSON: {},
	FieldTypeSingleMedia: {}, FieldTypeMultiMedia: {}, FieldTypeSingleRelation: {},
	FieldTypeMultiRelation: {}, FieldTypeObject: {}, FieldTypeRepeatableGroup: {},
}

type EnumOption struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

type FieldConstraints struct {
	MinLength     *int         `json:"min_length,omitempty"`
	MaxLength     *int         `json:"max_length,omitempty"`
	Minimum       *string      `json:"minimum,omitempty"`
	Maximum       *string      `json:"maximum,omitempty"`
	EnumOptions   []EnumOption `json:"enum_options,omitempty"`
	TargetModelID *string      `json:"target_model_id,omitempty"`
	Unique        bool         `json:"unique"`
	Filterable    bool         `json:"filterable"`
	Sortable      bool         `json:"sortable"`
}

type ContentFieldInput struct {
	Key          string              `json:"key"`
	DisplayName  string              `json:"display_name"`
	Description  string              `json:"description"`
	Type         FieldType           `json:"type"`
	Required     bool                `json:"required"`
	DefaultValue json.RawMessage     `json:"default_value"`
	Constraints  FieldConstraints    `json:"constraints"`
	Children     []ContentFieldInput `json:"children"`
}

func (f *ContentFieldInput) UnmarshalJSON(data []byte) error {
	type plain ContentFieldInput
	var value struct {
		plain
		DefaultValue json.RawMessage `json:"default_value"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(data, &properties); err != nil {
		return err
	}
	for _, name := range []string{"key", "display_name", "type", "description", "required", "constraints", "children"} {
		if raw, ok := properties[name]; ok && bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(name)}
		}
	}
	*f = ContentFieldInput(value.plain)
	if len(value.DefaultValue) == 0 {
		f.DefaultValue = json.RawMessage("null")
	} else {
		f.DefaultValue = value.DefaultValue
	}
	if f.Children == nil {
		f.Children = []ContentFieldInput{}
	}
	return nil
}

type ContentFieldPatch struct {
	DisplayName  *string
	Description  *string
	Type         *FieldType
	Required     *bool
	DefaultValue *json.RawMessage
	Constraints  *FieldConstraints
	Children     *[]ContentFieldInput
}

func (p *ContentFieldPatch) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	allowed := map[string]bool{"display_name": true, "description": true, "type": true, "required": true, "default_value": true, "constraints": true, "children": true}
	for key := range raw {
		if !allowed[key] {
			return &json.UnmarshalTypeError{Value: "unknown field " + key, Type: nil}
		}
	}
	if value, ok := raw["display_name"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf("")}
		}
		if err := json.Unmarshal(value, &p.DisplayName); err != nil {
			return err
		}
	}
	if value, ok := raw["description"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf("")}
		}
		if err := json.Unmarshal(value, &p.Description); err != nil {
			return err
		}
	}
	if value, ok := raw["type"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(FieldType(""))}
		}
		if err := json.Unmarshal(value, &p.Type); err != nil {
			return err
		}
	}
	if value, ok := raw["required"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(false)}
		}
		if err := json.Unmarshal(value, &p.Required); err != nil {
			return err
		}
	}
	if value, ok := raw["default_value"]; ok {
		copy := append(json.RawMessage(nil), value...)
		p.DefaultValue = &copy
	}
	if value, ok := raw["constraints"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf(FieldConstraints{})}
		}
		var v FieldConstraints
		if err := decodeStrict(value, &v); err != nil {
			return err
		}
		p.Constraints = &v
	}
	if value, ok := raw["children"]; ok {
		if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf([]ContentFieldInput{})}
		}
		var v []ContentFieldInput
		if err := json.Unmarshal(value, &v); err != nil {
			return err
		}
		p.Children = &v
	}
	return nil
}

func (p ContentFieldPatch) Empty() bool {
	return p.DisplayName == nil && p.Description == nil && p.Type == nil && p.Required == nil && p.DefaultValue == nil && p.Constraints == nil && p.Children == nil
}

type ContentField struct {
	ID           string           `json:"id"`
	Key          string           `json:"key"`
	DisplayName  string           `json:"display_name"`
	Description  string           `json:"description"`
	Type         FieldType        `json:"type"`
	Required     bool             `json:"required"`
	DefaultValue json.RawMessage  `json:"default_value"`
	Constraints  FieldConstraints `json:"constraints"`
	Children     []ContentField   `json:"children"`
	Status       ResourceStatus   `json:"status"`
	CreatedAt    time.Time        `json:"created_at"`
	UpdatedAt    time.Time        `json:"updated_at"`
	Depth        int              `json:"-"`
}

type CreateContentModelRequest struct {
	Key         string `json:"key"`
	DisplayName string `json:"display_name"`
	Description string `json:"description"`
}

type UpdateContentModelRequest struct {
	DisplayName *string `json:"display_name,omitempty"`
	Description *string `json:"description,omitempty"`
}

func (r *UpdateContentModelRequest) UnmarshalJSON(data []byte) error {
	type plain UpdateContentModelRequest
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(data, &properties); err != nil {
		return err
	}
	for key, raw := range properties {
		if key != "display_name" && key != "description" {
			return &json.UnmarshalTypeError{Value: "unknown field " + key, Type: reflect.TypeOf(plain{})}
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return &json.UnmarshalTypeError{Value: "null", Type: reflect.TypeOf("")}
		}
	}
	var value plain
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	*r = UpdateContentModelRequest(value)
	return nil
}

func (r UpdateContentModelRequest) Empty() bool { return r.DisplayName == nil && r.Description == nil }

type ContentModelSummary struct {
	ID          string         `json:"id"`
	Key         string         `json:"key"`
	DisplayName string         `json:"display_name"`
	Description string         `json:"description"`
	Status      ResourceStatus `json:"status"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
}

type ContentModel struct {
	ContentModelSummary
	Fields []ContentField `json:"fields"`
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}
