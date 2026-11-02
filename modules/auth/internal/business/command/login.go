package command

import (
	"context"
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
)

type LoginCommand struct {
	Username string `json:"username"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

type LoginHandler struct {
	repo   domain.AuthRepository
	secret []byte
}

func NewLoginHandler(repo domain.AuthRepository, jwtSecret []byte) *LoginHandler {
	return &LoginHandler{repo: repo, secret: jwtSecret}
}

// Handle thực thi đăng nhập chuẩn (Standard Business) - chỉ cần username/password
func (h *LoginHandler) Handle(ctx context.Context, cmd LoginCommand) (*domain.AuthSession, error) {
	tenant, err := multitenant.MustFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.repo.GetUserByUsername(ctx, cmd.Username)
	if err != nil || user.Password != cmd.Password {
		_ = h.repo.RecordSecurityEvent(ctx, "LOGIN_FAILED", cmd.Username, "Invalid username or password (Standard Flow)")
		return nil, domain.ErrInvalidCredentials
	}

	session, err := IssueSession(h.secret, tenant.TenantID, user, security.SecurityLevelStandard, 24*time.Hour)
	if err != nil {
		return nil, err
	}

	if err := h.repo.RecordSecurityEvent(ctx, "LOGIN_SUCCESS", cmd.Username, "Logged in with Standard security"); err != nil {
		return nil, err // không phát hành phiên nếu không ghi được audit
	}

	return session, nil
}

