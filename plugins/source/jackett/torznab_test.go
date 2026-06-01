package jackett

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, `"`, "&quot;")
	return s
}

func buildXML(items []torznabItem) []byte {
	var sb strings.Builder
	sb.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sb.WriteString(`<rss version="2.0" xmlns:torznab="http://torznab.com/schemas/2015/feed"><channel>`)
	for _, it := range items {
		fmt.Fprintf(&sb, `<item><title>%s</title>`, escapeXML(it.Title))
		if it.Enclosure.URL != "" {
			fmt.Fprintf(&sb, `<enclosure url="%s" type="application/x-bittorrent"/>`, escapeXML(it.Enclosure.URL))
		} else if it.Link != "" {
			fmt.Fprintf(&sb, `<link>%s</link>`, escapeXML(it.Link))
		}
		if it.PubDate != "" {
			fmt.Fprintf(&sb, `<pubDate>%s</pubDate>`, escapeXML(it.PubDate))
		}
		if it.Size > 0 {
			fmt.Fprintf(&sb, `<size>%d</size>`, it.Size)
		}
		for _, a := range it.Attrs {
			fmt.Fprintf(&sb, `<torznab:attr name="%s" value="%s"/>`, escapeXML(a.Name), escapeXML(a.Value))
		}
		sb.WriteString(`</item>`)
	}
	sb.WriteString(`</channel></rss>`)
	return []byte(sb.String())
}

func TestParseTorznabTorrentEntry(t *testing.T) {
	data := buildXML([]torznabItem{{
		Title:     "My.Show.S01E01.720p",
		Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "https://jackett.host/dl/idx/?key=abc&path=xyz&file=My.Show"},
		Size:      1_000_000_000,
		Attrs: []torznabAttr{
			{Name: "seeders", Value: "10"},
			{Name: "leechers", Value: "2"},
			{Name: "infohash", Value: "AABBCCDD"},
			{Name: "category", Value: "5030"},
		},
	}})

	entries, err := parseTorznab(data, "myindexer")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]

	if got := e.GetString(entry.FieldTorrentLinkType); got != "torrent" {
		t.Errorf("torrent_link_type: got %q, want torrent", got)
	}
	if !strings.Contains(e.URL, "jackett.host") {
		t.Errorf("URL should be Jackett proxy, got %q", e.URL)
	}
	if got := e.GetString(entry.FieldTorrentInfoHash); got != "aabbccdd" {
		t.Errorf("torrent_info_hash: got %q, want aabbccdd", got)
	}
	if got := e.GetInt(entry.FieldTorrentSeeds); got != 10 {
		t.Errorf("torrent_seeds: got %d, want 10", got)
	}
	if got := e.GetString(entry.FieldSource); got != "jackett:myindexer" {
		t.Errorf("source: got %q, want \"jackett:myindexer\"", got)
	}
}

func TestParseTorznabMagnetEntry(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=My+Show+S01E01"
	data := buildXML([]torznabItem{{
		Title:     "My.Show.S01E01.720p",
		Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "https://jackett.host/dl/idx/?key=abc&path=xyz&file=My.Show"},
		Attrs: []torznabAttr{
			{Name: "magneturl", Value: magnet},
			{Name: "seeders", Value: "5"},
		},
	}})

	entries, err := parseTorznab(data, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("want 1 entry, got %d", len(entries))
	}
	e := entries[0]

	if got := e.GetString(entry.FieldTorrentLinkType); got != "magnet" {
		t.Errorf("torrent_link_type: got %q, want magnet", got)
	}
	if e.URL != magnet {
		t.Errorf("URL: got %q, want magnet URI", e.URL)
	}
}

func TestEnsureMagnetDNAddsFromProxyFile(t *testing.T) {
	proxy := "https://jackett.host/dl/torrenting/?jackett_apikey=abc&path=XYZ&file=My+Show+S01E01+1080p"
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd"

	result := ensureMagnetDN(magnet, proxy)

	mu, err := url.Parse(result)
	if err != nil {
		t.Fatalf("parse result: %v", err)
	}
	if got := mu.Query().Get("dn"); got != "My Show S01E01 1080p" {
		t.Errorf("dn: got %q, want %q", got, "My Show S01E01 1080p")
	}
}

func TestEnsureMagnetDNPreservesExisting(t *testing.T) {
	proxy := "https://jackett.host/dl/torrenting/?file=Other+Name"
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=Original+Name"

	result := ensureMagnetDN(magnet, proxy)

	mu, _ := url.Parse(result)
	if got := mu.Query().Get("dn"); got != "Original Name" {
		t.Errorf("dn should not be overwritten: got %q", got)
	}
}

func TestEnsureMagnetDNNoFileParam(t *testing.T) {
	proxy := "https://jackett.host/dl/torrenting/?jackett_apikey=abc&path=XYZ"
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd"

	result := ensureMagnetDN(magnet, proxy)

	if result != magnet {
		t.Errorf("should return magnet unchanged when no file= param: got %q", result)
	}
}

func TestParseTorznabSkipsEmptyLink(t *testing.T) {
	data := buildXML([]torznabItem{{Title: "No Link Entry"}})
	entries, err := parseTorznab(data, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for item with no link, got %d", len(entries))
	}
}

// TestParseTorznabSkipsNonURLGUID covers the case where an indexer omits the
// link/enclosure but emits a non-URL GUID (an internal hash or numeric id).
// The item must be dropped rather than reaching downstream sinks as a
// scheme-less URL.
func TestParseTorznabSkipsNonURLGUID(t *testing.T) {
	xml := `<?xml version="1.0"?>
	<rss version="2.0"><channel>
		<item><title>Opaque GUID only</title><guid isPermaLink="false">7c1c8e4f</guid></item>
		<item><title>Real link</title><enclosure url="http://example.com/x.torrent" type="application/x-bittorrent"/></item>
	</channel></rss>`
	entries, err := parseTorznab([]byte(xml), "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (opaque-GUID item dropped), got %d", len(entries))
	}
	if entries[0].URL != "http://example.com/x.torrent" {
		t.Errorf("URL: got %q", entries[0].URL)
	}
}

// TestParseTorznabAcceptsURLGUID confirms that the GUID fallback still kicks
// in when the GUID is actually a usable URL.
func TestParseTorznabAcceptsURLGUID(t *testing.T) {
	xml := `<?xml version="1.0"?>
	<rss version="2.0"><channel>
		<item><title>GUID-as-permalink</title><guid isPermaLink="true">http://example.com/dl?id=42</guid></item>
	</channel></rss>`
	entries, err := parseTorznab([]byte(xml), "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].URL != "http://example.com/dl?id=42" {
		t.Fatalf("expected 1 entry with GUID URL, got %+v", entries)
	}
}

func TestParseTorznabPublishDate(t *testing.T) {
	t.Run("from pubDate element", func(t *testing.T) {
		item := torznabItem{
			Title:   "Show.S01E01",
			Link:    "http://example.com/1.torrent",
			PubDate: "Mon, 01 Jan 2024 12:00:00 +0000",
		}
		entries, err := parseTorznab(buildXML([]torznabItem{item}), "idx")
		if err != nil {
			t.Fatal(err)
		}
		if got := entries[0].GetString(entry.FieldPublishedDate); got != item.PubDate {
			t.Errorf("published_date: got %q, want %q", got, item.PubDate)
		}
	})

	t.Run("publishdate attr takes precedence over pubDate", func(t *testing.T) {
		item := torznabItem{
			Title:   "Show.S01E01",
			Link:    "http://example.com/1.torrent",
			PubDate: "Mon, 01 Jan 2024 12:00:00 +0000",
			Attrs:   []torznabAttr{{Name: "publishdate", Value: "2024-01-01T12:00:00Z"}},
		}
		entries, err := parseTorznab(buildXML([]torznabItem{item}), "idx")
		if err != nil {
			t.Fatal(err)
		}
		if got := entries[0].GetString(entry.FieldPublishedDate); got != "2024-01-01T12:00:00Z" {
			t.Errorf("published_date: got %q, want attr value", got)
		}
	})
}

func TestParseTorznabExtendedAttrs(t *testing.T) {
	item := torznabItem{
		Title: "Breaking.Bad.S03E07.720p",
		Link:  "http://example.com/1.torrent",
		Attrs: []torznabAttr{
			{Name: "imdbid", Value: "tt0903747"},
			{Name: "tvdbid", Value: "81189"},
			{Name: "tmdbid", Value: "1396"},
			{Name: "year", Value: "2010"},
			{Name: "season", Value: "3"},
			{Name: "episode", Value: "7"},
			{Name: "grabs", Value: "512"},
			{Name: "downloadvolumefactor", Value: "0.5"},
			{Name: "uploadvolumefactor", Value: "2.0"},
		},
	}
	entries, err := parseTorznab(buildXML([]torznabItem{item}), "idx")
	if err != nil {
		t.Fatal(err)
	}
	e := entries[0]

	tests := []struct {
		field string
		want  any
	}{
		{entry.FieldVideoImdbID, "tt0903747"},
		{"jackett_tvdb_id", "81189"},
		{"jackett_tmdb_id", "1396"},
		{entry.FieldVideoYear, 2010},
		{entry.FieldSeriesSeason, 3},
		{entry.FieldSeriesEpisode, 7},
		{entry.FieldTorrentGrabs, 512},
	}
	for _, tt := range tests {
		switch want := tt.want.(type) {
		case string:
			if got := e.GetString(tt.field); got != want {
				t.Errorf("%s: got %q, want %q", tt.field, got, want)
			}
		case int:
			if got := e.GetInt(tt.field); got != want {
				t.Errorf("%s: got %d, want %d", tt.field, got, want)
			}
		}
	}

	// Float fields need direct map access.
	if v, ok := e.Fields["jackett_dl_factor"]; !ok {
		t.Error("jackett_dl_factor not set")
	} else if f, ok := v.(float64); !ok || f != 0.5 {
		t.Errorf("jackett_dl_factor: got %v, want 0.5", v)
	}
	if v, ok := e.Fields["jackett_ul_factor"]; !ok {
		t.Error("jackett_ul_factor not set")
	} else if f, ok := v.(float64); !ok || f != 2.0 {
		t.Errorf("jackett_ul_factor: got %v, want 2.0", v)
	}
}

func TestCheckTorznabError(t *testing.T) {
	t.Run("detects error response", func(t *testing.T) {
		data := []byte(`<?xml version="1.0"?><error code="100" description="Incorrect user credentials"/>`)
		err := checkTorznabError(data)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "100") || !strings.Contains(err.Error(), "Incorrect user credentials") {
			t.Errorf("error message missing code/description: %v", err)
		}
	})

	t.Run("no error for normal feed", func(t *testing.T) {
		data := buildXML(nil)
		if err := checkTorznabError(data); err != nil {
			t.Errorf("unexpected error for normal feed: %v", err)
		}
	})
}

func TestParseTorznabReturnsErrorOnAPIError(t *testing.T) {
	data := []byte(`<?xml version="1.0"?><error code="200" description="Missing parameter"/>`)
	_, err := parseTorznab(data, "idx")
	if err == nil {
		t.Fatal("expected error from parseTorznab on API error response")
	}
}
