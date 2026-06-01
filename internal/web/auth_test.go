package web

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
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
