package memo

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestDoCallsOncePerKey(t *testing.T) {
	var c Cache[string, int]
	calls := 0
	fn := func(v int) func() (int, error) {
		return func() (int, error) { calls++; return v, nil }
	}

	for _, step := range []struct {
		key  string
		val  int
		want int
	}{
		{"a", 1, 1},
		{"a", 2, 1}, // remembered: fn is not called again
		{"b", 3, 3},
	} {
		got, err := c.Do(step.key, fn(step.val))
		if err != nil || got != step.want {
			t.Errorf("Do(%q) = %d, %v, want %d", step.key, got, err, step.want)
		}
	}
	if calls != 2 {
		t.Errorf("fn called %d times, want 2", calls)
	}
}

// A transient failure, a timeout or a 503, must not stick for the whole run.
func TestDoForgetsFailures(t *testing.T) {
	var c Cache[string, int]
	calls := 0
	fn := func() (int, error) {
		calls++
		if calls == 1 {
			return 0, errors.New("temporary")
		}
		return 7, nil
	}

	if _, err := c.Do("k", fn); err == nil {
		t.Fatal("first Do: want the error")
	}
	for range 2 {
		if got, err := c.Do("k", fn); err != nil || got != 7 {
			t.Fatalf("Do = %d, %v, want 7", got, err)
		}
	}
	if calls != 2 {
		t.Errorf("fn called %d times, want 2: once failing, once succeeding", calls)
	}
}

func TestDoIsSafeForConcurrentUse(t *testing.T) {
	var c Cache[int, string]
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			key := i % 5
			got, err := c.Do(key, func() (string, error) { return fmt.Sprint(key), nil })
			if err != nil || got != fmt.Sprint(key) {
				t.Errorf("Do(%d) = %q, %v", key, got, err)
			}
		}()
	}
	wg.Wait()
}
