package html

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

const testPage = `<!DOCTYPE html>
<html><body>
  <a href="/ep1">Episode One</a>
  <a href="/ep2">Episode Two</a>
  <a href="http://other.com/file.torrent">Torrent</a>
  <a href="/ignored">  </a>
  <abbr href="/not-a-link">should be ignored</abbr>
  <A HREF="/uppercase">Upper</A>
  <a href='/single-quote'>Single</a>
  <a href="nested"><b>Bold</b> text</a>
</body></html>`

func serve(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(body)) //nolint:errcheck
	}))
}

func TestExtractLinks(t *testing.T) {
	srv := serve(t, testPage)
	defer srv.Close()

	p, err := newPlugin(map[string]any{"url": srv.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.(*htmlPlugin).Run(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}

	// /ignored has empty text → title should be the URL.
	// /uppercase and /single-quote should be found.
	// abbr should NOT appear.
	wantURLs := map[string]string{
		srv.URL + "/ep1":                 "Episode One",
		srv.URL + "/ep2":                 "Episode Two",
		"http://other.com/file.torrent":  "Torrent",
		srv.URL + "/uppercase":           "Upper",
		srv.URL + "/single-quote":        "Single",
		srv.URL + "/nested":              "Bold text",
	}

	got := map[string]string{}
	for _, e := range entries {
		got[e.URL] = e.Title
	}

	for wantURL, wantTitle := range wantURLs {
		if gotTitle, ok := got[wantURL]; !ok {
			t.Errorf("missing URL %q", wantURL)
		} else if gotTitle != wantTitle {
			t.Errorf("URL %q: title got %q, want %q", wantURL, gotTitle, wantTitle)
		}
	}

	// abbr should not appear
	if _, ok := got[srv.URL+"/not-a-link"]; ok {
		t.Error("<abbr> href should not be extracted")
	}
}

func TestMaskFilter(t *testing.T) {
	srv := serve(t, testPage)
	defer srv.Close()

	p, err := newPlugin(map[string]any{"url": srv.URL, "mask": "*.torrent"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := p.(*htmlPlugin).Run(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("mask *.torrent: want 1 entry, got %d", len(entries))
	}
	if entries[0].URL != "http://other.com/file.torrent" {
		t.Errorf("unexpected URL: %q", entries[0].URL)
	}
}

func TestRelativeURLResolution(t *testing.T) {
	srv := serve(t, `<a href="/path/file.torrent">File</a>`)
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL}, nil)
	entries, err := p.(*htmlPlugin).Run(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	want := srv.URL + "/path/file.torrent"
	if entries[0].URL != want {
		t.Errorf("URL: got %q, want %q", entries[0].URL, want)
	}
}

func TestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL}, nil)
	_, err := p.(*htmlPlugin).Run(context.Background(), makeCtx())
	if err == nil {
		t.Error("expected error on 403")
	}
}

func TestMissingURL(t *testing.T) {
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("expected error when url missing")
	}
}

func TestHtmlPageField(t *testing.T) {
	srv := serve(t, `<a href="/x">X</a>`)
	defer srv.Close()

	p, _ := newPlugin(map[string]any{"url": srv.URL}, nil)
	entries, _ := p.(*htmlPlugin).Run(context.Background(), makeCtx())
	if len(entries) == 0 {
		t.Fatal("no entries")
	}
	if v := entries[0].GetString("html_page"); v != srv.URL {
		t.Errorf("html_page: got %q", v)
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("html")
	if !ok {
		t.Fatal("html plugin not registered")
	}
	if d.PluginPhase != plugin.PhaseInput {
		t.Errorf("phase: got %v", d.PluginPhase)
	}
}

// --- Unit tests for helper functions ---

func TestExtractAttr(t *testing.T) {
	cases := []struct{ attrs, name, want string }{
		{`href="http://example.com"`, "href", "http://example.com"},
		{`href='single'`, "href", "single"},
		{`HREF="upper"`, "href", "upper"},
		{`href=unquoted`, "href", "unquoted"},
		{`class="x" href="val"`, "href", "val"},
		{`class="x"`, "href", ""},
	}
	for _, tc := range cases {
		got := extractAttr(tc.attrs, tc.name)
		if got != tc.want {
			t.Errorf("extractAttr(%q, %q) = %q, want %q", tc.attrs, tc.name, got, tc.want)
		}
	}
}

func TestStripTags(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain text", "plain text"},
		{"<b>bold</b>", "bold"},
		{"<span class=\"x\">text</span>", "text"},
		{"no tags", "no tags"},
	}
	for _, tc := range cases {
		got := stripTags(tc.in)
		if got != tc.want {
			t.Errorf("stripTags(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
