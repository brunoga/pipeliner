// Package bluray provides a metainfo processor that enriches movie entries
// with Blu-ray.com data: release date, studio, codec, aspect ratio, runtime,
// audio tracks, subtitles, ratings, playback region, theatrical 3D status,
// and the two 3D booleans (bluray_3d_release, bluray_is_3d_edition).
//
// Lookup order for each entry:
//
//  1. If the entry already has bluray_id, fetch the detail page directly
//     (and check the embedded worldwide 3D catalog / sibling index).
//  2. Otherwise look up (normalisedTitle, year) in the local index cache that
//     the bluray_releases source populates from calendar passes, augmented with
//     the embedded 14,163-entry worldwide 3D Blu-ray catalog.
//  3. On index miss, check the negative-search cache.
//  4. On negative miss too, fall back to /search/ (and theatrical master-movie
//     edition lookup when needed) and write through to both caches.
//
// The same three buckets are used by the bluray_releases source so calendar
// passes and search lookups share one accumulating local mirror of the catalog.
//
// Config keys:
//
//	country            - "us" (default), "uk", "ca", "au", "de", "fr", "kr", "jp", "it", "es", "all", etc.
//	cache_ttl          - release-detail TTL (default 168h)
//	cache_index_ttl    - index TTL (default 720h)
//	cache_negative_ttl - negative-cache TTL (default 168h)
//	request_interval   - rate limit gap (default 1s)
//	user_agent         - custom User-Agent (default Chrome 131 profile)
package bluray

import (
	"context"
	"regexp"
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
		Description: "enrich movie entries with Blu-ray.com metadata (release date, codec, audio/subtitles, ratings, 3D-edition flag)",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalNone,
		MayProduce: []string{
			entry.FieldEnriched,
			entry.FieldBlurayID,
			entry.FieldBlurayParentID,
			entry.FieldBlurayURL,
			entry.FieldBlurayFormat,
			entry.FieldBlurayStudio,
			entry.FieldBlurayCountry,
			entry.FieldBlurayYear,
			entry.FieldBlurayReleaseDate,
			entry.FieldBlurayRuntimeMin,
			entry.FieldBlurayRated,
			entry.FieldBlurayCodec,
			entry.FieldBlurayResolution,
			entry.FieldBlurayAspectRatio,
			entry.FieldBlurayHDR,
			entry.FieldBlurayEdition,
			entry.FieldBlurayAudio,
			entry.FieldBluraySubtitles,
			entry.FieldBlurayPlayback,
			entry.FieldBlurayCoverURL,
			entry.FieldBlurayRating,
			entry.FieldBlurayReviewRating,
			entry.FieldBlurayUserRating,
			entry.FieldBlurayNative3D,
			entry.FieldBlurayConverted3D,
			entry.FieldBluray3DRelease,
			entry.FieldBlurayIs3DEdition,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "country", Type: plugin.FieldTypeEnum, Default: defaultCountry,
				Enum: bluray.AllCountries, Hint: "Blu-ray.com locale (or 'all' for worldwide)"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long to cache release detail (default 7 days)"},
			{Key: "cache_index_ttl", Type: plugin.FieldTypeDuration, Default: "720h",
				Hint: "How long to cache the title index (default 30 days)"},
			{Key: "cache_negative_ttl", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long to cache search misses (default 7 days)"},
			{Key: "request_interval", Type: plugin.FieldTypeDuration, Default: "1s",
				Hint: "Minimum gap between requests"},
			{Key: "user_agent", Type: plugin.FieldTypeString, Hint: "Custom User-Agent (default Chrome 131)"},
			{Key: "details", Type: plugin.FieldTypeBool, Default: false,
				Hint: "Fetch the release page for codec, audio and ratings even when the embedded 3D catalog answers"},
			{Key: "offline", Type: plugin.FieldTypeBool, Default: false,
				Hint: "Never contact Blu-ray.com; answer from the embedded 3D catalog only"},
		},
		Caches: []plugin.CacheInfo{
			{Name: bucketIndex, Display: "Blu-ray.com Title Index"},
			{Name: bucketDetail, Display: "Blu-ray.com Release Detail Cache"},
			{Name: bucketNeg, Display: "Blu-ray.com Negative Search Cache"},
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
	if err := plugin.OptEnum(cfg, "country", pluginName, bluray.AllCountries...); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"country", "cache_ttl", "cache_index_ttl", "cache_negative_ttl",
		"request_interval", "user_agent", "details", "offline")...)
	return errs
}

type processorPlugin struct {
	client      *bluray.Client
	indexCache  *cache.Cache[[]bluray.IndexEntry]
	negCache    *cache.Cache[time.Time]
	detailCache *cache.Cache[*bluray.Release]
	catalog3D   *bluray.Catalog3D
	country     string
	details     bool // fetch release pages even when the catalog answers
	offline     bool // never contact Blu-ray.com
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

	cat3D, _ := bluray.Embedded3DCatalog()

	p := &processorPlugin{
		client:      bluray.New(opts...),
		indexCache:  cache.NewPersistent[[]bluray.IndexEntry](indexTTL, db.Bucket(bucketIndex)),
		negCache:    cache.NewPersistent[time.Time](negTTL, db.Bucket(bucketNeg)),
		detailCache: cache.NewPersistent[*bluray.Release](detailTTL, db.Bucket(bucketDetail)),
		catalog3D:   cat3D,
		country:     country,
	}
	p.details, _ = cfg["details"].(bool)
	p.offline, _ = cfg["offline"].(bool)
	p.indexCache.Preload()
	return p, nil
}

func (p *processorPlugin) Name() string { return pluginName }

// Process enriches each entry in place. Entries with no resolvable title are
// passed through unchanged; lookup failures are warnings, not errors, so a
// dead Blu-ray.com query doesn't fail the whole pipeline.
func (p *processorPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		p.annotate(ctx, tc, e)
	}
	return entries, nil
}

// annotate resolves an entry to a Release and writes bluray_* fields.
func (p *processorPlugin) annotate(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) {
	if (!p.details || p.offline) && p.answerFromCatalog(tc, e) {
		return
	}
	if p.offline {
		tc.Logger.Debug("metainfo_bluray: not in the catalog's complete years; offline, left unset", "title", e.Title)
		return
	}
	id, slug, siblings, movieDetails, ok := p.resolve(ctx, tc, e)
	if !ok {
		tc.Logger.Debug("metainfo_bluray: entry unresolved", "title", e.Title)
		return
	}
	rel, ok := p.fetchDetail(ctx, tc, id, slug)
	if !ok {
		// Even if detail fetch failed, if we matched a 3D release from the embedded catalog,
		// populate from the embedded summary so offline/cached 3D detection still works.
		if p.catalog3D != nil {
			if rs, found := p.catalog3D.LookupID(id); found {
				rel = summaryToRelease(rs)
				ok = true
			}
		}
		if !ok {
			return
		}
	}

	// If we still don't see a 3D sibling in siblings, check if the release's ParentMovieID
	// has 3D editions via /products/menu_ajax.php or if the embedded 3D catalog has one.
	if !bluray.Is3DRelease(siblings) && rel.Format != bluray.FormatBD3D {
		title := searchTitle(e)
		year := entry.ReleaseYear(e)
		if year == 0 {
			year = rel.Year
		}
		if p.catalog3D != nil && title != "" {
			for _, ch := range p.catalog3D.LookupTitle(title, year, bluray.CountryAll) {
				siblings = append(siblings, ch.ToIndexEntry())
			}
		}
		if !bluray.Is3DRelease(siblings) && rel.ParentMovieID != "" && !p.catalog3D.IsAuthoritativeForYear(year) {
			if mr, err := p.client.GetMovieReleases(ctx, rel.ParentMovieID); err == nil && mr != nil {
				key := indexKey(title)
				for _, r3d := range mr.BluRay3D {
					ie := r3d.ToIndexEntry()
					siblings = append(siblings, ie)
					if key != "" {
						p.mergeIndex(key, ie)
					}
				}
			}
		}
	}

	p.populate(tc, e, rel, siblings, movieDetails)
}

// answerFromCatalog answers an entry from the embedded 3D catalog alone, with
// no request to Blu-ray.com, when the catalog is complete for the entry's
// year: a listed film gets the catalog release's fields and
// bluray_3d_release=true, an unlisted one bluray_3d_release=false. It returns
// false, leaving the entry untouched, when the catalog cannot answer.
func (p *processorPlugin) answerFromCatalog(tc *plugin.TaskContext, e *entry.Entry) bool {
	title, year := searchTitle(e), entry.ReleaseYear(e)
	// An entry that names its release is answered for that release; one the
	// catalog does not hold (a 2D edition) needs the release page.
	if rawID, _ := e.Fields[entry.FieldBlurayID].(string); rawID != "" {
		rs, ok := p.catalog3D.LookupID(rawID)
		if !ok {
			return false
		}
		siblings := []bluray.IndexEntry{rs.ToIndexEntry()}
		for _, h := range p.catalog3D.LookupTitle(title, year, bluray.CountryAll) {
			siblings = appendUniqueIndex(siblings, h.ToIndexEntry())
		}
		p.populate(tc, e, summaryToRelease(rs), siblings, nil)
		return true
	}
	if title == "" || !p.catalog3D.IsAuthoritativeForYear(year) {
		return false
	}
	hits := p.catalog3D.LookupTitle(title, year, bluray.CountryAll)
	if len(hits) == 0 {
		// "Star Wars VII The Force Awakens" is listed as "Star Wars:
		// Episode VII - The Force Awakens" and "Star Wars: The Force
		// Awakens"; without TMDb's title upstream, try it unnumbered.
		if ep := reNumbered.FindStringSubmatch(title); ep != nil {
			hits = p.catalog3D.LookupTitle(ep[1]+" "+ep[2], year, bluray.CountryAll)
		}
	}
	if len(hits) == 0 {
		e.Set(entry.FieldBluray3DRelease, false)
		tc.Logger.Debug("metainfo_bluray: no 3D Blu-ray in the catalog", "title", title, "year", year)
		return true
	}
	siblings := make([]bluray.IndexEntry, 0, len(hits))
	for _, h := range hits {
		siblings = append(siblings, h.ToIndexEntry())
	}
	best := pickBest(siblings, year)
	rs, ok := p.catalog3D.LookupID(best.ID)
	if !ok {
		rs = hits[0]
	}
	tc.Logger.Debug("metainfo_bluray: 3D Blu-ray in the catalog", "title", title, "year", year, "releases", len(hits), "picked_id", rs.ID)
	p.populate(tc, e, summaryToRelease(rs), siblings, nil)
	return true
}

func summaryToRelease(rs bluray.ReleaseSummary) *bluray.Release {
	relDate := rs.ReleaseDate
	if iso, ok := bluray.NormalizeDateISO(relDate); ok {
		relDate = iso
	}
	ed := rs.Edition
	if ed == "" {
		ed = rs.FormatSummary
	}
	return &bluray.Release{
		ID:          rs.ID,
		Title:       rs.Title,
		Subtitle:    rs.FormatSummary,
		Edition:     ed,
		URL:         rs.URL,
		Format:      rs.Format(),
		Country:     string(rs.Country),
		CountryCode: rs.Country,
		Studio:      rs.Studio,
		Year:        rs.Year,
		ReleaseDate: relDate,
	}
}

// resolve returns the release ID/slug to fetch, the catalog siblings used
// for the bluray_3d_release boolean, and optional theatrical MovieDetails.
func (p *processorPlugin) resolve(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) (id, slug string, siblings []bluray.IndexEntry, movie *bluray.MovieDetails, ok bool) {
	title := searchTitle(e)
	year := entry.ReleaseYear(e)

	// Fast path: the entry already carries a Blu-ray.com ID.
	if rawID, _ := e.Fields[entry.FieldBlurayID].(string); rawID != "" {
		key := indexKey(title)
		siblings, _ = p.indexCache.Get(key)
		if p.catalog3D != nil && title != "" {
			for _, ch := range p.catalog3D.LookupTitle(title, year, bluray.CountryAll) {
				siblings = appendUniqueIndex(siblings, ch.ToIndexEntry())
			}
		}
		for _, s := range siblings {
			if s.ID == rawID && s.Slug != "" {
				tc.Logger.Debug("metainfo_bluray: resolve by-id (canonical slug from index)",
					"title", e.Title, "id", rawID, "slug", s.Slug, "siblings", len(siblings))
				return rawID, s.Slug, siblings, nil, true
			}
		}
		if p.catalog3D != nil {
			if rs, found := p.catalog3D.LookupID(rawID); found {
				s, _ := bluray.SlugAndIDFromURL(rs.URL)
				return rawID, s, siblings, nil, true
			}
		}
		tc.Logger.Debug("metainfo_bluray: resolve by-id (slug via redirect)",
			"title", e.Title, "id", rawID, "siblings", len(siblings))
		return rawID, "", siblings, nil, true
	}

	if title == "" {
		tc.Logger.Debug("metainfo_bluray: no resolvable title", "entry", e.Title)
		return "", "", nil, nil, false
	}
	key := indexKey(title)

	if hits, found := p.indexCache.Get(key); found && len(hits) > 0 {
		if p.catalog3D != nil {
			for _, ch := range p.catalog3D.LookupTitle(title, year, bluray.CountryAll) {
				hits = appendUniqueIndex(hits, ch.ToIndexEntry())
			}
		}
		best := pickBest(hits, year)
		tc.Logger.Debug("metainfo_bluray: index cache hit",
			"title", title, "year", year, "key", key,
			"hits", len(hits), "picked_id", best.ID, "picked_format", string(best.Format))
		return best.ID, best.Slug, hits, nil, true
	}
	if _, found := p.negCache.Get(key); found {
		tc.Logger.Debug("metainfo_bluray: negative cache hit",
			"title", title, "year", year, "key", key)
		return "", "", nil, nil, false
	}

	t0 := time.Now()
	results, err := p.client.SearchTitle(ctx, title, year)
	if err != nil {
		tc.Logger.Warn("metainfo_bluray: search failed",
			"title", title, "year", year, "err", err,
			"duration", time.Since(t0).Round(time.Millisecond))
		return "", "", nil, nil, false
	}

	// Augment with embedded worldwide 3D catalog hits if any match the title and year,
	// even if country-scoped SearchTitle returned 0 results.
	if p.catalog3D != nil {
		for _, ch := range p.catalog3D.LookupTitle(title, year, bluray.CountryAll) {
			results = appendUniqueIndex(results, ch.ToIndexEntry())
		}
	}

	// If quicksearch + embedded 3D catalog still returned no results and the movie year
	// is not covered authoritatively by the embedded 3D catalog (>= 2026 or unknown year),
	// check theatrical movie search + master edition list (CheckMovieHas3D).
	var movDetails *bluray.MovieDetails
	if len(results) == 0 && !p.catalog3D.IsAuthoritativeForYear(year) {
		if check, err2 := p.client.CheckMovieHas3D(ctx, title, bluray.Country(p.country)); err2 == nil && check != nil {
			movDetails = check.Movie
			for _, r3d := range check.Releases3D {
				if year == 0 || r3d.Year == 0 || r3d.Year == year {
					results = appendUniqueIndex(results, r3d.ToIndexEntry())
				}
			}
		}
	}

	if len(results) == 0 {
		tc.Logger.Debug("metainfo_bluray: search returned no results (caching negative)",
			"title", title, "year", year, "key", key,
			"duration", time.Since(t0).Round(time.Millisecond))
		p.negCache.Set(key, time.Now())
		return "", "", nil, nil, false
	}
	for _, r := range results {
		p.mergeIndex(key, r)
	}
	hits, _ := p.indexCache.Get(key)
	best := pickBest(results, year)
	tc.Logger.Debug("metainfo_bluray: search succeeded",
		"title", title, "year", year, "key", key,
		"results", len(results), "picked_id", best.ID, "picked_format", string(best.Format),
		"duration", time.Since(t0).Round(time.Millisecond))
	return best.ID, best.Slug, hits, movDetails, true
}

func appendUniqueIndex(slice []bluray.IndexEntry, item bluray.IndexEntry) []bluray.IndexEntry {
	for _, x := range slice {
		if x.ID == item.ID {
			return slice
		}
	}
	return append(slice, item)
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
		tc.Logger.Debug("metainfo_bluray: detail cache hit", "id", id)
		return r, true
	}
	t0 := time.Now()
	r, err := p.client.GetRelease(ctx, id, slug)
	if err != nil {
		tc.Logger.Warn("metainfo_bluray: get release failed",
			"id", id, "slug", slug, "err", err,
			"duration", time.Since(t0).Round(time.Millisecond))
		return nil, false
	}
	tc.Logger.Debug("metainfo_bluray: detail fetched",
		"id", id, "slug", slug,
		"duration", time.Since(t0).Round(time.Millisecond))
	p.detailCache.Set(id, r)
	return r, true
}

// populate writes the bluray_* fields from a Release plus the sibling set.
func (p *processorPlugin) populate(tc *plugin.TaskContext, e *entry.Entry, r *bluray.Release, siblings []bluray.IndexEntry, movie *bluray.MovieDetails) {
	e.Set(entry.FieldBlurayID, r.ID)
	if r.ParentMovieID != "" {
		e.Set(entry.FieldBlurayParentID, r.ParentMovieID)
	}
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
	if r.Rated != "" {
		e.Set(entry.FieldBlurayRated, r.Rated)
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
	if r.Video.HDR != "" {
		e.Set(entry.FieldBlurayHDR, r.Video.HDR)
	}
	if r.Edition != "" {
		e.Set(entry.FieldBlurayEdition, r.Edition)
	}
	if len(r.Audio) > 0 {
		tracks := make([]string, 0, len(r.Audio))
		for _, a := range r.Audio {
			if a.Raw != "" {
				tracks = append(tracks, a.Raw)
			}
		}
		if len(tracks) > 0 {
			e.Set(entry.FieldBlurayAudio, tracks)
		}
	}
	if len(r.Subtitles) > 0 {
		e.Set(entry.FieldBluraySubtitles, r.Subtitles)
	}
	if r.Playback != "" {
		e.Set(entry.FieldBlurayPlayback, r.Playback)
	}
	if r.CoverURL != "" {
		e.Set(entry.FieldBlurayCoverURL, r.CoverURL)
	}
	if r.Ratings.MovieScore > 0 {
		e.Set(entry.FieldBlurayRating, r.Ratings.MovieScore)
	}
	if r.Ratings.ReviewOverall > 0 {
		e.Set(entry.FieldBlurayReviewRating, r.Ratings.ReviewOverall)
	}
	if r.Ratings.UserOverall > 0 {
		e.Set(entry.FieldBlurayUserRating, r.Ratings.UserOverall)
	}
	if movie != nil {
		if movie.IsNative3D {
			e.Set(entry.FieldBlurayNative3D, true)
		}
		if movie.IsConverted3D {
			e.Set(entry.FieldBlurayConverted3D, true)
		}
	}

	is3DEdition := r.Format == bluray.FormatBD3D ||
		r.Video.Is3D ||
		r.Discs.Is3D ||
		strings.Contains(strings.ToUpper(r.Codec), "MVC")
	if is3DEdition {
		e.Set(entry.FieldBlurayIs3DEdition, true)
	}
	siblings3D := 0
	for _, s := range siblings {
		if s.Format == bluray.FormatBD3D {
			siblings3D++
		}
	}
	is3DRelease := is3DEdition || siblings3D > 0
	if is3DRelease {
		e.Set(entry.FieldBluray3DRelease, true)
	}
	tc.Logger.Debug("metainfo_bluray: 3d classification",
		"title", e.Title, "id", r.ID, "format", string(r.Format), "codec", r.Codec,
		"siblings", len(siblings), "siblings_3d", siblings3D,
		"is_3d_edition", is3DEdition, "is_3d_release", is3DRelease)

	e.Set(entry.FieldEnriched, true)
}

// ---------- helpers ----------

// reNumbered splits a title around an episode number or a bare Roman
// numeral after its first words: "Star Wars VII The Force Awakens".
var reNumbered = regexp.MustCompile(`(?i)^(.+?)\s+(?:episode\s+(?:[ivx]+|\d+)|[ivx]+)\s+(.+)$`)

func searchTitle(e *entry.Entry) string {
	if v, _ := e.Fields[entry.FieldMovieTitle].(string); v != "" {
		return v
	}
	if m, ok := imovies.Parse(e.Title); ok && m != nil && m.Title != "" {
		return m.Title
	}
	return strings.TrimSpace(e.Title)
}

func indexKey(title string) string {
	return match.Normalize(stripFormatToken(title))
}

func stripFormatToken(s string) string {
	return bluray.StripFormatTokens(s)
}

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
