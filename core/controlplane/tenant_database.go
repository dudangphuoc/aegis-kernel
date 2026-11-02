package controlplane

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/db"
	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
	auditdb "github.com/dudangphuoc/aegis-kernel/modules/audit/db"
	authdb "github.com/dudangphuoc/aegis-kernel/modules/auth/db"
	partiesdb "github.com/dudangphuoc/aegis-kernel/modules/parties/db"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
)

// tenantModules: module dùng DB tenant, theo thứ tự áp script. Tạm import trực tiếp; từ Bước 2 script đi
// trong gói (platform pack) và danh sách lấy từ release của tenant.
var tenantModules = []func() (scripts.Package, error){authdb.Package, partiesdb.Package, auditdb.Package}

// TenantDatabaseName: một DB cho mỗi tenant, module tách bằng schema biz_<module>
func TenantDatabaseName(tenantID string) string { return "tenant_" + tenantID }

// legacyDatabaseNames: DB theo mô hình cũ (mỗi module một DB), chỉ còn để dọn khi xoá tenant
func legacyDatabaseNames(tenantID string) []string {
	return []string{"auth_" + tenantID, "parties_" + tenantID, "audit_" + tenantID}
}

// TenantPackages trả package script áp lên DB tenant: platform trước, rồi từng module
func TenantPackages() ([]scripts.Package, error) {
	p, err := db.Platform("kernel")
	if err != nil {
		return nil, err
	}
	pkgs := []scripts.Package{p}
	for _, load := range tenantModules {
		m, err := load()
		if err != nil {
			return nil, err
		}
		pkgs = append(pkgs, m)
	}
	return pkgs, nil
}

// ensureTenantDatabaseLocked tạo DB của tenant nếu chưa có, áp mọi script còn thiếu, gắn store gốc và trỏ
// mọi module của tenant về DB này. Idempotent: chạy mỗi lần Kernel khởi động. Caller giữ s.mu.
func (s *Store) ensureTenantDatabaseLocked(ctx context.Context, tenantID string) (string, error) {
	name := TenantDatabaseName(tenantID)
	if err := s.createDatabaseIfNotExists(ctx, name); err != nil {
		return "", fmt.Errorf("create database %s: %w", name, err)
	}
	adminDSN := s.BuildAdminConnectionString(name)

	conn, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	pkgs, err := TenantPackages()
	if err != nil {
		return "", err
	}
	// TrackChanges: Kernel đang đóng vai hub của store gốc nên mọi bảng đồng bộ được capture
	runner := &scripts.Runner{DB: conn, TrackChanges: true, Logger: s.logger.With(slog.String("tenant_id", tenantID))}
	if _, err := runner.Apply(ctx, pkgs...); err != nil {
		return "", fmt.Errorf("apply scripts on %s: %w", name, err)
	}

	storeID, err := s.rootStoreIDLocked(ctx, tenantID)
	if err != nil {
		return "", err
	}
	if _, err := conn.ExecContext(ctx, `SELECT platform.bind_store($1)`, storeID); err != nil {
		return "", fmt.Errorf("bind store on %s: %w", name, err)
	}

	// Mọi module có schema trong DB tenant đều được đăng ký (tenant cũ chưa có module mới, VD: audit)
	for _, p := range pkgs[1:] {
		if _, err := s.db.ExecContext(ctx, `
			INSERT INTO cp_tenant_modules (tenant_id, module_name, connection_string, features, settings)
			VALUES ($1, $2, $3, '{}'::jsonb, '{}'::jsonb)
			ON CONFLICT (tenant_id, module_name) DO UPDATE SET connection_string = EXCLUDED.connection_string, updated_at = NOW()`,
			tenantID, p.Name, adminDSN); err != nil {
			return "", err
		}
	}
	return adminDSN, nil
}

func (s *Store) rootStoreIDLocked(ctx context.Context, tenantID string) (string, error) {
	var id sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT root_store_id::text FROM cp_tenants WHERE tenant_id = $1`, tenantID).Scan(&id); err != nil {
		return "", err
	}
	if id.Valid {
		return id.String, nil
	}
	newID := newUUID()
	_, err := s.db.ExecContext(ctx, `UPDATE cp_tenants SET root_store_id = $2 WHERE tenant_id = $1`, tenantID, newID)
	return newID, err
}

// EnsureTenantDatabases áp script còn thiếu cho mọi tenant khi Kernel khởi động (thêm module, nâng version).
// Tenant còn ở mô hình cũ (mỗi module một DB) được chuyển sang DB tenant_<id>; dữ liệu cũ không được chép.
func (s *Store) EnsureTenantDatabases(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	ids, err := s.tenantIDs(ctx)
	if err != nil {
		return err
	}
	var errs []error
	for _, id := range ids {
		if _, err := s.ensureTenantDatabaseLocked(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("tenant %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// TenantIDs liệt kê tenant (change feed quét định kỳ)
func (s *Store) TenantIDs(ctx context.Context) ([]string, error) {
	return s.tenantIDs(ctx)
}

func (s *Store) tenantIDs(ctx context.Context) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT tenant_id FROM cp_tenants ORDER BY tenant_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// TenantDatabaseDSN trả DSN runtime (qua PgBouncer nếu có) tới DB của tenant
func (s *Store) TenantDatabaseDSN(ctx context.Context, tenantID string) (string, error) {
	var exists bool
	if err := s.db.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM cp_tenants WHERE tenant_id = $1)`, tenantID).Scan(&exists); err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("%w: %s", contract.ErrTenantNotFound, tenantID)
	}
	admin := s.BuildAdminConnectionString(TenantDatabaseName(tenantID))
	return s.BuildRuntimeConnectionString(tenantID, admin), nil
}

// seedTenantAdmin tạo (hoặc đặt lại mật khẩu) tài khoản admin trong schema biz_auth. Ghi kèm ngữ cảnh
// Control Plane để audit thấy ai tạo; mật khẩu bị che ở tầng audit.
func seedTenantAdmin(ctx context.Context, adminDSN, tenantID, username, password string) error {
	conn, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return err
	}
	defer conn.Close()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	rc := tenantdb.StoredContext{TenantID: tenantID, SourceModule: "controlplane", Action: "PROVISION_TENANT"}
	rc.Actor.ID = "CONTROL_PLANE"
	payload, _ := json.Marshal(rc)
	if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', 'biz_auth, platform', true), set_config($1, $2, true)`,
		tenantdb.ContextSetting, string(payload)); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO users (id, tenant_id, username, password, role, mfa_code)
		VALUES ($1, $2, $3, $4, 'TENANT_ADMIN', '654321')
		ON CONFLICT (store_id, username) DO UPDATE SET password = EXCLUDED.password`,
		"USR-"+tenantID+"-ADMIN", tenantID, username, password); err != nil {
		return err
	}
	return tx.Commit()
}

func newUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
