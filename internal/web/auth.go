package web

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

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

	s.writeJSON(w, map[string]string{"username": s.config.Auth.Username})
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
	s.writeJSON(w, map[string]string{"status": "logged out"})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	s.writeJSON(w, map[string]string{"username": s.config.Auth.Username})
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

func (s *Server) writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status line is already written; nothing left but to record it.
		s.logger.Warn("writing JSON response: %v", err)
	}
}
