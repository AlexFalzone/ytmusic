// One Throttle per service: two would each let the full rate through.
package throttle

import (
	"context"
	"sync"
	"time"
)

type Throttle struct {
	interval time.Duration

	mu   sync.Mutex
	next time.Time
}

func New(interval time.Duration) *Throttle {
	return &Throttle{interval: interval}
}

// Each caller books a slot and waits without the lock, so a cancelled one leaves at once.
func (t *Throttle) Wait(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	t.mu.Lock()
	slot := time.Now()
	if t.next.After(slot) {
		slot = t.next
	}
	t.next = slot.Add(t.interval)
	t.mu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
