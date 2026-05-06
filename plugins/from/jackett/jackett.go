// Package jackett provides a from plugin that queries a Jackett indexer
// proxy via the Torznab API. Unlike rss_search, it speaks Torznab natively:
// seeder/leecher counts, info hashes, and file sizes come back in the search
// response itself, so no separate metadata fetch is needed.
//
// Config keys:
//
//	url        - Jackett base URL, e.g. "http://localhost:9117" (required)
//	api_key    - Jackett API key (required)
//	indexers   - list of indexer IDs to query; use ["all"] for all configured
//	             indexers (default: ["all"]). All indexers are passed to Jackett
//	             in a single API call as a comma-separated list; Jackett
//	             aggregates results server-side.
//	categories - list of Torznab category codes to restrict results
//	             (optional). Common codes:
//	               2000  Movies        5000  TV
//	               2010  Movies/HD     5030  TV/HD
//	               2020  Movies/SD     5040  TV/SD
//	limit      - maximum number of results to return (optional, default: no limit).
//	timeout    - HTTP request timeout, e.g. "60s", "2m" (default: "60s").
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
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "jackett",
		Description: "search Jackett indexers via the Torznab API",
		PluginPhase: plugin.PhaseFrom,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "url", "jackett"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "api_key", "jackett"); err != nil {
		errs = append(errs, err)
	}
	if err := validateLimit(cfg, "jackett"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "timeout", "jackett"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "jackett", "url", "api_key", "indexers", "categories", "limit", "timeout")...)
	return errs
}

// validateLimit checks that the optional limit key, if set, is a positive integer.
func validateLimit(cfg map[string]any, pluginName string) error {
	v, ok := cfg["limit"]
	if !ok {
		return nil
	}
	var n int
	switch t := v.(type) {
	case int:
		n = t
	case int64:
		n = int(t)
	case float64:
		n = int(t)
	default:
		return fmt.Errorf("%s: \"limit\" must be a positive integer", pluginName)
	}
	if n <= 0 {
		return fmt.Errorf("%s: \"limit\" must be a positive integer, got %d", pluginName, n)
	}
	return nil
}

type jackettPlugin struct {
	baseURL    string
	apiKey     string
	indexers   []string
	categories string // comma-separated Torznab category codes
	limit      int    // 0 = no limit
	client     *http.Client
	timeout    time.Duration
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
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

	var limit int
	switch v := cfg["limit"].(type) {
	case int:
		limit = v
	case int64:
		limit = int(v)
	case float64:
		limit = int(v)
	}

	timeout := 60 * time.Second
	if v, _ := cfg["timeout"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("jackett: invalid timeout %q: %w", v, err)
		}
		timeout = d
	}

	return &jackettPlugin{
		baseURL:    baseURL,
		apiKey:     apiKey,
		indexers:   indexers,
		categories: strings.Join(categories, ","),
		limit:      limit,
		timeout:    timeout,
		client:     &http.Client{Timeout: timeout},
	}, nil
}

func (p *jackettPlugin) Name() string        { return "jackett" }
func (p *jackettPlugin) Phase() plugin.Phase { return plugin.PhaseFrom }

// Search queries all configured indexers in a single Jackett API call by
// passing them as a comma-separated list. Jackett aggregates the results
// server-side, so limit (if set) applies to the combined result set.
func (p *jackettPlugin) Search(ctx context.Context, tc *plugin.TaskContext, query string) ([]*entry.Entry, error) {
	indexer := strings.Join(p.indexers, ",")
	results, err := p.searchIndexer(ctx, indexer, query)
	if err != nil {
		tc.Logger.Warn("jackett: search failed", "indexers", indexer, "err", err)
		return nil, nil
	}
	return results, nil
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
	if p.limit > 0 {
		params.Set("limit", strconv.Itoa(p.limit))
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

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, indexer, snippet)
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
