// Package jackett provides a search sub-plugin that queries a Jackett indexer
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
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"strconv"

	ijackett "github.com/brunoga/pipeliner/internal/jackett"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "jackett",
		Description: "search Jackett indexers via the Torznab API; usable as a standalone DAG source or inside discover.search",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTorrentSeeds,
			entry.FieldTorrentLeechers,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentLinkType,
			entry.FieldTorrentFileSize,
		},
		Factory:        newPlugin,
		Validate:       validate,
		IsSearchPlugin: true,
		Schema: []plugin.FieldSchema{
			{Key: "url",        Type: plugin.FieldTypeString,   Required: true,  Hint: "Jackett base URL (e.g. http://localhost:9117)"},
			{Key: "api_key",    Type: plugin.FieldTypeString,   Required: true,  Hint: "Jackett API key"},
			{Key: "indexers",   Type: plugin.FieldTypeList,                      Hint: `Indexer IDs to query; omit or use ["all"] for all`},
			{Key: "categories", Type: plugin.FieldTypeList,                      Hint: "Torznab category codes (e.g. 2000, 5000)"},
			{Key: "limit",      Type: plugin.FieldTypeInt,                       Hint: "Maximum results per indexer"},
			{Key: "timeout",    Type: plugin.FieldTypeDuration,                  Hint: "HTTP request timeout (default 60s)"},
		},
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

// Generate implements SourcePlugin for DAG pipelines. It calls Search with an
// empty query, which returns recent results across configured indexers.
func (p *jackettPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return p.Search(ctx, tc, "")
}

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

	return ijackett.ParseTorznab(body, indexer)
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
