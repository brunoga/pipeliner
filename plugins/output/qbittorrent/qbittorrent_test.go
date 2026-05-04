package qbittorrent

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

type qbtMock struct {
	loginFail bool
	requests  []string // recorded request paths
	bodies    []string // recorded request bodies
}

func (m *qbtMock) handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.requests = append(m.requests, r.URL.Path)
		m.bodies = append(m.bodies, string(body))

		switch r.URL.Path {
		case "/api/v2/auth/login":
			if m.loginFail {
				w.Write([]byte("Fails.")) //nolint:errcheck
			} else {
				w.Write([]byte("Ok.")) //nolint:errcheck
			}
		case "/api/v2/torrents/add":
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("Ok.")) //nolint:errcheck
		}
	}
}

func newTestPlugin(t *testing.T, srv *httptest.Server) *qbtPlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{
		"host":     "127.0.0.1",
		"port":     0,
		"username": "admin",
		"password": "secret",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	qp := p.(*qbtPlugin)
	qp.baseURL = srv.URL
	return qp
}

func TestLoginAndAdd(t *testing.T) {
	mock := &qbtMock{}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	qp := newTestPlugin(t, srv)
	e := entry.New("My Show S01E01", "http://example.com/ep.torrent")
	err := qp.Output(context.Background(), makeCtx(), []*entry.Entry{e})
	if err != nil {
		t.Fatal(err)
	}

	if len(mock.requests) < 2 {
		t.Fatalf("expected at least 2 requests, got %d", len(mock.requests))
	}
	if mock.requests[0] != "/api/v2/auth/login" {
		t.Errorf("first request should be login, got %q", mock.requests[0])
	}
	if mock.requests[1] != "/api/v2/torrents/add" {
		t.Errorf("second request should be add, got %q", mock.requests[1])
	}
	if !strings.Contains(mock.bodies[1], "ep.torrent") {
		t.Errorf("add body should contain torrent URL; got: %s", mock.bodies[1])
	}
}

func TestLoginFailure(t *testing.T) {
	mock := &qbtMock{loginFail: true}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	qp := newTestPlugin(t, srv)
	e := entry.New("T", "http://x.com/a.torrent")
	err := qp.Output(context.Background(), makeCtx(), []*entry.Entry{e})
	if err == nil {
		t.Error("expected error on login failure")
	}
}

func TestCategoryAndTags(t *testing.T) {
	mock := &qbtMock{}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	p, _ := newPlugin(map[string]any{
		"host":     "127.0.0.1",
		"category": "tv",
		"tags":     "auto,pipeliner",
	}, nil)
	qp := p.(*qbtPlugin)
	qp.baseURL = srv.URL

	e := entry.New("T", "http://x.com/a.torrent")
	qp.Output(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	var addBody string
	for i, path := range mock.requests {
		if path == "/api/v2/torrents/add" {
			addBody = mock.bodies[i]
		}
	}
	if !strings.Contains(addBody, "tv") {
		t.Errorf("add body should contain category 'tv'; got: %s", addBody)
	}
	if !strings.Contains(addBody, "auto") {
		t.Errorf("add body should contain tags; got: %s", addBody)
	}
}

func TestSavepathTemplate(t *testing.T) {
	mock := &qbtMock{}
	srv := httptest.NewServer(mock.handler())
	defer srv.Close()

	p, _ := newPlugin(map[string]any{
		"host":     "127.0.0.1",
		"savepath": "/downloads/{{.series_name}}",
	}, nil)
	qp := p.(*qbtPlugin)
	qp.baseURL = srv.URL

	e := entry.New("My Show S01E01", "http://x.com/a.torrent")
	e.Set("series_name", "My Show")
	qp.Output(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck

	var addBody string
	for i, path := range mock.requests {
		if path == "/api/v2/torrents/add" {
			addBody = mock.bodies[i]
		}
	}
	if !strings.Contains(addBody, "My+Show") && !strings.Contains(addBody, "My%20Show") && !strings.Contains(addBody, "My Show") {
		t.Errorf("savepath should contain series name; got: %s", addBody)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("qbittorrent")
	if !ok {
		t.Fatal("qbittorrent plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseOutput {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}
