package dbaudit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/migrator"
	"github.com/dudangphuoc/aegis-kernel/sdk/migrator/testdb"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

const adminDSN = "postgres://postgres:postgres@localhost:5432/dbaudit_test?sslmode=disable"

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

var scripts = []migrator.MigrationScript{
	{Version: 1, Name: "widgets", UpSQL: `CREATE TABLE IF NOT EXISTS widgets (
		id TEXT PRIMARY KEY, name TEXT NOT NULL, secret TEXT NOT NULL DEFAULT '', infra TEXT NOT NULL DEFAULT '')`,
		DownSQL: `DROP TABLE IF EXISTS widgets`},
	Migration(2, Table{Name: "widgets", Entity: "Widget", PK: "id", Ignore: []string{"infra"}, Secrets: []string{"secret"}}),
}

type outboxEntry struct {
	txid    int64
	kind    string
	op      string
	before  map[string]any
	after   map[string]any
	context map[string]any
}

func setup(t *testing.T, pub Publisher) (*Manager, context.Context, *sql.DB) {
	t.Helper()
	testdb.EnsureMigrated(t, adminDSN, scripts)
	raw, err := sql.Open("postgres", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = raw.Close() })
	if _, err := raw.Exec(`TRUNCATE widgets, aegis_audit_outbox RESTART IDENTITY`); err != nil {
		t.Fatal(err)
	}
	m := NewManager("widgets-svc", pub, quiet)
	t.Cleanup(func() { _ = m.Close(context.Background()) })

	ctx := multitenant.WithTenant(context.Background(), contract.TenantRequestContext{TenantID: "acme", ConnectionString: testdb.RuntimeDSN(adminDSN)})
	ctx = WithRequestContext(ctx, &RequestContext{
		TenantID: "acme", RequestID: "req-1", SourceModule: "widgets-svc", Method: "POST", Path: "/widgets",
		Actor:  audit.Actor{ID: "USR-1", Role: "TENANT_ADMIN"},
		Client: audit.ClientInfo{IP: "10.0.0.7", Details: map[string]string{"app": "mobile"}},
	})
	return m, ctx, raw
}

func readOutbox(t *testing.T, raw *sql.DB) []outboxEntry {
	t.Helper()
	rows, err := raw.Query(`SELECT txid, kind, COALESCE(operation,''), before_row, after_row, context FROM aegis_audit_outbox ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var res []outboxEntry
	for rows.Next() {
		var e outboxEntry
		var b, a, c []byte
		if err := rows.Scan(&e.txid, &e.kind, &e.op, &b, &a, &c); err != nil {
			t.Fatal(err)
		}
		_ = json.Unmarshal(b, &e.before)
		_ = json.Unmarshal(a, &e.after)
		_ = json.Unmarshal(c, &e.context)
		res = append(res, e)
	}
	return res
}

func actorOf(e outboxEntry) string {
	if e.context == nil {
		return ""
	}
	return e.context["actor"].(map[string]any)["id"].(string)
}

func TestTriggerCapturesEveryWriteAtDatabaseLayer(t *testing.T) {
	m, ctx, raw := setup(t, nil)
	db, _, err := m.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	upsert := `INSERT INTO widgets (id, name, secret, infra) VALUES ($1, $2, $3, $4)
		ON CONFLICT (id) DO UPDATE SET name = EXCLUDED.name, secret = EXCLUDED.secret, infra = EXCLUDED.infra`

	mustExec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.ExecContext(ctx, q, args...); err != nil {
			t.Fatal(err)
		}
	}
	mustExec(upsert, "W1", "first", "s3cret", "dsn-1")         // 0: INSERT
	mustExec(upsert, "W1", "second", "rotated", "dsn-1")       // 1: upsert -> UPDATE (tên + secret đổi)
	mustExec(upsert, "W1", "second", "rotated", "dsn-2")       // chỉ đổi cột hạ tầng -> không phải thay đổi
	mustExec(`UPDATE widgets SET name = name WHERE id = 'W1'`) // không đổi gì -> bỏ qua
	// 2 lệnh trong 1 transaction -> cùng txid (1 bản ghi Layer 1)
	if err := db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES ('W2', 'a')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES ('W3', 'b')`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// Transaction lỗi -> không để lại dấu vết audit
	_ = db.InTx(ctx, func(tx *sql.Tx) error {
		_, _ = tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES ('W9', 'rolled back')`)
		return errors.New("boom")
	})
	mustExec(`DELETE FROM widgets WHERE id = 'W3'`) // DELETE
	// Ghi trực tiếp, KHÔNG qua SDK/API (VD: psql, script) -> vẫn bị bắt, không có ngữ cảnh
	if _, err := raw.Exec(`UPDATE widgets SET name = 'tampered' WHERE id = 'W1'`); err != nil {
		t.Fatal(err)
	}

	got := readOutbox(t, raw)
	ops := make([]string, len(got))
	for i, e := range got {
		ops[i] = e.op
	}
	if want := "INSERT,UPDATE,INSERT,INSERT,DELETE,UPDATE"; strings.Join(ops, ",") != want {
		t.Fatalf("ops = %v, want %s", ops, want)
	}

	ins, upd := got[0], got[1]
	if ins.after["secret"] != RedactedValue || ins.after["name"] != "first" || ins.before != nil {
		t.Fatalf("insert row not redacted/captured: %+v", ins.after)
	}
	if _, leaked := ins.after["infra"]; leaked {
		t.Fatal("ignored infra column must not be captured")
	}
	if upd.before["name"] != "first" || upd.after["name"] != "second" ||
		upd.before["secret"] != RedactedValue || upd.after["secret"] != RedactedChanged {
		t.Fatalf("upsert update not captured correctly: before=%v after=%v", upd.before, upd.after)
	}
	if actorOf(ins) != "USR-1" || ins.context["request_id"] != "req-1" || ins.context["action_method"] != "POST" {
		t.Fatalf("request context not attached: %v", ins.context)
	}
	if got[2].txid != got[3].txid || got[2].txid == got[1].txid {
		t.Fatal("statements of one transaction must share txid, others must not")
	}
	if got[5].context != nil || got[5].after["name"] != "tampered" {
		t.Fatalf("direct write must be captured without context: %+v", got[5])
	}
}

func TestSecurityEventGoesThroughOutbox(t *testing.T) {
	m, ctx, raw := setup(t, nil)
	db, _, _ := m.Resolve(ctx)
	if err := db.RecordSecurityEvent(ctx, "LOGIN_FAILED", "admin", "bad password"); err != nil {
		t.Fatal(err)
	}
	got := readOutbox(t, raw)
	if len(got) != 1 || got[0].kind != "EVENT" || actorOf(got[0]) != "USR-1" {
		t.Fatalf("unexpected outbox: %+v", got)
	}
}

type fakePublisher struct {
	mu     sync.Mutex
	reject int
	envs   []audit.Envelope
	ids    []string
}

func (f *fakePublisher) PublishAudit(_ context.Context, _ string, eventID string, env audit.Envelope) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.reject > 0 {
		f.reject--
		return false
	}
	f.envs = append(f.envs, env)
	f.ids = append(f.ids, eventID)
	return true
}

func (f *fakePublisher) snapshot() ([]audit.Envelope, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]audit.Envelope(nil), f.envs...), append([]string(nil), f.ids...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func countOutbox(raw *sql.DB) int {
	var n int
	_ = raw.QueryRow(`SELECT count(*) FROM aegis_audit_outbox`).Scan(&n)
	return n
}

func TestRelayGroupsByTransactionAndDeletesOnlyAfterAck(t *testing.T) {
	pub := &fakePublisher{reject: 1} // lần đầu Bus từ chối -> relay phải thử lại
	m, ctx, raw := setup(t, pub)
	// Dòng tồn từ "lần chạy trước": ghi trực tiếp, relay chỉ biết qua RegisterTenants lúc Initialize
	if _, err := raw.Exec(`INSERT INTO widgets (id, name) VALUES ('W0', 'seeded')`); err != nil {
		t.Fatal(err)
	}
	m.RegisterTenants(map[string]contract.TenantModuleConfig{"acme": {ConnectionString: testdb.RuntimeDSN(adminDSN)}})

	db, _, _ := m.Resolve(ctx)
	if err := db.InTx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES ('W1', 'a')`); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, `UPDATE widgets SET name = 'b' WHERE id = 'W1'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	waitFor(t, func() bool { envs, _ := pub.snapshot(); return len(envs) == 2 })
	envs, ids := pub.snapshot()
	seed, req := envs[0], envs[1]
	if seed.Actor.ID != "SYSTEM" || len(seed.Changes) != 1 || seed.Changes[0].EntityID != "W0" {
		t.Fatalf("direct write must become a SYSTEM audit: %+v", seed)
	}
	if req.Actor.ID != "USR-1" || req.RequestID != "req-1" || req.Client.Details["app"] != "mobile" ||
		len(req.Changes) != 2 || req.Changes[0].Operation != audit.OpInsert || req.Changes[1].Operation != audit.OpUpdate {
		t.Fatalf("transaction must become one envelope with its context: %+v", req)
	}
	if !strings.HasPrefix(req.ID, "AUD-widgets-svc-") || req.TenantID != "acme" {
		t.Fatalf("unexpected envelope id/tenant: %s %s", req.ID, req.TenantID)
	}

	// Chưa ack: dòng phải còn nguyên trong DB (không mất nếu module crash)
	time.Sleep(100 * time.Millisecond)
	if n := countOutbox(raw); n != 3 {
		t.Fatalf("rows must stay until acked, got %d", n)
	}
	m.Acked(ids)
	waitFor(t, func() bool { return countOutbox(raw) == 0 })
	if envs, _ := pub.snapshot(); len(envs) != 2 {
		t.Fatalf("in-flight rows must not be re-published, got %d envelopes", len(envs))
	}
}

func TestRelayRedeliversAfterRestartWithSameEnvelopeID(t *testing.T) {
	pub := &fakePublisher{}
	m, ctx, raw := setup(t, pub)
	db, _, _ := m.Resolve(ctx)
	if _, err := db.ExecContext(ctx, `INSERT INTO widgets (id, name) VALUES ('W1', 'a')`); err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { envs, _ := pub.snapshot(); return len(envs) == 1 })
	first, _ := pub.snapshot()
	_ = m.Close(context.Background()) // "crash" trước khi ack

	pub2 := &fakePublisher{}
	m2 := NewManager("widgets-svc", pub2, quiet)
	defer m2.Close(context.Background())
	m2.RegisterTenants(map[string]contract.TenantModuleConfig{"acme": {ConnectionString: testdb.RuntimeDSN(adminDSN)}})
	waitFor(t, func() bool { envs, _ := pub2.snapshot(); return len(envs) == 1 })
	again, ids := pub2.snapshot()
	if again[0].ID != first[0].ID {
		t.Fatalf("redelivery must keep envelope id for idempotent ingest: %s vs %s", again[0].ID, first[0].ID)
	}
	m2.Acked(ids)
	waitFor(t, func() bool { return countOutbox(raw) == 0 })
}

func TestPeriodicSweepCatchesWritesBypassingSDK(t *testing.T) {
	pub := &fakePublisher{}
	m, _, raw := setup(t, pub)
	m.RegisterTenants(map[string]contract.TenantModuleConfig{"acme": {ConnectionString: testdb.RuntimeDSN(adminDSN)}})
	time.Sleep(100 * time.Millisecond) // lượt quét ban đầu: outbox rỗng

	// Tenant không có request nào; ghi lén trực tiếp vào DB
	if _, err := raw.Exec(`INSERT INTO widgets (id, name) VALUES ('X1', 'sneaky')`); err != nil {
		t.Fatal(err)
	}
	m.relay.sweepAll() // giả lập tick quét định kỳ
	m.relay.signal()
	waitFor(t, func() bool { envs, _ := pub.snapshot(); return len(envs) == 1 })
	envs, _ := pub.snapshot()
	if envs[0].Actor.ID != "SYSTEM" || envs[0].Changes[0].EntityID != "X1" {
		t.Fatalf("unexpected envelope: %+v", envs[0])
	}
	if m.cm.Len() != 0 {
		t.Fatal("sweep of an idle tenant must not open a long-lived pool")
	}
}
