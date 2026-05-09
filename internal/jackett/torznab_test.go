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

func buildXML(items []Item) []byte {
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
	data := buildXML([]Item{{
		Title:     "My.Show.S01E01.720p",
		Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "https://jackett.host/dl/idx/?key=abc&path=xyz&file=My.Show"},
		Size:      1_000_000_000,
		Attrs: []Attr{
			{Name: "seeders", Value: "10"},
			{Name: "leechers", Value: "2"},
			{Name: "infohash", Value: "AABBCCDD"},
			{Name: "category", Value: "5030"},
		},
	}})

	entries, err := ParseTorznab(data, "myindexer")
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
	if got := e.GetString("jackett_indexer"); got != "myindexer" {
		t.Errorf("jackett_indexer: got %q", got)
	}
}

func TestParseTorznabMagnetEntry(t *testing.T) {
	magnet := "magnet:?xt=urn:btih:aabbccddeeff00112233445566778899aabbccdd&dn=My+Show+S01E01"
	data := buildXML([]Item{{
		Title:     "My.Show.S01E01.720p",
		Enclosure: struct{ URL string `xml:"url,attr"` }{URL: "https://jackett.host/dl/idx/?key=abc&path=xyz&file=My.Show"},
		Attrs: []Attr{
			{Name: "magneturl", Value: magnet},
			{Name: "seeders", Value: "5"},
		},
	}})

	entries, err := ParseTorznab(data, "idx")
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
	data := buildXML([]Item{{Title: "No Link Entry"}})
	entries, err := ParseTorznab(data, "idx")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("expected 0 entries for item with no link, got %d", len(entries))
	}
}
