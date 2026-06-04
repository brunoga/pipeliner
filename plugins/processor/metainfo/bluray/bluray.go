// Package bluray provides a metainfo processor that enriches movie entries
// with Blu-ray.com data: release date, studio, codec, aspect ratio, runtime,
// and the two 3D booleans (bluray_3d_release, bluray_is_3d_edition).
//
// Lookup order for each entry:
//
//  1. If the entry already has bluray_id, fetch the detail page directly.
//  2. Otherwise look up (normalisedTitle, year) in the local index cache that
//     the bluray_releases source populates from calendar passes.
//  3. On index miss, check the negative-search cache.
//  4. On negative miss too, fall back to /search/ and write through to both
//     caches.
//
// The same three buckets are used by the bluray_releases source so calendar
// passes and search lookups share one accumulating local mirror of the catalog.
//
// Config keys:
//
//	country            - "us" (default), "uk", "ca", "au", "de", "fr", "kr"
//	cache_ttl          - release-detail TTL (default 168h)
//	cache_index_ttl    - index TTL (default 720h)
//	cache_negative_ttl - negative-cache TTL (default 168h)
//	request_interval   - rate limit gap (default 1s)
//	user_agent         - custom User-Agent (default generic browser)
package bluray

import (
	"context"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/bluray"
	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const (
	pluginName = "metainfo_bluray"

	bucketIndex  = "cache_bluray_index"
	bucketNeg    = "cache_bluray_search_neg"
	bucketDetail = "cache_bluray_detail"

	defaultIndexTTL  = 720 * time.Hour
	defaultDetailTTL = 168 * time.Hour
	defaultNegTTL    = 168 * time.Hour
	defaultInterval  = 1 * time.Second
	defaultCountry   = "us"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "enrich movie entries with Blu-ray.com metadata (release date, codec, 3D-edition flag)",
		Role:        plugin.RoleProcessor,
		MayProduce: []string{
			entry.FieldEnriched,
			entry.FieldBlurayID,
			entry.FieldBlurayURL,
			entry.FieldBlurayFormat,
			entry.FieldBlurayStudio,
			entry.FieldBlurayCountry,
			entry.FieldBlurayYear,
			entry.FieldBlurayReleaseDate,
			entry.FieldBlurayRuntimeMin,
			entry.FieldBlurayCodec,
			entry.FieldBlurayResolution,
			entry.FieldBlurayAspectRatio,
			entry.FieldBlurayEdition,
			entry.FieldBluray3DRelease,
			entry.FieldBlurayIs3DEdition,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "country", Type: plugin.FieldTypeEnum, Default: defaultCountry,
				Enum: []string{"us", "uk", "ca", "au", "de", "fr", "kr"}, Hint: "Blu-ray.com locale"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long to cache release detail (default 7 days)"},
			{Key: "cache_index_ttl", Type: plugin.FieldTypeDuration, Default: "720h",
				Hint: "How long to cache the title index (default 30 days)"},
			{Key: "cache_negative_ttl", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long to cache search misses (default 7 days)"},
			{Key: "request_interval", Type: plugin.FieldTypeDuration, Default: "1s",
				Hint: "Minimum gap between requests"},
			{Key: "user_agent", Type: plugin.FieldTypeString, Hint: "Custom User-Agent (default generic browser)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	for _, key := range []string{"cache_ttl", "cache_index_ttl", "cache_negative_ttl", "request_interval"} {
		if err := plugin.OptDuration(cfg, key, pluginName); err != nil {
			errs = append(errs, err)
		}
	}
	if err := plugin.OptEnum(cfg, "country", pluginName,
		"us", "uk", "ca", "au", "de", "fr", "kr"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"country", "cache_ttl", "cache_index_ttl", "cache_negative_ttl",
		"request_interval", "user_agent")...)
	return errs
}

type processorPlugin struct {
	client      *bluray.Client
	indexCache  *cache.Cache[[]bluray.IndexEntry]
	negCache    *cache.Cache[time.Time]
	detailCache *cache.Cache[*bluray.Release]
	country     string
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	country := strOr(cfg, "country", defaultCountry)
	indexTTL := durOr(cfg, "cache_index_ttl", defaultIndexTTL)
	detailTTL := durOr(cfg, "cache_ttl", defaultDetailTTL)
	negTTL := durOr(cfg, "cache_negative_ttl", defaultNegTTL)
	interval := durOr(cfg, "request_interval", defaultInterval)

	opts := []bluray.Option{
		bluray.WithCountry(country),
		bluray.WithRequestInterval(interval),
	}
	if ua, _ := cfg["user_agent"].(string); ua != "" {
		opts = append(opts, bluray.WithUserAgent(ua))
	}

	p := &processorPlugin{
		client:      bluray.New(opts...),
		indexCache:  cache.NewPersistent[[]bluray.IndexEntry](indexTTL, db.Bucket(bucketIndex)),
		negCache:    cache.NewPersistent[time.Time](negTTL, db.Bucket(bucketNeg)),
		detailCache: cache.NewPersistent[*bluray.Release](detailTTL, db.Bucket(bucketDetail)),
		country:     country,
	}
	p.indexCache.Preload()
	return p, nil
}

func (p *processorPlugin) Name() string { return pluginName }

// Process enriches each entry in place. Entries with no resolvable title are
// passed through unchanged; lookup failures are warnings, not errors, so a
// dead Blu-ray.com query doesn't fail the whole pipeline.
func (p *processorPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		p.annotate(ctx, tc, e)
	}
	return entries, nil
}

// annotate resolves an entry to a Release and writes bluray_* fields.
func (p *processorPlugin) annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) {
	id, slug, siblings, ok := p.resolve(ctx, tc, e)
	if !ok {
		return
	}
	rel, ok := p.fetchDetail(ctx, tc, id, slug)
	if !ok {
		return
	}
	p.populate(e, rel, siblings)
}

// resolve returns the release ID/slug to fetch and the catalog siblings used
// for the bluray_3d_release boolean. The third return value is false when no
// candidate could be found and the entry should be left untouched.
func (p *processorPlugin) resolve(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) (id, slug string, siblings []bluray.IndexEntry, ok bool) {
	// Fast path: the entry already carries a Blu-ray.com ID.
	if rawID, _ := e.Fields[entry.FieldBlurayID].(string); rawID != "" {
		// Sibling lookup is by title; without a title we cannot answer
		// bluray_3d_release. The detail fetch alone still sets bluray_is_3d_edition.
		key := indexKey(searchTitle(e))
		siblings, _ = p.indexCache.Get(key)
		// If the index knows the slug for this ID, use it so the detail URL is
		// canonical (avoids relying on origin redirect behaviour).
		for _, s := range siblings {
			if s.ID == rawID && s.Slug != "" {
				return rawID, s.Slug, siblings, true
			}
		}
		return rawID, "", siblings, true
	}

	title := searchTitle(e)
	if title == "" {
		tc.Logger.Debug("metainfo_bluray: no resolvable title", "entry", e.Title)
		return "", "", nil, false
	}
	year := entry.ReleaseYear(e)
	key := indexKey(title)

	if hits, found := p.indexCache.Get(key); found && len(hits) > 0 {
		best := pickBest(hits, year)
		return best.ID, best.Slug, hits, true
	}
	if _, found := p.negCache.Get(key); found {
		return "", "", nil, false
	}

	results, err := p.client.SearchTitle(ctx, title, year)
	if err != nil {
		tc.Logger.Warn("metainfo_bluray: search failed", "title", title, "err", err)
		return "", "", nil, false
	}
	if len(results) == 0 {
		p.negCache.Set(key, time.Now())
		return "", "", nil, false
	}
	for _, r := range results {
		p.mergeIndex(key, r)
	}
	hits, _ := p.indexCache.Get(key)
	best := pickBest(results, year)
	return best.ID, best.Slug, hits, true
}

// mergeIndex appends one IndexEntry into the persisted slice, deduped by ID.
func (p *processorPlugin) mergeIndex(key string, e bluray.IndexEntry) {
	existing, _ := p.indexCache.Get(key)
	for _, x := range existing {
		if x.ID == e.ID {
			return
		}
	}
	p.indexCache.Set(key, append(existing, e))
}

// fetchDetail returns the Release for id, hitting the detail cache first.
func (p *processorPlugin) fetchDetail(ctx context.Context, tc *plugin.TaskContext, id, slug string) (*bluray.Release, bool) {
	if r, ok := p.detailCache.Get(id); ok && r != nil {
		return r, true
	}
	r, err := p.client.GetRelease(ctx, id, slug)
	if err != nil {
		tc.Logger.Warn("metainfo_bluray: get release failed", "id", id, "err", err)
		return nil, false
	}
	p.detailCache.Set(id, r)
	return r, true
}

// populate writes the bluray_* fields from a Release plus the sibling set.
func (p *processorPlugin) populate(e *entry.Entry, r *bluray.Release, siblings []bluray.IndexEntry) {
	e.Set(entry.FieldBlurayID, r.ID)
	if r.URL != "" {
		e.Set(entry.FieldBlurayURL, r.URL)
	}
	if r.Format != "" {
		e.Set(entry.FieldBlurayFormat, string(r.Format))
	}
	if r.Studio != "" {
		e.Set(entry.FieldBlurayStudio, r.Studio)
	}
	if r.Country != "" {
		e.Set(entry.FieldBlurayCountry, r.Country)
	}
	if r.Year > 0 {
		e.Set(entry.FieldBlurayYear, r.Year)
	}
	if r.ReleaseDate != "" {
		e.Set(entry.FieldBlurayReleaseDate, r.ReleaseDate)
	}
	if r.RuntimeMin > 0 {
		e.Set(entry.FieldBlurayRuntimeMin, r.RuntimeMin)
	}
	if r.Codec != "" {
		e.Set(entry.FieldBlurayCodec, r.Codec)
	}
	if r.Resolution != "" {
		e.Set(entry.FieldBlurayResolution, r.Resolution)
	}
	if r.AspectRatio != "" {
		e.Set(entry.FieldBlurayAspectRatio, r.AspectRatio)
	}
	if r.Edition != "" {
		e.Set(entry.FieldBlurayEdition, r.Edition)
	}

	is3DEdition := r.Format == bluray.FormatBD3D || strings.Contains(strings.ToUpper(r.Codec), "MVC")
	if is3DEdition {
		e.Set(entry.FieldBlurayIs3DEdition, true)
	}
	if is3DEdition || bluray.Is3DRelease(siblings) {
		e.Set(entry.FieldBluray3DRelease, true)
	}

	e.Set(entry.FieldEnriched, true)
}

// ---------- helpers ----------

// searchTitle picks the best title to query Blu-ray.com with. Preference:
// movie_title (clean canonical), then internal/movies.Parse on the raw title,
// then the raw title itself.
func searchTitle(e *entry.Entry) string {
	if v, _ := e.Fields[entry.FieldMovieTitle].(string); v != "" {
		return v
	}
	if m, ok := imovies.Parse(e.Title); ok && m != nil && m.Title != "" {
		return m.Title
	}
	return strings.TrimSpace(e.Title)
}

// indexKey mirrors the source plugin's keying: strip a trailing 3D/4K token
// from the title, then normalise.
func indexKey(title string) string {
	return match.Normalize(stripFormatToken(title))
}

func stripFormatToken(s string) string {
	t := strings.TrimSpace(s)
	low := strings.ToLower(t)
	switch {
	case strings.HasSuffix(low, " 3d"), strings.HasSuffix(low, " 4k"):
		return strings.TrimSpace(t[:len(t)-3])
	}
	return t
}

// pickBest selects the preferred release from a list of catalog hits. Rules:
// prefer entries that match the year hint; among those, prefer plain BD over
// UHD/BD3D so the processor's "primary" record describes the standard release,
// not the 3D edition. The 3D boolean is computed from the sibling set, not
// from the chosen record.
func pickBest(hits []bluray.IndexEntry, year int) bluray.IndexEntry {
	candidates := hits
	if year > 0 {
		var yearMatch []bluray.IndexEntry
		for _, h := range hits {
			if h.Year == year {
				yearMatch = append(yearMatch, h)
			}
		}
		if len(yearMatch) > 0 {
			candidates = yearMatch
		}
	}
	// Rank: BD > UHD > BD3D > DVD.
	rank := map[bluray.Format]int{
		bluray.FormatBD:   3,
		bluray.FormatUHD:  2,
		bluray.FormatBD3D: 1,
		bluray.FormatDVD:  0,
	}
	best := candidates[0]
	for _, h := range candidates[1:] {
		if rank[h.Format] > rank[best.Format] {
			best = h
		}
	}
	return best
}

func strOr(cfg map[string]any, key, def string) string {
	if v, _ := cfg[key].(string); v != "" {
		return v
	}
	return def
}

func durOr(cfg map[string]any, key string, def time.Duration) time.Duration {
	v, _ := cfg[key].(string)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return def
	}
	return d
}
