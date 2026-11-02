package dbaudit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

// Publisher đẩy Envelope tới module audit (Kernel Bus). eventID do relay cấp để đối chiếu ack.
// Trả false nếu chưa nhận được (VD: outbox của Bus đầy) — relay sẽ thử lại, dòng vẫn còn trong DB.
type Publisher interface {
	PublishAudit(ctx context.Context, tenantID, eventID string, env audit.Envelope) bool
}

// Manager quản lý DB pool theo tenant (đã bọc audit) và relay outbox của module
type Manager struct {
	module string
	cm     *multitenant.ConnectionManager[*DB]
	relay  *relay
}

// NewManager tạo Manager cho module. pub = nil: chỉ ghi outbox, không relay (dùng trong test).
func NewManager(module string, pub Publisher, logger *slog.Logger, opts ...multitenant.Option) *Manager {
	m := &Manager{module: module}
	if pub != nil {
		m.relay = newRelay(module, m, pub, logger)
	}
	m.cm = multitenant.NewConnectionManager[*DB](func(tenantID, dsn string) (*DB, error) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres connection for %s: %w", tenantID, err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(time.Hour)
		// Schema do Control Plane Migrator quản lý, không migrate trong request path
		return &DB{db: db, tenantID: tenantID, dsn: dsn, onCommit: m.committed}, nil
	}, opts...)
	return m
}

// Resolve trả DB của tenant trong context
func (m *Manager) Resolve(ctx context.Context) (*DB, contract.TenantRequestContext, error) {
	return m.cm.Resolve(ctx)
}

// RegisterTenants báo relay các tenant đã biết lúc Initialize, để giao nốt outbox còn tồn từ lần chạy trước
func (m *Manager) RegisterTenants(tenants map[string]contract.TenantModuleConfig) {
	if m.relay == nil {
		return
	}
	for id, t := range tenants {
		m.relay.markDirty(id, t.ConnectionString)
	}
}

// Acked được gọi khi Kernel Bus xác nhận đã giao các event
func (m *Manager) Acked(eventIDs []string) {
	if m.relay != nil {
		m.relay.acked(eventIDs)
	}
}

// Close dừng relay (xử lý nốt ack đang chờ) rồi đóng toàn bộ pool
func (m *Manager) Close(ctx context.Context) error {
	if m.relay != nil {
		m.relay.stop(ctx)
	}
	return m.cm.CloseAll()
}

// Forget bỏ mọi trạng thái của tenant (pool, relay) khi database của tenant đã bị xóa
func (m *Manager) Forget(tenantID string) {
	if m.relay != nil {
		m.relay.forget(tenantID)
	}
	m.cm.Evict(tenantID)
}

func (m *Manager) committed(tenantID, dsn string) {
	if m.relay != nil {
		m.relay.markDirty(tenantID, dsn)
	}
}

// withTenantDB cho relay dùng pool sẵn có của tenant mà không kéo dài vòng đời pool;
// tenant đã bị đóng pool (nhàn rỗi) thì dùng 1 kết nối tạm rồi đóng ngay.
func (m *Manager) withTenantDB(tenantID, dsn string, fn func(*sql.DB) error) error {
	if d, ok := m.cm.Peek(tenantID, dsn); ok {
		return fn(d.db)
	}
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	return fn(db)
}

// isDatabaseGone: database của tenant không còn (tenant đã bị xóa) — thử lại cũng vô ích
func isDatabaseGone(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "3D000" // invalid_catalog_name
}
