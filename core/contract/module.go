package contract

import "context"

// ModuleDescriptor chứa metadata định danh module
type ModuleDescriptor struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description"`
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
}
