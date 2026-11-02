package domain

import (
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrTenantMismatch = errors.New("audit envelope tenant does not match request tenant")
	ErrForbidden      = errors.New("tenant admin role required to read audit trail")
)

// AuditRecord là Layer 1 (audits)
type AuditRecord struct {
	ID                 string          `json:"id"`
	TenantID           string          `json:"tenant_id"`
	RequestID          string          `json:"request_id"`
	ActorID            string          `json:"actor_id"`
	ActorUsername      string          `json:"actor_username"`
	ActorRole          string          `json:"actor_role"`
	ActorSecurityLevel string          `json:"actor_security_level"`
	ClientIP           string          `json:"client_ip"`
	UserAgent          string          `json:"user_agent"`
	ClientInfo         json.RawMessage `json:"client_info,omitempty"` // nil khi rỗng: digest của audit cũ ('{}') giữ nguyên
	SourceModule       string          `json:"source_module"`
	ActionMethod       string          `json:"action_method"`
	ActionPath         string          `json:"action_path"`
	ActionName         string          `json:"action_name"`
	StatusCode         int             `json:"status_code"`
	SecurityEvents     json.RawMessage `json:"security_events"` // JSON đã chuẩn hóa
	OccurredAt         time.Time       `json:"occurred_at"`
}

// ChangeRecord là Layer 2 (entity_changes)
type ChangeRecord struct {
	ID         string          `json:"id"`
	AuditID    string          `json:"audit_id"`
	TableName  string          `json:"table_name"`
	EntityName string          `json:"entity_name"`
	EntityID   string          `json:"entity_id"`
	Operation  string          `json:"operation"`
	Before     json.RawMessage `json:"before_snapshot"` // nil nếu INSERT
	After      json.RawMessage `json:"after_snapshot"`  // nil nếu DELETE
	ChangedAt  time.Time       `json:"changed_at"`
}

// FieldLog là Layer 3 (entity_audit_logs)
type FieldLog struct {
	Seq            int64     `json:"seq"`
	ID             string    `json:"id"`
	EntityChangeID *string   `json:"entity_change_id"` // nil với seal của audit không có diff
	AuditID        string    `json:"audit_id"`
	TenantID       string    `json:"tenant_id"`
	FieldName      *string   `json:"field_name"`
	OldValue       *string   `json:"old_value"`
	NewValue       *string   `json:"new_value"`
	PrevHash       string    `json:"prev_hash"`
	IntegrityHash  string    `json:"integrity_hash"`
	CreatedAt      time.Time `json:"created_at"`
}

// AuditView là 1 audit kèm Layer 2 và Layer 3 để trả về API
type AuditView struct {
	AuditRecord
	Changes   []ChangeRecord `json:"entity_changes"`
	FieldLogs []FieldLog     `json:"field_logs"`
}

// IntegrityReport là kết quả kiểm tra hash chain
type IntegrityReport struct {
	Status        string `json:"status"` // VALID | COMPROMISED
	CheckedLogs   int64  `json:"checked_logs"`
	HeadHash      string `json:"head_hash,omitempty"`
	BrokenAtSeq   int64  `json:"broken_at_seq,omitempty"`
	BrokenLogID   string `json:"broken_log_id,omitempty"`
	FailureReason string `json:"failure_reason,omitempty"`
}
