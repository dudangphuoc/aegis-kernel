package securityaudit

import (
	"context"

	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
)

// SecurityAuditQueryHandler mở rộng API truy xuất nhật ký bảo mật cho Tenant
type SecurityAuditQueryHandler struct {
	repo domain.AuthRepository
}

func NewSecurityAuditQueryHandler(repo domain.AuthRepository) *SecurityAuditQueryHandler {
	return &SecurityAuditQueryHandler{repo: repo}
}

func (h *SecurityAuditQueryHandler) Handle(ctx context.Context) ([]*domain.SecurityAuditLog, error) {
	return h.repo.ListAuditLogs(ctx)
}

