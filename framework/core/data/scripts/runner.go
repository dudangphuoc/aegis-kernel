package scripts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"

	"github.com/lib/pq"
)

// Status của một script so với sổ trong DB
type Status string

const (
	StatusApplied Status = "applied"
	StatusPending Status = "pending"
	StatusEdited  Status = "edited" // đã áp nhưng nội dung file đổi (checksum khác) => dừng package
	StatusAhead   Status = "ahead"  // có trong sổ nhưng không có trong package (DB đi trước, VD sau khi hạ version)
)

// ErrMigrationEdited: file đã áp bị sửa; không có lệnh repair, phải thêm file mới
var ErrMigrationEdited = errors.New("migration-edited")

type ScriptState struct {
	Script         string `json:"script"`
	Status         Status `json:"status"`
	RollbackUnsafe bool   `json:"rollbackUnsafe,omitempty"`
}

type PackagePlan struct {
	Package string        `json:"package"`
	Schema  string        `json:"schema"`
	Scripts []ScriptState `json:"scripts"`
}

// Pending đếm script cần áp
func (p PackagePlan) Count(s Status) int {
	n := 0
	for _, st := range p.Scripts {
		if st.Status == s {
			n++
		}
	}
	return n
}

// Runner áp package lên một DB. DSN phải là kết nối trực tiếp (không qua PgBouncer transaction pooling):
// script concurrently cần chạy ngoài transaction trên cùng một session.
type Runner struct {
	DB *sql.DB
	// TrackChanges bật trên DB hub: sau khi áp, gắn trigger capture cho mọi bảng không thuộc localTables
	TrackChanges bool
	Logger       *slog.Logger
}

type ledgerRow struct {
	checksum       string
	rollbackUnsafe bool
}

// EnsureLedger tạo schema platform và sổ platform.migrations (idempotent, an toàn khi nhiều tiến trình cùng gọi)
func (r *Runner) EnsureLedger(ctx context.Context) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('migrate:ledger'))`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `
			CREATE SCHEMA IF NOT EXISTS platform;
			CREATE TABLE IF NOT EXISTS platform.migrations (
				package         text        NOT NULL,
				script          text        NOT NULL,
				checksum        text        NOT NULL,
				package_version text        NOT NULL DEFAULT '',
				rollback_unsafe boolean     NOT NULL DEFAULT false,
				applied_at      timestamptz NOT NULL DEFAULT now(),
				PRIMARY KEY (package, script)
			)`)
		return err
	})
}

// Plan so package với sổ; không ghi gì. DB chưa có sổ => mọi script pending.
func (r *Runner) Plan(ctx context.Context, pkgs ...Package) ([]PackagePlan, error) {
	out := make([]PackagePlan, 0, len(pkgs))
	for _, p := range pkgs {
		ledger, err := r.ledger(ctx, p.Name)
		if err != nil {
			return nil, err
		}
		out = append(out, plan(p, ledger))
	}
	return out, nil
}

func plan(p Package, ledger map[string]ledgerRow) PackagePlan {
	pp := PackagePlan{Package: p.Name, Schema: p.Schema}
	seen := make(map[string]bool, len(p.Scripts))
	for _, s := range p.Scripts {
		seen[s.Name] = true
		st := ScriptState{Script: s.Name, Status: StatusPending, RollbackUnsafe: s.RollbackUnsafe}
		if row, ok := ledger[s.Name]; ok {
			st.Status = StatusApplied
			if row.checksum != s.Checksum {
				st.Status = StatusEdited
			}
		}
		pp.Scripts = append(pp.Scripts, st)
	}
	var ahead []string
	for name := range ledger {
		if !seen[name] {
			ahead = append(ahead, name)
		}
	}
	sort.Strings(ahead)
	for _, name := range ahead {
		pp.Scripts = append(pp.Scripts, ScriptState{Script: name, Status: StatusAhead, RollbackUnsafe: ledger[name].rollbackUnsafe})
	}
	return pp
}

// Apply áp lần lượt từng package theo thứ tự truyền vào (platform -> master/module -> feature).
// Package có script bị sửa hoặc lỗi thì dừng package đó, các package sau vẫn chạy; trả lỗi gộp.
func (r *Runner) Apply(ctx context.Context, pkgs ...Package) ([]PackagePlan, error) {
	if err := r.EnsureLedger(ctx); err != nil {
		return nil, fmt.Errorf("ensure ledger: %w", err)
	}
	var errs []error
	plans := make([]PackagePlan, 0, len(pkgs))
	for _, p := range pkgs {
		pp, err := r.applyPackage(ctx, p)
		plans = append(plans, pp)
		if err != nil {
			errs = append(errs, fmt.Errorf("package %s: %w", p.Name, err))
		}
	}
	return plans, errors.Join(errs...)
}

func (r *Runner) applyPackage(ctx context.Context, p Package) (PackagePlan, error) {
	if vs := Lint(p); len(vs) > 0 {
		msgs := make([]string, len(vs))
		for i, v := range vs {
			msgs[i] = v.String()
		}
		return PackagePlan{Package: p.Name, Schema: p.Schema}, fmt.Errorf("lint failed:\n  %s", strings.Join(msgs, "\n  "))
	}
	ledger, err := r.ledger(ctx, p.Name)
	if err != nil {
		return PackagePlan{}, err
	}
	pp := plan(p, ledger)
	for _, st := range pp.Scripts {
		if st.Status == StatusEdited {
			return pp, fmt.Errorf("%w: %s", ErrMigrationEdited, st.Script)
		}
	}
	if p.Profile == ProfileModule {
		if err := r.provision(ctx, p); err != nil {
			return pp, fmt.Errorf("provision schema %s: %w", p.Schema, err)
		}
	}
	for i, s := range p.Scripts {
		if pp.Scripts[i].Status != StatusPending {
			continue
		}
		if err := r.applyScript(ctx, p, s); err != nil {
			return pp, fmt.Errorf("script %s: %w", s.Name, err)
		}
		pp.Scripts[i].Status = StatusApplied
		r.log().Info("applied script", slog.String("package", p.Name), slog.String("script", s.Name))
	}
	if r.TrackChanges && p.Profile == ProfileModule {
		if err := r.inTx(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `SELECT platform.track_schema($1)`, p.Schema)
			return err
		}); err != nil {
			return pp, fmt.Errorf("track schema %s: %w", p.Schema, err)
		}
	}
	return pp, nil
}

// provision tạo schema của gói cùng outbox/inbox và đăng ký localTables (hàm nằm trong db/platform)
func (r *Runner) provision(ctx context.Context, p Package) error {
	return r.inTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `SELECT platform.provision_schema($1, $2)`, p.Schema, pq.Array(nonNil(p.LocalTables)))
		return err
	})
}

func (r *Runner) applyScript(ctx context.Context, p Package, s Script) error {
	record := func(ex interface {
		ExecContext(context.Context, string, ...any) (sql.Result, error)
	}) error {
		_, err := ex.ExecContext(ctx, `
			INSERT INTO platform.migrations (package, script, checksum, package_version, rollback_unsafe)
			VALUES ($1, $2, $3, $4, $5)`, p.Name, s.Name, s.Checksum, p.Version, s.RollbackUnsafe)
		return err
	}
	searchPath := pq.QuoteIdentifier(p.Schema)
	if p.Schema != "platform" {
		searchPath += ", platform"
	}

	if s.Concurrently {
		// CREATE INDEX CONCURRENTLY không chạy được trong transaction: dùng một session riêng, ghi sổ sau
		conn, err := r.DB.Conn(ctx)
		if err != nil {
			return err
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, fmt.Sprintf(`SET search_path TO %s; SET statement_timeout = '%dmin'`, searchPath, s.TimeoutMinutes)); err != nil {
			return err
		}
		defer conn.ExecContext(context.Background(), `RESET search_path; RESET statement_timeout`)
		if _, err := conn.ExecContext(ctx, s.SQL); err != nil {
			return err
		}
		return record(conn)
	}

	return r.inTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext('migrate:' || $1))`, p.Name); err != nil {
			return err
		}
		// Có thể tiến trình khác vừa áp xong khi đang chờ khoá
		var exists bool
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM platform.migrations WHERE package = $1 AND script = $2)`, p.Name, s.Name).Scan(&exists); err != nil {
			return err
		}
		if exists {
			return nil
		}
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`SET LOCAL lock_timeout = '5s'; SET LOCAL statement_timeout = '%dmin'; SET LOCAL search_path TO %s`, s.TimeoutMinutes, searchPath)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, s.SQL); err != nil {
			return err
		}
		return record(tx)
	})
}

// Digest: sha256 trên các dòng "script checksum" đã áp của package (sắp theo tên), hub báo qua heartbeat
// để cloud biết DB hub có khớp release hay không mà không phải gửi cả sổ.
func (r *Runner) Digest(ctx context.Context, pkg string) (string, error) {
	ledger, err := r.ledger(ctx, pkg)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(ledger))
	for n := range ledger {
		names = append(names, n)
	}
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		fmt.Fprintf(h, "%s %s\n", n, ledger[n].checksum)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func (r *Runner) ledger(ctx context.Context, pkg string) (map[string]ledgerRow, error) {
	var exists bool
	if err := r.DB.QueryRowContext(ctx, `SELECT to_regclass('platform.migrations') IS NOT NULL`).Scan(&exists); err != nil {
		return nil, err
	}
	out := map[string]ledgerRow{}
	if !exists {
		return out, nil
	}
	rows, err := r.DB.QueryContext(ctx, `SELECT script, checksum, rollback_unsafe FROM platform.migrations WHERE package = $1`, pkg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var row ledgerRow
		if err := rows.Scan(&name, &row.checksum, &row.rollbackUnsafe); err != nil {
			return nil, err
		}
		out[name] = row
	}
	return out, rows.Err()
}

func (r *Runner) inTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := r.DB.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (r *Runner) log() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
