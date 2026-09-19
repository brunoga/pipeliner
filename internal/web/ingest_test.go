package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/ingest"
)

type triggerSpy struct {
	stubDaemon
	triggered []string
}

func (t *triggerSpy) Trigger(name string, _ bool) { t.triggered = append(t.triggered, name) }

func newIngestServer(t *testing.T, token string) (*Server, *triggerSpy, *httptest.Server) {
	t.Helper()
	spy := &triggerSpy{}
	srv := New(nil, spy, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetIngestToken(token)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/ingest/{queue}", srv.apiIngest)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return srv, spy, ts
}

func ingestPost(t *testing.T, url, token, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, url, strings.NewReader(body))
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestIngestDisabledIs404(t *testing.T) {
	_, _, ts := newIngestServer(t, "")
	resp := ingestPost(t, ts.URL+"/api/ingest/q1", "any", `{"title":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("disabled endpoint must 404, got %d", resp.StatusCode)
	}
}

func TestIngestAuth(t *testing.T) {
	_, _, ts := newIngestServer(t, "secret")
	for _, tok := range []string{"", "wrong"} {
		resp := ingestPost(t, ts.URL+"/api/ingest/q2", tok, `{"title":"x"}`)
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("token %q: want 401, got %d", tok, resp.StatusCode)
		}
	}
}

func TestIngestSingleAndArrayAndTrigger(t *testing.T) {
	_, spy, ts := newIngestServer(t, "secret")

	resp := ingestPost(t, ts.URL+"/api/ingest/q3", "secret", `{"title":"one","url":"https://x/1"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("single: %d", resp.StatusCode)
	}
	resp = ingestPost(t, ts.URL+"/api/ingest/q3?pipeline=push-pipe", "secret",
		`[{"title":"two"},{"title":""},{"title":"three","fields":{"k":"v"}}]`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("array: %d", resp.StatusCode)
	}

	items := ingest.Drain("q3")
	if len(items) != 3 { // "one", "two", "three"; empty title rejected
		t.Fatalf("queued: %+v", items)
	}
	if items[2].Fields["k"] != "v" {
		t.Errorf("fields lost: %+v", items[2])
	}
	if len(spy.triggered) != 1 || spy.triggered[0] != "push-pipe" {
		t.Fatalf("trigger: %+v", spy.triggered)
	}
}

func TestIngestBadJSON(t *testing.T) {
	_, _, ts := newIngestServer(t, "secret")
	resp := ingestPost(t, ts.URL+"/api/ingest/q4", "secret", `{not json`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad json: want 400, got %d", resp.StatusCode)
	}
}

// TestIngestReachableThroughTopLevelRouting exercises the REAL routing tree
// (buildHandler), not a hand-mounted test mux: /api/ingest/{queue} must
// dispatch to the open mux and authenticate via the ingest bearer token —
// never fall through to session auth. This was broken once: the route was
// registered on the open mux but the top-level dispatcher never sent
// /api/ingest/ traffic there, so every push died with a session 401.
func TestIngestReachableThroughTopLevelRouting(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetIngestToken("push-tok")
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp := ingestPost(t, ts.URL+"/api/ingest/routing-check", "push-tok", `{"title":"x"}`)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("ingest through real routing: got %d (%s), want 200", resp.StatusCode, body)
	}

	// Wrong token → the handler's own 401, not a session redirect/401.
	resp = ingestPost(t, ts.URL+"/api/ingest/routing-check", "wrong", `{"title":"x"}`)
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("wrong ingest token: got %d, want 401", resp.StatusCode)
	}

	// Other API routes still require a session.
	other, err := http.Get(ts.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	other.Body.Close()
	if other.StatusCode != http.StatusUnauthorized {
		t.Errorf("/api/status without session: got %d, want 401", other.StatusCode)
	}
}
