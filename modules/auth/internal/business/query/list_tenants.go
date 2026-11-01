package query

import (
	"context"

	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
)

type ListTenantsHandler struct {
	repo domain.TenantProvisionRepository
}

func NewListTenantsHandler(repo domain.TenantProvisionRepository) *ListTenantsHandler {
	return &ListTenantsHandler{repo: repo}
}

func (h *ListTenantsHandler) Handle(ctx context.Context) ([]*domain.TenantProvision, error) {
	return h.repo.ListAllTenants(ctx)
}

