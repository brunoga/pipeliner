package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"sync"
	"time"
)

const (
	sessionCookie  = "pipeliner_session"
	sessionTTL     = 24 * time.Hour
	sessionCleanup = time.Hour
)

type sessionStore struct {
	mu       sync.Mutex
	sessions map[string]time.Time // token → expiry
}

func newSessionStore() *sessionStore {
	s := &sessionStore{sessions: make(map[string]time.Time)}
	go s.runCleanup()
	return s
}

func (s *sessionStore) create() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token := hex.EncodeToString(buf)
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(sessionTTL)
	s.mu.Unlock()
	return token, nil
}

func (s *sessionStore) valid(token string) bool {
	s.mu.Lock()
	exp, ok := s.sessions[token]
	s.mu.Unlock()
	return ok && time.Now().Before(exp)
}

func (s *sessionStore) delete(token string) {
	s.mu.Lock()
	delete(s.sessions, token)
	s.mu.Unlock()
}

func (s *sessionStore) runCleanup() {
	for range time.Tick(sessionCleanup) {
		now := time.Now()
		s.mu.Lock()
		for tok, exp := range s.sessions {
			if now.After(exp) {
				delete(s.sessions, tok)
			}
		}
		s.mu.Unlock()
	}
}

// credentials holds hashed copies of the configured username and password.
type credentials struct {
	usernameHash []byte
	passwordHash []byte
}

func newCredentials(username, password string) credentials {
	uh := sha256.Sum256([]byte(username))
	ph := sha256.Sum256([]byte(password))
	return credentials{usernameHash: uh[:], passwordHash: ph[:]}
}

func (c credentials) matches(username, password string) bool {
	uh := sha256.Sum256([]byte(username))
	ph := sha256.Sum256([]byte(password))
	return subtle.ConstantTimeCompare(c.usernameHash, uh[:]) == 1 &&
		subtle.ConstantTimeCompare(c.passwordHash, ph[:]) == 1
}

// requireSession is middleware that redirects unauthenticated requests to /login.
func (s *Server) requireSession(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie(sessionCookie)
		if err != nil || !s.sessions.valid(cookie.Value) {
			http.Redirect(w, r, "/login", http.StatusSeeOther)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleLoginGet(w http.ResponseWriter, r *http.Request) {
	failed := r.URL.Query().Get("failed") == "1"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if failed {
		w.WriteHeader(http.StatusUnauthorized)
	}
	writeLoginPage(w, failed)
}

func (s *Server) handleLoginPost(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	if !s.creds.matches(r.FormValue("username"), r.FormValue("password")) {
		http.Redirect(w, r, "/login?failed=1", http.StatusSeeOther)
		return
	}
	token, err := s.sessions.create()
	if err != nil {
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(sessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(sessionCookie); err == nil {
		s.sessions.delete(cookie.Value)
	}
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   s.secure,
		SameSite: http.SameSiteStrictMode,
	})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func writeLoginPage(w http.ResponseWriter, failed bool) {
	errBlock := ""
	if failed {
		errBlock = `<p class="error">Invalid username or password.</p>`
	}
	page := `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Pipeliner — Login</title>
<style>
*,*::before,*::after{box-sizing:border-box;margin:0;padding:0}
:root{
  --bg:#0d1117;--surface:#161b22;--border:#30363d;
  --text:#c9d1d9;--muted:#8b949e;--accent:#58a6ff;--red:#f85149;
}
body{background:var(--bg);color:var(--text);font-family:'SF Mono',ui-monospace,monospace;
  font-size:13px;display:flex;align-items:center;justify-content:center;min-height:100vh}
.card{background:var(--surface);border:1px solid var(--border);border-radius:8px;
  padding:32px;width:320px}
h1{font-size:16px;font-weight:600;margin-bottom:24px;color:var(--text)}
h1 span{color:var(--accent);margin-right:6px}
label{display:block;font-size:11px;color:var(--muted);text-transform:uppercase;
  letter-spacing:.06em;margin-bottom:4px;margin-top:16px}
label:first-of-type{margin-top:0}
input{width:100%;background:#0d1117;border:1px solid var(--border);border-radius:6px;
  color:var(--text);font-family:inherit;font-size:13px;padding:8px 10px;outline:none;
  transition:border-color .15s}
input:focus{border-color:var(--accent)}
button{margin-top:20px;width:100%;padding:9px;background:transparent;
  border:1px solid var(--accent);border-radius:6px;color:var(--accent);
  font-family:inherit;font-size:13px;font-weight:500;cursor:pointer;
  transition:background .15s}
button:hover{background:rgba(88,166,255,.08)}
.error{margin-top:16px;font-size:12px;color:var(--red);text-align:center}
</style>
</head>
<body>
<div class="card">
  <h1><span>▶</span>Pipeliner</h1>
  <form method="post" action="/login">
    <label for="username">Username</label>
    <input id="username" name="username" type="text" autocomplete="username" autofocus required>
    <label for="password">Password</label>
    <input id="password" name="password" type="password" autocomplete="current-password" required>
    <button type="submit">Sign in</button>
  </form>` + errBlock + `
</div>
</body>
</html>`
	_, _ = w.Write([]byte(page))
}
