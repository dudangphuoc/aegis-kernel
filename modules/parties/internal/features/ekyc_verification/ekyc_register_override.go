package ekycverification

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/business/command"
	"github.com/dudangphuoc/AegisKernel/modules/parties/internal/domain"
	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

// EKYCRegisterOverrideHandler ghi đè luồng RegisterPartyCommand khi Tenant bật cờ "ekyc_verification"
// Quy tắc chặt chẽ: Chỉ chấp nhận CCCD gắn chip đúng 12 chữ số, từ chối CMND 9 số cũ
type EKYCRegisterOverrideHandler struct {
	repo domain.PartyRepository
}

func NewEKYCRegisterOverrideHandler(repo domain.PartyRepository) *EKYCRegisterOverrideHandler {
	return &EKYCRegisterOverrideHandler{repo: repo}
}

func (h *EKYCRegisterOverrideHandler) Handle(ctx context.Context, cmd command.RegisterPartyCommand) (*domain.Party, error) {
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

	// 🔒 eKYC Enforcement Rule: Bắt buộc CCCD 12 số
	if cmd.Identity.Type != domain.IdentityTypeCCCD || len(cmd.Identity.Number) != 12 {
		return nil, domain.ErrEKYCRequiresCCCD12Digits
	}

	// Kiểm tra xem số CCCD có toàn ký tự số hay không
	for _, ch := range cmd.Identity.Number {
		if ch < '0' || ch > '9' {
			return nil, domain.ErrEKYCRequiresCCCD12Digits
		}
	}

	// Đóng dấu xác thực eKYC Chip thành công
	cmd.Identity.VerificationStatus = "EKYC_CHIP_VERIFIED"

	party := &domain.Party{
		ID:             fmt.Sprintf("PRT-EKYC-%d", time.Now().UnixNano()),
		TenantID:       tenant.TenantID,
		Type:           cmd.Type,
		FullName:       cmd.FullName,
		Email:          cmd.Email,
		Phone:          cmd.Phone,
		Identity:       cmd.Identity,
		Employee:       cmd.Employee,
		Customer:       cmd.Customer,
		ProcessedBy:    "feature:ekyc_verification (override)",
		DatabaseSource: tenant.ConnectionString,
		CreatedAt:      time.Now(),
	}

	if err := h.repo.Save(ctx, party); err != nil {
		return nil, err
	}

	return party, nil
}

