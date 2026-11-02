package sdk

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dudangphuoc/aegis-kernel/core/contract"
)

const (
	defaultOutboxCapacity = 4096
	defaultInflightTTL    = 30 * time.Second
	maxPollBatch          = 500
)

// Outbox giữ sự kiện Module đã phát cho tới khi Kernel Bus poll và ack (giao at-least-once).
// Add không bao giờ chặn: đầy thì bỏ sự kiện và tăng bộ đếm Dropped.
type Outbox struct {
	mu          sync.Mutex
	pending     []contract.Event
	inflight    map[string]inflightEvent
	capacity    int
	inflightTTL time.Duration
	notify      chan struct{}
	dropped     atomic.Int64
	now         func() time.Time
	onAck       func(ids []string)
}

type inflightEvent struct {
	ev     contract.Event
	sentAt time.Time
}

func NewOutbox(capacity int) *Outbox {
	if capacity <= 0 {
		capacity = defaultOutboxCapacity
	}
	return &Outbox{
		inflight:    make(map[string]inflightEvent),
		capacity:    capacity,
		inflightTTL: defaultInflightTTL,
		notify:      make(chan struct{}, 1),
		now:         time.Now,
	}
}

// SetAckHandler đăng ký callback khi Kernel ack sự kiện (VD: relay audit xóa dòng outbox DB)
func (o *Outbox) SetAckHandler(fn func(ids []string)) {
	o.mu.Lock()
	o.onAck = fn
	o.mu.Unlock()
}

func (o *Outbox) acked(ids []string) {
	o.mu.Lock()
	fn := o.onAck
	o.mu.Unlock()
	if fn != nil && len(ids) > 0 {
		fn(ids)
	}
}

// Add đưa sự kiện vào hàng đợi, trả false nếu bị bỏ do đầy
func (o *Outbox) Add(ev contract.Event) bool {
	o.mu.Lock()
	if len(o.pending)+len(o.inflight) >= o.capacity {
		o.mu.Unlock()
		o.dropped.Add(1)
		return false
	}
	o.pending = append(o.pending, ev)
	o.mu.Unlock()

	select {
	case o.notify <- struct{}{}:
	default:
	}
	return true
}

func (o *Outbox) Dropped() int64 { return o.dropped.Load() }

// Len trả số sự kiện chưa được ack (đang chờ + đang giao)
func (o *Outbox) Len() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	return len(o.pending) + len(o.inflight)
}

// Poll xử lý ack, trả lại sự kiện giao quá hạn chưa ack, rồi trả tối đa req.Max sự kiện
// (chờ tối đa req.WaitMillis nếu chưa có gì).
func (o *Outbox) Poll(ctx context.Context, req contract.PollEventsRequest) contract.PollEventsResponse {
	max := req.Max
	if max <= 0 || max > maxPollBatch {
		max = maxPollBatch
	}

	o.mu.Lock()
	for _, id := range req.AckIDs {
		delete(o.inflight, id)
	}
	o.requeueLocked(req.ReclaimInflight)
	o.mu.Unlock()
	o.acked(req.AckIDs)

	if req.WaitMillis > 0 {
		timer := time.NewTimer(time.Duration(req.WaitMillis) * time.Millisecond)
		defer timer.Stop()
		for {
			o.mu.Lock()
			ready := len(o.pending) > 0
			o.mu.Unlock()
			if ready {
				break
			}
			select {
			case <-o.notify:
			case <-timer.C:
				return contract.PollEventsResponse{Events: []contract.Event{}}
			case <-ctx.Done():
				return contract.PollEventsResponse{Events: []contract.Event{}}
			}
		}
	}

	o.mu.Lock()
	defer o.mu.Unlock()
	n := min(max, len(o.pending))
	batch := make([]contract.Event, n)
	copy(batch, o.pending[:n])
	o.pending = o.pending[n:]
	now := o.now()
	for _, ev := range batch {
		o.inflight[ev.ID] = inflightEvent{ev: ev, sentAt: now}
	}
	return contract.PollEventsResponse{Events: batch}
}

// requeueLocked đưa sự kiện đang giao (quá hạn, hoặc tất cả nếu all) về đầu hàng đợi
func (o *Outbox) requeueLocked(all bool) {
	var back []contract.Event
	cutoff := o.now().Add(-o.inflightTTL)
	for id, in := range o.inflight {
		if all || in.sentAt.Before(cutoff) {
			back = append(back, in.ev)
			delete(o.inflight, id)
		}
	}
	if len(back) == 0 {
		return
	}
	sortEventsByTime(back)
	o.pending = append(back, o.pending...)
}

func sortEventsByTime(evs []contract.Event) {
	for i := 1; i < len(evs); i++ {
		for j := i; j > 0 && evs[j].OccurredAt.Before(evs[j-1].OccurredAt); j-- {
			evs[j], evs[j-1] = evs[j-1], evs[j]
		}
	}
}
