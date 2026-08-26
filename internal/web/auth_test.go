package web

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// testClock is a manually advanced clock so expiry can be tested without sleeping.
type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func newTestClock() *testClock {
	return &testClock{t: time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)}
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

func TestSessionCreateReturnsDistinctTokens(t *testing.T) {
	s := newSessionStore(time.Hour)

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		token, err := s.create()
		if err != nil {
			t.Fatalf("create: %v", err)
		}
		if token == "" {
			t.Fatal("token must not be empty")
		}
		if seen[token] {
			t.Fatalf("duplicate token on iteration %d", i)
		}
		seen[token] = true
	}
}

func TestSessionValidateAcceptsFreshToken(t *testing.T) {
	s := newSessionStore(time.Hour)

	token, err := s.create()
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if !s.validate(token) {
		t.Error("a freshly created token must validate")
	}
}

func TestSessionValidateRejectsUnknownToken(t *testing.T) {
	s := newSessionStore(time.Hour)

	if s.validate("not-a-real-token") {
		t.Error("unknown token must not validate")
	}
	if s.validate("") {
		t.Error("empty token must not validate")
	}
}

func TestSessionValidateRejectsExpiredToken(t *testing.T) {
	clock := newTestClock()
	s := newSessionStore(time.Hour)
	s.now = clock.now

	token, _ := s.create()
	clock.advance(time.Hour + time.Second)

	if s.validate(token) {
		t.Error("token past its TTL must not validate")
	}
}

// Sliding expiry: a session in continuous use must never expire.
func TestSessionValidateRenewsExpiry(t *testing.T) {
	clock := newTestClock()
	s := newSessionStore(time.Hour)
	s.now = clock.now

	token, _ := s.create()

	for i := 0; i < 10; i++ {
		clock.advance(50 * time.Minute)
		if !s.validate(token) {
			t.Fatalf("token expired on use %d despite continuous use", i)
		}
	}
}

func TestSessionRevoke(t *testing.T) {
	s := newSessionStore(time.Hour)

	token, _ := s.create()
	s.revoke(token)

	if s.validate(token) {
		t.Error("revoked token must not validate")
	}
}

func TestSessionGCRemovesOnlyExpired(t *testing.T) {
	clock := newTestClock()
	s := newSessionStore(time.Hour)
	s.now = clock.now

	old, _ := s.create()
	clock.advance(2 * time.Hour)
	fresh, _ := s.create()

	s.gc()

	if s.validate(old) {
		t.Error("expired session must be gone after gc")
	}
	if !s.validate(fresh) {
		t.Error("gc must not remove a live session")
	}
}

func TestSessionStoreConcurrentUse(t *testing.T) {
	s := newSessionStore(time.Hour)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := s.create()
			if err != nil {
				t.Errorf("create: %v", err)
				return
			}
			s.validate(token)
			s.gc()
			s.revoke(token)
		}()
	}
	wg.Wait()
}

// The GC loop must have a clear exit: a leaked goroutine outlives every job.
func TestSessionGCLoopStopsOnContextCancel(t *testing.T) {
	s := newSessionStore(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		s.runGC(ctx, time.Millisecond)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runGC did not return after context cancellation")
	}
}

func TestSessionGCLoopCollectsWhileRunning(t *testing.T) {
	clock := newTestClock()
	s := newSessionStore(time.Millisecond)
	s.now = clock.now

	token, _ := s.create()
	clock.advance(time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.runGC(ctx, time.Millisecond)

	deadline := time.After(2 * time.Second)
	for {
		s.mu.Lock()
		_, present := s.sessions[token]
		s.mu.Unlock()
		if !present {
			return
		}
		select {
		case <-deadline:
			t.Fatal("expired session still present: the loop is not calling gc")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestCheckPassword(t *testing.T) {
	hash, err := hashPassword("correct horse battery staple", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	tests := []struct {
		name     string
		hash     string
		password string
		want     bool
	}{
		{"correct password", hash, "correct horse battery staple", true},
		{"wrong password", hash, "hunter2", false},
		{"empty password", hash, "", false},
		{"malformed hash", "not-a-hash", "correct horse battery staple", false},
		{"empty hash", "", "correct horse battery staple", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := checkPassword(tt.hash, tt.password); got != tt.want {
				t.Errorf("checkPassword() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestHashPasswordUsesProductionCost(t *testing.T) {
	hash, err := HashPassword("whatever")
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if !strings.HasPrefix(hash, "$2a$") {
		t.Errorf("hash must be bcrypt, got %q", hash)
	}

	cost, err := bcrypt.Cost([]byte(hash))
	if err != nil {
		t.Fatalf("bcrypt.Cost: %v", err)
	}
	if cost != bcryptCost {
		t.Errorf("hash cost = %d, want %d", cost, bcryptCost)
	}
}

func TestHashPasswordRejectsEmpty(t *testing.T) {
	if _, err := HashPassword(""); err == nil {
		t.Error("an empty password must be rejected, not hashed")
	}
}
