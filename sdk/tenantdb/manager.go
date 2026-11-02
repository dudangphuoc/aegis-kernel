package tenantdb

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

// Manager quản lý pool tới DB của từng tenant cho một module (một schema)
type Manager struct {
	schema string
	cm     *multitenant.ConnectionManager[*DB]
}

// NewManager tạo Manager cho schema của module (VD: biz_parties)
func NewManager(schema string, opts ...multitenant.Option) *Manager {
	m := &Manager{schema: schema}
	// Tên schema đã được platform.provision_schema kiểm (^(biz|ft)_[a-z0-9_]+$) nên không cần quote
	searchPath := schema + ", platform"
	m.cm = multitenant.NewConnectionManager[*DB](func(tenantID, dsn string) (*DB, error) {
		db, err := sql.Open("postgres", dsn)
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres connection for %s: %w", tenantID, err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(time.Hour)
		// Schema do Control Plane áp (platform db apply), không migrate trong request path
		return &DB{db: db, searchPath: searchPath, onGone: func() { m.cm.Evict(tenantID) }}, nil
	}, opts...)
	return m
}

// Schema của module mà Manager phục vụ
func (m *Manager) Schema() string { return m.schema }

// Resolve trả DB của tenant trong context
func (m *Manager) Resolve(ctx context.Context) (*DB, contract.TenantRequestContext, error) {
	return m.cm.Resolve(ctx)
}

// Close đóng toàn bộ pool (module shutdown)
func (m *Manager) Close(context.Context) error {
	return m.cm.CloseAll()
}
