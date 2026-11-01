package multitenant

import (
	"context"
	"fmt"
	"sync"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

// ConnectionFactory là hàm tạo instance DB/Store cho một Tenant
type ConnectionFactory[DB any] func(tenantID, connectionString string) (DB, error)

// ConnectionManager quản lý pool kết nối độc lập cho từng Tenant
type ConnectionManager[DB any] struct {
	mu        sync.RWMutex
	factory   ConnectionFactory[DB]
	instances map[string]DB
}

func NewConnectionManager[DB any](factory ConnectionFactory[DB]) *ConnectionManager[DB] {
	return &ConnectionManager[DB]{
		factory:   factory,
		instances: make(map[string]DB),
	}
}

// Resolve trả về instance DB tương ứng với TenantID và ConnectionString trong context
func (m *ConnectionManager[DB]) Resolve(ctx context.Context) (DB, contract.TenantRequestContext, error) {
	tenant, err := MustFromContext(ctx)
	if err != nil {
		var zero DB
		return zero, tenant, err
	}

	key := fmt.Sprintf("%s:%s", tenant.TenantID, tenant.ConnectionString)

	m.mu.RLock()
	inst, exists := m.instances[key]
	m.mu.RUnlock()
	if exists {
		return inst, tenant, nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	// Double check
	if inst, exists := m.instances[key]; exists {
		return inst, tenant, nil
	}

	newInst, err := m.factory(tenant.TenantID, tenant.ConnectionString)
	if err != nil {
		var zero DB
		return zero, tenant, fmt.Errorf("failed to initialize db for tenant '%s': %w", tenant.TenantID, err)
	}

	m.instances[key] = newInst
	return newInst, tenant, nil
}

