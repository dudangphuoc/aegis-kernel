package migrator

import (
	"context"
	"database/sql"
	"fmt"
	"sort"
	"time"

	_ "github.com/lib/pq"
)

type MigrationScript struct {
	Version int    `json:"version"`
	Name    string `json:"name"`
	UpSQL   string `json:"-"`
	DownSQL string `json:"-"`
}

type AppliedMigration struct {
	Version     int       `json:"version"`
	Name        string    `json:"name"`
	AppliedAt   time.Time `json:"applied_at"`
	ExecutionMs int64     `json:"execution_ms"`
}

type TenantMigrationStatus struct {
	TenantID          string             `json:"tenant_id"`
	ModuleName        string             `json:"module_name"`
	ConnectionString  string             `json:"connection_string"`
	CurrentVersion    int                `json:"current_version"`
	LatestVersion     int                `json:"latest_version"`
	PendingCount      int                `json:"pending_count"`
	Status            string             `json:"status"` // UP_TO_DATE, PENDING_MIGRATIONS, ERROR
	AppliedMigrations []AppliedMigration `json:"applied_migrations"`
	PendingMigrations []string           `json:"pending_migrations"`
	ErrorMessage      string             `json:"error_message,omitempty"`
}

type Runner struct{}

func NewRunner() *Runner {
	return &Runner{}
}

// EnsureSchemaTable đảm bảo bảng schema_migrations tồn tại
func (r *Runner) EnsureSchemaTable(ctx context.Context, db *sql.DB) error {
	query := `
	CREATE TABLE IF NOT EXISTS schema_migrations (
		version INT PRIMARY KEY,
		name VARCHAR(255) NOT NULL,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		execution_ms BIGINT NOT NULL DEFAULT 0
	);`
	_, err := db.ExecContext(ctx, query)
	return err
}

// GetAppliedMigrations lấy danh sách migration đã áp dụng
func (r *Runner) GetAppliedMigrations(ctx context.Context, db *sql.DB) ([]AppliedMigration, error) {
	if err := r.EnsureSchemaTable(ctx, db); err != nil {
		return nil, err
	}
	rows, err := db.QueryContext(ctx, `SELECT version, name, applied_at, execution_ms FROM schema_migrations ORDER BY version ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var res []AppliedMigration
	for rows.Next() {
		var m AppliedMigration
		if err := rows.Scan(&m.Version, &m.Name, &m.AppliedAt, &m.ExecutionMs); err != nil {
			return nil, err
		}
		res = append(res, m)
	}
	return res, rows.Err()
}

// InspectStatus kiểm tra tình trạng migration của một DB
func (r *Runner) InspectStatus(ctx context.Context, db *sql.DB, tenantID, moduleName, connStr string, scripts []MigrationScript) TenantMigrationStatus {
	latestVersion := 0
	for _, s := range scripts {
		if s.Version > latestVersion {
			latestVersion = s.Version
		}
	}

	applied, err := r.GetAppliedMigrations(ctx, db)
	if err != nil {
		return TenantMigrationStatus{
			TenantID:         tenantID,
			ModuleName:       moduleName,
			ConnectionString: connStr,
			LatestVersion:    latestVersion,
			Status:           "ERROR",
			ErrorMessage:     err.Error(),
		}
	}

	currentVersion := 0
	appliedMap := make(map[int]bool)
	for _, a := range applied {
		appliedMap[a.Version] = true
		if a.Version > currentVersion {
			currentVersion = a.Version
		}
	}

	var pending []string
	for _, s := range scripts {
		if !appliedMap[s.Version] {
			pending = append(pending, fmt.Sprintf("v%04d_%s", s.Version, s.Name))
		}
	}

	status := "UP_TO_DATE"
	if len(pending) > 0 {
		status = "PENDING_MIGRATIONS"
	}

	return TenantMigrationStatus{
		TenantID:          tenantID,
		ModuleName:        moduleName,
		ConnectionString:  connStr,
		CurrentVersion:    currentVersion,
		LatestVersion:     latestVersion,
		PendingCount:      len(pending),
		Status:            status,
		AppliedMigrations: applied,
		PendingMigrations: pending,
	}
}

// MigrateUp chạy tất cả các script chưa chạy theo thứ tự tăng dần
func (r *Runner) MigrateUp(ctx context.Context, db *sql.DB, scripts []MigrationScript) error {
	if err := r.EnsureSchemaTable(ctx, db); err != nil {
		return err
	}

	// Dùng advisory lock tránh 2 instance cùng chạy migration
	var lockSuccess bool
	if err := db.QueryRowContext(ctx, `SELECT pg_try_advisory_lock(hashtext('golearn_migration'))`).Scan(&lockSuccess); err != nil || !lockSuccess {
		return fmt.Errorf("could not acquire advisory lock for migration: %w", err)
	}
	defer db.ExecContext(ctx, `SELECT pg_advisory_unlock(hashtext('golearn_migration'))`)

	applied, err := r.GetAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	appliedMap := make(map[int]bool)
	for _, a := range applied {
		appliedMap[a.Version] = true
	}

	// Sắp xếp scripts theo Version tăng dần
	sort.Slice(scripts, func(i, j int) bool {
		return scripts[i].Version < scripts[j].Version
	})

	for _, s := range scripts {
		if appliedMap[s.Version] {
			continue
		}

		start := time.Now()
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.ExecContext(ctx, s.UpSQL); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration v%d (%s) failed: %w", s.Version, s.Name, err)
		}

		duration := time.Since(start).Milliseconds()
		if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at, execution_ms) VALUES ($1, $2, NOW(), $3)`, s.Version, s.Name, duration); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration v%d: %w", s.Version, err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration v%d: %w", s.Version, err)
		}
	}

	return nil
}

// RollbackDown lùi lại đúng 1 version cao nhất hiện tại
func (r *Runner) RollbackDown(ctx context.Context, db *sql.DB, scripts []MigrationScript) error {
	if err := r.EnsureSchemaTable(ctx, db); err != nil {
		return err
	}

	applied, err := r.GetAppliedMigrations(ctx, db)
	if err != nil {
		return err
	}
	if len(applied) == 0 {
		return fmt.Errorf("no migrations applied to rollback")
	}

	// Tìm version cao nhất hiện tại
	current := applied[len(applied)-1]

	var targetScript *MigrationScript
	for _, s := range scripts {
		if s.Version == current.Version {
			targetScript = &s
			break
		}
	}
	if targetScript == nil || targetScript.DownSQL == "" {
		return fmt.Errorf("no down script found for version v%d (%s)", current.Version, current.Name)
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	if _, err := tx.ExecContext(ctx, targetScript.DownSQL); err != nil {
		tx.Rollback()
		return fmt.Errorf("rollback v%d failed: %w", current.Version, err)
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM schema_migrations WHERE version = $1`, current.Version); err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to remove migration record v%d: %w", current.Version, err)
	}

	return tx.Commit()
}

