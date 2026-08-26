package web

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// bcryptCost is deliberately above the library default: a login is a rare
// operation, so ~250ms per attempt is a good trade against offline cracking.
const bcryptCost = 12

// HashPassword returns a bcrypt hash suitable for auth.password_hash.
func HashPassword(password string) (string, error) {
	return hashPassword(password, bcryptCost)
}

// hashPassword takes an explicit cost so tests can use bcrypt.MinCost; at the
// production cost every hash takes ~250ms, which makes a test suite unusable.
func hashPassword(password string, cost int) (string, error) {
	if password == "" {
		return "", fmt.Errorf("password cannot be empty")
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(password), cost)
	if err != nil {
		return "", fmt.Errorf("hashing password: %w", err)
	}
	return string(hash), nil
}

// checkPassword reports whether password matches the bcrypt hash. A malformed
// hash is a mismatch, never a crash.
func checkPassword(hash, password string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
}

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

const (
	sessionCookieName = "ytmusic_session"
	loginPath         = "/login.html"
)

// publicPaths are reachable without a session: the login page, the assets it
// needs, and the health probe.
var publicPaths = map[string]bool{
	loginPath:     true,
	"/login.js":   true,
	"/style.css":  true,
	"/api/login":  true,
	"/api/health": true,
}

// requireAuth gates every request that is not public. API and WebSocket paths
// get a 401; browser navigation gets a redirect to the login page. Answering a
// fetch() with a redirect to HTML would look like a corrupt response, not like
// "you are logged out".
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.config.Auth.Enabled || publicPaths[r.URL.Path] {
			next.ServeHTTP(w, r)
			return
		}

		if s.hasSession(r) {
			next.ServeHTTP(w, r)
			return
		}

		if isAPIPath(r.URL.Path) {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		http.Redirect(w, r, loginPath, http.StatusFound)
	})
}

func (s *Server) hasSession(r *http.Request) bool {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		return false
	}
	return s.sessions.validate(cookie.Value)
}

func isAPIPath(path string) bool {
	return strings.HasPrefix(path, "/api/") || path == "/ws"
}

// isHTTPS reports whether the client connection is encrypted. Proxy headers are
// trusted only when a proxy has been declared: honouring them unconditionally
// would let any client claim an HTTPS connection and get a Secure cookie over
// plaintext.
func isHTTPS(r *http.Request, behindProxy bool) bool {
	if r.TLS != nil {
		return true
	}
	return behindProxy && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r, s.config.BehindProxy),
		Expires:  time.Now().Add(s.config.Auth.TTL()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   isHTTPS(r, s.config.BehindProxy),
		MaxAge:   -1,
	})
}

// Login throttling. Generous enough not to bother a human who mistypes,
// tight enough to make online guessing pointless.
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

// loginLimiter throttles failed logins per client IP.
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

// allow reports whether ip may attempt a login right now.
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

// reset clears the record after a successful login.
func (l *loginLimiter) reset(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.attempts, ip)
}

// gc drops records that are neither locked nor inside their window, so the map
// cannot grow without bound and become its own denial-of-service vector.
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

// clientIP identifies the caller for throttling. X-Forwarded-For is trusted only
// when a proxy is declared: behind a proxy without it every attempt would look
// like the same IP, so the first attacker would lock out the real user.
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

type loginRequestBody struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// invalidCredentials is deliberately the same for a wrong username and a wrong
// password: a different message would tell an attacker which half to keep.
const invalidCredentials = "Invalid credentials"

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !hasJSONContentType(r) {
		http.Error(w, "Content-Type must be application/json", http.StatusUnsupportedMediaType)
		return
	}

	ip := clientIP(r, s.config.BehindProxy)
	if !s.logins.allow(ip) {
		http.Error(w, "Too many failed attempts, try again later", http.StatusTooManyRequests)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<16)
	var req loginRequestBody
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}

	// Both checks always run: returning early on a username mismatch would make
	// a wrong username measurably faster than a wrong password.
	userOK := subtle.ConstantTimeCompare([]byte(req.Username), []byte(s.config.Auth.Username)) == 1
	passOK := checkPassword(s.config.Auth.PasswordHash, req.Password)

	if !userOK || !passOK {
		s.logins.fail(ip)
		s.logger.Warn("failed login attempt from %s", ip)
		http.Error(w, invalidCredentials, http.StatusUnauthorized)
		return
	}

	token, err := s.sessions.create()
	if err != nil {
		s.logger.Error("failed to create session: %v", err)
		http.Error(w, "Internal error", http.StatusInternalServerError)
		return
	}

	s.logins.reset(ip)
	s.setSessionCookie(w, r, token)
	s.logger.Info("login from %s", ip)

	writeJSON(w, map[string]string{"username": s.config.Auth.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Revoke server-side too: clearing the cookie alone would leave a copied
	// token valid until it expired.
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		s.sessions.revoke(cookie.Value)
	}

	s.clearSessionCookie(w, r)
	writeJSON(w, map[string]string{"status": "logged out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]string{"username": s.config.Auth.Username})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
}

// hasJSONContentType guards the endpoints that decode a body. Without it a
// cross-origin form post with text/plain is a simple request: no preflight, no
// protection.
func hasJSONContentType(r *http.Request) bool {
	ct := r.Header.Get("Content-Type")
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = ct[:i]
	}
	return strings.EqualFold(strings.TrimSpace(ct), "application/json")
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already written; nothing left but to record it.
		return
	}
}
