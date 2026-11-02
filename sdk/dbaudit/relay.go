package dbaudit

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
)

const (
	relayBatch      = 500
	relayRetryEvery = 2 * time.Second
	// relaySweepEvery: quét lại mọi tenant đã biết để bắt cả lần ghi KHÔNG đi qua SDK (psql, script, seed)
	relaySweepEvery = 30 * time.Second
)

// relay chuyển dòng outbox của từng tenant thành Envelope (1 transaction = 1 Envelope) và đẩy qua Publisher.
// Dòng chỉ bị xóa khi Bus ack (at-least-once); Envelope ID suy ra từ seq nên giao lại vẫn idempotent ở module audit.
type relay struct {
	module string
	m      *Manager
	pub    Publisher
	logger *slog.Logger

	mu       sync.Mutex
	known    map[string]string      // tenant -> dsn đã biết (Initialize + commit), dùng cho quét định kỳ
	dirty    map[string]tenantState // tenant -> cần quét outbox
	inflight map[string]map[int64]bool
	pending  map[string]pendingAck // eventID -> dòng cần xóa khi ack
	acks     []string

	wake chan struct{}
	quit chan struct{}
	done chan struct{}
}

type tenantState struct {
	dsn string
	gen uint64 // tăng mỗi lần có commit mới; chỉ xóa cờ dirty nếu không có commit nào chen vào lúc quét
}

type pendingAck struct {
	tenantID, dsn string
	seqs          []int64
}

func newRelay(module string, m *Manager, pub Publisher, logger *slog.Logger) *relay {
	r := &relay{
		module: module, m: m, pub: pub, logger: logger,
		known:    make(map[string]string),
		dirty:    make(map[string]tenantState),
		inflight: make(map[string]map[int64]bool),
		pending:  make(map[string]pendingAck),
		wake:     make(chan struct{}, 1),
		quit:     make(chan struct{}),
		done:     make(chan struct{}),
	}
	go r.run()
	return r
}

func (r *relay) markDirty(tenantID, dsn string) {
	if tenantID == "" {
		return
	}
	r.mu.Lock()
	r.known[tenantID] = dsn
	st := r.dirty[tenantID]
	st.dsn, st.gen = dsn, st.gen+1
	r.dirty[tenantID] = st
	r.mu.Unlock()
	r.signal()
}

// sweepAll đánh dấu mọi tenant đã biết cần quét outbox
func (r *relay) sweepAll() {
	r.mu.Lock()
	for id, dsn := range r.known {
		st := r.dirty[id]
		st.dsn, st.gen = dsn, st.gen+1
		r.dirty[id] = st
	}
	r.mu.Unlock()
}

// forget bỏ tenant khỏi relay: dòng outbox nằm trong database đã bị xóa nên không còn gì để giao
func (r *relay) forget(tenantID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.known, tenantID)
	delete(r.dirty, tenantID)
	delete(r.inflight, tenantID)
	for id, p := range r.pending {
		if p.tenantID == tenantID {
			delete(r.pending, id)
		}
	}
}

func (r *relay) acked(ids []string) {
	r.mu.Lock()
	r.acks = append(r.acks, ids...)
	r.mu.Unlock()
	r.signal()
}

func (r *relay) signal() {
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

func (r *relay) stop(ctx context.Context) {
	select {
	case <-r.quit:
	default:
		close(r.quit)
	}
	select {
	case <-r.done:
	case <-ctx.Done():
	}
}

func (r *relay) run() {
	defer close(r.done)
	ticker := time.NewTicker(relayRetryEvery)
	defer ticker.Stop()
	sweep := time.NewTicker(relaySweepEvery)
	defer sweep.Stop()
	for {
		select {
		case <-r.quit:
			r.processAcks() // xóa nốt các dòng đã được ack trước khi đóng pool
			return
		case <-r.wake:
		case <-ticker.C:
		case <-sweep.C:
			r.sweepAll()
		}
		r.processAcks()
		r.drainDirty()
	}
}

func (r *relay) processAcks() {
	r.mu.Lock()
	ids := r.acks
	r.acks = nil
	byTenant := make(map[string]*pendingAck)
	for _, id := range ids {
		p, ok := r.pending[id]
		if !ok {
			continue
		}
		delete(r.pending, id)
		agg := byTenant[p.tenantID]
		if agg == nil {
			agg = &pendingAck{tenantID: p.tenantID, dsn: p.dsn}
			byTenant[p.tenantID] = agg
		}
		agg.seqs = append(agg.seqs, p.seqs...)
	}
	r.mu.Unlock()

	for _, p := range byTenant {
		err := r.deleteRows(p)
		r.mu.Lock()
		for _, s := range p.seqs {
			delete(r.inflight[p.tenantID], s) // lỗi xóa: dòng còn trong DB và sẽ được gửi lại (idempotent)
		}
		r.mu.Unlock()
		if isDatabaseGone(err) {
			r.dropTenant(p.tenantID)
			continue
		}
		if err != nil {
			r.logger.Warn("delete acked audit outbox rows failed", slog.String("tenant_id", p.tenantID), slog.Any("error", err))
		}
	}
}

func (r *relay) deleteRows(p *pendingAck) error {
	return r.m.withTenantDB(p.tenantID, p.dsn, func(db *sql.DB) error {
		_, err := db.Exec(`DELETE FROM aegis_audit_outbox WHERE seq = ANY($1)`, pq.Array(p.seqs))
		return err
	})
}

func (r *relay) drainDirty() {
	r.mu.Lock()
	snapshot := make(map[string]tenantState, len(r.dirty))
	for k, v := range r.dirty {
		snapshot[k] = v
	}
	r.mu.Unlock()

	for tenantID, st := range snapshot {
		more, err := r.drainTenant(tenantID, st.dsn)
		if isDatabaseGone(err) {
			r.dropTenant(tenantID)
			continue
		}
		if err != nil {
			r.logger.Warn("audit outbox relay failed", slog.String("tenant_id", tenantID), slog.Any("error", err))
			continue // giữ dirty, thử lại ở tick sau
		}
		if more {
			r.signal()
			continue
		}
		r.mu.Lock()
		if cur, ok := r.dirty[tenantID]; ok && cur.gen == st.gen {
			delete(r.dirty, tenantID)
		}
		r.mu.Unlock()
	}
}

func (r *relay) dropTenant(tenantID string) {
	r.logger.Info("tenant database no longer exists, dropping tenant from audit relay", slog.String("tenant_id", tenantID))
	r.m.Forget(tenantID)
}

type outboxRow struct {
	seq       int64
	txid      int64
	kind      string
	table     sql.NullString
	entity    sql.NullString
	entityID  sql.NullString
	op        sql.NullString
	before    []byte
	after     []byte
	event     []byte
	context   []byte
	createdAt time.Time
}

// drainTenant đẩy các transaction chưa gửi của tenant; more = còn dòng chưa đọc hết
func (r *relay) drainTenant(tenantID, dsn string) (bool, error) {
	r.mu.Lock()
	skip := []int64{} // không dùng nil: pq.Array(nil) gửi NULL và NOT (seq = ANY(NULL)) loại mọi dòng
	for s := range r.inflight[tenantID] {
		skip = append(skip, s)
	}
	r.mu.Unlock()

	var batch []outboxRow
	err := r.m.withTenantDB(tenantID, dsn, func(db *sql.DB) error {
		rows, err := db.Query(`
			SELECT seq, txid, kind, table_name, entity_name, entity_id, operation,
			       before_row, after_row, event, context, created_at
			FROM aegis_audit_outbox WHERE NOT (seq = ANY($1)) ORDER BY seq LIMIT $2`, pq.Array(skip), relayBatch)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var o outboxRow
			if err := rows.Scan(&o.seq, &o.txid, &o.kind, &o.table, &o.entity, &o.entityID, &o.op,
				&o.before, &o.after, &o.event, &o.context, &o.createdAt); err != nil {
				return err
			}
			batch = append(batch, o)
		}
		return rows.Err()
	})
	if err != nil {
		return false, err
	}

	groups := groupByTx(batch)
	full := len(batch) == relayBatch
	if full && len(groups) > 1 {
		groups = groups[:len(groups)-1] // transaction cuối có thể bị LIMIT cắt dở: để lượt sau
	}

	for _, g := range groups {
		env, err := r.envelope(tenantID, g)
		if err != nil {
			return false, fmt.Errorf("build envelope from outbox seq %d: %w", g[0].seq, err)
		}
		seqs := make([]int64, len(g))
		for i, o := range g {
			seqs[i] = o.seq
		}
		eventID := newEventID()

		r.mu.Lock()
		if r.inflight[tenantID] == nil {
			r.inflight[tenantID] = make(map[int64]bool)
		}
		for _, s := range seqs {
			r.inflight[tenantID][s] = true
		}
		r.pending[eventID] = pendingAck{tenantID: tenantID, dsn: dsn, seqs: seqs}
		r.mu.Unlock()

		if !r.pub.PublishAudit(context.Background(), tenantID, eventID, env) {
			r.mu.Lock()
			delete(r.pending, eventID)
			for _, s := range seqs {
				delete(r.inflight[tenantID], s)
			}
			r.mu.Unlock()
			return false, fmt.Errorf("publisher rejected audit envelope %s", env.ID)
		}
	}
	return full, nil
}

// groupByTx gom dòng theo transaction, giữ thứ tự theo seq nhỏ nhất của mỗi transaction
func groupByTx(batch []outboxRow) [][]outboxRow {
	index := make(map[int64]int)
	var groups [][]outboxRow
	for _, o := range batch {
		i, ok := index[o.txid]
		if !ok {
			i = len(groups)
			index[o.txid] = i
			groups = append(groups, nil)
		}
		groups[i] = append(groups[i], o)
	}
	sort.SliceStable(groups, func(a, b int) bool { return groups[a][0].seq < groups[b][0].seq })
	return groups
}

func (r *relay) envelope(tenantID string, g []outboxRow) (audit.Envelope, error) {
	env := audit.Envelope{
		ID:           fmt.Sprintf("AUD-%s-%d", r.module, g[0].seq),
		TenantID:     tenantID,
		SourceModule: r.module,
		Actor:        audit.Actor{ID: "SYSTEM"}, // ghi không qua SDK (seed, script, psql)
		OccurredAt:   g[0].createdAt,
	}
	ctxSet := false
	for _, o := range g {
		if len(o.context) > 0 && !ctxSet {
			ctxSet = true
			var sc storedContext
			if err := json.Unmarshal(o.context, &sc); err != nil {
				return env, err
			}
			env.RequestID, env.ActionMethod, env.ActionPath, env.ActionName = sc.RequestID, sc.Method, sc.Path, sc.Action
			env.Actor, env.Client = sc.Actor, sc.Client
			if sc.SourceModule != "" {
				env.SourceModule = sc.SourceModule
			}
		}
		switch o.kind {
		case "CHANGE":
			before, err := decodeRow(o.before)
			if err != nil {
				return env, err
			}
			after, err := decodeRow(o.after)
			if err != nil {
				return env, err
			}
			env.Changes = append(env.Changes, audit.EntityChange{
				TableName: o.table.String, EntityName: o.entity.String, EntityID: o.entityID.String,
				Operation: o.op.String, Before: before, After: after, ChangedAt: o.createdAt,
			})
		case "EVENT":
			var ev audit.SecurityEvent
			if err := json.Unmarshal(o.event, &ev); err != nil {
				return env, err
			}
			env.Events = append(env.Events, ev)
		}
	}
	return env, nil
}

// decodeRow giữ số dạng chuỗi gốc (json.Number) để không mất độ chính xác khi chuẩn hóa hash
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

func newEventID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "EVT-" + hex.EncodeToString(b)
}
