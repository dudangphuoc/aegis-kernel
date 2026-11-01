package storage

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"
	authmigrations "github.com/dudangphuoc/AegisKernel/modules/auth/migrations"
	"github.com/dudangphuoc/AegisKernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/AegisKernel/sdk/migrator"
	"github.com/dudangphuoc/AegisKernel/sdk/multitenant"
)

type PostgresAuthRepo struct {
	connManager *multitenant.ConnectionManager[*sql.DB]
}

func NewPostgresAuthRepo() *PostgresAuthRepo {
	runner := migrator.NewRunner()
	cm := multitenant.NewConnectionManager[*sql.DB](func(tenantID, connStr string) (*sql.DB, error) {
		db, err := sql.Open("postgres", connStr)
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres connection for %s: %w", tenantID, err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(time.Hour)

		// Tự động chạy migrations v1 khởi tạo nếu DB chưa có
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := runner.MigrateUp(ctx, db, authmigrations.GetAuthMigrationScripts()); err != nil {
			_ = db.Close()
			return nil, fmt.Errorf("failed to auto-migrate auth db for %s: %w", tenantID, err)
		}

		return db, nil
	})
	return &PostgresAuthRepo{connManager: cm}
}

func (r *PostgresAuthRepo) GetUserByUsername(ctx context.Context, username string) (*domain.UserAccount, error) {
	db, tenant, err := r.connManager.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	query := `SELECT id, tenant_id, username, password, role, mfa_code, created_at FROM users WHERE username = $1`
	var u domain.UserAccount
	err = db.QueryRowContext(ctx, query, username).Scan(
		&u.ID, &u.TenantID, &u.Username, &u.Password, &u.Role, &u.MFACode, &u.CreatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, domain.ErrInvalidCredentials
	}
	if err != nil {
		return nil, err
	}
	u.TenantID = tenant.TenantID
	return &u, nil
}

func (r *PostgresAuthRepo) CreateUser(ctx context.Context, user *domain.UserAccount) error {
	db, tenant, err := r.connManager.Resolve(ctx)
	if err != nil {
		return err
	}

	query := `INSERT INTO users (id, tenant_id, username, password, role, mfa_code, created_at)
			  VALUES ($1, $2, $3, $4, $5, $6, $7)`
	_, err = db.ExecContext(ctx, query,
		user.ID, tenant.TenantID, user.Username, user.Password, user.Role, user.MFACode, time.Now(),
	)
	return err
}

func (r *PostgresAuthRepo) RecordAuditLog(ctx context.Context, log *domain.SecurityAuditLog) error {
	db, tenant, err := r.connManager.Resolve(ctx)
	if err != nil {
		return err
	}

	query := `INSERT INTO security_audit_logs (id, tenant_id, username, action, details, timestamp)
			  VALUES ($1, $2, $3, $4, $5, $6)`
	ts := log.Timestamp
	if ts.IsZero() {
		ts = time.Now()
	}
	_, err = db.ExecContext(ctx, query, log.ID, tenant.TenantID, log.Username, log.Action, log.Details, ts)
	return err
}

func (r *PostgresAuthRepo) ListAuditLogs(ctx context.Context) ([]*domain.SecurityAuditLog, error) {
	db, _, err := r.connManager.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	query := `SELECT id, tenant_id, username, action, details, timestamp FROM security_audit_logs ORDER BY timestamp DESC LIMIT 50`
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*domain.SecurityAuditLog
	for rows.Next() {
		var l domain.SecurityAuditLog
		if err := rows.Scan(&l.ID, &l.TenantID, &l.Username, &l.Action, &l.Details, &l.Timestamp); err != nil {
			return nil, err
		}
		logs = append(logs, &l)
	}
	return logs, rows.Err()
}

