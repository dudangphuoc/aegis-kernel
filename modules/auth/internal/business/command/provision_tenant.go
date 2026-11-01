package command

import (
	"context"
	"fmt"
	"time"

	"github.com/dudangphuoc/AegisKernel/modules/auth/internal/domain"
)

type ProvisionTenantCommand struct {
	TenantID         string          `json:"tenant_id"`
	OrganizationName string          `json:"organization_name"`
	Plan             string          `json:"plan"`
	Features         map[string]bool `json:"features"`
}

type ProvisionTenantHandler struct {
	repo domain.TenantProvisionRepository
}

func NewProvisionTenantHandler(repo domain.TenantProvisionRepository) *ProvisionTenantHandler {
	return &ProvisionTenantHandler{repo: repo}
}

func (h *ProvisionTenantHandler) Handle(ctx context.Context, cmd ProvisionTenantCommand) (*domain.TenantProvision, error) {
	if cmd.TenantID == "" || cmd.OrganizationName == "" {
		return nil, fmt.Errorf("tenant_id and organization_name are required")
	}

	if existing, _ := h.repo.GetTenantByID(ctx, cmd.TenantID); existing != nil {
		return nil, domain.ErrTenantExists
	}

	if cmd.Plan == "" {
		cmd.Plan = "standard"
	}

	connStr := fmt.Sprintf("postgres://%s_usr:secret@localhost:5432/%s_db?sslmode=disable", cmd.TenantID, cmd.TenantID)

	tenant := &domain.TenantProvision{
		TenantID:         cmd.TenantID,
		OrganizationName: cmd.OrganizationName,
		Plan:             cmd.Plan,
		ConnectionString: connStr,
		Features:         cmd.Features,
		Status:           "ACTIVE",
		ProvisionedAt:    time.Now(),
	}

	if err := h.repo.SaveTenant(ctx, tenant); err != nil {
		return nil, err
	}

	return tenant, nil
}

