package web

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

// sessionTokenBytes is the entropy of a session token. 32 bytes makes guessing
// infeasible, so the token itself is the only credential the cookie carries.
const sessionTokenBytes = 32

// sessionStore keeps live sessions in memory. Sessions are lost on restart,
// which is consistent with jobs also living in memory.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
	ttl      time.Duration
	now      func() time.Time // replaceable in tests
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
		now:      time.Now,
	}
}

// create returns a new session token. The error from the random source is
// propagated rather than raised as a panic: a server that cannot generate a
// token should refuse the login, not die.
func (s *sessionStore) create() (string, error) {
	b := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generating session token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(b)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = s.now().Add(s.ttl)

	return token, nil
}

// validate reports whether the token is live, and extends its lifetime when it
// is: a session in continuous use never expires under the user's hands.
func (s *sessionStore) validate(token string) bool {
	if token == "" {
		return false
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	expiry, ok := s.sessions[token]
	if !ok {
		return false
	}

	now := s.now()
	if now.After(expiry) {
		delete(s.sessions, token)
		return false
	}

	s.sessions[token] = now.Add(s.ttl)
	return true
}

func (s *sessionStore) revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, token)
}

// gc drops expired sessions so the map cannot grow without bound.
func (s *sessionStore) gc() {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for token, expiry := range s.sessions {
		if now.After(expiry) {
			delete(s.sessions, token)
		}
	}
}

// runGC collects expired sessions until ctx is cancelled. It blocks: the caller
// owns the goroutine, so the panic recovery lives where a logger is available.
func (s *sessionStore) runGC(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.gc()
		}
	}
}
