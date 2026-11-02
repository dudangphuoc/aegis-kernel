package domain

import (
	"errors"
	"time"
)

var (
	ErrInvalidPartyType         = errors.New("invalid party type: must be EMPLOYEE or CUSTOMER")
	ErrIdentityRequired         = errors.New("identity document is required")
	ErrInvalidIdentityType      = errors.New("identity type must be CMND or CCCD")
	ErrDuplicateIdentity        = errors.New("identity document number already registered in this tenant")
	ErrPartyNotFound            = errors.New("party not found")
	ErrEKYCRequiresCCCD12Digits = errors.New("eKYC policy violation: tenant requires 12-digit chip-based CCCD (legacy 9-digit CMND rejected)")
)

type PartyType string

const (
	PartyTypeEmployee PartyType = "EMPLOYEE"
	PartyTypeCustomer PartyType = "CUSTOMER"
)

type IdentityType string

const (
	IdentityTypeCMND IdentityType = "CMND" // 9 số cũ
	IdentityTypeCCCD IdentityType = "CCCD" // 12 số căn cước gắn chip
)

type IdentityDocument struct {
	Type               IdentityType `json:"type"`                // CMND | CCCD
	Number             string       `json:"number"`              // Số định danh
	IssuedDate         string       `json:"issued_date"`         // Ngày cấp
	IssuedPlace        string       `json:"issued_place"`        // Nơi cấp
	VerificationStatus string       `json:"verification_status"` // BASIC_DECLARED | EKYC_CHIP_VERIFIED
}

type EmployeeProfile struct {
	EmployeeCode string `json:"employee_code"`
	Department   string `json:"department"`
	Position     string `json:"position"`
}

type CustomerProfile struct {
	CustomerTier  string `json:"customer_tier"` // STANDARD, SILVER, GOLD, VIP
	LoyaltyPoints int    `json:"loyalty_points"`
}

type Party struct {
	ID          string           `json:"id"`
	TenantID    string           `json:"tenant_id"`
	Type        PartyType        `json:"type"` // EMPLOYEE | CUSTOMER
	FullName    string           `json:"full_name"`
	Email       string           `json:"email"`
	Phone       string           `json:"phone"`
	Identity    IdentityDocument `json:"identity"`
	Employee    *EmployeeProfile `json:"employee,omitempty"`
	Customer    *CustomerProfile `json:"customer,omitempty"`
	ProcessedBy string           `json:"-"` // Luồng xử lý (business/feature) — chỉ lưu DB, không trả về client
	CreatedAt   time.Time        `json:"created_at"`
}
