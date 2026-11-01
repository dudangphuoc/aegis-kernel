package multitenant

import (
	"context"
	"errors"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

type tenantCtxKey struct{}

var ErrTenantNotFoundInContext = errors.New("no tenant context found in request")

// WithTenant đưa TenantRequestContext vào Go context.Context
func WithTenant(ctx context.Context, tenant contract.TenantRequestContext) context.Context {
	return context.WithValue(ctx, tenantCtxKey{}, tenant)
}

// FromContext trích xuất TenantRequestContext từ context.Context
func FromContext(ctx context.Context) (contract.TenantRequestContext, bool) {
	val, ok := ctx.Value(tenantCtxKey{}).(contract.TenantRequestContext)
	return val, ok
}

// MustFromContext trả về TenantRequestContext hoặc báo lỗi nếu thiếu
func MustFromContext(ctx context.Context) (contract.TenantRequestContext, error) {
	val, ok := FromContext(ctx)
	if !ok || val.TenantID == "" {
		return contract.TenantRequestContext{}, ErrTenantNotFoundInContext
	}
	return val, nil
}

// IsFeatureEnabled kiểm tra cờ tính năng nhanh của Tenant hiện hành
func IsFeatureEnabled(ctx context.Context, featureFlag string) bool {
	if featureFlag == "" {
		return true
	}
	tenant, ok := FromContext(ctx)
	if !ok {
		return false
	}
	return tenant.Features[featureFlag]
}

