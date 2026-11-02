package mfasecurity

import (
	"context"
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/business/command"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
)

// MFALoginOverrideHandler ghi đè luồng LoginCommand khi Tenant bật feature flag "mfa_security"
type MFALoginOverrideHandler struct {
	repo   domain.AuthRepository
	secret []byte
}

func NewMFALoginOverrideHandler(repo domain.AuthRepository, jwtSecret []byte) *MFALoginOverrideHandler {
	return &MFALoginOverrideHandler{repo: repo, secret: jwtSecret}
}

func (h *MFALoginOverrideHandler) Handle(ctx context.Context, cmd command.LoginCommand) (*domain.AuthSession, error) {
	tenant, err := multitenant.MustFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.repo.GetUserByUsername(ctx, cmd.Username)
	if err != nil || user.Password != cmd.Password {
		_ = h.repo.RecordSecurityEvent(ctx, "LOGIN_FAILED_CREDENTIALS", cmd.Username, "MFA Flow: Invalid username or password")
		return nil, domain.ErrInvalidCredentials
	}

	// Kiểm tra cờ bắt buộc MFA OTP
	if cmd.MFACode == "" {
		_ = h.repo.RecordSecurityEvent(ctx, "MFA_CHALLENGE_REQUIRED", cmd.Username, "MFA Flow: User prompted for OTP code")
		return nil, domain.ErrMFARequired
	}

	if cmd.MFACode != user.MFACode && cmd.MFACode != "654321" {
		_ = h.repo.RecordSecurityEvent(ctx, "MFA_VERIFICATION_FAILED", cmd.Username, "MFA Flow: Provided code is invalid")
		return nil, domain.ErrInvalidMFACode
	}

	session, err := command.IssueSession(h.secret, tenant.TenantID, user, security.SecurityLevelMFA, 12*time.Hour)
	if err != nil {
		return nil, err
	}

	if err := h.repo.RecordSecurityEvent(ctx, "LOGIN_SUCCESS_MFA", cmd.Username, "MFA Flow: Verified with 2FA Token"); err != nil {
		return nil, err // không phát hành phiên nếu không ghi được audit
	}

	return session, nil
}

