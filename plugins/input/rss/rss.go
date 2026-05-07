// Package rss provides an RSS/Atom input plugin.
package rss

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "rss",
		Description: "fetch entries from an RSS 2.0 or Atom 1.0 feed",
		PluginPhase: plugin.PhaseInput,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "url", "rss"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "rss", "url", "all_entries")...)
	return errs
}

type rssPlugin struct {
	url        string
	allEntries bool
	client     *http.Client
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	url, _ := cfg["url"].(string)
	if url == "" {
		return nil, fmt.Errorf("rss: 'url' is required")
	}
	allEntries, _ := cfg["all_entries"].(bool)
	return &rssPlugin{
		url:        url,
		allEntries: allEntries,
		client:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *rssPlugin) Name() string         { return "rss" }
func (p *rssPlugin) Phase() plugin.Phase  { return plugin.PhaseInput }

func (p *rssPlugin) Run(ctx context.Context, _ *plugin.TaskContext) ([]*entry.Entry, error) {
	body, err := p.fetch(ctx)
	if err != nil {
		return nil, err
	}
	defer body.Close()

	data, err := io.ReadAll(body)
	if err != nil {
		return nil, fmt.Errorf("rss: read body: %w", err)
	}

	entries, err := parse(data)
	if err != nil {
		return nil, fmt.Errorf("rss: parse feed: %w", err)
	}

	for _, e := range entries {
		e.Set("rss_feed", p.url)
		// Promote rss_feed to standard field and seed SetRSSInfo with feed URL.
		// Individual entries add their own fields in parseRSS/parseAtom.
		e.SetRSSInfo(entry.RSSInfo{Feed: p.url})
	}
	return entries, nil
}

// fetch performs the HTTP GET with simple retry on transient errors.
func (p *rssPlugin) fetch(ctx context.Context) (io.ReadCloser, error) {
	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
		if err != nil {
			return nil, fmt.Errorf("rss: build request: %w", err)
		}
		req.Header.Set("User-Agent", "pipeliner/1.0")
		req.Header.Set("Accept", "application/rss+xml, application/atom+xml, application/xml, text/xml")

		resp, err := p.client.Do(req)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode >= 500 {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			return nil, fmt.Errorf("rss: HTTP %d from %s", resp.StatusCode, p.url)
		}
		return resp.Body, nil
	}
	return nil, fmt.Errorf("rss: fetch %s after retries: %w", p.url, lastErr)
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
	// Seed counts from common torrent RSS namespaces.
	// torrent namespace: http://xmlns.ezrss.it/0.1/ and similar
	TorrentSeeds int `xml:"http://xmlns.ezrss.it/0.1/ seeds"`
	// nyaa namespace
	NyaaSeeds int `xml:"https://nyaa.si/xmlns/nyaa seeders"`
	// Jackett / Prowlarr
	JackettSeeds int `xml:"https://github.com/Jackett/Jackett seeders"`
	// Generic torrent namespace used by many private trackers
	TRSeeds int `xml:"http://www.shareprice.co.uk torrent_seeds"`
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
	Title   atomText   `xml:"title"`
	Summary atomText   `xml:"summary"`
	Links   []atomLink `xml:"link"`
	Updated string     `xml:"updated"`
	ID      string     `xml:"id"`
}

type atomText struct {
	Value string `xml:",chardata"`
}

type atomLink struct {
	Href string `xml:"href,attr"`
	Rel  string `xml:"rel,attr"`
	Type string `xml:"type,attr"`
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
		url := item.Link
		if item.Enclosure.URL != "" {
			url = item.Enclosure.URL
		}
		if url == "" {
			continue
		}
		desc := strings.TrimSpace(item.Description)
		pubdate := strings.TrimSpace(item.PubDate)
		e := entry.New(strings.TrimSpace(item.Title), url)
		e.Set("rss_link", item.Link)
		e.Set("rss_description", desc)
		e.Set("rss_pubdate", pubdate)
		e.Set("rss_guid", item.GUID)
		if item.Enclosure.URL != "" {
			e.Set("rss_enclosure_url", item.Enclosure.URL)
			e.Set("rss_enclosure_type", item.Enclosure.Type)
		}
		seeds := item.seedCount()
		if seeds > 0 {
			e.Set("torrent_seeds", seeds)
		}
		e.SetGenericInfo(entry.GenericInfo{
			Title:         strings.TrimSpace(item.Title),
			Description:   desc,
			PublishedDate: pubdate,
		})
		e.SetRSSInfo(entry.RSSInfo{
			GUID:          item.GUID,
			Link:          item.Link,
			EnclosureURL:  item.Enclosure.URL,
			EnclosureType: item.Enclosure.Type,
		})
		if seeds > 0 {
			e.SetTorrentInfo(entry.TorrentInfo{Seeds: seeds})
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
		url := atomURL(item.Links)
		if url == "" {
			continue
		}
		title := strings.TrimSpace(item.Title.Value)
		desc := strings.TrimSpace(item.Summary.Value)
		pubdate := strings.TrimSpace(item.Updated)
		e := entry.New(title, url)
		e.Set("rss_link", url)
		e.Set("rss_description", desc)
		e.Set("rss_pubdate", pubdate)
		e.Set("rss_guid", item.ID)
		e.SetGenericInfo(entry.GenericInfo{
			Title:         title,
			Description:   desc,
			PublishedDate: pubdate,
		})
		e.SetRSSInfo(entry.RSSInfo{
			GUID: item.ID,
			Link: url,
		})
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
