// Package gaps provides the series_gaps processor, which turns tracked shows
// into search queries for the episodes you are missing.
//
// Upstream entries are shows (from series_tracker, tvdb_favorites,
// trakt_list, ...). For each show the plugin resolves the TVDB series (by
// tvdb_id when the entry carries one, else by name search), fetches the
// TTL-cached episode list, keeps only episodes that have already aired
// (season-0 specials excluded unless include_specials=true; undated episodes
// ignored), and diffs them against the series tracker's download records.
// Each missing episode becomes one fresh entry whose title is a searchable
// query ("<Show> S02E05") — feed the output into discover to search for the
// releases, exactly like a title list.
//
// Season packs: when the missing fraction of a season's aired episodes
// exceeds pack_threshold, one season-pack entry ("<Show> S02", series_season
// set, no series_episode/series_episode_id) replaces that season's
// per-episode entries.
//
// Per-run cap: at most max_per_run entries are emitted per run, in
// deterministic (show, season, episode) order. A cursor persisted in the
// store resumes the next run where this one stopped, wrapping around at the
// end, so large backlogs drain across runs instead of hammering the indexers.
//
// Shows deactivated in the series tracker (series_inactive) are skipped
// unless include_inactive=true.
//
// Config keys:
//
//	api_key          - TheTVDB API key (required)
//	cache_ttl        - how long to cache TVDB lookups (default: "24h")
//	include_specials - consider season-0 episodes as gap candidates (default: false)
//	include_inactive - also scan shows deactivated in the tracker (default: false)
//	pack_threshold   - missing fraction (0..1) above which a season emits one
//	                   season-pack entry instead of per-episode entries
//	                   (default: 0.5; 1 disables packs, 0 packs any gap)
//	max_per_run      - cap on emitted entries per run; 0 disables the cap
//	                   (default: 30)
package gaps

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

const pluginName = "series_gaps"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "diff tracked shows against TheTVDB's episode list and emit one search-query entry per missing aired episode (or per season pack)",
		Role:        plugin.RoleProcessor,
		// Upstream shows carry series_name (tracker key) or at least a title.
		Requires: plugin.RequireAny(entry.FieldSeriesName, entry.FieldTitle),
		// Emitted entries are freshly built, so every field below is set on
		// all of them...
		Produces: []string{
			entry.FieldTitle,
			entry.FieldSource,
			entry.FieldMediaType,
			entry.FieldSeriesName,
			entry.FieldSeriesSeason,
			"tvdb_id",
		},
		// ...except episode number/id, which season-pack entries omit.
		MayProduce: []string{
			entry.FieldSeriesEpisode,
			entry.FieldSeriesEpisodeID,
		},
		// Upstream show entries are consumed as context; the emitted gap
		// entries have their own URLs and lifetimes (same shape as discover).
		ReplacesUpstream: true,
		Factory:          newPlugin,
		Validate:         validate,
		Schema: []plugin.FieldSchema{
			{Key: "api_key", Type: plugin.FieldTypeString, Required: true, Hint: "TheTVDB v4 API key"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "24h", Hint: "How long to cache TVDB lookups"},
			{Key: "include_specials", Type: plugin.FieldTypeBool, Default: false, Hint: "Consider season-0 specials as gap candidates"},
			{Key: "include_inactive", Type: plugin.FieldTypeBool, Default: false, Hint: "Also scan shows deactivated in the series tracker"},
			{Key: "pack_threshold", Type: plugin.FieldTypeString, Default: "0.5", Hint: "Missing fraction (0-1) above which a season emits one season-pack query instead of per-episode queries"},
			{Key: "max_per_run", Type: plugin.FieldTypeInt, Default: 30, Hint: "Max entries emitted per run (0 = unlimited); a persisted cursor resumes next run"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_series_gaps", Display: "Series Gaps Search Cache"},
			{Name: "cache_series_gaps_eps", Display: "Series Gaps Episodes Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "api_key", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", pluginName); err != nil {
		errs = append(errs, err)
	}
	if _, err := packThreshold(cfg); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"api_key", "cache_ttl", "include_specials", "include_inactive",
		"pack_threshold", "max_per_run")...)
	return errs
}

// packThreshold reads pack_threshold as a float in [0,1]. Starlark configs
// pass a float (pack_threshold=0.5) or int (0/1); the visual editor passes a
// string ("0.5") — all three are accepted.
func packThreshold(cfg map[string]any) (float64, error) {
	v, ok := cfg["pack_threshold"]
	if !ok || v == nil {
		return 0.5, nil
	}
	var f float64
	switch t := v.(type) {
	case float64:
		f = t
	case int:
		f = float64(t)
	case int64:
		f = float64(t)
	case string:
		parsed, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return 0, fmt.Errorf("%s: invalid pack_threshold %q: %w", pluginName, t, err)
		}
		f = parsed
	default:
		return 0, fmt.Errorf("%s: pack_threshold must be a number, got %T", pluginName, v)
	}
	if f < 0 || f > 1 {
		return 0, fmt.Errorf("%s: pack_threshold must be between 0 and 1, got %v", pluginName, f)
	}
	return f, nil
}

type gapsPlugin struct {
	resolver        *itvdb.Resolver
	tracker         *series.Tracker
	inactive        *series.InactiveSet
	db              *store.SQLiteStore
	includeSpecials bool
	includeInactive bool
	packThreshold   float64
	maxPerRun       int
	// now is the reference time for "already aired"; overridable in tests.
	now func() time.Time
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	apiKey, _ := cfg["api_key"].(string)
	if apiKey == "" {
		return nil, fmt.Errorf("%s: 'api_key' is required", pluginName)
	}

	ttl := 24 * time.Hour
	if v, _ := cfg["cache_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid cache_ttl %q: %w", pluginName, v, err)
		}
		ttl = d
	}

	threshold, err := packThreshold(cfg)
	if err != nil {
		return nil, err
	}

	return &gapsPlugin{
		resolver: itvdb.NewResolver(itvdb.New(apiKey), ttl,
			db.Bucket("cache_series_gaps"), db.Bucket("cache_series_gaps_eps")),
		tracker:         series.NewTracker(db.Bucket(series.TrackerBucketName)),
		inactive:        series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
		db:              db,
		includeSpecials: plugin.OptBool(cfg, "include_specials", false),
		includeInactive: plugin.OptBool(cfg, "include_inactive", false),
		packThreshold:   threshold,
		maxPerRun:       plugin.IntVal(cfg["max_per_run"], 30),
		now:             time.Now,
	}, nil
}

func (p *gapsPlugin) Name() string { return pluginName }

// candidate is one missing episode (or season pack) awaiting emission.
type candidate struct {
	show   string // display title, used to build the search query
	norm   string // normalized tracker key
	tvdbID string
	season int
	// episode is 0 for season-pack candidates.
	episode int
	pack    bool
}

// key returns the candidate's deterministic sort/cursor key. Zero-padding
// makes lexicographic order equal (show, season, episode) order; a season's
// pack candidate (episode 0000) sorts before any of its episodes, which is
// irrelevant in practice because a season contributes either the pack or the
// episodes, never both.
func (c *candidate) key() string {
	return fmt.Sprintf("%s|%04d|%04d", c.norm, c.season, c.episode)
}

// toEntry builds the emitted entry. The URL is synthetic but stable across
// runs so seen/dedup work naturally.
func (c *candidate) toEntry() *entry.Entry {
	var title, slug string
	if c.pack {
		slug = fmt.Sprintf("S%02d", c.season)
		title = fmt.Sprintf("%s %s", c.show, slug)
	} else {
		slug = fmt.Sprintf("S%02dE%02d", c.season, c.episode)
		title = fmt.Sprintf("%s %s", c.show, slug)
	}
	e := entry.New(title, "pipeliner://gap/"+url.PathEscape(c.norm)+"/"+slug)
	e.Set(entry.FieldSource, pluginName+":tvdb")
	e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
	e.Set(entry.FieldSeriesName, c.norm)
	e.Set(entry.FieldSeriesSeason, c.season)
	e.Set("tvdb_id", c.tvdbID)
	if !c.pack {
		e.Set(entry.FieldSeriesEpisode, c.episode)
		// The canonical tracker episode ID (series.EpisodeID form): "S02E05"
		// for regular seasons, "EP001" for season-0 specials.
		e.Set(entry.FieldSeriesEpisodeID,
			series.EpisodeID(&series.Episode{Season: c.season, Episode: c.episode}))
	}
	return e
}

// cursorRecord is the persisted resume position: the sort key of the last
// candidate emitted by the previous run.
type cursorRecord struct {
	Key string `json:"key"`
}

const cursorKey = "cursor"

// Process turns upstream show entries into gap-query entries. Upstream
// entries are consumed as context and not returned (ReplacesUpstream).
func (p *gapsPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	var candidates []*candidate
	seenShow := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cs := p.showCandidates(ctx, tc, e, seenShow)
		candidates = append(candidates, cs...)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].key() < candidates[j].key()
	})

	emit, err := p.applyCursor(tc, candidates)
	if err != nil {
		return nil, err
	}

	out := make([]*entry.Entry, 0, len(emit))
	for _, c := range emit {
		out = append(out, c.toEntry())
	}
	tc.Logger.Info(fmt.Sprintf("%s: emitted %d of %d candidate gaps", pluginName, len(out), len(candidates)))
	return out, nil
}

// showCandidates resolves one upstream show entry and returns its missing
// episodes / season packs. Failures skip the show (a broken lookup must not
// abort a big backlog scan); duplicates and inactive shows return nothing.
func (p *gapsPlugin) showCandidates(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry, seenShow map[string]bool) []*candidate {
	searchName := e.GetString(entry.FieldTitle)
	trackerName := e.GetString(entry.FieldSeriesName)
	if searchName == "" {
		searchName = trackerName
	}
	if trackerName == "" {
		trackerName = match.Normalize(searchName)
	}
	if searchName == "" {
		tc.Logger.Warn(pluginName + ": entry has neither series_name nor title; skipping")
		return nil
	}
	if seenShow[trackerName] {
		return nil
	}
	seenShow[trackerName] = true

	if !p.includeInactive && p.inactive.IsInactive(trackerName) {
		tc.Logger.Debug(pluginName+": skipping inactive show", "series", trackerName)
		return nil
	}

	s, err := p.resolver.ResolveSeries(ctx, e.GetString("tvdb_id"), searchName)
	if err != nil {
		tc.Logger.Warn(pluginName+": TVDB lookup failed; skipping show", "series", searchName, "err", err)
		return nil
	}
	if s == nil {
		tc.Logger.Warn(pluginName+": TVDB lookup found no match; skipping show", "series", searchName)
		return nil
	}

	eps, err := p.resolver.Episodes(ctx, s.ID, searchName)
	if err != nil {
		tc.Logger.Warn(pluginName+": episode list unavailable; skipping show", "series", searchName, "err", err)
		return nil
	}

	// Prefer TVDB's display name for the search query; it is the canonical
	// spelling indexers are most likely to match.
	show := s.Name
	if show == "" {
		show = searchName
	}

	return p.diffSeasons(eps, show, trackerName, s.ID)
}

// diffSeasons groups aired episodes by season, finds the ones missing from
// the tracker, and applies the season-pack heuristic: when the missing
// fraction of a season's aired episodes strictly exceeds pack_threshold, one
// pack candidate replaces that season's per-episode candidates.
func (p *gapsPlugin) diffSeasons(eps []itvdb.Episode, show, trackerName, tvdbID string) []*candidate {
	now := p.now()
	airedBySeason := map[int]int{}
	missingBySeason := map[int][]int{}
	for i := range eps {
		ep := &eps[i]
		if !itvdb.EpisodeAired(ep, now, p.includeSpecials) {
			continue
		}
		airedBySeason[ep.SeasonNumber]++
		epID := series.EpisodeID(&series.Episode{Season: ep.SeasonNumber, Episode: ep.EpisodeNumber})
		if !p.tracker.IsSeen(trackerName, epID) {
			missingBySeason[ep.SeasonNumber] = append(missingBySeason[ep.SeasonNumber], ep.EpisodeNumber)
		}
	}

	var out []*candidate
	for season, missing := range missingBySeason {
		if len(missing) == 0 {
			continue
		}
		fraction := float64(len(missing)) / float64(airedBySeason[season])
		if fraction > p.packThreshold {
			out = append(out, &candidate{
				show: show, norm: trackerName, tvdbID: tvdbID,
				season: season, pack: true,
			})
			continue
		}
		for _, ep := range missing {
			out = append(out, &candidate{
				show: show, norm: trackerName, tvdbID: tvdbID,
				season: season, episode: ep,
			})
		}
	}
	return out
}

// applyCursor caps the sorted candidate list at max_per_run, resuming from
// the persisted cursor (the key of the last candidate emitted by the previous
// run) and wrapping around at the end of the list. The cursor is stored in a
// per-task bucket; dry-run reads it but never advances it, so a dry-run
// previews exactly what the next real run would emit.
func (p *gapsPlugin) applyCursor(tc *plugin.TaskContext, candidates []*candidate) ([]*candidate, error) {
	if len(candidates) == 0 {
		return nil, nil
	}

	bucket := p.db.Bucket(pluginName + ":" + tc.Name)
	var cur cursorRecord
	if _, err := bucket.Get(cursorKey, &cur); err != nil {
		return nil, fmt.Errorf("%s: read cursor: %w", pluginName, err)
	}

	start := 0
	if cur.Key != "" {
		// First candidate strictly after the cursor; wraps to 0 when the
		// cursor sits at or past the end (or the candidates around it
		// disappeared between runs).
		start = sort.Search(len(candidates), func(i int) bool {
			return candidates[i].key() > cur.Key
		})
		if start == len(candidates) {
			start = 0
		}
	}

	n := len(candidates)
	if p.maxPerRun > 0 && p.maxPerRun < n {
		n = p.maxPerRun
	}

	emit := make([]*candidate, 0, n)
	for i := 0; i < n; i++ {
		emit = append(emit, candidates[(start+i)%len(candidates)])
	}

	if !tc.DryRun {
		rec := cursorRecord{Key: emit[len(emit)-1].key()}
		if err := bucket.Put(cursorKey, rec); err != nil {
			tc.Logger.Warn(pluginName+": persist cursor failed", "err", err)
		}
	}
	return emit, nil
}
