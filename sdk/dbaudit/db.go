package dbaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
)

// DB bọc *sql.DB của 1 tenant. Mọi lệnh ghi chạy trong transaction đã gắn ngữ cảnh request,
// để trigger ghi actor/IP/request id cùng thay đổi. Không expose ExecContext thô của *sql.DB.
type DB struct {
	db       *sql.DB
	tenantID string
	dsn      string
	onCommit func(tenantID, dsn string)
}

// QueryContext / QueryRowContext: đọc không phát sinh audit
func (d *DB) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	return d.db.QueryContext(ctx, query, args...)
}

func (d *DB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return d.db.QueryRowContext(ctx, query, args...)
}

// ExecContext chạy 1 lệnh ghi trong transaction có ngữ cảnh audit
func (d *DB) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	var res sql.Result
	err := d.InTx(ctx, func(tx *sql.Tx) error {
		var err error
		res, err = tx.ExecContext(ctx, query, args...)
		return err
	})
	return res, err
}

// InTx chạy fn trong 1 transaction; mọi thay đổi trong đó thuộc cùng 1 bản ghi audit (Layer 1)
func (d *DB) InTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if rc := FromContext(ctx); rc != nil {
		payload, err := rc.marshal()
		if err != nil {
			return err
		}
		// is_local = true: chỉ sống trong transaction này (an toàn với PgBouncer transaction pooling)
		if _, err := tx.ExecContext(ctx, `SELECT set_config('`+contextSetting+`', $1, true)`, payload); err != nil {
			return fmt.Errorf("set audit context: %w", err)
		}
	}
	if err := fn(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if d.onCommit != nil {
		d.onCommit(d.tenantID, d.dsn)
	}
	return nil
}

// RecordSecurityEvent ghi sự kiện bảo mật (LOGIN_SUCCESS, LOGIN_FAILED...) vào outbox của tenant.
// Sự kiện không đổi bảng nghiệp vụ nào nên được ghi tường minh, nhưng vẫn đi chung đường ống với thay đổi dữ liệu.
func (d *DB) RecordSecurityEvent(ctx context.Context, name, subject, details string) error {
	ev, err := json.Marshal(audit.SecurityEvent{Name: name, Subject: subject, Details: details, OccurredAt: time.Now()})
	if err != nil {
		return err
	}
	_, err = d.ExecContext(ctx, `
		INSERT INTO aegis_audit_outbox (kind, event, context)
		VALUES ('EVENT', $1::jsonb, NULLIF(current_setting('`+contextSetting+`', true), '')::jsonb)`, string(ev))
	return err
}

func (d *DB) Close() error { return d.db.Close() }
