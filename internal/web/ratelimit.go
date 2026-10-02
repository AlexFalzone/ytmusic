package web

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	maxLoginAttempts = 5
	loginWindow      = 15 * time.Minute
	loginLockout     = 15 * time.Minute
)

type attemptRecord struct {
	count       int
	windowStart time.Time
	lockedUntil time.Time
}

type loginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptRecord
	now      func() time.Time
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{
		attempts: make(map[string]*attemptRecord),
		now:      time.Now,
	}
}

func (l *loginLimiter) allow(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	rec, ok := l.attempts[ip]
	if !ok {
		return true
	}
	return !l.now().Before(rec.lockedUntil)
}

func (l *loginLimiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	rec, ok := l.attempts[ip]
	if !ok || now.Sub(rec.windowStart) > loginWindow {
		l.attempts[ip] = &attemptRecord{count: 1, windowStart: now}
		return
	}

	rec.count++
	if rec.count >= maxLoginAttempts {
		rec.lockedUntil = now.Add(loginLockout)
	}
}

func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

func (l *loginLimiter) gc() {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := l.now()
	for ip, rec := range l.attempts {
		if now.Before(rec.lockedUntil) {
			continue
		}
		if now.Sub(rec.windowStart) > loginWindow {
			delete(l.attempts, ip)
		}
	}
}

// X-Forwarded-For only behind a declared proxy: anyone can forge it otherwise.
func clientIP(r *http.Request, behindProxy bool) string {
	if behindProxy {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			if i := strings.Index(xff, ","); i > 0 {
				return strings.TrimSpace(xff[:i])
			}
			return strings.TrimSpace(xff)
		}
	}

	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
