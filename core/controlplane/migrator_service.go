package controlplane

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	auditmigrations "github.com/dudangphuoc/aegis-kernel/modules/audit/migrations"
	authmigrations "github.com/dudangphuoc/aegis-kernel/modules/auth/migrations"
	partiesmigrations "github.com/dudangphuoc/aegis-kernel/modules/parties/migrations"
	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
)

type MigrationService struct {
	store  *Store
	runner *migrator.Runner
}

func NewMigrationService(store *Store) *MigrationService {
	return &MigrationService{
		store:  store,
		runner: migrator.NewRunner(),
	}
}

func (s *MigrationService) getScriptsForModule(moduleName string) []migrator.MigrationScript {
	switch moduleName {
	case "auth":
		return authmigrations.GetAuthMigrationScripts()
	case "parties":
		return partiesmigrations.GetPartiesMigrationScripts()
	case "audit":
		return auditmigrations.GetAuditMigrationScripts()
	default:
		return nil
	}
}

// GetAllTenantMigrationStatuses lấy trạng thái migration của tất cả (Tenant x Module)
func (s *MigrationService) GetAllTenantMigrationStatuses(ctx context.Context) ([]migrator.TenantMigrationStatus, error) {
	tenants, err := s.store.GetTenants(ctx)
	if err != nil {
		return nil, err
	}

	var results []migrator.TenantMigrationStatus
	for tenantID, t := range tenants {
		for modName, m := range t.Modules {
			scripts := s.getScriptsForModule(modName)
			connStr := m.ConnectionString

			db, err := sql.Open("postgres", connStr)
			if err != nil {
				results = append(results, migrator.TenantMigrationStatus{
					TenantID:         tenantID,
					ModuleName:       modName,
					ConnectionString: connStr,
					Status:           "ERROR",
					ErrorMessage:     err.Error(),
				})
				continue
			}

			statusCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
			status := s.runner.InspectStatus(statusCtx, db, tenantID, modName, connStr, scripts)
			cancel()
			_ = db.Close()

			results = append(results, status)
		}
	}
	return results, nil
}

// RunMigrationForTenant chạy up hoặc down cho 1 Tenant x Module
func (s *MigrationService) RunMigrationForTenant(ctx context.Context, tenantID, moduleName, action string) error {
	// Migrator dùng DSN admin (:5432): pg_advisory_lock theo session không chạy được qua PgBouncer transaction mode
	adminDSN, err := s.store.ResolveAdminConnectionString(ctx, tenantID, moduleName)
	if err != nil {
		return err
	}

	scripts := s.getScriptsForModule(moduleName)
	if len(scripts) == 0 {
		return fmt.Errorf("no migration scripts registered for module '%s'", moduleName)
	}

	db, err := sql.Open("postgres", adminDSN)
	if err != nil {
		return fmt.Errorf("failed to connect to db for tenant '%s': %w", tenantID, err)
	}
	defer db.Close()

	switch action {
	case "up":
		return s.runner.MigrateUp(ctx, db, scripts)
	case "down":
		return s.runner.RollbackDown(ctx, db, scripts)
	default:
		return fmt.Errorf("invalid migration action '%s', must be 'up' or 'down'", action)
	}
}

// BatchMigrateAllTenants nâng cấp tất cả tenants lên phiên bản mới nhất
func (s *MigrationService) BatchMigrateAllTenants(ctx context.Context) (map[string]string, error) {
	tenants, err := s.store.GetTenants(ctx)
	if err != nil {
		return nil, err
	}

	report := make(map[string]string)
	var mu sync.Mutex
	var wg sync.WaitGroup

	for tID, t := range tenants {
		for mName, m := range t.Modules {
			tenantID := tID
			moduleName := mName
			connStr := m.ConnectionString
			scripts := s.getScriptsForModule(moduleName)

			wg.Add(1)
			go func() {
				defer wg.Done()
				key := fmt.Sprintf("%s:%s", tenantID, moduleName)
				db, err := sql.Open("postgres", connStr)
				if err != nil {
					mu.Lock()
					report[key] = fmt.Sprintf("FAILED: connect db error (%v)", err)
					mu.Unlock()
					return
				}
				defer db.Close()

				mCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()

				if err := s.runner.MigrateUp(mCtx, db, scripts); err != nil {
					mu.Lock()
					report[key] = fmt.Sprintf("FAILED: %v", err)
					mu.Unlock()
				} else {
					mu.Lock()
					report[key] = "SUCCESS: Up to date"
					mu.Unlock()
				}
			}()
		}
	}

	wg.Wait()
	return report, nil
}

