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
	PhoneMasked    *string      `json:"phone_masked"`
	AuthMethods    []AuthMethod `json:"auth_methods"`
	EmergencyAdmin bool         `json:"is_emergency_admin"`
	HighRiskRole   bool         `json:"has_high_risk_role"`
	Status         UserStatus   `json:"status"`
	CreatedAt      time.Time    `json:"created_at"`
	UpdatedAt      time.Time    `json:"updated_at"`
}

type User struct {
	UserSummary
	Phone   *string  `json:"phone"`
	RoleIDs []string `json:"role_ids"`
}

func (u User) VisibleTo(Principal) User {
	u.Phone = nil
	return u
}

type CreateUserRequest struct {
	DisplayName string   `json:"display_name"`
	Phone       string   `json:"phone"`
	RoleIDs     []string `json:"role_ids"`
}

type UpdatePhoneRequest struct {
	Phone string `json:"phone"`
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
