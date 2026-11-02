// Package audit định nghĩa Envelope — hợp đồng dữ liệu audit giữa module nguồn (relay outbox DB, xem sdk/dbaudit)
// và module audit (ingest 3 layer + hash chain).
package audit

import "time"

const (
	OpInsert = "INSERT"
	OpUpdate = "UPDATE"
	OpDelete = "DELETE"
)

// Actor là người thực hiện request (Gateway giải mã từ JWT)
type Actor struct {
	ID            string `json:"id"` // ANONYMOUS nếu request không có JWT
	Username      string `json:"username,omitempty"`
	Role          string `json:"role,omitempty"`
	SecurityLevel string `json:"security_level,omitempty"`
}

// ClientInfo là ngữ cảnh mạng/thiết bị của request.
// IP do Gateway xác định (tin cậy); Details là metadata client tự khai báo qua header (không xác thực).
type ClientInfo struct {
	IP        string            `json:"ip,omitempty"`
	UserAgent string            `json:"user_agent,omitempty"`
	Details   map[string]string `json:"details,omitempty"` // app, app_version, device_id, accept_language, platform, mobile
}

// EntityChange là 1 thực thể bị tác động kèm snapshot trước/sau
type EntityChange struct {
	TableName  string         `json:"table_name"`
	EntityName string         `json:"entity_name"`
	EntityID   string         `json:"entity_id"`
	Operation  string         `json:"operation"`
	Before     map[string]any `json:"before_snapshot,omitempty"` // nil nếu INSERT
	After      map[string]any `json:"after_snapshot,omitempty"`  // nil nếu DELETE
	ChangedAt  time.Time      `json:"changed_at"`
}

// SecurityEvent là sự kiện bảo mật không gắn với thay đổi dữ liệu (LOGIN_SUCCESS, LOGIN_FAILED...)
type SecurityEvent struct {
	Name       string    `json:"name"`
	Subject    string    `json:"subject,omitempty"` // VD: username đang đăng nhập
	Details    string    `json:"details,omitempty"`
	OccurredAt time.Time `json:"occurred_at"`
}

// Envelope là gói audit của 1 request (Layer 1 + danh sách Layer 2 của cookbook 03)
type Envelope struct {
	ID           string          `json:"id"`
	TenantID     string          `json:"tenant_id"`
	RequestID    string          `json:"request_id,omitempty"`
	SourceModule string          `json:"source_module"`
	ActionMethod string          `json:"action_method"`
	ActionPath   string          `json:"action_path"`
	ActionName   string          `json:"action_name,omitempty"`
	StatusCode   int             `json:"status_code"`
	Actor        Actor           `json:"actor"`
	Client       ClientInfo      `json:"client"`
	Changes      []EntityChange  `json:"entity_changes,omitempty"`
	Events       []SecurityEvent `json:"security_events,omitempty"`
	OccurredAt   time.Time       `json:"occurred_at"`
}
