package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidCredentials = errors.New("invalid username or password")
	ErrMFARequired        = errors.New("multi-factor authentication (MFA) required")
	ErrInvalidMFACode     = errors.New("invalid or expired MFA code")
	ErrTenantNotFound     = errors.New("tenant not found")
	ErrTenantExists       = errors.New("tenant id already registered")
)

type UserAccount struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Username  string    `json:"username"`
	Password  string    `json:"password"`
	Role      string    `json:"role"`
	MFACode   string    `json:"mfa_code,omitempty"` // Mã OTP demo (ví dụ: 654321)
	CreatedAt time.Time `json:"created_at"`
}

type TenantProvision struct {
	TenantID         string            `json:"tenant_id"`
	OrganizationName string            `json:"organization_name"`
	Plan             string            `json:"plan"` // standard, enterprise
	ConnectionString string            `json:"connection_string"`
	Features         map[string]bool   `json:"features"`
	Status           string            `json:"status"`
	ProvisionedAt    time.Time         `json:"provisioned_at"`
}

// AuthSession là phản hồi đăng nhập: JWT đã ký + thông tin user tối thiểu.
// Không chứa thông tin hạ tầng (DSN, luồng xử lý) — xem GET /api/v1/system/runtime-info.
type AuthSession struct {
	AccessToken string      `json:"access_token"`
	TokenType   string      `json:"token_type"` // Bearer
	ExpiresIn   int64       `json:"expires_in"` // Giây
	User        SessionUser `json:"user"`
}

type SessionUser struct {
	ID            string `json:"id"`
	Username      string `json:"username"`
	TenantID      string `json:"tenant_id"`
	Role          string `json:"role"`
	SecurityLevel string `json:"security_level"` // STANDARD | MFA_HIGH_ASSURANCE
}
