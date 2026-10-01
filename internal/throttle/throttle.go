// Package throttle spaces out the requests sent to a service that allows only
// so many per second. A Throttle belongs to the one client of that service:
// two of them would each let the full rate through.
package throttle

import (
	"context"
	"sync"
	"time"
)

// Throttle lets one caller through per interval.
type Throttle struct {
	interval time.Duration

	mu   sync.Mutex
	next time.Time // the earliest moment the next caller may go
}

// New returns a Throttle that lets one caller through per interval.
func New(interval time.Duration) *Throttle {
	return &Throttle{interval: interval}
}

// Wait blocks until the caller may send its request, or until ctx ends. Each
// caller books its own slot and waits for it without holding the lock, so a
// cancelled caller leaves at once instead of queueing behind the others.
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
