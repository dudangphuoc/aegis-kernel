package bus_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/bus"
	"github.com/dudangphuoc/aegis-kernel/core/contract"
	"github.com/dudangphuoc/aegis-kernel/sdk"
	"github.com/dudangphuoc/aegis-kernel/sdk/audit"
	"github.com/dudangphuoc/aegis-kernel/sdk/multitenant"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

type resolver struct{}

func (resolver) ResolveTenantRequestContext(_ context.Context, tenantID, module string) (contract.TenantRequestContext, error) {
	if tenantID == "" {
		return contract.TenantRequestContext{}, errors.New("tenant not found")
	}
	return contract.TenantRequestContext{TenantID: tenantID, ConnectionString: module + "_" + tenantID}, nil
}

type recorder struct {
	mu   sync.Mutex
	envs []audit.Envelope
	dsns []string
	fail int // số lần đầu trả lỗi để kiểm tra retry
}

func (r *recorder) handle(ctx context.Context, ev contract.Event) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.fail > 0 {
		r.fail--
		return errors.New("temporary failure")
	}
	var env audit.Envelope
	if err := json.Unmarshal(ev.Payload, &env); err != nil {
		return err
	}
	t, _ := multitenant.FromContext(ctx)
	r.envs = append(r.envs, env)
	r.dsns = append(r.dsns, t.ConnectionString)
	return nil
}

func (r *recorder) count() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.envs)
}

// newApps dựng module "parties" phát audit và module "audit" nhận qua Bus, giống khi chạy dưới Kernel
func newApps(rec *recorder) (*sdk.ModuleApp, *sdk.ModuleApp) {
	producer := sdk.NewModuleApp(contract.ModuleDescriptor{Name: "parties"})
	// Module phát sự kiện qua outbox của SDK; Bus giao tới module audit
	producer.RegisterRoute(contract.RouteDefinition{Method: "POST", Path: "/p"},
		func(ctx context.Context, req contract.InvocationRequest) (contract.InvocationResponse, error) {
			env := audit.Envelope{ID: "AUD-" + time.Now().Format("150405.000000000"), TenantID: req.Tenant.TenantID,
				Changes: []audit.EntityChange{{TableName: "users", EntityID: "U1", Operation: audit.OpInsert}}}
			_, _ = producer.Publish(ctx, sdk.TopicAuditRecorded, req.Tenant.TenantID, env)
			return sdk.JSONResponse(201, nil)
		})
	producer.UseKernelBus()

	consumer := sdk.NewModuleApp(contract.ModuleDescriptor{Name: "audit"})
	consumer.Subscribe(sdk.TopicAuditRecorded, rec.handle)
	consumer.UseKernelBus()
	return producer, consumer
}

func nodes(apps ...*sdk.ModuleApp) []bus.Node {
	var res []bus.Node
	for _, a := range apps {
		d, _ := a.GetDescriptor(context.Background())
		res = append(res, bus.Node{Name: d.Name, Module: a, Subscriptions: d.Subscriptions})
	}
	return res
}

func call(t *testing.T, app *sdk.ModuleApp, tenant string) {
	t.Helper()
	if _, err := app.Execute(context.Background(), contract.InvocationRequest{
		Method: "POST", Path: "/p", Tenant: contract.TenantRequestContext{TenantID: tenant},
	}); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met in time")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestBusDeliversAuditToSubscriberWithItsTenantContext(t *testing.T) {
	rec := &recorder{}
	producer, consumer := newApps(rec)
	b := bus.New(nodes(producer, consumer), resolver{}, bus.Config{PollWait: 50 * time.Millisecond}, quiet)
	b.Start()

	call(t, producer, "acme")
	waitFor(t, func() bool { return rec.count() == 1 })
	if err := b.Drain(context.Background()); err != nil {
		t.Fatal(err)
	}

	if rec.envs[0].TenantID != "acme" || rec.envs[0].Changes[0].EntityID != "U1" {
		t.Fatalf("unexpected envelope: %+v", rec.envs[0])
	}
	if rec.dsns[0] != "audit_acme" {
		t.Fatalf("subscriber must receive its own tenant DSN, got %s", rec.dsns[0])
	}
	waitFor(t, func() bool { return producer.PendingEvents() == 0 })
}

func TestBusRetriesThenSucceeds(t *testing.T) {
	rec := &recorder{fail: 2}
	producer, consumer := newApps(rec)
	b := bus.New(nodes(producer, consumer), resolver{}, bus.Config{PollWait: 50 * time.Millisecond, RetryBackoff: time.Millisecond}, quiet)
	b.Start()
	call(t, producer, "acme")
	waitFor(t, func() bool { return rec.count() == 1 })
	_ = b.Drain(context.Background())
	if s := b.Stats(); s.Delivered != 1 || s.DeadLettered != 0 {
		t.Fatalf("unexpected stats: %+v", s)
	}
}

func TestBusDrainDeliversEverythingPublishedBeforeShutdown(t *testing.T) {
	rec := &recorder{}
	producer, consumer := newApps(rec)
	// PollWait dài: sự kiện nằm trong outbox, chỉ Drain mới giao hết
	b := bus.New(nodes(producer, consumer), resolver{}, bus.Config{PollWait: time.Hour}, quiet)
	b.Start()
	for i := 0; i < 50; i++ {
		call(t, producer, "acme")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := b.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if rec.count() < 50 {
		t.Fatalf("drain lost events: delivered %d of 50", rec.count())
	}
	if producer.PendingEvents() != 0 {
		t.Fatalf("outbox not acknowledged after drain: %d", producer.PendingEvents())
	}
}

func TestBusBlocksEventCycles(t *testing.T) {
	// ping phát "ping.topic", pong nhận rồi phát "pong.topic", ping nhận -> ping đã có trong chain => bị chặn
	ping := sdk.NewModuleApp(contract.ModuleDescriptor{Name: "ping"})
	pong := sdk.NewModuleApp(contract.ModuleDescriptor{Name: "pong"})
	var pingHandled, pongHandled sync.WaitGroup
	pongHandled.Add(1)
	ping.Subscribe("pong.topic", func(ctx context.Context, ev contract.Event) error {
		pingHandled.Done() // không được gọi
		return nil
	})
	pong.Subscribe("ping.topic", func(ctx context.Context, ev contract.Event) error {
		defer pongHandled.Done()
		_, err := pong.Publish(ctx, "pong.topic", ev.TenantID, map[string]string{"reply": "pong"})
		return err
	})
	ping.UseKernelBus()
	pong.UseKernelBus()

	b := bus.New(nodes(ping, pong), resolver{}, bus.Config{PollWait: 50 * time.Millisecond}, quiet)
	b.Start()
	if _, err := ping.Publish(context.Background(), "ping.topic", "acme", map[string]string{"msg": "ping"}); err != nil {
		t.Fatal(err)
	}
	pongHandled.Wait()
	waitFor(t, func() bool { return b.Stats().CycleBlocked == 1 })
	_ = b.Drain(context.Background())
	if s := b.Stats(); s.Delivered != 1 || s.CycleBlocked != 1 {
		t.Fatalf("expected ping->pong delivered and pong->ping blocked, got %+v", s)
	}
}
