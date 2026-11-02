package domain

import "context"

type AuthRepository interface {
	GetUserByUsername(ctx context.Context, username string) (*UserAccount, error)
	CreateUser(ctx context.Context, user *UserAccount) error
	// RecordSecurityEvent ghi sự kiện bảo mật vào outbox audit của tenant (tầng DB)
	RecordSecurityEvent(ctx context.Context, name, subject, details string) error
}

type TenantProvisionRepository interface {
	SaveTenant(ctx context.Context, tenant *TenantProvision) error
	GetTenantByID(ctx context.Context, tenantID string) (*TenantProvision, error)
	ListAllTenants(ctx context.Context) ([]*TenantProvision, error)
}

