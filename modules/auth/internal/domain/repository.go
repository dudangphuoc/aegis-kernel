package domain

import "context"

type AuthRepository interface {
	GetUserByUsername(ctx context.Context, username string) (*UserAccount, error)
	CreateUser(ctx context.Context, user *UserAccount) error
	RecordAuditLog(ctx context.Context, log *SecurityAuditLog) error
	ListAuditLogs(ctx context.Context) ([]*SecurityAuditLog, error)
}

type TenantProvisionRepository interface {
	SaveTenant(ctx context.Context, tenant *TenantProvision) error
	GetTenantByID(ctx context.Context, tenantID string) (*TenantProvision, error)
	ListAllTenants(ctx context.Context) ([]*TenantProvision, error)
}

