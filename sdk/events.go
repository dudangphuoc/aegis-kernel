package sdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

// TopicAuditRecorded là topic Kernel Bus dùng để chuyển audit.Envelope tới module audit
const TopicAuditRecorded = "audit.recorded"

// EventHandlerFunc xử lý sự kiện đã đăng ký; ctx đã mang Tenant Context của Module nhận
type EventHandlerFunc func(ctx context.Context, ev contract.Event) error

type chainKey struct{}

// withCallChain gắn danh sách module đã đi qua vào context
func withCallChain(ctx context.Context, chain []string) context.Context {
	return context.WithValue(ctx, chainKey{}, chain)
}

// CallChain trả danh sách module request/sự kiện hiện tại đã đi qua
func CallChain(ctx context.Context) []string {
	c, _ := ctx.Value(chainKey{}).([]string)
	return c
}

// ParseCallChain đọc header X-Aegis-Call-Chain ("auth,parties")
func ParseCallChain(header string) []string {
	var chain []string
	for _, p := range strings.Split(header, ",") {
		if p = strings.TrimSpace(p); p != "" {
			chain = append(chain, p)
		}
	}
	return chain
}

// Subscribe đăng ký xử lý một topic. Gọi trước sdk.Serve để Kernel biết qua Descriptor.
// Handler sự kiện không có ngữ cảnh request: thay đổi DB trong đó vẫn được trigger ghi, với actor SYSTEM.
func (app *ModuleApp) Subscribe(topic string, handler EventHandlerFunc) {
	app.mu.Lock()
	defer app.mu.Unlock()
	if _, exists := app.subscriptions[topic]; !exists {
		app.descriptor.Subscriptions = append(app.descriptor.Subscriptions, topic)
	}
	app.subscriptions[topic] = handler
}

// Publish phát sự kiện bất đồng bộ tới các Module đăng ký topic qua Kernel Bus (không chặn).
// Trả false nếu outbox đầy. Ở chế độ standalone (không có Kernel) sự kiện chỉ được ghi log.
func (app *ModuleApp) Publish(ctx context.Context, topic, tenantID string, payload any) (bool, error) {
	return app.publish(ctx, newEventID(), topic, tenantID, payload)
}

func (app *ModuleApp) publish(ctx context.Context, eventID, topic, tenantID string, payload any) (bool, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return false, fmt.Errorf("marshal event payload: %w", err)
	}
	ev := contract.Event{
		ID:           eventID,
		Topic:        topic,
		TenantID:     tenantID,
		SourceModule: app.descriptor.Name,
		Chain:        appendHop(CallChain(ctx), app.descriptor.Name),
		Payload:      body,
		OccurredAt:   time.Now(),
	}

	app.mu.RLock()
	busEnabled := app.busEnabled
	app.mu.RUnlock()
	if !busEnabled {
		app.logger.Info("event (no kernel bus, standalone mode)", slog.String("topic", topic), slog.String("payload", string(body)))
		app.outbox.acked([]string{eventID}) // log là đích cuối ở standalone: coi như đã giao
		return true, nil
	}
	if !app.outbox.Add(ev) {
		app.logger.Warn("event outbox full, event dropped", slog.String("topic", topic), slog.String("event_id", ev.ID))
		return false, nil
	}
	return true, nil
}

// PendingEvents trả số sự kiện trong outbox chưa được Kernel ack
func (app *ModuleApp) PendingEvents() int { return app.outbox.Len() }

func (app *ModuleApp) PollEvents(ctx context.Context, req contract.PollEventsRequest) (contract.PollEventsResponse, error) {
	return app.outbox.Poll(ctx, req), nil
}

func (app *ModuleApp) HandleEvent(ctx context.Context, d contract.EventDelivery) error {
	app.mu.RLock()
	handler, ok := app.subscriptions[d.Event.Topic]
	app.mu.RUnlock()
	if !ok {
		return fmt.Errorf("module '%s' has no handler for topic '%s'", app.descriptor.Name, d.Event.Topic)
	}
	// Cycle Guard phía Module: không xử lý lại sự kiện đã đi qua chính mình
	if slices.Contains(d.Event.Chain, app.descriptor.Name) {
		return fmt.Errorf("event cycle detected: '%s' already in chain %v", app.descriptor.Name, d.Event.Chain)
	}
	ctx = multitenant.WithTenant(ctx, d.Tenant)
	ctx = withCallChain(ctx, appendHop(d.Event.Chain, app.descriptor.Name))
	return handler(ctx, d.Event)
}

// UseKernelBus chuyển sự kiện (gồm audit từ relay outbox DB) sang outbox để Kernel Bus poll khi chạy dưới Kernel
func (app *ModuleApp) UseKernelBus() {
	app.mu.Lock()
	app.busEnabled = true
	app.mu.Unlock()
}

// PublishAudit implement dbaudit.Publisher: Envelope từ outbox DB -> sự kiện audit.recorded
func (app *ModuleApp) PublishAudit(ctx context.Context, tenantID, eventID string, env audit.Envelope) bool {
	ok, err := app.publish(ctx, eventID, TopicAuditRecorded, tenantID, env)
	return ok && err == nil
}

func appendHop(chain []string, module string) []string {
	out := slices.Clone(chain)
	if len(out) == 0 || out[len(out)-1] != module {
		out = append(out, module)
	}
	return out
}

func newEventID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	return "EVT-" + hex.EncodeToString(b)
}
