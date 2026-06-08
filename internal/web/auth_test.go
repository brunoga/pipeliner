package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// TestSessionStoreClearAllInvalidatesEverySession is the core invariant for
// single-session enforcement: once clearAll runs, every previously-issued
// token must immediately fail valid().
func TestSessionStoreClearAllInvalidatesEverySession(t *testing.T) {
	s := newSessionStore()

	t1, err := s.create()
	if err != nil {
		t.Fatalf("create t1: %v", err)
	}
	t2, err := s.create()
	if err != nil {
		t.Fatalf("create t2: %v", err)
	}
	if !s.valid(t1) || !s.valid(t2) {
		t.Fatalf("baseline: both tokens should be valid")
	}

	s.clearAll()

	if s.valid(t1) {
		t.Error("clearAll: t1 should no longer be valid")
	}
	if s.valid(t2) {
		t.Error("clearAll: t2 should no longer be valid")
	}

	// New tokens created after clearAll must still work.
	t3, err := s.create()
	if err != nil {
		t.Fatalf("create t3: %v", err)
	}
	if !s.valid(t3) {
		t.Error("clearAll must not break subsequent create()")
	}
}

// TestLoginKicksPreviousSession is the integration view: two successful
// logins must leave only the second session's cookie valid. The first cookie
// must be rejected on the next request.
func TestLoginKicksPreviousSession(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "alice", "secret")

	mux := http.NewServeMux()
	mux.HandleFunc("POST /login", srv.handleLoginPost)

	httpSrv := httptest.NewServer(mux)
	defer httpSrv.Close()

	loginAs := func(name, pass string) *http.Cookie {
		t.Helper()
		form := url.Values{"username": {name}, "password": {pass}}
		req, _ := http.NewRequestWithContext(context.Background(),
			http.MethodPost, httpSrv.URL+"/login",
			strings.NewReader(form.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		client := &http.Client{
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("login %s: %v", name, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusSeeOther {
			t.Fatalf("login %s: status %d, want 303", name, resp.StatusCode)
		}
		for _, c := range resp.Cookies() {
			if c.Name == sessionCookie {
				return c
			}
		}
		t.Fatalf("login %s: no session cookie returned", name)
		return nil
	}

	first := loginAs("alice", "secret")
	if !srv.sessions.valid(first.Value) {
		t.Fatal("first session should be valid after login")
	}

	second := loginAs("alice", "secret")
	if srv.sessions.valid(first.Value) {
		t.Error("first session should have been kicked when the second login succeeded")
	}
	if !srv.sessions.valid(second.Value) {
		t.Error("second session should be valid after login")
	}
	if first.Value == second.Value {
		t.Error("the two logins must mint distinct session tokens")
	}
}

// TestFaviconServesWithoutSession verifies /favicon.svg is reachable from
// the login page, which renders before the user signs in. The favicon route
// has to live on the open-mux *and* be exposed through the top mux ahead of
// the session-protected catch-all — otherwise requests get redirected to
// /login and the browser shows a broken icon.
func TestFaviconServesWithoutSession(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "alice", "secret")

	// Mirror Start()'s composition for just the bits the favicon needs.
	open := http.NewServeMux()
	open.HandleFunc("GET /favicon.svg", srv.serveFavicon)

	protected := http.NewServeMux()
	protected.Handle("/", srv.staticHandler())

	top := http.NewServeMux()
	top.Handle("/favicon.svg", open)
	top.Handle("/", srv.requireSession(protected))

	ts := httptest.NewServer(top)
	defer ts.Close()

	// No cookies — the request must not be redirected to /login.
	client := &http.Client{
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/favicon.svg", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("GET /favicon.svg: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 (favicon must be reachable without a session)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "image/svg+xml") {
		t.Errorf("Content-Type = %q, want image/svg+xml", ct)
	}
}

// TestCredentialsMatches covers the bcrypt-based credential check end to end:
// the right (user, pass) pair succeeds; wrong username, wrong password, and
// fully-wrong pairs all fail. Implicit guarantee: bcrypt.CompareHashAndPassword
// runs even on username mismatch so timing doesn't leak username existence.
func TestCredentialsMatches(t *testing.T) {
	c := newCredentials("alice", "correct horse")

	cases := []struct {
		name     string
		user     string
		pass     string
		wantPass bool
	}{
		{"right pair", "alice", "correct horse", true},
		{"wrong password", "alice", "wrong", false},
		{"wrong username", "bob", "correct horse", false},
		{"both wrong", "bob", "nope", false},
		{"empty password", "alice", "", false},
		{"empty username", "", "correct horse", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := c.matches(tc.user, tc.pass); got != tc.wantPass {
				t.Errorf("matches(%q, %q) = %v, want %v", tc.user, tc.pass, got, tc.wantPass)
			}
		})
	}
}

// TestCredentialsHashIsNotPlaintext is a quick sanity check that the stored
// password hash isn't trivially recoverable. A regression to a raw-string
// or naive-hash storage would make this assertion fail.
func TestCredentialsHashIsNotPlaintext(t *testing.T) {
	c := newCredentials("alice", "secret123")
	if strings.Contains(string(c.passwordHash), "secret123") {
		t.Error("stored hash must not contain plaintext password")
	}
	// bcrypt hashes start with a $2 version prefix.
	if !strings.HasPrefix(string(c.passwordHash), "$2") {
		t.Errorf("stored hash %q does not look like a bcrypt hash", c.passwordHash)
	}
}

// TestLoginPostThrottlesFailedAttempts proves the failure-path delay is in
// effect. A successful login completes in well under the throttle; a failed
// one waits at least failedLoginDelay before responding.
func TestLoginPostThrottlesFailedAttempts(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "alice", "secret")

	form := url.Values{"username": {"alice"}, "password": {"wrong"}}
	req := httptest.NewRequest(http.MethodPost, "/login",
		strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()

	start := time.Now()
	srv.handleLoginPost(rec, req)
	elapsed := time.Since(start)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d, want 303", rec.Code)
	}
	if elapsed < failedLoginDelay {
		t.Errorf("failed login completed in %v, want >= %v (throttle missing?)",
			elapsed, failedLoginDelay)
	}
}

// TestRequireSessionBehaviorByPath verifies that the middleware distinguishes
// API requests (which must get a 401 JSON response so the SPA's fetch wrapper
// can detect expiry) from HTML page requests (which get a 303 redirect to
// /login so the browser navigates there directly).
func TestRequireSessionBehaviorByPath(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "alice", "secret")

	protected := http.NewServeMux()
	protected.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := srv.requireSession(protected)

	cases := []struct {
		name           string
		path           string
		wantStatus     int
		wantBodyPrefix string // "" means don't check body
		wantLocation   string // "" means no Location header expected
	}{
		{"api request returns 401 JSON", "/api/status", http.StatusUnauthorized, `{"error":`, ""},
		{"html page redirects to login", "/", http.StatusSeeOther, "", "/login"},
		{"api subpath also returns 401", "/api/db/buckets", http.StatusUnauthorized, `{"error":`, ""},
		{"non-api page redirects", "/guide", http.StatusSeeOther, "", "/login"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, c.path, nil)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != c.wantStatus {
				t.Errorf("status = %d, want %d", rec.Code, c.wantStatus)
			}
			if c.wantBodyPrefix != "" && !strings.HasPrefix(rec.Body.String(), c.wantBodyPrefix) {
				t.Errorf("body = %q, want prefix %q", rec.Body.String(), c.wantBodyPrefix)
			}
			if c.wantLocation != "" && rec.Header().Get("Location") != c.wantLocation {
				t.Errorf("Location = %q, want %q", rec.Header().Get("Location"), c.wantLocation)
			}
		})
	}
}
