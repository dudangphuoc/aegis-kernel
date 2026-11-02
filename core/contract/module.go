package contract

import (
	"context"
	"time"
)

// ModuleDescriptor chứa metadata định danh module
type ModuleDescriptor struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
	// Subscriptions là các topic sự kiện mà Module muốn Kernel Bus giao tới (qua HandleEvent)
	Subscriptions []string `json:"subscriptions,omitempty"`
}

// TenantModuleConfig cấu hình riêng của một Tenant cho một Module cụ thể
type TenantModuleConfig struct {
	TenantID         string            `json:"tenant_id"`
	ConnectionString string            `json:"connection_string"`
	Features         map[string]bool   `json:"features"`
	Settings         map[string]string `json:"settings"`
}

// ModuleInitConfig được Kernel inject vào Module khi khởi tạo
type ModuleInitConfig struct {
	Name              string                        `json:"name"`
	Version           string                        `json:"version"`
	Environment       string                        `json:"environment"`
	DefaultConnection string                        `json:"default_connection"`
	DefaultFeatures   map[string]bool               `json:"default_features"`
	Tenants           map[string]TenantModuleConfig `json:"tenants"` // Key: TenantID
}

// TenantRequestContext mang thông tin ngữ cảnh của Tenant gửi theo từng Request
type TenantRequestContext struct {
	TenantID         string            `json:"tenant_id"`
	ConnectionString string            `json:"connection_string"`
	Features         map[string]bool   `json:"features"`
	Settings         map[string]string `json:"settings"`
}

// RouteDefinition đặc tả một API endpoint mà Module muốn Kernel Gateway expose ra ngoài
type RouteDefinition struct {
	Method         string `json:"method"`
	Path           string `json:"path"`
	Summary        string `json:"summary"`
	RequireAuth    bool   `json:"require_auth"`
	OptionalTenant bool   `json:"optional_tenant"` // True cho các API cấp phát Tenant hoặc public
	FeatureFlag    string `json:"feature_flag"`
}

// Header nội bộ Gateway gắn vào InvocationRequest.Headers sau khi xác thực JWT.
// Gateway luôn xóa các header này từ client trước khi gắn lại, nên Module có thể tin cậy chúng.
const (
	HeaderActorID       = "X-Actor-Id"
	HeaderActorUsername = "X-Actor-Username"
	HeaderActorRole     = "X-Actor-Role"
	HeaderActorSecLevel = "X-Actor-Security-Level"
	HeaderClientIP      = "X-Client-Ip"
	HeaderUserAgent     = "X-User-Agent"
	HeaderRequestID     = "X-Request-Id"
	// HeaderCallChain liệt kê các module request đã đi qua (VD: "auth,parties") để chặn gọi vòng A -> B -> A
	HeaderCallChain = "X-Aegis-Call-Chain"
)

// MaxCallChainHops giới hạn độ sâu chuỗi gọi/sự kiện giữa các module
const MaxCallChainHops = 8

// Event là sự kiện bất đồng bộ giữa các Module, đi qua Kernel Bus
type Event struct {
	ID           string    `json:"id"`
	Topic        string    `json:"topic"`
	TenantID     string    `json:"tenant_id"`
	SourceModule string    `json:"source_module"`
	Chain        []string  `json:"chain"` // Các module đã xử lý trên đường đi, dùng cho Cycle Guard
	Payload      []byte    `json:"payload"`
	OccurredAt   time.Time `json:"occurred_at"`
}

// PollEventsRequest: Kernel long-poll outbox của Module
type PollEventsRequest struct {
	AckIDs          []string `json:"ack_ids"`          // Sự kiện đã giao xong, Module được phép xóa
	Max             int      `json:"max"`              // Số sự kiện tối đa mỗi lần
	WaitMillis      int      `json:"wait_millis"`      // Chờ tối đa khi outbox rỗng (0 = trả ngay)
	ReclaimInflight bool     `json:"reclaim_inflight"` // Đưa sự kiện đã gửi nhưng chưa ack về lại hàng đợi (dùng khi drain)
}

type PollEventsResponse struct {
	Events []Event `json:"events"`
}

// EventDelivery là sự kiện Kernel giao tới Module đăng ký, kèm Tenant Context đã resolve cho Module đó
type EventDelivery struct {
	Event  Event                `json:"event"`
	Tenant TenantRequestContext `json:"tenant"`
}

// InvocationRequest truyền payload khi Gateway nhận request từ HTTP
type InvocationRequest struct {
	Path        string               `json:"path"`
	Method      string               `json:"method"`
	QueryParams map[string]string    `json:"query_params"`
	Headers     map[string]string    `json:"headers"`
	Body        []byte               `json:"body"`
	Tenant      TenantRequestContext `json:"tenant"`
}

// InvocationResponse trả dữ liệu từ Module về cho Gateway
type InvocationResponse struct {
	StatusCode int               `json:"status_code"`
	Headers    map[string]string `json:"headers"`
	Body       []byte            `json:"body"`
}

// MicroModule là Interface hợp đồng cốt lõi mà mọi Business Module (Microservice) phải tuân thủ
type MicroModule interface {
	GetDescriptor(ctx context.Context) (ModuleDescriptor, error)
	Initialize(ctx context.Context, config ModuleInitConfig) error
	GetRoutes(ctx context.Context) ([]RouteDefinition, error)
	Execute(ctx context.Context, req InvocationRequest) (InvocationResponse, error)
	HealthCheck(ctx context.Context) (bool, error)
	// Shutdown đóng sạch DB pools và flush hàng đợi trước khi tiến trình module kết thúc
	Shutdown(ctx context.Context) error
	// PollEvents trả các sự kiện Module đã phát (outbox) cho Kernel Bus
	PollEvents(ctx context.Context, req PollEventsRequest) (PollEventsResponse, error)
	// HandleEvent xử lý sự kiện thuộc topic Module đã đăng ký
	HandleEvent(ctx context.Context, delivery EventDelivery) error
}
