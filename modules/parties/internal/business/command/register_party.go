package command

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/parties/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

type RegisterPartyCommand struct {
	Type     domain.PartyType        `json:"type"` // EMPLOYEE | CUSTOMER
	FullName string                  `json:"full_name"`
	Email    string                  `json:"email"`
	Phone    string                  `json:"phone"`
	Identity domain.IdentityDocument `json:"identity"`
	Employee *domain.EmployeeProfile `json:"employee,omitempty"`
	Customer *domain.CustomerProfile `json:"customer,omitempty"`
}

type RegisterPartyHandler struct {
	repo domain.PartyRepository
}

func NewRegisterPartyHandler(repo domain.PartyRepository) *RegisterPartyHandler {
	return &RegisterPartyHandler{repo: repo}
}

// Handle thực thi đăng ký đối tác chuẩn (Standard Business)
// Chấp nhận cả CMND 9 số cũ lẫn CCCD 12 số mới
func (h *RegisterPartyHandler) Handle(ctx context.Context, cmd RegisterPartyCommand) (*domain.Party, error) {
	tenant, err := multitenant.MustFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if cmd.Type != domain.PartyTypeEmployee && cmd.Type != domain.PartyTypeCustomer {
		return nil, domain.ErrInvalidPartyType
	}

	cmd.Identity.Number = strings.TrimSpace(cmd.Identity.Number)
	if cmd.Identity.Number == "" {
		return nil, domain.ErrIdentityRequired
	}

	if cmd.Identity.Type != domain.IdentityTypeCMND && cmd.Identity.Type != domain.IdentityTypeCCCD {
		return nil, domain.ErrInvalidIdentityType
	}

	// Quy tắc chuẩn: Đánh dấu cơ bản (Basic Declared)
	cmd.Identity.VerificationStatus = "BASIC_DECLARED"

	party := &domain.Party{
		ID:          fmt.Sprintf("PRT-%d", time.Now().UnixNano()),
		TenantID:    tenant.TenantID,
		Type:        cmd.Type,
		FullName:    cmd.FullName,
		Email:       cmd.Email,
		Phone:       cmd.Phone,
		Identity:    cmd.Identity,
		Employee:    cmd.Employee,
		Customer:    cmd.Customer,
		ProcessedBy: "business:standard_register_party",
		CreatedAt:   time.Now(),
	}

	if err := h.repo.Save(ctx, party); err != nil {
		return nil, err
	}

	return party, nil
}
