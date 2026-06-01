package rss

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func serveFile(t *testing.T, path string) *httptest.Server {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		w.Write(data) //nolint:errcheck
	}))
}

func serveXML(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/rss+xml")
		fmt.Fprint(w, body) //nolint:errcheck
	}))
}

func makeFixedPlugin(t *testing.T, srvURL string) *rssPlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{"url": srvURL}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*rssPlugin)
}

func makeSearchPlugin(t *testing.T, tmpl string) *rssPlugin {
	t.Helper()
	p, err := newPlugin(map[string]any{"url_template": tmpl}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*rssPlugin)
}

// --- RSS 2.0 parsing tests ---

func TestRSS2Parse(t *testing.T) {
	srv := serveFile(t, "testdata/rss2.xml")
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Title != "Episode One" {
		t.Errorf("entry 0 title: got %q", entries[0].Title)
	}
	if entries[0].URL != "http://example.com/ep1" {
		t.Errorf("entry 0 URL: got %q", entries[0].URL)
	}
	if v := entries[0].GetString(entry.FieldPublishedDate); v == "" {
		t.Error("published_date should be set")
	}
	if v := entries[0].GetString(entry.FieldRSSFeed); v != srv.URL {
		t.Errorf("rss_feed: got %q, want %q", v, srv.URL)
	}
	if v := entries[0].GetString(entry.FieldSource); v != "rss:"+hostFromURL(srv.URL) {
		t.Errorf("source: got %q", v)
	}
}

func TestRSS2EnclosureURL(t *testing.T) {
	srv := serveFile(t, "testdata/rss2.xml")
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, _ := p.Generate(context.Background(), makeCtx())

	torrentEntry := entries[2]
	if torrentEntry.URL != "http://example.com/file.torrent" {
		t.Errorf("enclosure URL: got %q, want torrent URL", torrentEntry.URL)
	}
	if v := torrentEntry.GetString(entry.FieldRSSLink); v != "http://example.com/torrent-page" {
		t.Errorf("rss_link: got %q", v)
	}
}

// --- Atom 1.0 parsing tests ---

func TestAtomParse(t *testing.T) {
	srv := serveFile(t, "testdata/atom.xml")
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatalf("want 3 entries, got %d", len(entries))
	}
	if entries[0].Title != "Atom Entry One" {
		t.Errorf("entry 0 title: got %q", entries[0].Title)
	}
	if entries[0].URL != "http://example.com/atom-ep1" {
		t.Errorf("entry 0 URL: got %q", entries[0].URL)
	}
}

func TestAtomEnclosurePreferred(t *testing.T) {
	srv := serveFile(t, "testdata/atom.xml")
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, _ := p.Generate(context.Background(), makeCtx())

	enc := entries[2]
	if enc.URL != "http://example.com/atom.torrent" {
		t.Errorf("atom enclosure URL: got %q", enc.URL)
	}
}

func TestAtomIDFallback(t *testing.T) {
	xml := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
		<entry>
			<title>No Links</title>
			<id>https://example.com/entry/42</id>
			<updated>2024-01-01T00:00:00Z</updated>
		</entry>
	</feed>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry using ID fallback, got %d", len(entries))
	}
	if entries[0].URL != "https://example.com/entry/42" {
		t.Errorf("URL from Atom ID: got %q", entries[0].URL)
	}
}

func TestAtomCategory(t *testing.T) {
	xml := `<?xml version="1.0"?><feed xmlns="http://www.w3.org/2005/Atom">
		<entry>
			<title>Categorised</title>
			<id>https://example.com/1</id>
			<link href="https://example.com/1" rel="alternate"/>
			<updated>2024-01-01T00:00:00Z</updated>
			<category term="anime" label="Anime"/>
			<category term="sub" label="English Sub"/>
		</entry>
	</feed>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if v := entries[0].GetString(entry.FieldRSSCategory); v != "Anime, English Sub" {
		t.Errorf("rss_category: got %q", v)
	}
}

// --- Extended RSS 2.0 attribute tests ---

func TestRSS2DCDateFallback(t *testing.T) {
	xml := `<?xml version="1.0"?><rss version="2.0"
		xmlns:dc="http://purl.org/dc/elements/1.1/">
		<channel><item>
			<title>DC Date Item</title>
			<link>http://example.com/1</link>
			<dc:date>2024-06-15T10:30:00Z</dc:date>
		</item></channel>
	</rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if v := entries[0].GetString(entry.FieldPublishedDate); v != "2024-06-15T10:30:00Z" {
		t.Errorf("published_date from dc:date: got %q", v)
	}
}

func TestRSS2NyaaFields(t *testing.T) {
	xml := `<?xml version="1.0"?><rss version="2.0"
		xmlns:nyaa="https://nyaa.si/xmlns/nyaa">
		<channel><item>
			<title>Show.S01E01.720p</title>
			<link>http://nyaa.si/view/1</link>
			<enclosure url="https://nyaa.si/download/1.torrent" type="application/x-bittorrent"/>
			<nyaa:seeders>42</nyaa:seeders>
			<nyaa:leechers>7</nyaa:leechers>
			<nyaa:downloads>512</nyaa:downloads>
			<nyaa:infoHash>AABBCCDDEEFF00112233445566778899AABBCCDD</nyaa:infoHash>
			<nyaa:category>Anime - English-translated</nyaa:category>
		</item></channel>
	</rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]

	if v := e.GetInt(entry.FieldTorrentSeeds); v != 42 {
		t.Errorf("torrent_seeds: got %d, want 42", v)
	}
	if v := e.GetInt(entry.FieldTorrentLeechers); v != 7 {
		t.Errorf("torrent_leechers: got %d, want 7", v)
	}
	if v := e.GetInt(entry.FieldTorrentGrabs); v != 512 {
		t.Errorf("torrent_grabs: got %d, want 512", v)
	}
	if v := e.GetString(entry.FieldTorrentInfoHash); v != "aabbccddeeff00112233445566778899aabbccdd" {
		t.Errorf("torrent_info_hash: got %q", v)
	}
	if v := e.GetString(entry.FieldRSSCategory); v != "Anime - English-translated" {
		t.Errorf("rss_category: got %q", v)
	}
}

func TestRSS2StandardCategory(t *testing.T) {
	xml := `<?xml version="1.0"?><rss version="2.0"><channel><item>
		<title>Item</title>
		<link>http://example.com/1</link>
		<category>Technology</category>
		<category>Software</category>
	</item></channel></rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if v := entries[0].GetString(entry.FieldRSSCategory); v != "Technology, Software" {
		t.Errorf("rss_category: got %q", v)
	}
}

func TestRSS2MediaContentFallback(t *testing.T) {
	xml := `<?xml version="1.0"?><rss version="2.0"
		xmlns:media="http://search.yahoo.com/mrss/">
		<channel><item>
			<title>Media Item</title>
			<link>http://example.com/page</link>
			<media:content url="http://example.com/video.mp4" type="video/mp4"/>
		</item></channel>
	</rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	// media:content is preferred over link but loses to enclosure.
	if entries[0].URL != "http://example.com/video.mp4" {
		t.Errorf("media:content URL: got %q", entries[0].URL)
	}
}

func TestRSS2EnclosureBeatsMediaContent(t *testing.T) {
	xml := `<?xml version="1.0"?><rss version="2.0"
		xmlns:media="http://search.yahoo.com/mrss/">
		<channel><item>
			<title>Item</title>
			<link>http://example.com/page</link>
			<enclosure url="http://example.com/file.torrent" type="application/x-bittorrent"/>
			<media:content url="http://example.com/video.mp4" type="video/mp4"/>
		</item></channel>
	</rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].URL != "http://example.com/file.torrent" {
		t.Errorf("enclosure should beat media:content: got %q", entries[0].URL)
	}
}

// --- HTTP behaviour tests ---

func TestHTTPErrorNonRetriable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	_, err := p.Generate(context.Background(), makeCtx())
	if err == nil {
		t.Error("expected error on 404")
	}
}

func TestHTTPError5xxRetried(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	_, err := p.Generate(context.Background(), makeCtx())
	if err == nil {
		t.Error("expected error after retries")
	}
	if attempts < 2 {
		t.Errorf("expected retry — got only %d attempt(s)", attempts)
	}
}

func TestHTTPRecoversAfterTransient(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>OK</title><link>http://example.com/1</link></item></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatalf("expected recovery after transient, got: %v", err)
	}
	if len(entries) != 1 {
		t.Errorf("expected 1 entry after recovery, got %d", len(entries))
	}
}

func TestAcceptHeader(t *testing.T) {
	var gotAccept string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAccept = r.Header.Get("Accept")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	p.Generate(context.Background(), makeCtx()) //nolint:errcheck
	if !strings.Contains(gotAccept, "rss+xml") {
		t.Errorf("Accept header missing rss+xml: %q", gotAccept)
	}
}

// --- Validation tests ---

func TestValidationRequiresURLOrTemplate(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Error("expected error when neither url nor url_template is set")
	}
}

func TestValidationRejectsBoth(t *testing.T) {
	_, err := newPlugin(map[string]any{
		"url":          "http://example.com/rss",
		"url_template": "http://example.com/search?q={Query}",
	}, nil)
	if err == nil {
		t.Error("expected error when both url and url_template are set")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("rss")
	if !ok {
		t.Fatal("rss plugin not registered")
	}
	if d.Role != plugin.RoleSource {
		t.Errorf("role: got %v", d.Role)
	}
	if !d.IsSearchPlugin {
		t.Error("rss should be a search plugin")
	}
}

// --- Search (url_template) mode tests ---

func TestSearchModeURLRendered(t *testing.T) {
	var receivedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makeSearchPlugin(t, srv.URL+"/search?q={{.QueryEscaped}}")
	_, err := p.Search(context.Background(), makeCtx(), "Breaking Bad")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if receivedQuery != "Breaking Bad" {
		t.Errorf("query not received correctly; got %q", receivedQuery)
	}
}

func TestSearchModeSourceField(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>T</title><link>http://x.com/1</link></item></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makeSearchPlugin(t, srv.URL+"/search?q={Query}")
	entries, err := p.Search(context.Background(), makeCtx(), "test")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("expected entries")
	}
	if v := entries[0].GetString(entry.FieldSource); !strings.HasPrefix(v, "rss:") {
		t.Errorf("source: got %q, want rss:<host>", v)
	}
}

func TestSearchModeRSSFeedIsRenderedURL(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel><item><title>T</title><link>http://x.com/1</link></item></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makeSearchPlugin(t, srv.URL+"/search?q={Query}")
	entries, err := p.Search(context.Background(), makeCtx(), "myquery")
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := srv.URL + gotPath
	if v := entries[0].GetString(entry.FieldRSSFeed); v != want {
		t.Errorf("rss_feed: got %q, want %q", v, want)
	}
}

func TestSearchModeHandlesAtom(t *testing.T) {
	srv := serveFile(t, "testdata/atom.xml")
	defer srv.Close()

	// In search mode, Atom feeds must be parsed correctly too.
	p := makeSearchPlugin(t, srv.URL)
	entries, err := p.Search(context.Background(), makeCtx(), "")
	if err != nil {
		t.Fatalf("Search on Atom feed: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("expected 3 Atom entries in search mode, got %d", len(entries))
	}
}

func TestSearchModeEnclosurePrecedence(t *testing.T) {
	// Verifies the old rss_search link-priority bug is fixed:
	// enclosure must win over link, even in search (url_template) mode.
	xml := `<?xml version="1.0"?><rss version="2.0"><channel><item>
		<title>Torrent</title>
		<link>http://example.com/page</link>
		<enclosure url="http://example.com/file.torrent" type="application/x-bittorrent"/>
	</item></channel></rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeSearchPlugin(t, srv.URL)
	entries, err := p.Search(context.Background(), makeCtx(), "test")
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].URL != "http://example.com/file.torrent" {
		t.Errorf("enclosure should win in search mode: got %q", entries[0].URL)
	}
}

func TestSearchModeNyaaSeedsPresent(t *testing.T) {
	// Verifies that torrent namespace seeds are parsed in search (url_template) mode.
	xml := `<?xml version="1.0"?><rss version="2.0"
		xmlns:nyaa="https://nyaa.si/xmlns/nyaa">
		<channel><item>
			<title>Show</title>
			<link>http://nyaa.si/view/1</link>
			<nyaa:seeders>99</nyaa:seeders>
		</item></channel>
	</rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeSearchPlugin(t, srv.URL)
	entries, err := p.Search(context.Background(), makeCtx(), "")
	if err != nil {
		t.Fatal(err)
	}
	if v := entries[0].GetInt(entry.FieldTorrentSeeds); v != 99 {
		t.Errorf("torrent_seeds in search mode: got %d, want 99", v)
	}
}

// TestRSS2NonURLGUIDSkipped guards the fallback path: an item with no link,
// enclosure, or media:content but a GUID like "abc-123" (isPermaLink="false")
// must be skipped — otherwise the non-URL GUID propagates as e.URL and a
// downstream sink (e.g. deluge) gets a scheme-less string.
func TestRSS2NonURLGUIDSkipped(t *testing.T) {
	xml := `<?xml version="1.0"?>
	<rss version="2.0"><channel>
		<item>
			<title>Has only opaque GUID</title>
			<guid isPermaLink="false">deadbeef-internal-id</guid>
		</item>
		<item>
			<title>Has real link</title>
			<link>http://example.com/ep.torrent</link>
		</item>
	</channel></rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry (opaque-GUID item skipped), got %d", len(entries))
	}
	if entries[0].URL != "http://example.com/ep.torrent" {
		t.Errorf("URL: got %q", entries[0].URL)
	}
}

// TestRSS2URLLikeGUIDUsed confirms that the GUID fallback still works when
// the GUID actually is a fetchable URL (the legitimate use case).
func TestRSS2URLLikeGUIDUsed(t *testing.T) {
	xml := `<?xml version="1.0"?>
	<rss version="2.0"><channel>
		<item>
			<title>GUID-as-permalink</title>
			<guid isPermaLink="true">https://example.com/posts/42</guid>
		</item>
	</channel></rss>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].URL != "https://example.com/posts/42" {
		t.Fatalf("want 1 entry with URL from GUID, got %+v", entries)
	}
}

// TestAtomURNIDSkipped guards the Atom <id> fallback. Atom IDs are IRIs that
// often look like urn:uuid:… or tag:host,date:…; those are not fetchable and
// must be rejected as URL candidates.
func TestAtomURNIDSkipped(t *testing.T) {
	xml := `<?xml version="1.0"?>
	<feed xmlns="http://www.w3.org/2005/Atom">
		<entry>
			<title>No links, opaque id</title>
			<id>urn:uuid:f81d4fae-7dec-11d0-a765-00a0c91e6bf6</id>
		</entry>
		<entry>
			<title>Real link</title>
			<link href="http://example.com/atom-ep1" rel="alternate"/>
			<id>tag:example.com,2026:ep1</id>
		</entry>
	</feed>`
	srv := serveXML(t, xml)
	defer srv.Close()

	p := makeFixedPlugin(t, srv.URL)
	entries, err := p.Generate(context.Background(), makeCtx())
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry (urn-id item skipped), got %d", len(entries))
	}
	if entries[0].URL != "http://example.com/atom-ep1" {
		t.Errorf("URL: got %q", entries[0].URL)
	}
}

func TestGenerateEmptyQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query().Get("q")
		fmt.Fprint(w, `<?xml version="1.0"?><rss version="2.0"><channel></channel></rss>`) //nolint:errcheck
	}))
	defer srv.Close()

	p := makeSearchPlugin(t, srv.URL+"/search?q={{.QueryEscaped}}")
	p.Generate(context.Background(), makeCtx()) //nolint:errcheck
	if gotQuery != "" {
		t.Errorf("Generate should use empty query, got %q", gotQuery)
	}
}
