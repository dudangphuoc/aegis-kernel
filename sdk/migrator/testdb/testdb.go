// Package testdb chuẩn bị database Tenant cho integration test: tạo DB nếu chưa có và chạy migrations.
// Repository không còn tự migrate trong request path, nên test phải làm thay Control Plane.
package testdb

import (
	"context"
	"database/sql"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
)

// EnsureMigrated tạo database của dsn (nếu chưa tồn tại) rồi chạy MigrateUp.
func EnsureMigrated(t *testing.T, dsn string, scripts []migrator.MigrationScript) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("invalid dsn: %v", err)
	}
	dbName := strings.TrimPrefix(u.Path, "/")
	admin := *u
	admin.Path = "/postgres"

	adminDB, err := sql.Open("postgres", admin.String())
	if err != nil {
		t.Fatalf("open admin db: %v", err)
	}
	defer adminDB.Close()

	var exists bool
	if err := adminDB.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname = $1)`, dbName).Scan(&exists); err != nil {
		t.Fatalf("check database %s: %v", dbName, err)
	}
	if !exists {
		if _, err := adminDB.ExecContext(ctx, "CREATE DATABASE "+pq.QuoteIdentifier(dbName)); err != nil {
			t.Fatalf("create database %s: %v", dbName, err)
		}
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatalf("open %s: %v", dbName, err)
	}
	defer db.Close()
	if err := migrator.NewRunner().MigrateUp(ctx, db, scripts); err != nil {
		t.Fatalf("migrate %s: %v", dbName, err)
	}
}

// RuntimeDSN trả DSN mà module dùng lúc chạy. Nếu đặt TEST_PGBOUNCER_ADDR (VD: localhost:6432),
// DSN được chuyển qua PgBouncer kèm binary_parameters=yes giống Control Plane, để test bắt được
// lỗi chỉ xuất hiện ở transaction pooling. Migration vẫn chạy bằng adminDSN.
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
