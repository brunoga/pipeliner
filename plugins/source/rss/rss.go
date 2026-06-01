// Package rss provides an RSS/Atom source and search plugin.
//
// Use url= for a fixed feed URL (passive source mode) or url_template= for a
// parameterized URL (search mode, usable inside discover.search). The two
// modes use identical parsing and fetching logic.
//
// Config keys:
//
//	url           - Fixed feed URL (required unless url_template is set)
//	url_template  - URL pattern with {Query} or {QueryEscaped} placeholder (required unless url is set)
//	timeout       - HTTP request timeout (default: "30s")
package rss

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/interp"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:     "rss",
		Description:    "fetch entries from an RSS 2.0 or Atom 1.0 feed; use url= for a fixed feed or url_template= as a discover.search backend",
		Role:           plugin.RoleSource,
		IsSearchPlugin: true,
		Produces: []string{
			entry.FieldSource,
			entry.FieldTitle,
			entry.FieldRSSFeed,
		},
		MayProduce: []string{
			entry.FieldDescription,
			entry.FieldPublishedDate,
			entry.FieldRSSGUID,
			entry.FieldRSSLink,
			entry.FieldRSSEnclosureURL,
			entry.FieldRSSEnclosureType,
			entry.FieldRSSCategory,
			entry.FieldTorrentSeeds,
			entry.FieldTorrentLeechers,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentGrabs,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "url",          Type: plugin.FieldTypeString,   Hint: "Fixed feed URL (passive source)"},
			{Key: "url_template", Type: plugin.FieldTypeString,   Hint: "URL with {Query}/{QueryEscaped} placeholder (search backend)"},
			{Key: "timeout",      Type: plugin.FieldTypeDuration, Hint: "HTTP request timeout (default 30s)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	hasURL, _ := cfg["url"].(string)
	hasTmpl, _ := cfg["url_template"].(string)
	switch {
	case hasURL == "" && hasTmpl == "":
		errs = append(errs, fmt.Errorf("rss: one of 'url' or 'url_template' is required"))
	case hasURL != "" && hasTmpl != "":
		errs = append(errs, fmt.Errorf("rss: specify either 'url' or 'url_template', not both"))
	}
	if err := plugin.OptDuration(cfg, "timeout", "rss"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "rss", "url", "url_template", "timeout")...)
	return errs
}

type rssPlugin struct {
	urlIP  *interp.Interpolator
	client *http.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	rawURL, _ := cfg["url"].(string)
	tmpl, _ := cfg["url_template"].(string)

	var pattern string
	switch {
	case rawURL != "" && tmpl != "":
		return nil, fmt.Errorf("rss: specify either 'url' or 'url_template', not both")
	case rawURL != "":
		pattern = rawURL
	case tmpl != "":
		pattern = tmpl
	default:
		return nil, fmt.Errorf("rss: one of 'url' or 'url_template' is required")
	}

	ip, err := interp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("rss: invalid url_template: %w", err)
	}

	timeout := 30 * time.Second
	if v, _ := cfg["timeout"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("rss: invalid timeout %q: %w", v, err)
		}
		timeout = d
	}

	return &rssPlugin{
		urlIP:  ip,
		client: &http.Client{Timeout: timeout},
	}, nil
}

func (p *rssPlugin) Name() string { return "rss" }

// Generate implements SourcePlugin — fetches the feed with an empty query.
func (p *rssPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return p.Search(ctx, tc, "")
}

// Search implements SearchPlugin — renders the URL template with query, fetches
// and parses the feed, then returns the resulting entries.
func (p *rssPlugin) Search(ctx context.Context, tc *plugin.TaskContext, query string) ([]*entry.Entry, error) {
	fetchURL, err := p.urlIP.Render(map[string]any{
		"Query":        query,
		"QueryEscaped": url.QueryEscape(query),
	})
	if err != nil {
		return nil, fmt.Errorf("rss: render URL for %q: %w", query, err)
	}

	tc.Logger.Debug("rss: fetching feed", "url", fetchURL)

	data, err := p.fetch(ctx, tc, fetchURL)
	if err != nil {
		return nil, err
	}

	entries, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("rss: parse feed %s: %w", fetchURL, err)
	}

	tc.Logger.Debug("rss: feed parsed", "url", fetchURL, "entries", len(entries))

	src := "rss:" + hostFromURL(fetchURL)
	for _, e := range entries {
		e.Set(entry.FieldSource, src)
		e.SetRSSInfo(entry.RSSInfo{Feed: fetchURL})
	}
	return entries, nil
}

// fetch retrieves the feed body, retrying up to 3 times on transient errors.
func (p *rssPlugin) fetch(ctx context.Context, tc *plugin.TaskContext, fetchURL string) ([]byte, error) {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			tc.Logger.Warn("rss: transient error, retrying",
				"url", fetchURL, "attempt", attempt+1, "of", 3, "err", lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		data, retry, err := p.doFetch(ctx, fetchURL)
		if err == nil {
			return data, nil
		}
		if !retry {
			return nil, err
		}
		lastErr = err
	}
	return nil, fmt.Errorf("rss: %s after retries: %w", fetchURL, lastErr)
}

// doFetch performs a single HTTP request. Returns (data, retry, err) where
// retry is true for transient failures (network errors, HTTP 5xx).
func (p *rssPlugin) doFetch(ctx context.Context, fetchURL string) (_ []byte, retry bool, _ error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("rss: build request: %w", err)
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")
	req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("rss: fetch %s: %w", fetchURL, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(io.LimitReader(resp.Body, 10*1024*1024))
	if err != nil {
		return nil, true, fmt.Errorf("rss: read body from %s: %w", fetchURL, err)
	}

	if resp.StatusCode >= 500 {
		return nil, true, fmt.Errorf("rss: HTTP %d from %s", resp.StatusCode, fetchURL)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, false, fmt.Errorf("rss: HTTP %d from %s", resp.StatusCode, fetchURL)
	}
	return data, false, nil
}

func hostFromURL(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return rawURL
	}
	return u.Host
}

// isFetchableURL reports whether s starts with a scheme that downstream sinks
// can act on (HTTP(S) for direct fetches, magnet: for magnet links). Used to
// reject opaque RSS GUIDs / Atom IDs (urn:uuid:…, internal hashes) that would
// otherwise be propagated as e.URL.
func isFetchableURL(s string) bool {
	return strings.HasPrefix(s, "http://") ||
		strings.HasPrefix(s, "https://") ||
		strings.HasPrefix(s, "magnet:")
}

// --- XML structs for RSS 2.0 and Atom 1.0 ---

// feedEnvelope peeks at the root element to dispatch to the right parser.
type feedEnvelope struct {
	XMLName xml.Name
}

// rss20 is the RSS 2.0 document structure.
type rss20 struct {
	Channel struct {
		Items []rss20Item `xml:"item"`
	} `xml:"channel"`
}

type rss20Item struct {
	Title       string       `xml:"title"`
	Link        string       `xml:"link"`
	Description string       `xml:"description"`
	PubDate     string       `xml:"pubDate"`
	GUID        string       `xml:"guid"`
	Enclosure   rssEnclosure `xml:"enclosure"`
	Categories  []string     `xml:"category"`

	// Media RSS namespace — alternative or supplement to enclosure.
	MediaContent struct {
		URL  string `xml:"url,attr"`
		Type string `xml:"type,attr"`
	} `xml:"http://search.yahoo.com/mrss/ content"`

	// Dublin Core — date fallback when pubDate is absent.
	DCDate string `xml:"http://purl.org/dc/elements/1.1/ date"`

	// Torrent seed counts from common torrent RSS namespaces.
	TorrentSeeds int `xml:"http://xmlns.ezrss.it/0.1/ seeds"`
	NyaaSeeds    int `xml:"https://nyaa.si/xmlns/nyaa seeders"`
	JackettSeeds int `xml:"https://github.com/Jackett/Jackett seeders"`
	TRSeeds      int `xml:"http://www.shareprice.co.uk torrent_seeds"`

	// Nyaa extended fields.
	NyaaLeechers  int    `xml:"https://nyaa.si/xmlns/nyaa leechers"`
	NyaaDownloads int    `xml:"https://nyaa.si/xmlns/nyaa downloads"`
	NyaaInfoHash  string `xml:"https://nyaa.si/xmlns/nyaa infoHash"`
	NyaaCategory  string `xml:"https://nyaa.si/xmlns/nyaa category"`
}

type rssEnclosure struct {
	URL  string `xml:"url,attr"`
	Type string `xml:"type,attr"`
}

// seedCount returns the first non-zero seed count found across all known
// torrent RSS namespace fields.
func (it *rss20Item) seedCount() int {
	for _, n := range []int{it.TorrentSeeds, it.NyaaSeeds, it.JackettSeeds, it.TRSeeds} {
		if n > 0 {
			return n
		}
	}
	return 0
}

// atom10 is the Atom 1.0 document structure.
type atom10 struct {
	Entries []atom10Entry `xml:"entry"`
}

type atom10Entry struct {
	Title      atomText       `xml:"title"`
	Summary    atomText       `xml:"summary"`
	Links      []atomLink     `xml:"link"`
	Updated    string         `xml:"updated"`
	ID         string         `xml:"id"`
	Categories []atomCategory `xml:"category"`
}

type atomText struct {
	Value string `xml:",chardata"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
}

type atomCategory struct {
	Term  string `xml:"term,attr"`
	Label string `xml:"label,attr"`
}

func parse(data []byte) ([]*entry.Entry, error) {
	var env feedEnvelope
	if err := xml.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("identify feed format: %w", err)
	}

	switch strings.ToLower(env.XMLName.Local) {
	case "rss":
		return parseRSS(data)
	case "feed":
		return parseAtom(data)
	default:
		return nil, fmt.Errorf("unknown feed root element %q", env.XMLName.Local)
	}
}

func parseRSS(data []byte) ([]*entry.Entry, error) {
	var feed rss20
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	var entries []*entry.Entry
	for _, item := range feed.Channel.Items {
		// URL priority: enclosure > media:content > link > GUID.
		// Enclosure is preferred because for torrent feeds it is the download URL.
		// GUID is only used when it looks like a fetchable URL — RSS GUIDs with
		// isPermaLink="false" are arbitrary identifiers (hashes, internal IDs)
		// and must not be passed to downstream sinks as a URL.
		u := item.Link
		if u == "" && isFetchableURL(item.GUID) {
			u = item.GUID
		}
		if item.MediaContent.URL != "" {
			u = item.MediaContent.URL
		}
		if item.Enclosure.URL != "" {
			u = item.Enclosure.URL
		}
		if u == "" {
			continue
		}

		title := strings.TrimSpace(item.Title)
		if title == "" {
			title = u
		}

		// Published date: pubDate, fall back to dc:date.
		pubDate := strings.TrimSpace(item.PubDate)
		if pubDate == "" {
			pubDate = strings.TrimSpace(item.DCDate)
		}

		e := entry.New(title, u)
		e.SetGenericInfo(entry.GenericInfo{
			Title:         title,
			Description:   strings.TrimSpace(item.Description),
			PublishedDate: pubDate,
		})
		e.SetRSSInfo(entry.RSSInfo{
			GUID:          item.GUID,
			Link:          item.Link,
			EnclosureURL:  item.Enclosure.URL,
			EnclosureType: item.Enclosure.Type,
		})

		// Torrent metadata.
		ti := entry.TorrentInfo{}
		if seeds := item.seedCount(); seeds > 0 {
			ti.Seeds = seeds
		}
		if item.NyaaLeechers > 0 {
			ti.Leechers = item.NyaaLeechers
		}
		if item.NyaaInfoHash != "" {
			ti.InfoHash = strings.ToLower(item.NyaaInfoHash)
		}
		e.SetTorrentInfo(ti)
		if item.NyaaDownloads > 0 {
			e.Set(entry.FieldTorrentGrabs, item.NyaaDownloads)
		}

		// Category: prefer standard <category> elements; fall back to nyaa:category.
		if cats := strings.Join(item.Categories, ", "); cats != "" {
			e.Set(entry.FieldRSSCategory, cats)
		} else if item.NyaaCategory != "" {
			e.Set(entry.FieldRSSCategory, item.NyaaCategory)
		}

		entries = append(entries, e)
	}
	return entries, nil
}

func parseAtom(data []byte) ([]*entry.Entry, error) {
	var feed atom10
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, err
	}
	var entries []*entry.Entry
	for _, item := range feed.Entries {
		u := atomURL(item.Links)
		// Fall back to the Atom ID only when it is a fetchable URL. Atom IDs
		// are IRIs and may be opaque URNs (e.g. urn:uuid:..., tag:host,date:...),
		// which would otherwise reach sinks as scheme-less URLs.
		if u == "" {
			id := strings.TrimSpace(item.ID)
			if isFetchableURL(id) {
				u = id
			}
		}
		if u == "" {
			continue
		}

		title := strings.TrimSpace(item.Title.Value)
		if title == "" {
			title = u
		}

		e := entry.New(title, u)
		e.SetGenericInfo(entry.GenericInfo{
			Title:         title,
			Description:   strings.TrimSpace(item.Summary.Value),
			PublishedDate: strings.TrimSpace(item.Updated),
		})
		e.SetRSSInfo(entry.RSSInfo{
			GUID: item.ID,
			Link: u,
		})

		if cats := atomCategories(item.Categories); cats != "" {
			e.Set(entry.FieldRSSCategory, cats)
		}

		entries = append(entries, e)
	}
	return entries, nil
}

// atomURL picks the best link from an Atom entry's link list.
// Prefers rel="enclosure", then rel="alternate", then the first href.
func atomURL(links []atomLink) string {
	var alternate, first string
	for _, l := range links {
		if l.Href == "" {
			continue
		}
		if first == "" {
			first = l.Href
		}
		switch l.Rel {
		case "enclosure":
			return l.Href
		case "alternate":
			alternate = l.Href
		}
	}
	if alternate != "" {
		return alternate
	}
	return first
}

// atomCategories joins the human-readable labels (or terms) from Atom
// category elements into a single comma-separated string.
func atomCategories(cats []atomCategory) string {
	var parts []string
	for _, c := range cats {
		label := c.Label
		if label == "" {
			label = c.Term
		}
		if label != "" {
			parts = append(parts, label)
		}
	}
	return strings.Join(parts, ", ")
}
