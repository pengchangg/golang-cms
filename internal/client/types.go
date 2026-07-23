package client

import (
	"encoding/json"
	"time"
)

type APIKeyStatus string

const (
	APIKeyActive  APIKeyStatus = "active"
	APIKeyExpired APIKeyStatus = "expired"
	APIKeyRevoked APIKeyStatus = "revoked"
)

type APIKey struct {
	ID                 string       `json:"id"`
	Name               string       `json:"name"`
	Prefix             string       `json:"prefix"`
	ModelIDs           []string     `json:"model_ids"`
	ConfigNamespaceIDs []string     `json:"config_namespace_ids"`
	Status             APIKeyStatus `json:"status"`
	ExpiresAt          *time.Time   `json:"expires_at"`
	RevokedAt          *time.Time   `json:"revoked_at"`
	LastUsedAt         *time.Time   `json:"last_used_at"`
	RotatedFromID      *string      `json:"rotated_from_id"`
	ReplacedByID       *string      `json:"replaced_by_id"`
	CreatedBy          string       `json:"created_by"`
	CreatedAt          time.Time    `json:"created_at"`
	Salt               []byte       `json:"-"`
	Hash               []byte       `json:"-"`
}

type APIKeySecret struct {
	APIKey
	Key string `json:"key"`
}

type APIKeyList struct {
	Items      []APIKey `json:"items"`
	NextCursor *string  `json:"next_cursor"`
}

type CreateAPIKeyRequest struct {
	Name               string     `json:"name"`
	ModelIDs           []string   `json:"model_ids"`
	ConfigNamespaceIDs []string   `json:"config_namespace_ids"`
	ExpiresAt          *time.Time `json:"expires_at"`
	expiresAtSet       bool
}

func (r *CreateAPIKeyRequest) UnmarshalJSON(data []byte) error {
	type request CreateAPIKeyRequest
	var properties map[string]json.RawMessage
	if err := json.Unmarshal(data, &properties); err != nil {
		return err
	}
	for key := range properties {
		if key != "name" && key != "model_ids" && key != "config_namespace_ids" && key != "expires_at" {
			return &json.UnmarshalTypeError{Value: "unknown field " + key}
		}
	}
	var decoded request
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	*r = CreateAPIKeyRequest(decoded)
	_, r.expiresAtSet = properties["expires_at"]
	return nil
}

type optionalTime struct {
	Set   bool
	Value *time.Time
}

func (o *optionalTime) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Value = nil
		return nil
	}
	var value time.Time
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	o.Value = &value
	return nil
}

type RotateAPIKeyRequest struct {
	Name               *string      `json:"name"`
	ModelIDs           *[]string    `json:"model_ids"`
	ConfigNamespaceIDs *[]string    `json:"config_namespace_ids"`
	ExpiresAt          optionalTime `json:"expires_at"`
}

type RequestMeta struct{ RequestID, IP, UserAgent string }

type AuthenticatedKey struct {
	ID                 string
	Prefix             string
	ModelIDs           []string
	ConfigNamespaceIDs []string
	ShouldTouchLastUse bool
}
