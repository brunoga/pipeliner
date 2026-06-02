package jackett

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
)

type torznabFeed struct {
	Channel struct {
		Items []torznabItem `xml:"item"`
	} `xml:"channel"`
}

type torznabItem struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
	PubDate   string `xml:"pubDate"`
	Size      int64  `xml:"size"`
	Enclosure struct {
		URL string `xml:"url,attr"`
	} `xml:"enclosure"`
	Attrs []torznabAttr `xml:"http://torznab.com/schemas/2015/feed attr"`
}

type torznabAttr struct {
	XMLName xml.Name `xml:"http://torznab.com/schemas/2015/feed attr"`
	Name    string   `xml:"name,attr"`
	Value   string   `xml:"value,attr"`
}

// parseTorznab parses a raw Torznab XML response and returns one entry per item.
//
// It sets torrent_link_type on every entry:
//   - "magnet"  — the Torznab magneturl attribute is present; e.URL is set to
//     the magnet URI so metainfo_magnet and output plugins handle it naturally
//   - "torrent" — no magnet URI; the enclosure URL serves a .torrent file
func parseTorznab(data []byte, indexer string, logger *slog.Logger) ([]*entry.Entry, error) {
	if err := checkTorznabError(data); err != nil {
		return nil, err
	}

	var feed torznabFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("parse Torznab response: %w", err)
	}

	var entries []*entry.Entry
	for _, item := range feed.Channel.Items {
		// Prefer the enclosure URL over the item link or GUID. Each candidate
		// must be a fetchable URL — indexers occasionally emit whitespace-only
		// link elements, empty enclosure attributes, or GUIDs that are opaque
		// identifiers (e.g. isPermaLink="false" hashes), any of which would
		// otherwise reach a sink as an empty or scheme-less URL.
		var link string
		for _, candidate := range []string{item.Enclosure.URL, item.Link, item.GUID} {
			if c := strings.TrimSpace(candidate); isFetchableURL(c) {
				link = c
				break
			}
		}
		if link == "" {
			if logger != nil {
				logger.Warn("jackett: dropping item with no fetchable URL",
					"indexer", indexer, "title", strings.TrimSpace(item.Title))
			}
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
		e.Set(entry.FieldTitle, title)
		e.Set(entry.FieldTorrentLinkType, linkType)
		e.Set(entry.FieldSource, "jackett:"+indexer)

		// --- Core torrent fields ---
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

		// --- Published date: prefer Torznab publishdate attr, fall back to RSS pubDate ---
		if v := attrs["publishdate"]; v != "" {
			e.Set(entry.FieldPublishedDate, v)
		} else if item.PubDate != "" {
			e.Set(entry.FieldPublishedDate, item.PubDate)
		}

		// --- Standard video/series fields from Torznab metadata ---
		if v := attrs["year"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				e.Set(entry.FieldVideoYear, n)
			}
		}
		if v := attrs["season"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				e.Set(entry.FieldSeriesSeason, n)
			}
		}
		if v := attrs["episode"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				e.Set(entry.FieldSeriesEpisode, n)
			}
		}

		// --- Indexer statistics ---
		if v := attrs["grabs"]; v != "" {
			if n, err := strconv.Atoi(v); err == nil && n > 0 {
				e.Set(entry.FieldTorrentGrabs, n)
			}
		}

		// --- Jackett-specific fields ---
		// IDs provided by Jackett but owned by their respective metainfo plugins;
		// stored under jackett_ prefix so downstream plugins can use them for
		// fast by-ID lookups (same pattern as trakt_tmdb_id from trakt_list).
		if v := attrs["imdbid"]; v != "" {
			e.Set("jackett_imdb_id", v)
		}
		if v := attrs["tvdbid"]; v != "" {
			e.Set("jackett_tvdb_id", v)
		}
		if v := attrs["tmdbid"]; v != "" {
			e.Set("jackett_tmdb_id", v)
		}
		// Private-tracker ratio fields: 0.0 = freeleech, 0.5 = half-leech, 1.0 = normal.
		if v := attrs["downloadvolumefactor"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				e.Set("jackett_dl_factor", f)
			}
		}
		if v := attrs["uploadvolumefactor"]; v != "" {
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				e.Set("jackett_ul_factor", f)
			}
		}
		if v := attrs["category"]; v != "" {
			e.Set("jackett_category", v)
		}

		entries = append(entries, e)
	}
	return entries, nil
}

// buildSearchParams translates a query entry's hint fields into a Torznab
// parameter map. Backends that don't recognise the chosen t= category (e.g. an
// indexer configured only for movies receiving a t=tvsearch) just return empty
// results — that's the indexer's job to decide, not ours.
//
// The picked t= category is:
//   - t=movie    when media_type == "movie"
//   - t=tvsearch when media_type == "series"
//   - t=search   otherwise (the generic free-text search Jackett uses in
//     source/recent-feed mode)
//
// Year and ID hints are added when t=movie or t=tvsearch — the generic t=search
// category doesn't accept typed filters.
func buildSearchParams(qe *entry.Entry) url.Values {
	params := url.Values{}
	q := ""
	if qe != nil {
		q = strings.TrimSpace(qe.Title)
	}
	params.Set("q", q)

	if qe == nil {
		params.Set("t", "search")
		return params
	}

	mediaType := qe.GetString(entry.FieldMediaType)
	switch mediaType {
	case entry.MediaTypeMovie:
		params.Set("t", "movie")
		addMovieHints(params, qe)
	case entry.MediaTypeSeries:
		params.Set("t", "tvsearch")
		addSeriesHints(params, qe)
	default:
		params.Set("t", "search")
	}
	return params
}

func addMovieHints(params url.Values, qe *entry.Entry) {
	if y := qe.GetInt(entry.FieldVideoYear); y > 0 {
		params.Set("year", strconv.Itoa(y))
	}
	if id := imdbIDDigits(firstNonEmpty(
		qe.GetString(entry.FieldVideoImdbID),
		qe.GetString("jackett_imdb_id"),
		qe.GetString("trakt_imdb_id"),
	)); id != "" {
		params.Set("imdbid", id)
	}
	if id := firstNonEmpty(
		qe.GetString("jackett_tmdb_id"),
		intToString(qe.GetInt("trakt_tmdb_id")),
	); id != "" {
		params.Set("tmdbid", id)
	}
}

func addSeriesHints(params url.Values, qe *entry.Entry) {
	if y := qe.GetInt(entry.FieldVideoYear); y > 0 {
		params.Set("year", strconv.Itoa(y))
	}
	if id := imdbIDDigits(firstNonEmpty(
		qe.GetString(entry.FieldVideoImdbID),
		qe.GetString("jackett_imdb_id"),
		qe.GetString("trakt_imdb_id"),
	)); id != "" {
		params.Set("imdbid", id)
	}
	if id := firstNonEmpty(
		qe.GetString("jackett_tvdb_id"),
		intToString(qe.GetInt("tvdb_id")),
	); id != "" {
		params.Set("tvdbid", id)
	}
	if id := firstNonEmpty(
		qe.GetString("jackett_tmdb_id"),
		intToString(qe.GetInt("trakt_tmdb_id")),
	); id != "" {
		params.Set("tmdbid", id)
	}
	if s := qe.GetInt(entry.FieldSeriesSeason); s > 0 {
		params.Set("season", strconv.Itoa(s))
	}
	if ep := qe.GetInt(entry.FieldSeriesEpisode); ep > 0 {
		params.Set("ep", strconv.Itoa(ep))
	}
}

// imdbIDDigits strips a leading "tt" prefix and returns the numeric portion of
// an IMDb ID — Torznab indexers expect the bare digits ("0903747"), not the
// canonical "tt0903747" form.
func imdbIDDigits(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "tt")
	if s == "" {
		return ""
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return s
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func intToString(n int) string {
	if n <= 0 {
		return ""
	}
	return strconv.Itoa(n)
}

// checkTorznabError inspects data for a Torznab error response
// (<error code="..." description="..."/>) and returns a non-nil error if one
// is found. Returns nil for normal feed responses.
func checkTorznabError(data []byte) error {
	dec := xml.NewDecoder(bytes.NewReader(data))
	for {
		tok, err := dec.Token()
		if err != nil {
			return nil // not parseable as an error response; let the feed parser handle it
		}
		se, ok := tok.(xml.StartElement)
		if !ok {
			continue
		}
		if se.Name.Local != "error" {
			return nil // first element is not <error>; normal feed
		}
		var code, desc string
		for _, a := range se.Attr {
			switch a.Name.Local {
			case "code":
				code = a.Value
			case "description":
				desc = a.Value
			}
		}
		return fmt.Errorf("torznab error %s: %s", code, desc)
	}
}

// isFetchableURL reports whether s starts with a scheme that downstream sinks
// can act on (HTTP(S) for .torrent downloads, magnet: for magnet links).
func isFetchableURL(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "magnet:")
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
