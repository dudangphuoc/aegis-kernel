package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	_ "github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/modules/auth/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
)

// PostgresAuthRepo làm việc trên schema biz_auth của DB tenant. Thay đổi bảng users được trigger capture
// ghi lại cùng transaction; mật khẩu / mã MFA bị che ở tầng audit (AuditTables), repository không tự ghi audit.
type PostgresAuthRepo struct {
	dbm *tenantdb.Manager
}

func NewPostgresAuthRepo(dbm *tenantdb.Manager) *PostgresAuthRepo {
	return &PostgresAuthRepo{dbm: dbm}
}

func (r *PostgresAuthRepo) GetUserByUsername(ctx context.Context, username string) (*domain.UserAccount, error) {
	db, tenant, err := r.dbm.Resolve(ctx)
	if err != nil {
		return nil, err
	}
	var u domain.UserAccount
	err = db.Read(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `SELECT id, tenant_id, username, password, role, mfa_code, created_at FROM users WHERE username = $1`, username).
			Scan(&u.ID, &u.TenantID, &u.Username, &u.Password, &u.Role, &u.MFACode, &u.CreatedAt)
	})
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
	return db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO users (id, tenant_id, username, password, role, mfa_code, created_at)
			VALUES ($1, $2, $3, $4, $5, $6, $7)`,
			user.ID, tenant.TenantID, user.Username, user.Password, user.Role, user.MFACode, time.Now())
		return err
	})
}

// RecordSecurityEvent ghi sự kiện bảo mật (đăng nhập...) vào platform.audit_events của tenant
func (r *PostgresAuthRepo) RecordSecurityEvent(ctx context.Context, name, subject, details string) error {
	db, _, err := r.dbm.Resolve(ctx)
	if err != nil {
		return err
	}
	return db.RecordSecurityEvent(ctx, name, subject, details)
}
