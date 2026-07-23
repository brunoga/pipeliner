package web

import (
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
	req, _ := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
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
