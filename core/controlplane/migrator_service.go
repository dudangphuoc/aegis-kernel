package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
)

// TenantMigrationStatus là trạng thái script của một package (platform / module) trên DB một tenant.
// Giữ dạng JSON cũ cho dashboard; "version" là số thứ tự script trong package.
type TenantMigrationStatus struct {
	TenantID          string             `json:"tenant_id"`
	ModuleName        string             `json:"module_name"`
	Schema            string             `json:"schema"`
	CurrentVersion    int                `json:"current_version"`
	LatestVersion     int                `json:"latest_version"`
	PendingCount      int                `json:"pending_count"`
	Status            string             `json:"status"` // UP_TO_DATE, PENDING_MIGRATIONS, ERROR
	AppliedMigrations []AppliedMigration `json:"applied_migrations"`
	PendingMigrations []string           `json:"pending_migrations"`
	ErrorMessage      string             `json:"error_message,omitempty"`
}

type AppliedMigration struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	AppliedAt   time.Time `json:"applied_at"`
	ExecutionMs int64     `json:"execution_ms"`
}

// MigrationService xem và áp script trên DB của tenant. Script chỉ thêm (add-only): không có rollback.
type MigrationService struct {
	store *Store
}

func NewMigrationService(store *Store) *MigrationService {
	return &MigrationService{store: store}
}

// GetAllTenantMigrationStatuses: trạng thái từng (tenant × package) trên DB tenant_<id>
func (s *MigrationService) GetAllTenantMigrationStatuses(ctx context.Context) ([]TenantMigrationStatus, error) {
	ids, err := s.store.TenantIDs(ctx)
	if err != nil {
		return nil, err
	}
	pkgs, err := TenantPackages()
	if err != nil {
		return nil, err
	}
	var out []TenantMigrationStatus
	for _, id := range ids {
		out = append(out, s.tenantStatus(ctx, id, pkgs)...)
	}
	return out, nil
}

func (s *MigrationService) tenantStatus(ctx context.Context, tenantID string, pkgs []scripts.Package) []TenantMigrationStatus {
	fail := func(err error) []TenantMigrationStatus {
		out := make([]TenantMigrationStatus, len(pkgs))
		for i, p := range pkgs {
			out[i] = TenantMigrationStatus{TenantID: tenantID, ModuleName: p.Name, Schema: p.Schema, Status: "ERROR", ErrorMessage: err.Error()}
		}
		return out
	}
	conn, err := sql.Open("postgres", s.store.BuildAdminConnectionString(TenantDatabaseName(tenantID)))
	if err != nil {
		return fail(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	plans, err := (&scripts.Runner{DB: conn}).Plan(ctx, pkgs...)
	if err != nil {
		return fail(err)
	}
	appliedAt := map[string]time.Time{}
	if rows, err := conn.QueryContext(ctx, `SELECT package || '/' || script, applied_at FROM platform.migrations`); err == nil {
		for rows.Next() {
			var k string
			var at time.Time
			if rows.Scan(&k, &at) == nil {
				appliedAt[k] = at
			}
		}
		rows.Close()
	}

	out := make([]TenantMigrationStatus, 0, len(plans))
	for _, p := range plans {
		st := TenantMigrationStatus{TenantID: tenantID, ModuleName: p.Package, Schema: p.Schema, Status: "UP_TO_DATE",
			AppliedMigrations: []AppliedMigration{}, PendingMigrations: []string{}}
		for i, sc := range p.Scripts {
			switch sc.Status {
			case scripts.StatusApplied, scripts.StatusAhead:
				st.AppliedMigrations = append(st.AppliedMigrations, AppliedMigration{Version: i + 1, Name: sc.Script, AppliedAt: appliedAt[p.Package+"/"+sc.Script]})
				st.CurrentVersion++
			case scripts.StatusPending:
				st.PendingMigrations = append(st.PendingMigrations, sc.Script)
			case scripts.StatusEdited:
				st.Status, st.ErrorMessage = "ERROR", fmt.Sprintf("%v: %s", scripts.ErrMigrationEdited, sc.Script)
			}
			if sc.Status != scripts.StatusAhead {
				st.LatestVersion++
			}
		}
		st.PendingCount = len(st.PendingMigrations)
		if st.PendingCount > 0 && st.Status != "ERROR" {
			st.Status = "PENDING_MIGRATIONS"
		}
		out = append(out, st)
	}
	return out
}

// RunMigrationForTenant áp mọi script còn thiếu trên DB của tenant. Script dùng chung một DB nên
// moduleName chỉ để báo lỗi; "down" bị từ chối vì script chỉ thêm (hạ version giữ nguyên schema).
func (s *MigrationService) RunMigrationForTenant(ctx context.Context, tenantID, moduleName, action string) error {
	switch action {
	case "up":
		s.store.mu.Lock()
		defer s.store.mu.Unlock()
		_, err := s.store.ensureTenantDatabaseLocked(ctx, tenantID)
		return err
	case "down":
		return fmt.Errorf("scripts are add-only: rollback of '%s' is not supported (release an older package version instead)", moduleName)
	default:
		return fmt.Errorf("invalid migration action '%s', must be 'up'", action)
	}
}

// BatchMigrateAllTenants áp script còn thiếu cho mọi tenant
func (s *MigrationService) BatchMigrateAllTenants(ctx context.Context) (map[string]string, error) {
	ids, err := s.store.TenantIDs(ctx)
	if err != nil {
		return nil, err
	}
	report := make(map[string]string, len(ids))
	for _, id := range ids {
		if err := s.RunMigrationForTenant(ctx, id, "", "up"); err != nil {
			report[id] = fmt.Sprintf("FAILED: %v", err)
			continue
		}
		report[id] = "SUCCESS: Up to date"
	}
	return report, nil
}
