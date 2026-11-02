package command

import (
	"time"

	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/security"
)

// IssueSession ký JWT cho user đã xác thực và đóng gói thành AuthSession
func IssueSession(secret []byte, tenantID string, user *domain.UserAccount, securityLevel string, ttl time.Duration) (*domain.AuthSession, error) {
	now := time.Now()
	token, err := security.SignToken(secret, security.TokenClaims{
		TokenID:       security.NewTokenID(),
		Subject:       user.ID,
		Username:      user.Username,
		TenantID:      tenantID,
		Role:          user.Role,
		SecurityLevel: securityLevel,
		IssuedAt:      now.Unix(),
		ExpiresAt:     now.Add(ttl).Unix(),
	})
	if err != nil {
		return nil, err
	}
	return &domain.AuthSession{
		AccessToken: token,
		TokenType:   "Bearer",
		ExpiresIn:   int64(ttl.Seconds()),
		User: domain.SessionUser{
			ID:            user.ID,
			Username:      user.Username,
			TenantID:      tenantID,
			Role:          user.Role,
			SecurityLevel: securityLevel,
		},
	}, nil
}
