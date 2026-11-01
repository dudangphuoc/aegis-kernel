package command

import (
	"context"
	"fmt"
	"time"

	"github.com/dudangphuoc/AegisKernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

type LoginCommand struct {
	Username string `json:"username"`
	Password string `json:"password"`
	MFACode  string `json:"mfa_code,omitempty"`
}

type LoginHandler struct {
	repo domain.AuthRepository
}

func NewLoginHandler(repo domain.AuthRepository) *LoginHandler {
	return &LoginHandler{repo: repo}
}

// Handle thực thi đăng nhập chuẩn (Standard Business) - chỉ cần username/password
func (h *LoginHandler) Handle(ctx context.Context, cmd LoginCommand) (*domain.AuthSession, error) {
	tenant, err := multitenant.MustFromContext(ctx)
	if err != nil {
		return nil, err
	}

	user, err := h.repo.GetUserByUsername(ctx, cmd.Username)
	if err != nil || user.Password != cmd.Password {
		_ = h.repo.RecordAuditLog(ctx, &domain.SecurityAuditLog{
			ID:        fmt.Sprintf("LOG-%d", time.Now().UnixNano()),
			Username:  cmd.Username,
			Action:    "LOGIN_FAILED",
			Details:   "Invalid username or password (Standard Flow)",
			Timestamp: time.Now(),
		})
		return nil, domain.ErrInvalidCredentials
	}

	session := &domain.AuthSession{
		Token:          fmt.Sprintf("std-token-%s-%s-%d", tenant.TenantID, user.Username, time.Now().Unix()),
		TenantID:       tenant.TenantID,
		Username:       user.Username,
		Role:           user.Role,
		SecurityLevel:  "STANDARD",
		AuthFlowUsed:   "business:standard_login",
		DatabaseSource: tenant.ConnectionString,
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}

	_ = h.repo.RecordAuditLog(ctx, &domain.SecurityAuditLog{
		ID:        fmt.Sprintf("LOG-%d", time.Now().UnixNano()),
		Username:  cmd.Username,
		Action:    "LOGIN_SUCCESS",
		Details:   "Logged in with Standard security",
		Timestamp: time.Now(),
	})

	return session, nil
}

