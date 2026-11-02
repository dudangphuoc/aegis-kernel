// Package tenanttest dựng DB tenant cho integration test giống Control Plane: một DB, schema platform,
// schema riêng mỗi module, gắn store gốc và bật change capture.
package tenanttest

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/db"
	"github.com/dudangphuoc/aegis-kernel/framework/core/data/scripts"
)

// StoreID là store gốc mà mọi DB test được gắn
const StoreID = "00000000-0000-0000-0000-00000000a001"

// Tenant đảm bảo DB name tồn tại với platform + pkgs đã áp (giữ dữ liệu giữa các lần chạy, script idempotent)
// và trả DSN quản trị (trực tiếp :5432) cùng DSN runtime (qua PgBouncer nếu đặt TEST_PGBOUNCER_ADDR).
func Tenant(t *testing.T, name string, pkgs ...scripts.Package) (adminDSN, runtimeDSN string) {
	t.Helper()
	return prepare(t, name, false, pkgs)
}

// FreshTenant giống Tenant nhưng xoá DB cũ trước (test cần trạng thái sạch)
func FreshTenant(t *testing.T, name string, pkgs ...scripts.Package) (adminDSN, runtimeDSN string) {
	t.Helper()
	return prepare(t, name, true, pkgs)
}

func prepare(t *testing.T, name string, fresh bool, pkgs []scripts.Package) (string, string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	base := os.Getenv("TEST_PG_ADMIN_DSN")
	if base == "" {
		base = "postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable"
	}
	admin, err := sql.Open("postgres", base)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := admin.PingContext(ctx); err != nil {
		t.Skipf("postgres not available: %v", err)
	}
	if fresh {
		if _, err := admin.ExecContext(ctx, "DROP DATABASE IF EXISTS "+pq.QuoteIdentifier(name)+" WITH (FORCE)"); err != nil {
			t.Fatal(err)
		}
	}
	var exists bool
	if err := admin.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, name).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if !exists {
		if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(name)); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatal(err)
		}
	}

	u, _ := url.Parse(base)
	u.Path = "/" + name
	adminDSN := u.String()
	conn, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	platform, err := db.Platform("test")
	if err != nil {
		t.Fatal(err)
	}
	r := &scripts.Runner{DB: conn, TrackChanges: true}
	if _, err := r.Apply(ctx, append([]scripts.Package{platform}, pkgs...)...); err != nil {
		t.Fatalf("apply scripts on %s: %v", name, err)
	}
	if _, err := conn.ExecContext(ctx, `SELECT platform.bind_store($1)`, StoreID); err != nil {
		t.Fatal(err)
	}
	return adminDSN, RuntimeDSN(adminDSN)
}

// RuntimeDSN trả DSN module dùng lúc chạy. Đặt TEST_PGBOUNCER_ADDR (VD: localhost:6432) để đi qua
// PgBouncer kèm binary_parameters=yes như Control Plane, bắt lỗi chỉ có ở transaction pooling.
func RuntimeDSN(adminDSN string) string {
	addr := os.Getenv("TEST_PGBOUNCER_ADDR")
	if addr == "" {
		return adminDSN
	}
	u, err := url.Parse(adminDSN)
	if err != nil {
		return adminDSN
	}
	u.Host = addr
	q := u.Query()
	q.Set("binary_parameters", "yes")
	u.RawQuery = q.Encode()
	return u.String()
}
