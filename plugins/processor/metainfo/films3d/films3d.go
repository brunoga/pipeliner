// Package films3d provides the metainfo_3d processor, which marks whether a
// movie was released in 3D by its studio. It reads Wikipedia's 3D film lists
// (internal/films3d) and sets movie_3d_official: true when the film is listed,
// false when it is not. A 3D copy of an unlisted film is a fan conversion of
// the 2D film, however it is labelled, which is what a 3D pipeline gates on.
//
// The list is fetched once per refresh period and kept in the store; when a
// refresh fails the last list is used, and when there has never been one the
// fields are left unset, so a gate on movie_3d_official refuses rather than
// waving everything through.
package films3d

import (
	"context"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/entry"
	ifilms3d "github.com/brunoga/pipeliner/internal/films3d"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const (
	pluginName = "metainfo_3d"

	// CacheBucket holds the parsed film list.
	CacheBucket = "cache_metainfo_3d"
	cacheKey    = "films"

	defaultRefresh = 168 * time.Hour
	fetchTimeout   = time.Minute
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "mark whether a movie was released in 3D by its studio (Wikipedia's 3D film lists); a 3D copy of one that was not is a fan conversion",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalNone,
		MayProduce: []string{
			entry.FieldMovie3DOfficial,
			entry.FieldMovie3DNative,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "refresh", Type: plugin.FieldTypeDuration, Default: "168h",
				Hint: "How long the downloaded film list is used before it is fetched again"},
		},
		Caches: []plugin.CacheInfo{
			{Name: CacheBucket, Display: "3D Film List"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptDuration(cfg, "refresh", pluginName); err != nil {
		errs = append(errs, err)
	}
	return append(errs, plugin.OptUnknownKeys(cfg, pluginName, "refresh")...)
}

type processorPlugin struct {
	films   *cache.Cache[[]ifilms3d.Film]
	baseURL string
	pages   []string
	client  *http.Client

	mu    sync.Mutex
	index *ifilms3d.Index
	until time.Time // when index must be checked against the cache again
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	refresh := defaultRefresh
	if s, _ := cfg["refresh"].(string); s != "" {
		d, err := time.ParseDuration(s)
		if err != nil {
			return nil, err
		}
		refresh = d
	}
	return &processorPlugin{
		films:   cache.NewPersistent[[]ifilms3d.Film](refresh, db.Bucket(CacheBucket)),
		baseURL: ifilms3d.DefaultBaseURL,
		pages:   ifilms3d.DefaultPages,
		client:  &http.Client{Timeout: fetchTimeout},
	}, nil
}

func (p *processorPlugin) Name() string { return pluginName }

func (p *processorPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	if len(entries) == 0 {
		return entries, nil
	}
	ix := p.loadIndex(ctx, tc)
	if ix == nil {
		return entries, nil
	}
	for _, e := range entries {
		f, ok := ix.Lookup(titles(e), year(e))
		e.Set(entry.FieldMovie3DOfficial, ok)
		if ok {
			e.Set(entry.FieldMovie3DNative, f.Native)
			tc.Logger.Debug("metainfo_3d: studio 3D release", "entry", e.Title, "listed_as", f.Title, "years", f.Years)
		}
	}
	return entries, nil
}

// loadIndex returns the film index, fetching the lists when the stored copy
// has expired. A failed fetch falls back to the expired copy; nil means no
// list has ever been read.
func (p *processorPlugin) loadIndex(ctx context.Context, tc *plugin.TaskContext) *ifilms3d.Index {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.index != nil && time.Now().Before(p.until) {
		return p.index
	}
	if films, ok := p.films.Get(cacheKey); ok {
		p.use(films, time.Hour)
		return p.index
	}
	fctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	films, err := ifilms3d.Fetch(fctx, p.client, p.baseURL, p.pages)
	if err == nil {
		// Films are only ever added to these lists. Merging into the stored
		// copy, rather than replacing it, means a vandalised or restructured
		// page can never make a known studio 3D film read as a fan conversion.
		if prev, ok := p.films.Peek(cacheKey); ok {
			films = ifilms3d.Merge(prev, films)
		}
		p.films.Set(cacheKey, films)
		tc.Logger.Info("metainfo_3d: read the 3D film lists", "films", len(films))
		p.use(films, time.Hour)
		return p.index
	}
	if stale, ok := p.films.Peek(cacheKey); ok {
		tc.Logger.Warn("metainfo_3d: could not refresh the 3D film lists; using the last copy", "err", err)
		// Retry within the hour rather than on every run of the day.
		p.use(stale, time.Hour)
		return p.index
	}
	tc.Logger.Warn("metainfo_3d: could not read the 3D film lists; movie_3d_official left unset", "err", err)
	return nil
}

func (p *processorPlugin) use(films []ifilms3d.Film, recheck time.Duration) {
	p.index = ifilms3d.NewIndex(films)
	p.until = time.Now().Add(recheck)
}

// titles lists every title the entry's film may be known by: TMDb's title,
// original title and alternatives, the movies tracker's match, and the title
// parsed from the release name. The release name alone misses the films whose
// release names number them ("Star Wars Episode VII The Force Awakens" for the
// listed "Star Wars: The Force Awakens"); TMDb's title is what catches those.
func titles(e *entry.Entry) []string {
	var out []string
	add := func(s string) {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	add(e.GetString(entry.FieldMovieTitle))
	add(e.GetString(entry.FieldVideoOriginalTitle))
	add(e.GetString(entry.FieldMoviesTrackerTitle))
	if m, ok := imovies.Parse(e.Title); ok && m != nil {
		add(m.Title)
		// "Star Wars Episode VII The Force Awakens" is listed as "Star Wars:
		// The Force Awakens"; without TMDb's title upstream, the name is all
		// there is to match on.
		if ep := reEpisode.FindStringSubmatch(m.Title); ep != nil {
			add(ep[1] + " " + ep[2])
		}
	}
	if aliases, ok := e.Fields[entry.FieldVideoAliases].([]string); ok {
		for _, a := range aliases {
			add(a)
		}
	}
	return out
}

var reEpisode = regexp.MustCompile(`(?i)^(.+?)\s+episode\s+(?:[ivx]+|\d+)\s+(.+)$`)

func year(e *entry.Entry) int {
	if y := e.GetInt(entry.FieldVideoYear); y > 0 {
		return y
	}
	if m, ok := imovies.Parse(e.Title); ok && m != nil {
		return m.Year
	}
	return 0
}
