package throttle

import (
	"context"
	"testing"
	"time"
)

func TestWaitSpacesCallsOneIntervalApart(t *testing.T) {
	th := New(50 * time.Millisecond)
	start := time.Now()
	for range 3 {
		if err := th.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	// The first call goes at once, the next two one interval apart each.
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("three calls took %v, want at least 100ms", elapsed)
	}
}

func TestWaitSpacesConcurrentCallers(t *testing.T) {
	th := New(50 * time.Millisecond)
	start := time.Now()
	done := make(chan error)
	for range 3 {
		go func() { done <- th.Wait(context.Background()) }()
	}
	for range 3 {
		if err := <-done; err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Errorf("three concurrent callers were let through in %v, want at least 100ms", elapsed)
	}
}

func TestWaitLeavesWhenContextEnds(t *testing.T) {
	th := New(time.Hour)
	if err := th.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := th.Wait(ctx); err != context.DeadlineExceeded {
		t.Errorf("Wait = %v, want %v", err, context.DeadlineExceeded)
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("Wait returned after %v, want it to leave with the context", elapsed)
	}
}

func TestZeroIntervalNeverWaits(t *testing.T) {
	th := New(0)
	start := time.Now()
	for range 100 {
		if err := th.Wait(context.Background()); err != nil {
			t.Fatalf("Wait: %v", err)
		}
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("100 calls took %v with no interval", elapsed)
	}
}
