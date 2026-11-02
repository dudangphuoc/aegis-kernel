package multitenant

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

const (
	DefaultIdleTTL     = 15 * time.Minute
	DefaultMaxTenants  = 500
	defaultJanitorTick = time.Minute
)

// ErrManagerClosed được trả về khi Resolve sau khi đã gọi CloseAll
var ErrManagerClosed = errors.New("connection manager is closed")

// ConnectionFactory là hàm tạo instance DB/Store cho một Tenant
type ConnectionFactory[DB any] func(tenantID, connectionString string) (DB, error)

// Option tùy chỉnh ConnectionManager
type Option func(*options)

type options struct {
	idleTTL     time.Duration
	maxTenants  int
	janitorTick time.Duration
}

// WithIdleTTL đặt thời gian nhàn rỗi tối đa trước khi pool của Tenant bị đóng (<= 0 để tắt)
func WithIdleTTL(d time.Duration) Option { return func(o *options) { o.idleTTL = d } }

// WithMaxTenants giới hạn số pool giữ trong RAM, vượt quá sẽ đóng pool ít dùng nhất (LRU) (<= 0 để tắt)
func WithMaxTenants(n int) Option { return func(o *options) { o.maxTenants = n } }

// WithJanitorInterval đặt chu kỳ quét các pool nhàn rỗi
func WithJanitorInterval(d time.Duration) Option { return func(o *options) { o.janitorTick = d } }

type entry[DB any] struct {
	inst     DB
	connStr  string
	lastUsed time.Time
}

// ConnectionManager quản lý pool kết nối độc lập cho từng Tenant:
// - Idle TTL Eviction: đóng pool không có request sau IdleTTL
// - LRU Eviction: giới hạn số Tenant giữ pool đồng thời
// - Safe Rebind: đổi ConnectionString thì đóng pool cũ trước khi dùng pool mới
// - CloseAll: đóng toàn bộ pool khi module Shutdown
// Instance DB được đóng nếu nó implement io.Closer (ví dụ *sql.DB).
type ConnectionManager[DB any] struct {
	mu        sync.Mutex
	factory   ConnectionFactory[DB]
	opts      options
	instances map[string]*entry[DB] // Key: TenantID
	closed    bool
	stop      chan struct{}
	done      chan struct{}
	now       func() time.Time
}

func NewConnectionManager[DB any](factory ConnectionFactory[DB], opts ...Option) *ConnectionManager[DB] {
	o := options{idleTTL: DefaultIdleTTL, maxTenants: DefaultMaxTenants, janitorTick: defaultJanitorTick}
	for _, opt := range opts {
		opt(&o)
	}
	m := &ConnectionManager[DB]{
		factory:   factory,
		opts:      o,
		instances: make(map[string]*entry[DB]),
		stop:      make(chan struct{}),
		done:      make(chan struct{}),
		now:       time.Now,
	}
	if o.idleTTL > 0 && o.janitorTick > 0 {
		go m.janitor()
	} else {
		close(m.done)
	}
	return m
}

// Resolve trả về instance DB tương ứng với TenantID và ConnectionString trong context
func (m *ConnectionManager[DB]) Resolve(ctx context.Context) (DB, contract.TenantRequestContext, error) {
	var zero DB
	tenant, err := MustFromContext(ctx)
	if err != nil {
		return zero, tenant, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return zero, tenant, ErrManagerClosed
	}

	if e, ok := m.instances[tenant.TenantID]; ok {
		if e.connStr == tenant.ConnectionString {
			e.lastUsed = m.now()
			return e.inst, tenant, nil
		}
		// Safe Rebind: Tenant đã đổi ConnectionString -> đóng pool cũ
		closeInstance(e.inst)
		delete(m.instances, tenant.TenantID)
	}

	newInst, err := m.factory(tenant.TenantID, tenant.ConnectionString)
	if err != nil {
		return zero, tenant, fmt.Errorf("failed to initialize db for tenant '%s': %w", tenant.TenantID, err)
	}

	if m.opts.maxTenants > 0 && len(m.instances) >= m.opts.maxTenants {
		m.evictLRULocked()
	}
	m.instances[tenant.TenantID] = &entry[DB]{inst: newInst, connStr: tenant.ConnectionString, lastUsed: m.now()}
	return newInst, tenant, nil
}

// Peek trả pool đang mở của tenant (đúng connectionString) mà KHÔNG làm mới thời điểm sử dụng,
// để tác vụ nền (VD: relay audit) không giữ pool của tenant nhàn rỗi sống mãi.
func (m *ConnectionManager[DB]) Peek(tenantID, connectionString string) (DB, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.instances[tenantID]; ok && !m.closed && e.connStr == connectionString {
		return e.inst, true
	}
	var zero DB
	return zero, false
}

// Len trả về số pool đang được giữ
func (m *ConnectionManager[DB]) Len() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.instances)
}

// Evict đóng và bỏ pool của tenant (VD: tenant đã bị xóa, database không còn tồn tại)
func (m *ConnectionManager[DB]) Evict(tenantID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.instances[tenantID]; ok {
		closeInstance(e.inst)
		delete(m.instances, tenantID)
	}
}

// CloseAll dừng janitor và đóng toàn bộ pool. Resolve sau đó trả về ErrManagerClosed.
func (m *ConnectionManager[DB]) CloseAll() error {
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil
	}
	m.closed = true
	var errs []error
	for id, e := range m.instances {
		if err := closeInstance(e.inst); err != nil {
			errs = append(errs, fmt.Errorf("close pool of tenant '%s': %w", id, err))
		}
		delete(m.instances, id)
	}
	m.mu.Unlock()

	close(m.stop)
	<-m.done
	return errors.Join(errs...)
}

func (m *ConnectionManager[DB]) janitor() {
	defer close(m.done)
	ticker := time.NewTicker(m.opts.janitorTick)
	defer ticker.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.evictIdle()
		}
	}
}

// evictIdle đóng các pool không được dùng trong khoảng IdleTTL
func (m *ConnectionManager[DB]) evictIdle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	cutoff := m.now().Add(-m.opts.idleTTL)
	for id, e := range m.instances {
		if e.lastUsed.Before(cutoff) {
			closeInstance(e.inst)
			delete(m.instances, id)
		}
	}
}

func (m *ConnectionManager[DB]) evictLRULocked() {
	var oldestID string
	var oldest time.Time
	for id, e := range m.instances {
		if oldestID == "" || e.lastUsed.Before(oldest) {
			oldestID, oldest = id, e.lastUsed
		}
	}
	if oldestID != "" {
		closeInstance(m.instances[oldestID].inst)
		delete(m.instances, oldestID)
	}
}

func closeInstance(inst any) error {
	if c, ok := inst.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
