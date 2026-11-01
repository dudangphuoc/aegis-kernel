package feature

import (
	"context"
	"github.com/dudangphuoc/aegis-kernel/core/controlplane"
)

type FeatureManager struct {
	store *controlplane.Store
}

func NewFeatureManager(store *controlplane.Store) *FeatureManager {
	return &FeatureManager{
		store: store,
	}
}

func (fm *FeatureManager) IsEnabledForTenant(tenantID, moduleName, featureFlag string) bool {
	if featureFlag == "" {
		return true
	}
	ctx, err := fm.store.ResolveTenantRequestContext(context.Background(), tenantID, moduleName)
	if err != nil {
		return false
	}
	return ctx.Features[featureFlag]
}
