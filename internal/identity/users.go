package identity

import "time"

type UserStatus string

const (
	UserEnabled  UserStatus = "enabled"
	UserDisabled UserStatus = "disabled"
)

type UserSummary struct {
	ID             string       `json:"id"`
	DisplayName    string       `json:"display_name"`
	Email          *string      `json:"email"`
	AuthMethods    []AuthMethod `json:"auth_methods"`
	EmergencyAdmin bool         `json:"is_emergency_admin"`
	Status         UserStatus   `json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type User struct {
	UserSummary
	RoleIDs []string `json:"role_ids"`
}

type UserFilter struct {
	Status     *UserStatus
	AuthMethod *AuthMethod
	Query      string
	Limit      int
	Cursor     string
}

type UserList struct {
	Items      []UserSummary `json:"items"`
	NextCursor *string       `json:"next_cursor"`
}

type RequestMeta struct{ RequestID, IP, UserAgent string }
