// Package jackett provides a search plugin that queries a Jackett indexer
// proxy via the Torznab API. Unlike search_rss, it speaks Torznab natively:
// seeder/leecher counts, info hashes, and file sizes come back in the search
// response itself, so no separate metadata fetch is needed.
//
// Config keys:
//
//	url        - Jackett base URL, e.g. "http://localhost:9117" (required)
//	api_key    - Jackett API key (required)
//	indexers   - list of indexer IDs to query; use ["all"] for all configured
//	             indexers (default: ["all"])
//	categories - list of Torznab category codes to restrict results
//	             (optional). Common codes:
//	               2000  Movies        5000  TV
//	               2010  Movies/HD     5030  TV/HD
//	               2020  Movies/SD     5040  TV/SD
package jackett

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "jackett",
		Description: "search Jackett indexers via the Torznab API",
		PluginPhase: plugin.PhaseSearch,
		Factory:     newPlugin,
	})
}

type jackettPlugin struct {
	baseURL    string
	apiKey     string
	indexers   []string
	categories string // comma-separated Torznab category codes
	client     *http.Client
}

func newPlugin(cfg map[string]any) (plugin.Plugin, error) {
	baseURL, _ := cfg["url"].(string)
	if baseURL == "" {
		return nil, fmt.Errorf("jackett: 'url' is required")
	}
	baseURL = strings.TrimRight(baseURL, "/")

	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("jackett: 'api_key' is required")
	}

	indexers := toStringSlice(cfg["indexers"])
	if len(indexers) == 0 {
		indexers = []string{"all"}
	}

	categories := toStringSlice(cfg["categories"])

	return &jackettPlugin{
		baseURL:    baseURL,
		apiKey:     apiKey,
		indexers:   indexers,
		categories: strings.Join(categories, ","),
		client:     &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (p *jackettPlugin) Name() string        { return "jackett" }
func (p *jackettPlugin) Phase() plugin.Phase { return plugin.PhaseSearch }

// Search queries each configured indexer and returns the merged, deduplicated
// results. Indexer errors are logged and skipped rather than aborting the
// search, so a single broken indexer doesn't prevent results from others.
func (p *jackettPlugin) Search(ctx context.Context, tc *plugin.TaskContext, query string) ([]*entry.Entry, error) {
	seen := map[string]bool{}
	var all []*entry.Entry

	for _, indexer := range p.indexers {
		results, err := p.searchIndexer(ctx, indexer, query)
		if err != nil {
			tc.Logger.Warn("jackett: indexer search failed", "indexer", indexer, "err", err)
			continue
		}
		for _, e := range results {
			if !seen[e.URL] {
				seen[e.URL] = true
				all = append(all, e)
			}
		}
	}
	return all, nil
}

func (p *jackettPlugin) searchIndexer(ctx context.Context, indexer, query string) ([]*entry.Entry, error) {
	endpoint := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api",
		p.baseURL, url.PathEscape(indexer))

	params := url.Values{
		"apikey": {p.apiKey},
		"t":      {"search"},
		"q":      {query},
	}
	if p.categories != "" {
		params.Set("cat", p.categories)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, indexer)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	return parseTorznab(body, indexer)
}

// --- Torznab XML types ---

type torznabFeed struct {
	Channel struct {
		Items []torznabItem `xml:"item"`
	} `xml:"channel"`
}

type torznabItem struct {
	Title     string `xml:"title"`
	Link      string `xml:"link"`
	GUID      string `xml:"guid"`
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

func parseTorznab(data []byte, indexer string) ([]*entry.Entry, error) {
	var feed torznabFeed
	if err := xml.Unmarshal(data, &feed); err != nil {
		return nil, fmt.Errorf("parse Torznab response: %w", err)
	}

	var entries []*entry.Entry
	for _, item := range feed.Channel.Items {
		// Prefer the enclosure (the actual .torrent / magnet link) over the
		// info page link.
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

		e := entry.New(title, link)

		// Index the torznab attrs by name for easy lookup.
		attrs := make(map[string]string, len(item.Attrs))
		for _, a := range item.Attrs {
			attrs[a.Name] = a.Value
		}

		if item.Size > 0 {
			e.Set("torrent_size", item.Size)
		}
		if v := attrs["seeders"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				e.Set("torrent_seeders", int(n))
			}
		}
		if v := attrs["leechers"]; v != "" {
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				e.Set("torrent_leechers", int(n))
			}
		}
		if v := attrs["infohash"]; v != "" {
			e.Set("torrent_info_hash", strings.ToLower(v))
		}
		if v := attrs["category"]; v != "" {
			e.Set("jackett_category", v)
		}
		e.Set("jackett_indexer", indexer)

		entries = append(entries, e)
	}
	return entries, nil
}

// --- config helpers ---

func toStringSlice(v any) []string {
	switch val := v.(type) {
	case string:
		if val == "" {
			return nil
		}
		return []string{val}
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			switch s := item.(type) {
			case string:
				out = append(out, s)
			case int64:
				out = append(out, strconv.FormatInt(s, 10))
			case float64:
				out = append(out, strconv.Itoa(int(s)))
			}
		}
		return out
	}
	return nil
}
