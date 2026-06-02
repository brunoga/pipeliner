// Package jackett provides a Jackett source plugin that queries Jackett indexer
// proxies via the Torznab API. It works as both a passive source (returning
// recent results without a query) and as an active search backend for
// discover.search.
//
// Config keys:
//
//	url        - Jackett base URL, e.g. "http://localhost:9117" (required)
//	api_key    - Jackett API key (required)
//	indexers   - list of indexer IDs to query; use ["all"] for all (default: ["all"])
//	categories - Torznab category codes to restrict results (optional)
//	query      - static search query for passive-source mode; empty returns recent results
//	limit      - maximum results to return (optional)
//	timeout    - HTTP request timeout (default: "60s")
package jackett

import (
	"context"
	"fmt"
	"io"
	"log/slog"
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
		PluginName:     "jackett",
		Description:    "query Jackett indexers via Torznab; use as a passive feed source or as a discover.search backend",
		Role:           plugin.RoleSource,
		IsSearchPlugin: true,
		Produces: []string{
			entry.FieldTitle,
			entry.FieldSource,
			entry.FieldTorrentLinkType,
		},
		MayProduce: []string{
			entry.FieldTorrentSeeds,
			entry.FieldTorrentLeechers,
			entry.FieldTorrentInfoHash,
			entry.FieldTorrentFileSize,
			entry.FieldTorrentGrabs,
			entry.FieldPublishedDate,
			entry.FieldVideoYear,
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			"jackett_category",
			"jackett_imdb_id",
			"jackett_tvdb_id",
			"jackett_tmdb_id",
			"jackett_dl_factor",
			"jackett_ul_factor",
		},
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "url",        Type: plugin.FieldTypeString,   Required: true, Hint: "Jackett base URL (e.g. http://localhost:9117)"},
			{Key: "api_key",    Type: plugin.FieldTypeString,   Required: true, Hint: "Jackett API key"},
			{Key: "indexers",   Type: plugin.FieldTypeList,                     Hint: `Indexer IDs to query; omit or use ["all"] for all`},
			{Key: "categories", Type: plugin.FieldTypeList,                     Hint: "Torznab category codes (e.g. 2000, 5000)"},
			{Key: "query",      Type: plugin.FieldTypeString,                   Hint: "Static search query; empty returns recent results (source mode only)"},
			{Key: "limit",      Type: plugin.FieldTypeInt,                      Hint: "Maximum results"},
			{Key: "timeout",    Type: plugin.FieldTypeDuration,                 Hint: "HTTP request timeout (default 60s)"},
		},
	})
}

type jackettPlugin struct {
	baseURL    string
	apiKey     string
	indexers   []string
	categories string // comma-separated Torznab category codes
	query      string // static query for source mode; empty = recent results
	limit      int    // 0 = no limit
	client     *http.Client
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

	query, _ := cfg["query"].(string)

	return &jackettPlugin{
		baseURL:    baseURL,
		apiKey:     apiKey,
		indexers:   indexers,
		categories: strings.Join(categories, ","),
		query:      query,
		limit:      limit,
		client:     &http.Client{Timeout: timeout},
	}, nil
}

func (p *jackettPlugin) Name() string { return "jackett" }

// Generate implements SourcePlugin — returns recent results, or results for the
// configured static query if one was set. Wraps the static query in a synthetic
// entry so Search's hint-extraction path naturally falls back to a plain
// title-only Torznab call (no year/imdbid/season/etc. on the entry).
func (p *jackettPlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	return p.Search(ctx, tc, entry.New(p.query, ""))
}

type indexerResult struct {
	indexer string
	entries []*entry.Entry
	err     error
}

// Search implements SearchPlugin — queries all configured indexers concurrently
// and returns the merged, deduplicated results. Per-indexer errors are logged
// and skipped so a single broken indexer never aborts results from others.
// Deduplication prefers the entry with the most seeds when the same info hash
// appears from multiple indexers.
//
// Hint fields on the query entry (media_type, video_year, IMDb/TMDb/TVDB IDs,
// season/episode) are translated into typed Torznab parameters (t=movie or
// t=tvsearch with year=/imdbid=/season=/ep=/etc.) so the indexer can filter
// server-side rather than the caller filtering returned results.
func (p *jackettPlugin) Search(ctx context.Context, tc *plugin.TaskContext, qe *entry.Entry) ([]*entry.Entry, error) {
	params := buildSearchParams(qe)

	ch := make(chan indexerResult, len(p.indexers))
	for _, indexer := range p.indexers {
		go func() {
			entries, err := p.searchIndexer(ctx, tc, indexer, params)
			ch <- indexerResult{indexer: indexer, entries: entries, err: err}
		}()
	}

	// Collect all results from all indexers.
	var raw []*entry.Entry
	for range p.indexers {
		r := <-ch
		if r.err != nil {
			tc.Logger.Error("jackett: indexer search failed", "indexer", r.indexer, "err", r.err)
			continue
		}
		raw = append(raw, r.entries...)
	}

	// First pass: find the best entry per info hash (most seeds wins).
	bestByHash := make(map[string]*entry.Entry)
	for _, e := range raw {
		if hash := e.GetString(entry.FieldTorrentInfoHash); hash != "" {
			if ex, ok := bestByHash[hash]; !ok || e.GetInt(entry.FieldTorrentSeeds) > ex.GetInt(entry.FieldTorrentSeeds) {
				bestByHash[hash] = e
			}
		}
	}

	// Second pass: emit entries, deduplicating by hash then by URL.
	seenURL := make(map[string]bool)
	var all []*entry.Entry
	for _, e := range raw {
		if hash := e.GetString(entry.FieldTorrentInfoHash); hash != "" {
			if bestByHash[hash] == e { // emit only the best entry for this hash
				bestByHash[hash] = nil // prevent re-emission
				seenURL[e.URL] = true
				all = append(all, e)
			}
			continue
		}
		// No info hash: fall back to URL deduplication.
		if !seenURL[e.URL] {
			seenURL[e.URL] = true
			all = append(all, e)
		}
	}
	return all, nil
}

// searchIndexer fetches results from a single indexer, retrying up to 3 times
// on transient failures (network errors and HTTP 5xx). Permanent failures
// (4xx, Torznab API errors) are returned immediately without retry.
func (p *jackettPlugin) searchIndexer(ctx context.Context, tc *plugin.TaskContext, indexer string, baseParams url.Values) ([]*entry.Entry, error) {
	endpoint := fmt.Sprintf("%s/api/v2.0/indexers/%s/results/torznab/api",
		p.baseURL, url.PathEscape(indexer))
	// Clone so per-indexer apikey/cat/limit additions don't mutate the shared
	// param map.
	params := url.Values{}
	for k, v := range baseParams {
		params[k] = v
	}
	params.Set("apikey", p.apiKey)
	if p.categories != "" {
		params.Set("cat", p.categories)
	}
	if p.limit > 0 {
		params.Set("limit", strconv.Itoa(p.limit))
	}
	fetchURL := endpoint + "?" + params.Encode()

	tc.Logger.Debug("jackett: querying indexer",
		"indexer", indexer, "t", params.Get("t"), "q", params.Get("q"))

	var lastErr error
	for attempt := range 3 {
		if attempt > 0 {
			tc.Logger.Warn("jackett: transient error, retrying",
				"indexer", indexer, "attempt", attempt+1, "of", 3, "err", lastErr)
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
		}
		entries, retry, err := p.doFetch(ctx, fetchURL, indexer, tc.Logger)
		if err == nil {
			tc.Logger.Debug("jackett: indexer returned results", "indexer", indexer, "count", len(entries))
			return entries, nil
		}
		if !retry {
			return nil, err
		}
		lastErr = err
	}
	return nil, lastErr
}

// doFetch performs a single HTTP fetch. It returns (entries, retry, err).
// retry is true for transient failures (network errors, HTTP 5xx) and false
// for permanent failures (HTTP 4xx, Torznab API errors, parse errors).
func (p *jackettPlugin) doFetch(ctx context.Context, fetchURL, indexer string, logger *slog.Logger) (_ []*entry.Entry, retry bool, _ error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fetchURL, nil)
	if err != nil {
		return nil, false, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("fetch: %w", err) // network error — transient
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4*1024*1024))
	if err != nil {
		return nil, true, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode >= 500 {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, true, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, indexer, snippet) // 5xx — transient
	}
	if resp.StatusCode != http.StatusOK {
		snippet := strings.TrimSpace(string(body))
		if len(snippet) > 200 {
			snippet = snippet[:200]
		}
		return nil, false, fmt.Errorf("HTTP %d from %s: %s", resp.StatusCode, indexer, snippet) // 4xx — permanent
	}

	entries, err := parseTorznab(body, indexer, logger)
	return entries, false, err // parse/API errors — permanent
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "url", "jackett"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.RequireString(cfg, "api_key", "jackett"); err != nil {
		errs = append(errs, err)
	}
	if err := validateLimit(cfg); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "timeout", "jackett"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "jackett", "url", "api_key", "indexers", "categories", "query", "limit", "timeout")...)
	return errs
}

func validateLimit(cfg map[string]any) error {
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
		return fmt.Errorf("jackett: \"limit\" must be a positive integer")
	}
	if n <= 0 {
		return fmt.Errorf("jackett: \"limit\" must be a positive integer, got %d", n)
	}
	return nil
}

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
