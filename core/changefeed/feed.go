// Package changefeed đọc thay đổi dữ liệu (platform.changes) và sự kiện bảo mật (platform.audit_events)
// từ DB của từng tenant, gom theo transaction thành audit.Envelope rồi giao cho module audit qua Kernel Bus.
//
// Thay cho relay trong từng module (dbaudit): capture nằm ở tầng DB nên mọi lần ghi — kể cả psql, script —
// đều có mặt; Kernel đọc theo cursor (platform.dispatch_cursors, subscriber @audit) và chỉ tiến cursor khi
// module audit đã nhận. Giao lại an toàn vì Envelope ID suy ra từ seq.
package changefeed

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
	"github.com/dudangphuoc/aegis-kernel/sdk/tenantdb"
)

const (
	// Subscriber là tên consumer trong platform.dispatch_cursors
	Subscriber = "@audit"
	// cursorPublisher: mọi consumer đọc platform.changes dùng chung publisher này; changes chỉ bị xoá tới
	// cursor nhỏ nhất của chúng (sau này thêm @cloud cho đồng bộ lên cloud)
	cursorPublisher = "platform"

	batchSize  = 500
	retryEvery = 2 * time.Second
	sweepEvery = 30 * time.Second
)

// Publisher giao sự kiện tới subscriber (Kernel Bus)
type Publisher interface {
	Publish(ctx context.Context, ev contract.Event) error
}

// TenantSource liệt kê tenant và DSN runtime tới DB của tenant
type TenantSource interface {
	TenantIDs(ctx context.Context) ([]string, error)
	TenantDatabaseDSN(ctx context.Context, tenantID string) (string, error)
}

type Feed struct {
	src    TenantSource
	pub    Publisher
	logger *slog.Logger
	cm     *multitenant.ConnectionManager[*sql.DB]

	mu       sync.Mutex
	policies map[string]map[string]contract.AuditTable // schema -> table -> policy
	dirty    map[string]bool

	wake    chan struct{}
	quit    chan struct{}
	done    chan struct{}
	started sync.Once
}

func New(src TenantSource, pub Publisher, logger *slog.Logger) *Feed {
	return &Feed{
		src: src, pub: pub, logger: logger,
		cm: multitenant.NewConnectionManager[*sql.DB](func(_, dsn string) (*sql.DB, error) {
			db, err := sql.Open("postgres", dsn)
			if err != nil {
				return nil, err
			}
			db.SetMaxOpenConns(2)
			db.SetMaxIdleConns(1)
			return db, nil
		}),
		policies: make(map[string]map[string]contract.AuditTable),
		dirty:    make(map[string]bool),
		wake:     make(chan struct{}, 1),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// SetPolicies nạp chính sách audit khai trong descriptor của các module (schema biz_<tên module>)
func (f *Feed) SetPolicies(modules map[string][]contract.AuditTable) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for name, tables := range modules {
		schema := "biz_" + name
		f.policies[schema] = make(map[string]contract.AuditTable, len(tables))
		for _, t := range tables {
			f.policies[schema][t.Table] = t
		}
	}
}

// MarkDirty báo tenant vừa có request (có thể đã ghi): feed đọc lại sớm thay vì chờ lượt quét
func (f *Feed) MarkDirty(tenantID string) {
	if tenantID == "" {
		return
	}
	f.mu.Lock()
	f.dirty[tenantID] = true
	f.mu.Unlock()
	select {
	case f.wake <- struct{}{}:
	default:
	}
}

// Start chạy vòng đọc nền và quét mọi tenant ngay lần đầu (giao nốt phần còn tồn từ lần chạy trước)
func (f *Feed) Start() {
	f.started.Do(func() {
		f.sweep()
		go f.run()
	})
}

// Stop dừng vòng đọc sau khi xử lý nốt các tenant đang dirty, rồi đóng pool.
// Gọi sau khi HTTP đã ngừng nhận request và trước khi tắt module audit.
func (f *Feed) Stop(ctx context.Context) error {
	f.started.Do(func() { close(f.done) }) // chưa Start: không có vòng chạy để chờ
	select {
	case <-f.quit:
	default:
		close(f.quit)
	}
	select {
	case <-f.done:
	case <-ctx.Done():
	}
	return f.cm.CloseAll()
}

func (f *Feed) run() {
	defer close(f.done)
	retry := time.NewTicker(retryEvery)
	defer retry.Stop()
	sweep := time.NewTicker(sweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-f.quit:
			f.drainDirty(context.Background())
			return
		case <-f.wake:
		case <-retry.C:
		case <-sweep.C:
			f.sweep()
		}
		f.drainDirty(context.Background())
	}
}

func (f *Feed) sweep() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ids, err := f.src.TenantIDs(ctx)
	if err != nil {
		f.logger.Warn("change feed: list tenants failed", slog.Any("error", err))
		return
	}
	f.mu.Lock()
	for _, id := range ids {
		f.dirty[id] = true
	}
	f.mu.Unlock()
}

func (f *Feed) drainDirty(ctx context.Context) {
	f.mu.Lock()
	ids := make([]string, 0, len(f.dirty))
	for id := range f.dirty {
		ids = append(ids, id)
	}
	f.mu.Unlock()

	for _, id := range ids {
		more, err := f.Drain(ctx, id)
		switch {
		case isDatabaseGone(err), errors.Is(err, contract.ErrTenantNotFound):
			f.logger.Info("tenant removed, dropping it from change feed", slog.String("tenant_id", id), slog.Any("reason", err))
			f.cm.Evict(id)
		case err != nil:
			f.logger.Warn("change feed drain failed", slog.String("tenant_id", id), slog.Any("error", err))
			continue // giữ dirty, thử lại ở tick sau
		case more:
			f.MarkDirty(id)
			continue
		}
		f.mu.Lock()
		delete(f.dirty, id)
		f.mu.Unlock()
	}
}

type row struct {
	seq       int64
	txid      int64
	kind      string
	schema    string
	table     sql.NullString
	op        sql.NullString
	after     []byte
	before    []byte
	event     []byte
	context   []byte
	createdAt time.Time
}

// Drain giao một lô của tenant; more = còn dữ liệu chưa đọc hết
func (f *Feed) Drain(ctx context.Context, tenantID string) (bool, error) {
	dsn, err := f.src.TenantDatabaseDSN(ctx, tenantID)
	if err != nil {
		return false, err
	}
	db, _, err := f.cm.Resolve(multitenant.WithTenant(ctx, contract.TenantRequestContext{TenantID: tenantID, ConnectionString: dsn}))
	if err != nil {
		return false, err
	}

	var cursor int64
	if err := db.QueryRowContext(ctx, `SELECT COALESCE((SELECT last_seq FROM platform.dispatch_cursors WHERE subscriber = $1 AND publisher = $2), 0)`,
		Subscriber, cursorPublisher).Scan(&cursor); err != nil {
		return false, err
	}

	// Một câu SELECT = một snapshot: changes và audit_events dùng chung dãy seq theo thứ tự commit,
	// nên dòng của một transaction luôn liền nhau và không có transaction commit muộn mang seq nhỏ hơn.
	rows, err := db.QueryContext(ctx, `
		SELECT seq, txid, kind, schema_name, table_name, op, row_json, old_json, event, context, created_at FROM (
			SELECT seq, txid, 'CHANGE' AS kind, schema_name, table_name, op, row_json, old_json, NULL::jsonb AS event, context, created_at
			FROM platform.changes WHERE seq > $1
			UNION ALL
			SELECT seq, txid, 'EVENT', schema_name, NULL, NULL, NULL, NULL, event, context, created_at
			FROM platform.audit_events WHERE seq > $1
		) x ORDER BY seq LIMIT $2`, cursor, batchSize)
	if err != nil {
		return false, err
	}
	var batch []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.seq, &r.txid, &r.kind, &r.schema, &r.table, &r.op, &r.after, &r.before, &r.event, &r.context, &r.createdAt); err != nil {
			rows.Close()
			return false, err
		}
		batch = append(batch, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	groups := groupByTx(batch)
	full := len(batch) == batchSize
	if full && len(groups) > 1 {
		groups = groups[:len(groups)-1] // transaction cuối có thể bị LIMIT cắt dở: để lượt sau
	}

	delivered := cursor
	var pubErr error
	for _, g := range groups {
		env, err := f.envelope(tenantID, g)
		if err != nil {
			pubErr = fmt.Errorf("build envelope from seq %d: %w", g[0].seq, err)
			break
		}
		payload, err := json.Marshal(env)
		if err != nil {
			pubErr = err
			break
		}
		if err := f.pub.Publish(ctx, contract.Event{
			ID: env.ID, Topic: contract.TopicAuditRecorded, TenantID: tenantID, SourceModule: "kernel",
			Payload: payload, OccurredAt: env.OccurredAt,
		}); err != nil {
			pubErr = err
			break
		}
		delivered = g[len(g)-1].seq
	}

	if delivered > cursor {
		if err := advance(ctx, db, delivered); err != nil {
			return false, errors.Join(pubErr, fmt.Errorf("advance cursor: %w", err))
		}
	}
	if pubErr != nil {
		return false, pubErr
	}
	return full, nil
}

// advance ghi cursor và dọn dòng mọi consumer đã đọc qua
func advance(ctx context.Context, db *sql.DB, seq int64) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO platform.dispatch_cursors (subscriber, publisher, last_seq) VALUES ($1, $2, $3)
		ON CONFLICT (subscriber, publisher) DO UPDATE SET last_seq = GREATEST(platform.dispatch_cursors.last_seq, EXCLUDED.last_seq)`,
		Subscriber, cursorPublisher, seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM platform.audit_events WHERE seq <= $1`, seq); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM platform.changes
		WHERE seq <= (SELECT min(last_seq) FROM platform.dispatch_cursors WHERE publisher = $1)`, cursorPublisher); err != nil {
		return err
	}
	return tx.Commit()
}

// groupByTx: dòng cùng transaction nằm liền nhau (capture giữ khoá tới commit)
func groupByTx(batch []row) [][]row {
	var groups [][]row
	for i, r := range batch {
		if i == 0 || r.txid != batch[i-1].txid {
			groups = append(groups, nil)
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], r)
	}
	return groups
}

func (f *Feed) envelope(tenantID string, g []row) (audit.Envelope, error) {
	env := audit.Envelope{
		ID:           fmt.Sprintf("AUD-%d", g[0].seq),
		TenantID:     tenantID,
		SourceModule: strings.TrimPrefix(g[0].schema, "biz_"),
		Actor:        audit.Actor{ID: "SYSTEM"}, // ghi không qua SDK (seed, script, psql)
		OccurredAt:   g[0].createdAt,
	}
	ctxSet := false
	for _, r := range g {
		if len(r.context) > 0 && !ctxSet {
			ctxSet = true
			var sc tenantdb.StoredContext
			if err := json.Unmarshal(r.context, &sc); err != nil {
				return env, err
			}
			env.RequestID, env.ActionMethod, env.ActionPath, env.ActionName = sc.RequestID, sc.Method, sc.Path, sc.Action
			env.Actor, env.Client = sc.Actor, sc.Client
			if sc.SourceModule != "" {
				env.SourceModule = sc.SourceModule
			}
		}
		switch r.kind {
		case "CHANGE":
			c, err := f.change(r)
			if err != nil {
				return env, err
			}
			env.Changes = append(env.Changes, c)
		case "EVENT":
			var ev audit.SecurityEvent
			if err := json.Unmarshal(r.event, &ev); err != nil {
				return env, err
			}
			env.Events = append(env.Events, ev)
		}
	}
	return env, nil
}

func (f *Feed) change(r row) (audit.EntityChange, error) {
	f.mu.Lock()
	policy, ok := f.policies[r.schema][r.table.String]
	f.mu.Unlock()
	if !ok {
		policy = contract.AuditTable{Table: r.table.String, Entity: r.table.String, PK: "id"}
	}

	cur, err := decodeRow(r.after)
	if err != nil {
		return audit.EntityChange{}, err
	}
	var before, after map[string]any
	switch r.op.String {
	case audit.OpInsert:
		after = cur
	case audit.OpUpdate:
		after = cur
		if before, err = decodeRow(r.before); err != nil {
			return audit.EntityChange{}, err
		}
	case audit.OpDelete:
		before = cur
	}
	entityID := fmt.Sprint(cur[policy.PK])
	redact(policy, before, after)
	return audit.EntityChange{
		TableName: r.table.String, EntityName: policy.Entity, EntityID: entityID,
		Operation: r.op.String, Before: before, After: after, ChangedAt: r.createdAt,
	}, nil
}

// redact bỏ cột hạ tầng và che cột bí mật: chỉ ghi nhận "có đổi", không ghi giá trị
func redact(p contract.AuditTable, before, after map[string]any) {
	for _, col := range p.Ignore {
		delete(before, col)
		delete(after, col)
	}
	for _, col := range p.Secrets {
		_, inBefore := before[col]
		_, inAfter := after[col]
		changed := inBefore && inAfter && !equalJSON(before[col], after[col])
		if inBefore {
			before[col] = "[REDACTED]"
		}
		if inAfter {
			after[col] = "[REDACTED]"
			if changed {
				after[col] = "[REDACTED:CHANGED]"
			}
		}
	}
}

func equalJSON(a, b any) bool {
	x, _ := json.Marshal(a)
	y, _ := json.Marshal(b)
	return bytes.Equal(x, y)
}

// decodeRow giữ số dạng chuỗi gốc (json.Number) để không mất độ chính xác khi chuẩn hoá hash
func decodeRow(raw []byte) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

func isDatabaseGone(err error) bool {
	var pqErr *pq.Error
	return errors.As(err, &pqErr) && pqErr.Code == "3D000"
}
