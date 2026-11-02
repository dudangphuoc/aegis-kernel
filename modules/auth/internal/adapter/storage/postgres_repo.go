package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/lib/pq"
	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/dbaudit"
)

// PostgresAuthRepo dùng DB đã bọc audit: thay đổi bảng users được trigger ghi vào outbox
// cùng transaction (mật khẩu / mã MFA bị che), repository không tự ghi audit.
type PostgresAuthRepo struct {
	dbm *dbaudit.Manager
}

func NewPostgresAuthRepo(dbm *dbaudit.Manager) *PostgresAuthRepo {
	return &PostgresAuthRepo{dbm: dbm}
}

func (r *PostgresAuthRepo) GetUserByUsername(ctx context.Context, username string) (*domain.UserAccount, error) {
	db, tenant, err := r.dbm.Resolve(ctx)
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
	db, tenant, err := r.dbm.Resolve(ctx)
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

// RecordSecurityEvent ghi sự kiện bảo mật (đăng nhập...) vào outbox audit của tenant
func (r *PostgresAuthRepo) RecordSecurityEvent(ctx context.Context, name, subject, details string) error {
	db, _, err := r.dbm.Resolve(ctx)
	if err != nil {
		return err
	}
	return db.RecordSecurityEvent(ctx, name, subject, details)
}
