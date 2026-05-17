// Package jackett provides shared Torznab parsing logic used by the jackett
// and jackett_search plugins.
package jackett

import (
	"encoding/xml"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
)

// Feed is the top-level Torznab RSS envelope.
type Feed struct {
	Channel struct {
		Items []Item `xml:"item"`
	} `xml:"channel"`
}

// Item is a single result in a Torznab feed.
type Item struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
	Size      int64  `xml:"size"`
	Enclosure struct {
		URL string `xml:"url,attr"`
	} `xml:"enclosure"`
	Attrs []Attr `xml:"http://torznab.com/schemas/2015/feed attr"`
}

// Attr is a Torznab extension attribute (<torznab:attr name="..." value="..."/>).
type Attr struct {
	XMLName xml.Name `xml:"http://torznab.com/schemas/2015/feed attr"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

// ParseTorznab parses a raw Torznab XML response and returns one entry per item.
//
// It sets torrent_link_type on every entry:
//   - "magnet"  — the Torznab magneturl attribute is present; e.URL is set to
//     the magnet URI so metainfo_magnet and output plugins handle it naturally
//   - "torrent" — no magnet URI; the enclosure URL serves a .torrent file
//
// When torrent_link_type is absent (non-Jackett entries), metainfo plugins
// fall back to inspecting the URL directly.
func ParseTorznab(data []byte, indexer string) ([]*entry.Entry, error) {
	var feed Feed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("parse Torznab response: %w", err)
	}

	var entries []*entry.Entry
	for _, item := range feed.Channel.Items {
		// Prefer the enclosure URL over the item link or GUID.
		link := item.Enclosure.URL
		if link == "" {
			link = item.Link
		}
		if link == "" {
			link = item.GUID
		}
		if link == "" {
			continue
		}

		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = link
		}

		// Index torznab attrs by name for O(1) lookup.
		attrs := make(map[string]string, len(item.Attrs))
		for _, a := range item.Attrs {
			attrs[a.Name] = a.Value
		}

		// If the indexer provides a magnet URI, use it as the entry URL — the
		// proxy URL would only 302-redirect to the magnet anyway, which standard
		// HTTP clients cannot follow (magnet: is not an HTTP scheme).
		// If the magnet has no dn= (display name), populate it from the file=
		// query parameter of the Jackett proxy URL, which carries the torrent
		// title as a human-readable filename.
		linkType := "torrent"
		if v := attrs["magneturl"]; strings.HasPrefix(v, "magnet:") {
			link = ensureMagnetDN(v, link)
			linkType = "magnet"
		}

		e := entry.New(title, link)
		e.Set(entry.FieldTorrentLinkType, linkType)

		ti := entry.TorrentInfo{}
		if item.Size > 0 {
			ti.FileSize = item.Size
		}
		if v := attrs["seeders"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				ti.Seeds = int(n)
			}
		}
		if v := attrs["leechers"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				ti.Leechers = int(n)
			}
		}
		if v := attrs["infohash"]; v != "" {
			ti.InfoHash = strings.ToLower(v)
		}
		e.SetTorrentInfo(ti)

		if v := attrs["category"]; v != "" {
			e.Set("jackett_category", v)
		}
		e.Set("jackett_indexer", indexer)

		entries = append(entries, e)
	}
	return entries, nil
}

// ensureMagnetDN returns magnetURI unchanged if it already contains a dn=
// parameter. Otherwise it extracts the file= query parameter from proxyURL
// (the Jackett download proxy URL, which encodes the torrent title there) and
// appends it as dn= so the magnet link carries a human-readable display name.
func ensureMagnetDN(magnetURI, proxyURL string) string {
	mu, err := url.Parse(magnetURI)
	if err != nil {
		return magnetURI
	}
	q := mu.Query()
	if q.Get("dn") != "" {
		return magnetURI // already has a display name
	}
	pu, err := url.Parse(proxyURL)
	if err != nil {
		return magnetURI
	}
	if name := pu.Query().Get("file"); name != "" {
		q.Set("dn", name)
		mu.RawQuery = q.Encode()
		return mu.String()
	}
	return magnetURI
}
