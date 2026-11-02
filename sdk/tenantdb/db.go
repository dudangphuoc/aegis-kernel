// Package tenantdb cấp cho module kết nối tới DB của tenant, gắn sẵn schema của chính module.
// Module được tách bằng schema (biz_<key>) và search_path, không bằng role: mọi truy vấn chạy trong
// transaction mở bằng set_config('search_path', ..., true), nên an toàn với PgBouncer transaction pooling.
package tenantdb

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
)

// DB là pool tới DB của 1 tenant, chỉ nhìn thấy schema của module (cộng platform cho hàm nền tảng).
// Không expose *sql.DB: mọi truy vấn phải qua Read/Write để có search_path và ngữ cảnh audit.
type DB struct {
	db         *sql.DB
	searchPath string
	onGone     func() // database đã bị xoá (tenant bị xoá): bỏ pool
}

// Read chạy fn trong transaction chỉ đọc
func (d *DB) Read(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return d.run(ctx, true, fn)
}

// Write chạy fn trong transaction ghi. Mọi thay đổi trong đó được trigger capture ghi kèm ngữ cảnh request
// và thuộc cùng một bản ghi audit (Layer 1).
func (d *DB) Write(ctx context.Context, fn func(tx *sql.Tx) error) error {
	return d.run(ctx, false, fn)
}

// RecordSecurityEvent ghi sự kiện bảo mật (LOGIN_SUCCESS, LOGIN_FAILED...) không gắn với dòng dữ liệu nào,
// đi chung đường ống và thứ tự với thay đổi dữ liệu (platform.audit_events).
func (d *DB) RecordSecurityEvent(ctx context.Context, name, subject, details string) error {
	ev, err := json.Marshal(audit.SecurityEvent{Name: name, Subject: subject, Details: details, OccurredAt: time.Now()})
	if err != nil {
		return err
	}
	return d.Write(ctx, func(tx *sql.Tx) error {
		// jsonb truyền dạng string: binary_parameters=yes (PgBouncer) gửi []byte ở định dạng binary
		_, err := tx.ExecContext(ctx, `SELECT platform.record_event('SECURITY', $1)`, string(ev))
		return err
	})
}

func (d *DB) run(ctx context.Context, readOnly bool, fn func(tx *sql.Tx) error) error {
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: readOnly})
	if err != nil {
		return d.gone(err)
	}
	defer func() { _ = tx.Rollback() }()

	payload := ""
	if rc := FromContext(ctx); rc != nil && !readOnly {
		if payload, err = rc.marshal(); err != nil {
			return err
		}
	}
	// is_local = true: chỉ sống trong transaction này
	if _, err := tx.ExecContext(ctx, `SELECT set_config('search_path', $1, true), set_config('`+ContextSetting+`', $2, true)`, d.searchPath, payload); err != nil {
		return d.gone(fmt.Errorf("prepare transaction: %w", err))
	}
	if err := fn(tx); err != nil {
		return err
	}
	return d.gone(tx.Commit())
}

func (d *DB) gone(err error) error {
	if isDatabaseGone(err) && d.onGone != nil {
		d.onGone()
	}
	return err
}

func (d *DB) Close() error { return d.db.Close() }

// isDatabaseGone: database của tenant không còn (tenant đã bị xoá) — thử lại cũng vô ích
func isDatabaseGone(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "3D000" // invalid_catalog_name
}
