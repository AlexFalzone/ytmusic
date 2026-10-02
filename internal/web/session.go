package web

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"
)

const sessionTokenBytes = 32

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
	ttl      time.Duration
	now      func() time.Time
}

func newSessionStore(ttl time.Duration) *sessionStore {
	return &sessionStore{
		sessions: make(map[string]time.Time),
		ttl:      ttl,
		now:      time.Now,
	}
}

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

// Sliding expiry: a session in use never expires.
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
