package rss

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

func rssServer(t *testing.T, items []struct{ title, link string }) *httptest.Server {
	t.Helper()
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel>`)
	for _, it := range items {
		fmt.Fprintf(&sb, `<item><title>%s</title><link>%s</link></item>`, it.title, it.link)
	}
	sb.WriteString(`</channel></rss>`)
	body := sb.String()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, body) //nolint:errcheck
	}))
}

func makePlugin(t *testing.T, urlTemplate string) *searchRSSPlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{"url_template": urlTemplate}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*searchRSSPlugin)
}

func TestSearchRSSURLTemplateRendered(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	// Use QueryEscaped to produce a valid URL (spaces → %2B or +).
	p := makePlugin(t, srv.URL+"/search?q={{.QueryEscaped}}")
	tc := &plugin.TaskContext{Name: "test"}
	_, err := p.Search(context.Background(), tc, "Breaking Bad")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if receivedQuery != "Breaking Bad" {
		t.Errorf("query not received correctly; got %q", receivedQuery)
	}
}

func TestSearchRSSQueryEscaped(t *testing.T) {
	var receivedRaw string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRaw = r.URL.RawQuery
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, srv.URL+"/search?q={{.QueryEscaped}}")
	tc := &plugin.TaskContext{Name: "test"}
	_, err := p.Search(context.Background(), tc, "Breaking Bad")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	// url.QueryEscape uses + for spaces; the raw query should reflect that.
	if !strings.Contains(receivedRaw, "Breaking") {
		t.Errorf("encoded query not found in raw query %q", receivedRaw)
	}
}

func TestSearchRSSParsesEntries(t *testing.T) {
	srv := rssServer(t, []struct{ title, link string }{
		{"Breaking.Bad.S01E01.720p", "http://example.com/1"},
		{"Breaking.Bad.S01E02.720p", "http://example.com/2"},
	})
	defer srv.Close()

	// Use a fixed URL (no query substitution needed).
	p := makePlugin(t, srv.URL)
	tc := &plugin.TaskContext{Name: "test"}
	entries, err := p.Search(context.Background(), tc, "Breaking Bad")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("got %d entries, want 2", len(entries))
	}
	if entries[0].Title != "Breaking.Bad.S01E01.720p" {
		t.Errorf("title: got %q", entries[0].Title)
	}
}

func TestSearchRSSEmptyFeed(t *testing.T) {
	srv := rssServer(t, nil)
	defer srv.Close()

	p := makePlugin(t, srv.URL)
	tc := &plugin.TaskContext{Name: "test"}
	entries, err := p.Search(context.Background(), tc, "anything")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("got %d entries, want 0", len(entries))
	}
}

func TestSearchRSSMissingURLTemplate(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Error("expected error when url_template is missing")
	}
}

func TestSearchRSSPhase(t *testing.T) {
	p := makePlugin(t, "http://example.com?q={{.Query}}")
	if p.Phase() != plugin.PhaseSearch {
		t.Errorf("phase: got %q, want %q", p.Phase(), plugin.PhaseSearch)
	}
}
