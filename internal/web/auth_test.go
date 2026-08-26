package web

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"ytmusic/internal/config"
	"ytmusic/internal/logger"

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

// --- middleware ---

func newTestServer(t *testing.T, modify func(*config.Config)) *Server {
	t.Helper()

	hash, err := hashPassword("hunter2", bcrypt.MinCost)
	if err != nil {
		t.Fatalf("hashPassword: %v", err)
	}

	cfg := config.Config{
		Auth: config.AuthConfig{
			Enabled:      true,
			Username:     "alex",
			PasswordHash: hash,
			SessionTTL:   "1h",
		},
	}
	if modify != nil {
		modify(&cfg)
	}

	return NewServer(context.Background(), NewJobManager(), cfg, logger.New(false))
}

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, "reached")
	})
}

func TestRequireAuthDisabledLetsEverythingThrough(t *testing.T) {
	s := newTestServer(t, func(c *config.Config) { c.Auth.Enabled = false })

	for _, path := range []string{"/", "/api/jobs", "/ws"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.requireAuth(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200 when auth is disabled", path, rec.Code)
		}
	}
}

func TestRequireAuthRejectsAPIWithoutSession(t *testing.T) {
	s := newTestServer(t, nil)

	for _, path := range []string{"/api/jobs", "/api/jobs/abc", "/api/download", "/ws"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.requireAuth(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("%s: got %d, want 401", path, rec.Code)
		}
		if rec.Body.String() == "reached" {
			t.Errorf("%s: handler must not run without a session", path)
		}
	}
}

func TestRequireAuthRedirectsBrowserNavigation(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	s.requireAuth(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("got %d, want 302", rec.Code)
	}
	if got := rec.Header().Get("Location"); got != "/login.html" {
		t.Errorf("Location = %q, want /login.html", got)
	}
}

func TestRequireAuthAllowsPublicPaths(t *testing.T) {
	s := newTestServer(t, nil)

	for _, path := range []string{"/login.html", "/login.js", "/style.css", "/api/login", "/api/health"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		s.requireAuth(okHandler()).ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("%s: got %d, want 200 (public path)", path, rec.Code)
		}
	}
}

func TestRequireAuthAcceptsValidSession(t *testing.T) {
	s := newTestServer(t, nil)

	token, err := s.sessions.create()
	if err != nil {
		t.Fatalf("create session: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	rec := httptest.NewRecorder()
	s.requireAuth(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("got %d, want 200 with a valid session", rec.Code)
	}
}

func TestRequireAuthRejectsForgedSession(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/jobs", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: "forged"})
	rec := httptest.NewRecorder()
	s.requireAuth(okHandler()).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401 for a forged token", rec.Code)
	}
}

// --- cookie flags ---

func setCookieHeader(t *testing.T, s *Server, r *http.Request) string {
	t.Helper()
	rec := httptest.NewRecorder()
	s.setSessionCookie(rec, r, "token-value")
	return rec.Header().Get("Set-Cookie")
}

func TestSessionCookieAlwaysHardened(t *testing.T) {
	s := newTestServer(t, nil)
	header := setCookieHeader(t, s, httptest.NewRequest(http.MethodGet, "/", nil))

	for _, want := range []string{"HttpOnly", "SameSite=Lax", "Path=/"} {
		if !strings.Contains(header, want) {
			t.Errorf("cookie must contain %q, got: %s", want, header)
		}
	}
}

func TestSessionCookieSecureFlag(t *testing.T) {
	tests := []struct {
		name        string
		behindProxy bool
		tls         bool
		forwarded   string
		wantSecure  bool
	}{
		{name: "plain http", wantSecure: false},
		{name: "direct https", tls: true, wantSecure: true},
		{
			name:       "forwarded https but proxy not declared",
			forwarded:  "https",
			wantSecure: false,
		},
		{
			name:        "forwarded https with proxy declared",
			behindProxy: true,
			forwarded:   "https",
			wantSecure:  true,
		},
		{
			name:        "forwarded http with proxy declared",
			behindProxy: true,
			forwarded:   "http",
			wantSecure:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, func(c *config.Config) { c.BehindProxy = tt.behindProxy })

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tt.forwarded != "" {
				req.Header.Set("X-Forwarded-Proto", tt.forwarded)
			}

			header := setCookieHeader(t, s, req)
			gotSecure := strings.Contains(header, "Secure")

			if gotSecure != tt.wantSecure {
				t.Errorf("Secure = %v, want %v (header: %s)", gotSecure, tt.wantSecure, header)
			}
		})
	}
}

// --- login / logout / me ---

func loginRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = "192.0.2.10:5555"
	return req
}

func TestLoginSucceedsWithCorrectCredentials(t *testing.T) {
	s := newTestServer(t, nil)

	rec := httptest.NewRecorder()
	s.handleLogin(rec, loginRequest(`{"username":"alex","password":"hunter2"}`))

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200. body: %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Header().Get("Set-Cookie")
	if !strings.Contains(cookie, sessionCookieName) {
		t.Fatalf("expected a session cookie, got: %q", cookie)
	}
}

func TestLoginRejectsBadCredentialsIdentically(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"wrong password", `{"username":"alex","password":"wrong"}`},
		{"wrong username", `{"username":"eve","password":"hunter2"}`},
		{"both wrong", `{"username":"eve","password":"wrong"}`},
		{"empty credentials", `{"username":"","password":""}`},
	}

	var messages []string
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := newTestServer(t, nil)
			rec := httptest.NewRecorder()
			s.handleLogin(rec, loginRequest(tt.body))

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("got %d, want 401", rec.Code)
			}
			if rec.Header().Get("Set-Cookie") != "" {
				t.Error("a failed login must not set a session cookie")
			}
			messages = append(messages, rec.Body.String())
		})
	}

	// A different message for a wrong username would reveal which half was wrong.
	for i := 1; i < len(messages); i++ {
		if messages[i] != messages[0] {
			t.Errorf("error messages differ between cases: %q vs %q", messages[0], messages[i])
		}
	}
}

func TestLoginRequiresJSONContentType(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodPost, "/api/login", strings.NewReader(`{"username":"alex","password":"hunter2"}`))
	req.Header.Set("Content-Type", "text/plain")
	req.RemoteAddr = "192.0.2.10:5555"

	rec := httptest.NewRecorder()
	s.handleLogin(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("got %d, want 415", rec.Code)
	}
	if rec.Header().Get("Set-Cookie") != "" {
		t.Error("must not authenticate a request with the wrong content type")
	}
}

func TestLoginRejectsMalformedBody(t *testing.T) {
	s := newTestServer(t, nil)

	rec := httptest.NewRecorder()
	s.handleLogin(rec, loginRequest(`not json`))

	if rec.Code != http.StatusBadRequest {
		t.Errorf("got %d, want 400", rec.Code)
	}
}

func TestLoginLocksOutAfterRepeatedFailures(t *testing.T) {
	s := newTestServer(t, nil)

	for i := 0; i < maxLoginAttempts; i++ {
		rec := httptest.NewRecorder()
		s.handleLogin(rec, loginRequest(`{"username":"alex","password":"wrong"}`))
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: got %d, want 401", i+1, rec.Code)
		}
	}

	rec := httptest.NewRecorder()
	s.handleLogin(rec, loginRequest(`{"username":"alex","password":"wrong"}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt %d: got %d, want 429", maxLoginAttempts+1, rec.Code)
	}

	// The lockout must hold even for the right password, or it is no lockout.
	rec = httptest.NewRecorder()
	s.handleLogin(rec, loginRequest(`{"username":"alex","password":"hunter2"}`))
	if rec.Code != http.StatusTooManyRequests {
		t.Errorf("correct credentials during lockout: got %d, want 429", rec.Code)
	}
}

func TestLoginSucceedsAfterLockoutExpires(t *testing.T) {
	s := newTestServer(t, nil)
	clock := newTestClock()
	s.logins.now = clock.now

	for i := 0; i <= maxLoginAttempts; i++ {
		rec := httptest.NewRecorder()
		s.handleLogin(rec, loginRequest(`{"username":"alex","password":"wrong"}`))
		_ = rec
	}

	clock.advance(loginLockout + time.Minute)

	rec := httptest.NewRecorder()
	s.handleLogin(rec, loginRequest(`{"username":"alex","password":"hunter2"}`))
	if rec.Code != http.StatusOK {
		t.Errorf("after the lockout expired: got %d, want 200", rec.Code)
	}
}

func TestLoginResetsCounterOnSuccess(t *testing.T) {
	s := newTestServer(t, nil)

	for i := 0; i < maxLoginAttempts-1; i++ {
		rec := httptest.NewRecorder()
		s.handleLogin(rec, loginRequest(`{"username":"alex","password":"wrong"}`))
		_ = rec
	}

	rec := httptest.NewRecorder()
	s.handleLogin(rec, loginRequest(`{"username":"alex","password":"hunter2"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}

	// A successful login clears the record, so failures start counting again.
	for i := 0; i < maxLoginAttempts; i++ {
		rec := httptest.NewRecorder()
		s.handleLogin(rec, loginRequest(`{"username":"alex","password":"wrong"}`))
		if rec.Code == http.StatusTooManyRequests {
			t.Fatalf("attempt %d locked out: the counter was not reset on success", i+1)
		}
	}
}

func TestLogoutRevokesSessionServerSide(t *testing.T) {
	s := newTestServer(t, nil)

	token, _ := s.sessions.create()
	req := httptest.NewRequest(http.MethodPost, "/api/logout", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})

	rec := httptest.NewRecorder()
	s.handleLogout(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	// Clearing only the cookie would leave a copied token usable.
	if s.sessions.validate(token) {
		t.Error("logout must revoke the session server-side, not just clear the cookie")
	}
}

func TestMeReturnsUsernameForValidSession(t *testing.T) {
	s := newTestServer(t, nil)

	token, _ := s.sessions.create()
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})

	rec := httptest.NewRecorder()
	s.requireAuth(http.HandlerFunc(s.handleMe)).ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want 200", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "alex") {
		t.Errorf("body must contain the username, got: %s", rec.Body.String())
	}
}

func TestMeRejectsMissingSession(t *testing.T) {
	s := newTestServer(t, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rec := httptest.NewRecorder()
	s.requireAuth(http.HandlerFunc(s.handleMe)).ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("got %d, want 401", rec.Code)
	}
}
