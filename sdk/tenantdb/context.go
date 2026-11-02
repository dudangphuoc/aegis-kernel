package dbaudit

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
)

// RequestContext là ngữ cảnh của request được gắn vào transaction để trigger ghi cùng thay đổi.
// Nó không quyết định CÓ audit hay không — việc đó do trigger ở tầng DB đảm nhận.
type RequestContext struct {
	mu           sync.Mutex
	TenantID     string
	RequestID    string
	SourceModule string
	Method       string
	Path         string
	Action       string
	Actor        audit.Actor
	Client       audit.ClientInfo
}

type ctxKey struct{}

// WithRequestContext gắn ngữ cảnh request vào context
func WithRequestContext(ctx context.Context, rc *RequestContext) context.Context {
	return context.WithValue(ctx, ctxKey{}, rc)
}

// FromContext lấy ngữ cảnh request (nil nếu không có, VD: job nền)
func FromContext(ctx context.Context) *RequestContext {
	rc, _ := ctx.Value(ctxKey{}).(*RequestContext)
	return rc
}

// SetAction đặt tên nghiệp vụ (VD: REGISTER_PARTY) cho các thay đổi ghi sau đó
func SetAction(ctx context.Context, name string) {
	if rc := FromContext(ctx); rc != nil {
		rc.mu.Lock()
		rc.Action = name
		rc.mu.Unlock()
	}
}

// RequestContextFromInvocation dựng ngữ cảnh từ InvocationRequest (header do Gateway đặt sau khi xác thực JWT)
func RequestContextFromInvocation(module string, req contract.InvocationRequest) *RequestContext {
	actorID := req.Headers[contract.HeaderActorID]
	if actorID == "" {
		actorID = "ANONYMOUS"
	}
	return &RequestContext{
		TenantID:     req.Tenant.TenantID,
		RequestID:    req.Headers[contract.HeaderRequestID],
		SourceModule: module,
		Method:       req.Method,
		Path:         req.Path,
		Actor: audit.Actor{
			ID:            actorID,
			Username:      req.Headers[contract.HeaderActorUsername],
			Role:          req.Headers[contract.HeaderActorRole],
			SecurityLevel: req.Headers[contract.HeaderActorSecLevel],
		},
		Client: audit.ClientInfo{
			IP:        req.Headers[contract.HeaderClientIP],
			UserAgent: req.Headers[contract.HeaderUserAgent],
			Details:   ClientDetails(req.Headers),
		},
	}
}

// storedContext là dạng JSON lưu trong aegis_audit_outbox.context
type storedContext struct {
	TenantID     string           `json:"tenant_id,omitempty"`
	RequestID    string           `json:"request_id,omitempty"`
	SourceModule string           `json:"source_module,omitempty"`
	Method       string           `json:"action_method,omitempty"`
	Path         string           `json:"action_path,omitempty"`
	Action       string           `json:"action_name,omitempty"`
	Actor        audit.Actor      `json:"actor"`
	Client       audit.ClientInfo `json:"client"`
}

func (rc *RequestContext) marshal() (string, error) {
	rc.mu.Lock()
	sc := storedContext{
		TenantID: rc.TenantID, RequestID: rc.RequestID, SourceModule: rc.SourceModule,
		Method: rc.Method, Path: rc.Path, Action: rc.Action, Actor: rc.Actor, Client: rc.Client,
	}
	rc.mu.Unlock()
	b, err := json.Marshal(sc)
	return string(b), err
}

// clientDetailHeaders: header (canonical) client được phép khai báo -> key trong client_info
var clientDetailHeaders = map[string]string{
	"X-Client-App":       "app",
	"X-Client-Version":   "app_version",
	"X-Device-Id":        "device_id",
	"Accept-Language":    "accept_language",
	"Sec-Ch-Ua-Platform": "platform",
	"Sec-Ch-Ua-Mobile":   "mobile",
}

const maxClientDetailLen = 256

// ClientDetails trích metadata client tự khai báo (không xác thực) từ header được phép
func ClientDetails(headers map[string]string) map[string]string {
	var details map[string]string
	for header, key := range clientDetailHeaders {
		v := strings.TrimSpace(headers[header])
		if v == "" {
			continue
		}
		if len(v) > maxClientDetailLen {
			v = v[:maxClientDetailLen]
		}
		if details == nil {
			details = make(map[string]string)
		}
		details[key] = v
	}
	return details
}
