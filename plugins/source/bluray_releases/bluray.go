// Package bluray_releases scrapes Blu-ray.com's release calendar and emits one
// entry per release. It also implements SearchPlugin so it can be used as a
// title-search backend inside discover().
//
// Config keys:
//
//	country            - "us" (default), "uk", "ca", "au", "de", "fr", "kr"
//	months             - number of months back from current to scan (default 1)
//	from_year/from_month - explicit start month (overrides months)
//	to_year/to_month   - explicit end month (default: current month)
//	formats            - subset of ["BD","UHD","BD3D"] (default all)
//	cache_ttl          - index bucket TTL (default 720h)
//	cache_detail_ttl   - release-detail TTL (default 168h)
//	cache_negative_ttl - negative search TTL (default 168h)
//	request_interval   - rate limit gap (default "1s")
//	user_agent         - HTTP User-Agent (default generic browser)
package bluray_releases

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/bluray"
	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const (
	pluginName = "bluray_releases"

	bucketIndex  = "cache_bluray_index"
	bucketNeg    = "cache_bluray_search_neg"
	bucketDetail = "cache_bluray_detail"

	defaultIndexTTL  = 720 * time.Hour
	defaultDetailTTL = 168 * time.Hour
	defaultNegTTL    = 168 * time.Hour
	defaultInterval  = 1 * time.Second
	defaultCountry   = "us"
	defaultMonths    = 1
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "scrape Blu-ray.com release calendar; usable as a DAG source, a discover() search backend, or a series.list/movies.list title source",
		Role:        plugin.RoleSource,
		Produces: []string{
			entry.FieldTitle,
			entry.FieldMovieTitle,
			entry.FieldSource,
			entry.FieldMediaType,
			entry.FieldBlurayID,
			entry.FieldBlurayURL,
			entry.FieldBlurayFormat,
		},
		MayProduce: []string{
			entry.FieldVideoYear,
			entry.FieldBlurayStudio,
			entry.FieldBlurayYear,
			entry.FieldBlurayReleaseDate,
			entry.FieldBlurayEdition,
			entry.FieldBluray3DRelease,
			entry.FieldBlurayIs3DEdition,
		},
		Factory:        newPlugin,
		Validate:       validate,
		IsSearchPlugin: true,
		IsListPlugin:   true,
		Schema: []plugin.FieldSchema{
			{Key: "country", Type: plugin.FieldTypeEnum, Default: defaultCountry,
				Enum: []string{"us", "uk", "ca", "au", "de", "fr", "kr"}, Hint: "Blu-ray.com locale"},
			{Key: "months", Type: plugin.FieldTypeInt, Default: defaultMonths,
				Hint: "Number of months back from current month to scan (default 1)"},
			{Key: "from_year", Type: plugin.FieldTypeInt, Hint: "Explicit start year (overrides months)"},
			{Key: "from_month", Type: plugin.FieldTypeInt, Hint: "Explicit start month 1-12"},
			{Key: "to_year", Type: plugin.FieldTypeInt, Hint: "Explicit end year (default current)"},
			{Key: "to_month", Type: plugin.FieldTypeInt, Hint: "Explicit end month 1-12"},
			{Key: "formats", Type: plugin.FieldTypeList,
				Hint: "Subset of [BD, UHD, BD3D] (default all)"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "720h",
				Hint: "How long to cache the index (default 30 days)"},
			{Key: "cache_detail_ttl", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long to cache release detail (default 7 days)"},
			{Key: "cache_negative_ttl", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long to cache search misses (default 7 days)"},
			{Key: "request_interval", Type: plugin.FieldTypeDuration, Default: "1s",
				Hint: "Minimum gap between requests"},
			{Key: "user_agent", Type: plugin.FieldTypeString, Hint: "Custom User-Agent (default generic browser)"},
		},
		// Shared with metainfo_bluray: both plugins write the same buckets so the
		// title index built by weekly calendar passes warms per-entry enrichment.
		// Duplicate names across descriptors are deduplicated by the web layer.
		Caches: []plugin.CacheInfo{
			{Name: bucketIndex, Display: "Blu-ray.com Title Index"},
			{Name: bucketDetail, Display: "Blu-ray.com Release Detail Cache"},
			{Name: bucketNeg, Display: "Blu-ray.com Negative Search Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	for _, key := range []string{"cache_ttl", "cache_detail_ttl", "cache_negative_ttl", "request_interval"} {
		if err := plugin.OptDuration(cfg, key, pluginName); err != nil {
			errs = append(errs, err)
		}
	}
	if err := plugin.OptEnum(cfg, "country", pluginName,
		"us", "uk", "ca", "au", "de", "fr", "kr"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"country", "months", "from_year", "from_month", "to_year", "to_month",
		"formats", "cache_ttl", "cache_detail_ttl", "cache_negative_ttl",
		"request_interval", "user_agent")...)
	return errs
}

type sourcePlugin struct {
	client      *bluray.Client
	indexCache  *cache.Cache[[]bluray.IndexEntry]
	negCache    *cache.Cache[time.Time]
	detailCache *cache.Cache[*bluray.Release]

	country               string
	months                int
	fromYear, fromMonth   int
	toYear, toMonth       int
	formats               map[bluray.Format]bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	country := strOr(cfg, "country", defaultCountry)
	months := plugin.IntVal(cfg["months"], defaultMonths)
	if months < 1 {
		months = defaultMonths
	}

	indexTTL := durOr(cfg, "cache_ttl", defaultIndexTTL)
	detailTTL := durOr(cfg, "cache_detail_ttl", defaultDetailTTL)
	negTTL := durOr(cfg, "cache_negative_ttl", defaultNegTTL)
	interval := durOr(cfg, "request_interval", defaultInterval)

	formats := parseFormats(plugin.ToStringSlice(cfg["formats"]))

	clientOpts := []bluray.Option{
		bluray.WithCountry(country),
		bluray.WithRequestInterval(interval),
	}
	if ua, _ := cfg["user_agent"].(string); ua != "" {
		clientOpts = append(clientOpts, bluray.WithUserAgent(ua))
	}

	p := &sourcePlugin{
		client:      bluray.New(clientOpts...),
		indexCache:  cache.NewPersistent[[]bluray.IndexEntry](indexTTL, db.Bucket(bucketIndex)),
		negCache:    cache.NewPersistent[time.Time](negTTL, db.Bucket(bucketNeg)),
		detailCache: cache.NewPersistent[*bluray.Release](detailTTL, db.Bucket(bucketDetail)),
		country:     country,
		months:      months,
		fromYear:    plugin.IntVal(cfg["from_year"], 0),
		fromMonth:   plugin.IntVal(cfg["from_month"], 0),
		toYear:      plugin.IntVal(cfg["to_year"], 0),
		toMonth:     plugin.IntVal(cfg["to_month"], 0),
		formats:     formats,
	}
	p.indexCache.Preload()
	return p, nil
}

func (p *sourcePlugin) Name() string { return pluginName }

// Generate scans the configured month window of the release calendar, persists
// every row into the shared index cache, and emits one entry per row whose
// format is in the configured formats set.
//
// When the configured format set is exactly {BD3D}, Generate auto-routes to
// the BD3D-specific calendar (/3d/releasedates.php) which is server-side
// filtered. This is dramatically cheaper for the 3D-only use case: a typical
// recent month has 100+ rows total but only 0-3 BD3D entries, so the regular
// calendar mostly parses data we'd discard. The 3D calendar also makes
// historical backfilling feasible — scanning back to BD3D launch (~2010-09)
// returns hundreds of rows from ~180 small pages instead of ~180 large ones.
func (p *sourcePlugin) Generate(ctx context.Context, tc *plugin.TaskContext) ([]*entry.Entry, error) {
	windows := p.windows(time.Now())
	use3DCalendar := len(p.formats) == 1 && p.formats[bluray.FormatBD3D]
	var entries []*entry.Entry
	for _, w := range windows {
		var rows []bluray.CalendarEntry
		var err error
		if use3DCalendar {
			rows, err = p.client.List3DMonth(ctx, w.year, w.month)
		} else {
			rows, err = p.client.ListMonth(ctx, w.year, w.month)
		}
		if err != nil {
			tc.Logger.Warn("bluray_releases: list month failed",
				"year", w.year, "month", w.month, "err", err)
			continue
		}
		for _, row := range rows {
			p.writeIndex(row.IndexEntry)
		}
		for _, row := range rows {
			if !p.formats[row.Format] {
				continue
			}
			entries = append(entries, p.entryFromCalendar(row))
		}
	}
	return entries, nil
}

// Search satisfies plugin.SearchPlugin. Used by the discover processor and any
// other search-backend consumer. The lookup hierarchy is:
//
//  1. positive index cache (populated by calendar passes and by past searches)
//  2. negative cache (recently confirmed missing)
//  3. /search/ (writes through to both caches)
func (p *sourcePlugin) Search(ctx context.Context, tc *plugin.TaskContext, qe *entry.Entry) ([]*entry.Entry, error) {
	title := strings.TrimSpace(qe.Title)
	if title == "" {
		return nil, nil
	}
	year := entry.ReleaseYear(qe)
	key := indexKey(title)

	if hits, ok := p.indexCache.Get(key); ok && len(hits) > 0 {
		return p.entriesFromIndex(filterByYear(hits, year)), nil
	}
	if _, ok := p.negCache.Get(key); ok {
		return nil, nil
	}

	results, err := p.client.SearchTitle(ctx, title, year)
	if err != nil {
		tc.Logger.Warn("bluray_releases: search failed", "title", title, "err", err)
		return nil, nil
	}
	if len(results) == 0 {
		p.negCache.Set(key, time.Now())
		return nil, nil
	}
	for _, r := range results {
		p.writeIndex(r)
	}
	return p.entriesFromIndex(results), nil
}

// writeIndex merges one IndexEntry into the persisted set for its title key.
func (p *sourcePlugin) writeIndex(e bluray.IndexEntry) {
	key := indexKey(e.Title)
	existing, _ := p.indexCache.Get(key)
	for _, x := range existing {
		if x.ID == e.ID {
			return
		}
	}
	p.indexCache.Set(key, append(existing, e))
}

// entryFromCalendar promotes a CalendarEntry into a pipeline entry.
//
// Title strips any trailing " 3D" / " 4K" format token so consumers that fuzzy
// match on Title (e.g. movies.list= via ResolveDynamicList) see the canonical
// title. The format is preserved structurally in bluray_format /
// bluray_is_3d_edition.
func (p *sourcePlugin) entryFromCalendar(ce bluray.CalendarEntry) *entry.Entry {
	cleanTitle := stripFormatToken(ce.Title)
	url := fmt.Sprintf("https://www.blu-ray.com/movies/%s/%s/", ce.Slug, ce.ID)
	e := entry.New(cleanTitle, url)
	e.Set(entry.FieldTitle, cleanTitle)
	e.Set(entry.FieldMovieTitle, cleanTitle)
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	e.Set(entry.FieldSource, pluginName+":"+strings.ToLower(string(ce.Format)))
	e.Set(entry.FieldBlurayID, ce.ID)
	e.Set(entry.FieldBlurayURL, url)
	e.Set(entry.FieldBlurayFormat, string(ce.Format))
	if ce.Studio != "" {
		e.Set(entry.FieldBlurayStudio, ce.Studio)
	}
	if ce.Year > 0 {
		e.Set(entry.FieldBlurayYear, ce.Year)
		e.Set(entry.FieldVideoYear, ce.Year)
	}
	if ce.ReleaseDate != "" {
		e.Set(entry.FieldBlurayReleaseDate, ce.ReleaseDate)
	}
	if ce.Edition != "" {
		e.Set(entry.FieldBlurayEdition, ce.Edition)
	}
	if ce.Format == bluray.FormatBD3D {
		e.Set(entry.FieldBluray3DRelease, true)
		e.Set(entry.FieldBlurayIs3DEdition, true)
	} else if siblings, ok := p.indexCache.Get(indexKey(ce.Title)); ok && bluray.Is3DRelease(siblings) {
		e.Set(entry.FieldBluray3DRelease, true)
	}
	return e
}

// entriesFromIndex converts cached IndexEntry records into pipeline entries.
// Used by Search() so each search result can be a separate entry downstream.
func (p *sourcePlugin) entriesFromIndex(rows []bluray.IndexEntry) []*entry.Entry {
	out := make([]*entry.Entry, 0, len(rows))
	for _, r := range rows {
		cleanTitle := stripFormatToken(r.Title)
		url := fmt.Sprintf("https://www.blu-ray.com/movies/%s/%s/", r.Slug, r.ID)
		e := entry.New(cleanTitle, url)
		e.Set(entry.FieldTitle, cleanTitle)
		e.Set(entry.FieldMovieTitle, cleanTitle)
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.Set(entry.FieldSource, pluginName+":search")
		e.Set(entry.FieldBlurayID, r.ID)
		e.Set(entry.FieldBlurayURL, url)
		e.Set(entry.FieldBlurayFormat, string(r.Format))
		if r.Year > 0 {
			e.Set(entry.FieldVideoYear, r.Year)
			e.Set(entry.FieldBlurayYear, r.Year)
		}
		if r.Format == bluray.FormatBD3D {
			e.Set(entry.FieldBluray3DRelease, true)
			e.Set(entry.FieldBlurayIs3DEdition, true)
		} else if bluray.Is3DRelease(rows) {
			e.Set(entry.FieldBluray3DRelease, true)
		}
		out = append(out, e)
	}
	return out
}

// window represents one (year, month) calendar fetch.
type window struct{ year, month int }

// windows returns the set of (year, month) pairs to fetch on a Generate() call,
// based on the months/from/to config. Inclusive on both ends.
func (p *sourcePlugin) windows(now time.Time) []window {
	from, to := p.windowBounds(now)
	var out []window
	y, m := from.year, from.month
	for {
		out = append(out, window{y, m})
		if y == to.year && m == to.month {
			break
		}
		m++
		if m > 12 {
			m = 1
			y++
		}
		// Guard against accidental infinite loops if config is malformed.
		// 600 months = 50 years, comfortably covers BD3D (2010-) and any
		// full historical backfill someone might attempt.
		if len(out) > 600 {
			break
		}
	}
	return out
}

func (p *sourcePlugin) windowBounds(now time.Time) (from, to window) {
	to = window{p.toYear, p.toMonth}
	if to.year == 0 || to.month == 0 {
		to = window{now.Year(), int(now.Month())}
	}
	from = window{p.fromYear, p.fromMonth}
	if from.year == 0 || from.month == 0 {
		// from = to minus (months-1) months
		y, m := to.year, to.month-(p.months-1)
		for m <= 0 {
			m += 12
			y--
		}
		from = window{y, m}
	}
	return from, to
}

// ---------- helpers ----------

// indexKey strips trailing format tokens ("3D", "4K") before normalising, so
// "Avatar 3D" and "Avatar 4K" and "Avatar" all share one index entry.
func indexKey(title string) string {
	return match.Normalize(stripFormatToken(title))
}

// stripFormatToken removes a trailing " 3D" / " 4K" from a Blu-ray.com title.
// Idempotent.
func stripFormatToken(s string) string {
	t := strings.TrimSpace(s)
	switch {
	case strings.HasSuffix(strings.ToLower(t), " 3d"):
		return strings.TrimSpace(t[:len(t)-3])
	case strings.HasSuffix(strings.ToLower(t), " 4k"):
		return strings.TrimSpace(t[:len(t)-3])
	}
	return t
}

func filterByYear(rows []bluray.IndexEntry, year int) []bluray.IndexEntry {
	if year <= 0 {
		return rows
	}
	out := make([]bluray.IndexEntry, 0, len(rows))
	for _, r := range rows {
		if r.Year == 0 || r.Year == year {
			out = append(out, r)
		}
	}
	return out
}

func parseFormats(values []string) map[bluray.Format]bool {
	out := map[bluray.Format]bool{}
	if len(values) == 0 {
		out[bluray.FormatBD] = true
		out[bluray.FormatUHD] = true
		out[bluray.FormatBD3D] = true
		return out
	}
	for _, v := range values {
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "BD":
			out[bluray.FormatBD] = true
		case "UHD", "4K":
			out[bluray.FormatUHD] = true
		case "BD3D", "3D":
			out[bluray.FormatBD3D] = true
		case "DVD":
			out[bluray.FormatDVD] = true
		}
	}
	return out
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
