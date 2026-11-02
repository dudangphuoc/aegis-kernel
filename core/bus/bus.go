// Package bus là Kernel Event Bus: long-poll outbox của từng Module, định tuyến sự kiện theo topic
// tới các Module đăng ký (HandleEvent), có retry, Cycle Guard và Drain khi Kernel tắt.
package bus

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

// TenantResolver resolve Tenant Context của Module nhận sự kiện (connection string, features)
type TenantResolver interface {
	ResolveTenantRequestContext(ctx context.Context, tenantID, moduleName string) (contract.TenantRequestContext, error)
}

// Node là 1 Module tham gia Bus
type Node struct {
	Name          string
	Module        contract.MicroModule
	Subscriptions []string
}

// Config tinh chỉnh Bus; giá trị 0 dùng mặc định
type Config struct {
	PollBatch      int
	PollWait       time.Duration
	PollErrorDelay time.Duration
	DeliverRetries int
	RetryBackoff   time.Duration
	DeliverTimeout time.Duration
}

func (c Config) withDefaults() Config {
	if c.PollBatch <= 0 {
		c.PollBatch = 100
	}
	if c.PollWait <= 0 {
		c.PollWait = time.Second
	}
	if c.PollErrorDelay <= 0 {
		c.PollErrorDelay = time.Second
	}
	if c.DeliverRetries <= 0 {
		c.DeliverRetries = 3
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = 100 * time.Millisecond
	}
	if c.DeliverTimeout <= 0 {
		c.DeliverTimeout = 10 * time.Second
	}
	return c
}

// Stats phục vụ giám sát
type Stats struct {
	Delivered    int64 `json:"delivered"`
	DeadLettered int64 `json:"dead_lettered"`
	CycleBlocked int64 `json:"cycle_blocked"`
	Unrouted     int64 `json:"unrouted"`
}

type Bus struct {
	cfg      Config
	resolver TenantResolver
	logger   *slog.Logger

	nodes map[string]Node
	subs  map[string][]string // topic -> module names

	mu      sync.Mutex
	pending map[string][]string // module -> ack IDs chưa gửi lại cho module
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	delivered, deadLettered, cycleBlocked, unrouted atomic.Int64
}

func New(nodes []Node, resolver TenantResolver, cfg Config, logger *slog.Logger) *Bus {
	b := &Bus{
		cfg:      cfg.withDefaults(),
		resolver: resolver,
		logger:   logger,
		nodes:    make(map[string]Node),
		subs:     make(map[string][]string),
		pending:  make(map[string][]string),
	}
	for _, n := range nodes {
		b.nodes[n.Name] = n
		for _, topic := range n.Subscriptions {
			b.subs[topic] = append(b.subs[topic], n.Name)
		}
	}
	return b
}

func (b *Bus) Stats() Stats {
	return Stats{
		Delivered:    b.delivered.Load(),
		DeadLettered: b.deadLettered.Load(),
		CycleBlocked: b.cycleBlocked.Load(),
		Unrouted:     b.unrouted.Load(),
	}
}

// Start chạy 1 poller cho mỗi Module
func (b *Bus) Start() {
	ctx, cancel := context.WithCancel(context.Background())
	b.cancel = cancel
	for name := range b.nodes {
		b.wg.Add(1)
		go func(name string) {
			defer b.wg.Done()
			b.pollLoop(ctx, name)
		}(name)
	}
	b.logger.Info("kernel event bus started", slog.Int("modules", len(b.nodes)), slog.Any("subscriptions", b.subs))
}

// Drain dừng các poller rồi xả hết outbox của mọi Module (kể cả sự kiện đã gửi mà chưa ack).
// Gọi sau khi HTTP server đã dừng nhận request và trước khi Shutdown các Module.
func (b *Bus) Drain(ctx context.Context) error {
	if b.cancel != nil {
		b.cancel()
	}
	b.wg.Wait()

	var errs []error
	for name := range b.nodes {
		if err := b.drainModule(ctx, name); err != nil {
			errs = append(errs, err)
		}
	}
	s := b.Stats()
	b.logger.Info("kernel event bus drained",
		slog.Int64("delivered", s.Delivered), slog.Int64("dead_lettered", s.DeadLettered),
		slog.Int64("cycle_blocked", s.CycleBlocked), slog.Int64("unrouted", s.Unrouted))
	return errors.Join(errs...)
}

func (b *Bus) pollLoop(ctx context.Context, name string) {
	mod := b.nodes[name].Module
	for ctx.Err() == nil {
		resp, err := mod.PollEvents(ctx, contract.PollEventsRequest{
			AckIDs:     b.takeAcks(name),
			Max:        b.cfg.PollBatch,
			WaitMillis: int(b.cfg.PollWait / time.Millisecond),
		})
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			b.logger.Warn("poll events failed", slog.String("module", name), slog.Any("error", err))
			sleepCtx(ctx, b.cfg.PollErrorDelay)
			continue
		}
		b.dispatchBatch(context.Background(), name, resp.Events)
	}
}

func (b *Bus) drainModule(ctx context.Context, name string) error {
	mod := b.nodes[name].Module
	reclaim := true // lần đầu: lấy lại cả sự kiện poller đã nhận nhưng chưa kịp ack
	for {
		resp, err := mod.PollEvents(ctx, contract.PollEventsRequest{
			AckIDs:          b.takeAcks(name),
			Max:             b.cfg.PollBatch,
			ReclaimInflight: reclaim,
		})
		reclaim = false
		if err != nil {
			return err
		}
		if len(resp.Events) == 0 {
			return nil
		}
		b.dispatchBatch(ctx, name, resp.Events)
	}
}

// dispatchBatch giao lần lượt (giữ thứ tự theo publisher) rồi ghi nhận ack.
// Sự kiện được ack kể cả khi dead-letter để không chặn hàng đợi; subscriber phải idempotent.
func (b *Bus) dispatchBatch(ctx context.Context, source string, events []contract.Event) {
	if len(events) == 0 {
		return
	}
	acks := make([]string, 0, len(events))
	for _, ev := range events {
		b.dispatch(ctx, ev)
		acks = append(acks, ev.ID)
	}
	b.mu.Lock()
	b.pending[source] = append(b.pending[source], acks...)
	b.mu.Unlock()
}

func (b *Bus) dispatch(ctx context.Context, ev contract.Event) {
	targets := b.subs[ev.Topic]
	if len(targets) == 0 {
		b.unrouted.Add(1)
		return
	}
	for _, target := range targets {
		// Cycle Guard: không giao lại cho module đã có trong chuỗi, giới hạn độ sâu
		if slices.Contains(ev.Chain, target) || len(ev.Chain) >= contract.MaxCallChainHops {
			b.cycleBlocked.Add(1)
			b.logger.Warn("event cycle blocked", slog.String("event_id", ev.ID), slog.String("topic", ev.Topic),
				slog.String("target", target), slog.Any("chain", ev.Chain))
			continue
		}
		if err := b.deliver(ctx, target, ev); err != nil {
			b.deadLettered.Add(1)
			b.logger.Error("event dead-lettered", slog.String("event_id", ev.ID), slog.String("topic", ev.Topic),
				slog.String("target", target), slog.String("tenant_id", ev.TenantID), slog.Any("error", err))
			continue
		}
		b.delivered.Add(1)
	}
}

func (b *Bus) deliver(ctx context.Context, target string, ev contract.Event) error {
	tenant, err := b.resolver.ResolveTenantRequestContext(ctx, ev.TenantID, target)
	if err != nil {
		return err // tenant không tồn tại/không có module này: retry vô ích
	}
	mod := b.nodes[target].Module
	backoff := b.cfg.RetryBackoff
	for attempt := 1; ; attempt++ {
		dctx, cancel := context.WithTimeout(ctx, b.cfg.DeliverTimeout)
		err = mod.HandleEvent(dctx, contract.EventDelivery{Event: ev, Tenant: tenant})
		cancel()
		if err == nil || attempt >= b.cfg.DeliverRetries || ctx.Err() != nil {
			return err
		}
		sleepCtx(ctx, backoff)
		backoff *= 4
	}
}

func (b *Bus) takeAcks(name string) []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	acks := b.pending[name]
	delete(b.pending, name)
	return acks
}

func sleepCtx(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
	case <-ctx.Done():
	}
}
