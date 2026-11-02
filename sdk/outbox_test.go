package sdk

import (
	"context"
	"testing"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

func ev(id string) contract.Event { return contract.Event{ID: id, OccurredAt: time.Now()} }

func ids(r contract.PollEventsResponse) []string {
	var out []string
	for _, e := range r.Events {
		out = append(out, e.ID)
	}
	return out
}

func TestOutboxAckAndRedelivery(t *testing.T) {
	o := NewOutbox(10)
	now := time.Unix(0, 0)
	o.now = func() time.Time { return now }
	o.Add(ev("a"))
	o.Add(ev("b"))
	ctx := context.Background()

	if got := ids(o.Poll(ctx, contract.PollEventsRequest{Max: 1})); len(got) != 1 || got[0] != "a" {
		t.Fatalf("first poll: %v", got)
	}
	// a đang giao, chưa ack: không được trả lại trước khi hết hạn
	if got := ids(o.Poll(ctx, contract.PollEventsRequest{Max: 10})); len(got) != 1 || got[0] != "b" {
		t.Fatalf("second poll: %v", got)
	}
	// ack b; a hết hạn -> giao lại
	now = now.Add(defaultInflightTTL + time.Second)
	if got := ids(o.Poll(ctx, contract.PollEventsRequest{Max: 10, AckIDs: []string{"b"}})); len(got) != 1 || got[0] != "a" {
		t.Fatalf("expired redelivery: %v", got)
	}
	o.Poll(ctx, contract.PollEventsRequest{AckIDs: []string{"a"}})
	if o.Len() != 0 {
		t.Fatalf("acked events must be removed, len=%d", o.Len())
	}
}

func TestOutboxReclaimAndCapacity(t *testing.T) {
	o := NewOutbox(2)
	ctx := context.Background()
	o.Add(ev("a"))
	o.Add(ev("b"))
	if o.Add(ev("c")) || o.Dropped() != 1 {
		t.Fatal("full outbox must drop")
	}
	o.Poll(ctx, contract.PollEventsRequest{Max: 2})
	if got := ids(o.Poll(ctx, contract.PollEventsRequest{Max: 10, ReclaimInflight: true})); len(got) != 2 || got[0] != "a" {
		t.Fatalf("reclaim must redeliver in order: %v", got)
	}
}

func TestOutboxLongPollWakesOnAdd(t *testing.T) {
	o := NewOutbox(10)
	go func() {
		time.Sleep(20 * time.Millisecond)
		o.Add(ev("late"))
	}()
	start := time.Now()
	got := ids(o.Poll(context.Background(), contract.PollEventsRequest{Max: 10, WaitMillis: 2000}))
	if len(got) != 1 || time.Since(start) > time.Second {
		t.Fatalf("long poll should return on add: %v after %v", got, time.Since(start))
	}
	if got := ids(o.Poll(context.Background(), contract.PollEventsRequest{WaitMillis: 30})); len(got) != 0 {
		t.Fatalf("expected empty after timeout: %v", got)
	}
}
