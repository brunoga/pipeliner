package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/actionlink"
	"github.com/brunoga/pipeliner/internal/ingest"
)

func actionLink(t *testing.T, queue, pipeline, title string) (enc, sig string) {
	t.Helper()
	raw, err := actionlink.URL(actionlink.Payload{
		Queue: queue, Pipeline: pipeline, Title: title,
		Fields: map[string]string{"tvdb_id": "12345"}, Label: "⭐ Follow",
	})
	if err != nil || raw == "" {
		t.Fatalf("mint link: %q %v", raw, err)
	}
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("p"), u.Query().Get("sig")
}

// TestActionGetDoesNotAct is the security property that shapes the whole
// flow: mail providers and scanners prefetch links, so a GET must only show
// a confirmation page. If GET enqueued, every notification would act on
// itself the moment it was delivered.
func TestActionGetDoesNotAct(t *testing.T) {
	actionlink.Configure("https://p.example.com", "secret")
	t.Cleanup(func() { actionlink.Configure("", "") })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	enc, sig := actionLink(t, "prefetch-q", "some-pipeline", "Some Show")
	t.Cleanup(func() { ingest.Drain("prefetch-q") })

	rec := httptest.NewRecorder()
	srv.apiAction(rec, httptest.NewRequest(http.MethodGet, "/action?p="+url.QueryEscape(enc)+"&sig="+sig, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "<form method=\"post\"") {
		t.Error("GET should render a confirmation form")
	}
	if n := ingest.Len("prefetch-q"); n != 0 {
		t.Errorf("GET must not enqueue anything, queue holds %d", n)
	}
}

// POST from that form performs the action.
func TestActionPostEnqueuesAndTriggers(t *testing.T) {
	actionlink.Configure("https://p.example.com", "secret")
	t.Cleanup(func() { actionlink.Configure("", "") })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	enc, sig := actionLink(t, "act-q", "fav-pipeline", "Some Show")
	t.Cleanup(func() { ingest.Drain("act-q") })

	rec := httptest.NewRecorder()
	srv.apiAction(rec, httptest.NewRequest(http.MethodPost, "/action?p="+url.QueryEscape(enc)+"&sig="+sig, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("POST status = %d: %s", rec.Code, rec.Body.String())
	}
	items := ingest.Drain("act-q")
	if len(items) != 1 {
		t.Fatalf("queued %d items, want 1", len(items))
	}
	if items[0].Title != "Some Show" || items[0].Fields["tvdb_id"] != "12345" {
		t.Errorf("queued item lost data: %+v", items[0])
	}
}

// A tampered link is refused outright, and nothing is queued.
func TestActionRejectsBadSignature(t *testing.T) {
	actionlink.Configure("https://p.example.com", "secret")
	t.Cleanup(func() { actionlink.Configure("", "") })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	enc, _ := actionLink(t, "bad-q", "p", "Show")
	t.Cleanup(func() { ingest.Drain("bad-q") })

	rec := httptest.NewRecorder()
	srv.apiAction(rec, httptest.NewRequest(http.MethodPost,
		"/action?p="+url.QueryEscape(enc)+"&sig="+strings.Repeat("0", 64), nil))
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403", rec.Code)
	}
	if n := ingest.Len("bad-q"); n != 0 {
		t.Errorf("a refused link must queue nothing, queue holds %d", n)
	}
}

// With no public URL configured the route does not exist at all.
func TestActionDisabledWhenUnconfigured(t *testing.T) {
	actionlink.Configure("", "")
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	rec := httptest.NewRecorder()
	srv.apiAction(rec, httptest.NewRequest(http.MethodGet, "/action?p=x&sig=y", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
}
