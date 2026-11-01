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

type AuthSession struct {
	Token          string    `json:"token"`
	TenantID       string    `json:"tenant_id"`
	Username       string    `json:"username"`
	Role           string    `json:"role"`
	SecurityLevel  string    `json:"security_level"`  // STANDARD | MFA_HIGH_ASSURANCE
	AuthFlowUsed   string    `json:"auth_flow_used"`  // business:standard_login | feature:mfa_security (override)
	DatabaseSource string    `json:"database_source"` // DB URL chứng minh tính cô lập
	ExpiresAt      time.Time `json:"expires_at"`
}

type SecurityAuditLog struct {
	ID        string    `json:"id"`
	TenantID  string    `json:"tenant_id"`
	Username  string    `json:"username"`
	Action    string    `json:"action"`
	Details   string    `json:"details"`
	Timestamp time.Time `json:"timestamp"`
}

