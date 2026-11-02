package storage

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/lib/pq"

	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/domain"
	"github.com/dudangphuoc/aegis-kernel/modules/audit/internal/ledger"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

// Khóa advisory theo transaction (an toàn với PgBouncer transaction pooling) để tuần tự hóa hash chain
const chainLockSQL = `SELECT pg_advisory_xact_lock(hashtext('aegis_audit_chain'))`

type PostgresAuditRepo struct {
	connManager *multitenant.ConnectionManager[*sql.DB]
}

func NewPostgresAuditRepo() *PostgresAuditRepo {
	cm := multitenant.NewConnectionManager[*sql.DB](func(tenantID, connStr string) (*sql.DB, error) {
		db, err := sql.Open("postgres", connStr)
		if err != nil {
			return nil, fmt.Errorf("failed to open postgres connection for %s: %w", tenantID, err)
		}
		db.SetMaxOpenConns(10)
		db.SetMaxIdleConns(2)
		db.SetConnMaxLifetime(time.Hour)
		// Schema do Control Plane Migrator quản lý (provision / Migrations tab), không migrate trong request path
		return db, nil
	})
	return &PostgresAuditRepo{connManager: cm}
}

// Close đóng toàn bộ DB pool của mọi Tenant (gọi khi module Shutdown)
func (r *PostgresAuditRepo) Close() error {
	return r.connManager.CloseAll()
}

// Ingest ghi 1 Envelope vào 3 layer trong 1 transaction và nối tiếp hash chain.
// Idempotent theo envelope ID: giao lại cùng envelope (at-least-once) không tạo bản ghi trùng.
func (r *PostgresAuditRepo) Ingest(ctx context.Context, env audit.Envelope) error {
	db, tenant, err := r.connManager.Resolve(ctx)
	if err != nil {
		return err
	}
	if env.TenantID != tenant.TenantID {
		return domain.ErrTenantMismatch
	}

	rec, changes, err := toRecords(env)
	if err != nil {
		return err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, chainLockSQL); err != nil {
		return fmt.Errorf("acquire audit chain lock: %w", err)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO audits (id, tenant_id, request_id, actor_id, actor_username, actor_role, actor_security_level,
			client_ip, user_agent, client_info, source_module, action_method, action_path, action_name, status_code,
			security_events, occurred_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17)
		ON CONFLICT (id) DO NOTHING`,
		rec.ID, rec.TenantID, rec.RequestID, rec.ActorID, rec.ActorUsername, rec.ActorRole, rec.ActorSecurityLevel,
		rec.ClientIP, rec.UserAgent, clientInfoParam(rec.ClientInfo), rec.SourceModule, rec.ActionMethod, rec.ActionPath, rec.ActionName, rec.StatusCode,
		string(rec.SecurityEvents), rec.OccurredAt)
	if err != nil {
		return fmt.Errorf("insert audit: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil // đã ingest trước đó
	}

	prev := ledger.GenesisHash
	if err := tx.QueryRowContext(ctx, `SELECT integrity_hash FROM entity_audit_logs ORDER BY seq DESC LIMIT 1`).Scan(&prev); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read chain head: %w", err)
	}

	auditDigest := ledger.AuditDigest(rec)
	appendLog := func(changeID *string, changeDigest string, field, oldV, newV *string) error {
		hash := ledger.ChainHash(prev, rec.TenantID, rec.ID, auditDigest, changeID, changeDigest, field, oldV, newV)
		_, err := tx.ExecContext(ctx, `
			INSERT INTO entity_audit_logs (id, entity_change_id, audit_id, tenant_id, field_name, old_value, new_value, prev_hash, integrity_hash)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`,
			newID("LOG"), changeID, rec.ID, rec.TenantID, field, oldV, newV, prev, hash)
		if err != nil {
			return fmt.Errorf("insert field log: %w", err)
		}
		prev = hash
		return nil
	}

	logged := 0
	for _, c := range changes {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO entity_changes (id, audit_id, tenant_id, table_name, entity_name, entity_id, operation, before_snapshot, after_snapshot, changed_at)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`,
			c.ID, c.AuditID, rec.TenantID, c.TableName, c.EntityName, c.EntityID, c.Operation, nullJSON(c.Before), nullJSON(c.After), c.ChangedAt); err != nil {
			return fmt.Errorf("insert entity change: %w", err)
		}
		diffs, err := ledger.Diff(c.Before, c.After)
		if err != nil {
			return err
		}
		changeID, changeDigest := c.ID, ledger.ChangeDigest(c)
		for _, d := range diffs {
			field := d.Field
			if err := appendLog(&changeID, changeDigest, &field, d.Old, d.New); err != nil {
				return err
			}
			logged++
		}
	}
	// Audit không có diff (sự kiện bảo mật, UPDATE không đổi gì) vẫn được niêm phong vào chuỗi
	if logged == 0 {
		if err := appendLog(nil, "", nil, nil, nil); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// List trả về các audit mới nhất kèm Layer 2 và Layer 3
func (r *PostgresAuditRepo) List(ctx context.Context, limit int) ([]domain.AuditView, error) {
	db, _, err := r.connManager.Resolve(ctx)
	if err != nil {
		return nil, err
	}

	rows, err := db.QueryContext(ctx, `SELECT `+auditColumns+` FROM audits a ORDER BY a.occurred_at DESC, a.id LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	views := []domain.AuditView{}
	index := make(map[string]int)
	var ids []string
	for rows.Next() {
		rec, err := scanAudit(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		index[rec.ID] = len(views)
		ids = append(ids, rec.ID)
		views = append(views, domain.AuditView{AuditRecord: rec, Changes: []domain.ChangeRecord{}, FieldLogs: []domain.FieldLog{}})
	}
	rows.Close()
	if err := rows.Err(); err != nil || len(ids) == 0 {
		return views, err
	}

	crows, err := db.QueryContext(ctx, `SELECT `+changeColumns+` FROM entity_changes c WHERE c.audit_id = ANY($1) ORDER BY c.changed_at, c.id`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		c, err := scanChange(crows)
		if err != nil {
			crows.Close()
			return nil, err
		}
		v := &views[index[c.AuditID]]
		v.Changes = append(v.Changes, c)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	lrows, err := db.QueryContext(ctx, `SELECT `+logColumns+` FROM entity_audit_logs l WHERE l.audit_id = ANY($1) ORDER BY l.seq`, pq.Array(ids))
	if err != nil {
		return nil, err
	}
	defer lrows.Close()
	for lrows.Next() {
		l, err := scanLog(lrows)
		if err != nil {
			return nil, err
		}
		v := &views[index[l.AuditID]]
		v.FieldLogs = append(v.FieldLogs, l)
	}
	return views, lrows.Err()
}

// VerifyIntegrity duyệt toàn bộ chuỗi theo seq, tính lại digest của Layer 1/2 và hash của Layer 3.
// Phát hiện: sửa/xóa giữa chuỗi, sửa audits/entity_changes/entity_audit_logs.
// Không phát hiện được việc cắt bỏ phần đuôi chuỗi (cần neo head_hash ra ngoài để chống).
func (r *PostgresAuditRepo) VerifyIntegrity(ctx context.Context) (domain.IntegrityReport, error) {
	db, tenant, err := r.connManager.Resolve(ctx)
	if err != nil {
		return domain.IntegrityReport{}, err
	}

	rows, err := db.QueryContext(ctx, `
		SELECT `+logColumns+`, `+auditColumns+`,
			c.id IS NOT NULL, COALESCE(c.id,''), COALESCE(c.audit_id,''), COALESCE(c.table_name,''), COALESCE(c.entity_name,''),
			COALESCE(c.entity_id,''), COALESCE(c.operation,''), c.before_snapshot, c.after_snapshot, COALESCE(c.changed_at, 'epoch'::timestamptz)
		FROM entity_audit_logs l
		JOIN audits a ON a.id = l.audit_id
		LEFT JOIN entity_changes c ON c.id = l.entity_change_id
		ORDER BY l.seq`)
	if err != nil {
		return domain.IntegrityReport{}, err
	}
	defer rows.Close()

	report := domain.IntegrityReport{Status: "VALID"}
	prev := ledger.GenesisHash
	fail := func(l domain.FieldLog, reason string) (domain.IntegrityReport, error) {
		report.Status, report.BrokenAtSeq, report.BrokenLogID, report.FailureReason = "COMPROMISED", l.Seq, l.ID, reason
		return report, nil
	}

	for rows.Next() {
		var (
			l         domain.FieldLog
			a         domain.AuditRecord
			c         domain.ChangeRecord
			hasChange bool
			events    []byte
			clientInf []byte
			before    []byte
			after     []byte
		)
		dest := append(logDest(&l), auditDest(&a, &events, &clientInf)...)
		dest = append(dest, &hasChange, &c.ID, &c.AuditID, &c.TableName, &c.EntityName, &c.EntityID, &c.Operation, &before, &after, &c.ChangedAt)
		if err := rows.Scan(dest...); err != nil {
			return report, err
		}
		if a.SecurityEvents, err = ledger.Canonical(events); err != nil {
			return fail(l, "audit security_events is not valid JSON")
		}
		if a.ClientInfo, err = ledger.CanonicalClientInfo(clientInf); err != nil {
			return fail(l, "audit client_info is not valid JSON")
		}

		if l.TenantID != tenant.TenantID || a.TenantID != tenant.TenantID {
			return fail(l, "record belongs to another tenant")
		}
		if l.PrevHash != prev {
			return fail(l, "prev_hash does not link to previous record (record removed or reordered)")
		}

		changeDigest := ""
		if l.EntityChangeID != nil {
			if !hasChange {
				return fail(l, "referenced entity change is missing")
			}
			if c.Before, err = ledger.Canonical(before); err != nil {
				return fail(l, "before_snapshot is not valid JSON")
			}
			if c.After, err = ledger.Canonical(after); err != nil {
				return fail(l, "after_snapshot is not valid JSON")
			}
			changeDigest = ledger.ChangeDigest(c)
		}

		expected := ledger.ChainHash(prev, l.TenantID, l.AuditID, ledger.AuditDigest(a), l.EntityChangeID, changeDigest, l.FieldName, l.OldValue, l.NewValue)
		if expected != l.IntegrityHash {
			return fail(l, "integrity_hash mismatch (audit, entity change or field log was modified)")
		}
		prev = l.IntegrityHash
		report.CheckedLogs++
	}
	if err := rows.Err(); err != nil {
		return report, err
	}
	if report.CheckedLogs > 0 {
		report.HeadHash = prev
	}
	return report, nil
}

const (
	auditColumns = `a.id, a.tenant_id, a.request_id, a.actor_id, a.actor_username, a.actor_role, a.actor_security_level,
		a.client_ip, a.user_agent, a.client_info, a.source_module, a.action_method, a.action_path, a.action_name, a.status_code,
		a.security_events, a.occurred_at`
	changeColumns = `c.id, c.audit_id, c.table_name, c.entity_name, c.entity_id, c.operation, c.before_snapshot, c.after_snapshot, c.changed_at`
	logColumns    = `l.seq, l.id, l.entity_change_id, l.audit_id, l.tenant_id, l.field_name, l.old_value, l.new_value, l.prev_hash, l.integrity_hash, l.created_at`
)

func auditDest(a *domain.AuditRecord, events, clientInfo *[]byte) []any {
	return []any{&a.ID, &a.TenantID, &a.RequestID, &a.ActorID, &a.ActorUsername, &a.ActorRole, &a.ActorSecurityLevel,
		&a.ClientIP, &a.UserAgent, clientInfo, &a.SourceModule, &a.ActionMethod, &a.ActionPath, &a.ActionName, &a.StatusCode,
		events, &a.OccurredAt}
}

func logDest(l *domain.FieldLog) []any {
	return []any{&l.Seq, &l.ID, &l.EntityChangeID, &l.AuditID, &l.TenantID, &l.FieldName, &l.OldValue, &l.NewValue, &l.PrevHash, &l.IntegrityHash, &l.CreatedAt}
}

func scanAudit(rows *sql.Rows) (domain.AuditRecord, error) {
	var a domain.AuditRecord
	var events, clientInfo []byte
	if err := rows.Scan(auditDest(&a, &events, &clientInfo)...); err != nil {
		return a, err
	}
	a.SecurityEvents = events
	ci, err := ledger.CanonicalClientInfo(clientInfo)
	a.ClientInfo = ci
	return a, err
}

func scanChange(rows *sql.Rows) (domain.ChangeRecord, error) {
	var c domain.ChangeRecord
	var before, after []byte
	err := rows.Scan(&c.ID, &c.AuditID, &c.TableName, &c.EntityName, &c.EntityID, &c.Operation, &before, &after, &c.ChangedAt)
	c.Before, c.After = before, after
	return c, err
}

func scanLog(rows *sql.Rows) (domain.FieldLog, error) {
	var l domain.FieldLog
	err := rows.Scan(logDest(&l)...)
	return l, err
}

// toRecords chuyển Envelope sang Layer 1/2 với JSON đã chuẩn hóa và thời gian chuẩn hóa
func toRecords(env audit.Envelope) (domain.AuditRecord, []domain.ChangeRecord, error) {
	events := env.Events
	if events == nil {
		events = []audit.SecurityEvent{}
	}
	eventsJSON, err := ledger.CanonicalValue(events)
	if err != nil {
		return domain.AuditRecord{}, nil, err
	}
	var clientInfo json.RawMessage
	if len(env.Client.Details) > 0 {
		if clientInfo, err = ledger.CanonicalValue(env.Client.Details); err != nil {
			return domain.AuditRecord{}, nil, err
		}
	}
	rec := domain.AuditRecord{
		ID:                 env.ID,
		TenantID:           env.TenantID,
		RequestID:          env.RequestID,
		ActorID:            env.Actor.ID,
		ActorUsername:      env.Actor.Username,
		ActorRole:          env.Actor.Role,
		ActorSecurityLevel: env.Actor.SecurityLevel,
		ClientIP:           env.Client.IP,
		UserAgent:          env.Client.UserAgent,
		ClientInfo:         clientInfo,
		SourceModule:       env.SourceModule,
		ActionMethod:       env.ActionMethod,
		ActionPath:         env.ActionPath,
		ActionName:         env.ActionName,
		StatusCode:         env.StatusCode,
		SecurityEvents:     eventsJSON,
		OccurredAt:         ledger.NormalizeTime(env.OccurredAt),
	}
	if rec.ID == "" {
		return rec, nil, errors.New("audit envelope id is required")
	}

	changes := make([]domain.ChangeRecord, 0, len(env.Changes))
	for _, ch := range env.Changes {
		c := domain.ChangeRecord{
			ID:         newID("CHG"),
			AuditID:    rec.ID,
			TableName:  ch.TableName,
			EntityName: ch.EntityName,
			EntityID:   ch.EntityID,
			Operation:  ch.Operation,
			ChangedAt:  ledger.NormalizeTime(ch.ChangedAt),
		}
		if ch.Before != nil {
			if c.Before, err = ledger.CanonicalValue(ch.Before); err != nil {
				return rec, nil, err
			}
		}
		if ch.After != nil {
			if c.After, err = ledger.CanonicalValue(ch.After); err != nil {
				return rec, nil, err
			}
		}
		changes = append(changes, c)
	}
	return rec, changes, nil
}

// clientInfoParam ghi '{}' khi không có metadata (cột NOT NULL), dạng string vì binary_parameters
func clientInfoParam(b json.RawMessage) string {
	if b == nil {
		return "{}"
	}
	return string(b)
}

// nullJSON truyền JSONB dạng string: với binary_parameters=yes (PgBouncer), []byte bị gửi ở định dạng
// binary và Postgres từ chối jsonb thiếu byte version
func nullJSON(b json.RawMessage) any {
	if b == nil {
		return nil
	}
	return string(b)
}

func newID(prefix string) string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return prefix + "-" + hex.EncodeToString(b)
}
