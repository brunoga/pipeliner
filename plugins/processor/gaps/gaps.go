// Package gaps provides the series_gaps processor, which turns tracked shows
// into search queries for the episodes you are missing.
//
// Upstream entries are shows (from series_tracker, tvdb_favorites,
// trakt_list, ...). For each show the plugin resolves the TVDB series (by
// tvdb_id when the entry carries one, else by name search), fetches the
// TTL-cached episode list, keeps only episodes that have already aired
// (season-0 specials excluded unless include_specials=true; undated episodes
// ignored), and diffs them against what you
// already have.
//
// What counts as "already have" depends on whether a media server is
// configured. Without one it is the series tracker: what pipeliner grabbed.
// With backend=plex (or jellyfin) it is the server's own library, which is
// disk truth -- it sees content acquired outside pipeliner, and an episode
// you delete becomes a gap again. The tracker is deliberately not consulted
// in that mode, because a record there says pipeliner once grabbed the
// episode, not that you still have it; consulting it would leave a deleted
// episode permanently unreachable.
//
// Not consulting the tracker loses one useful thing, so a per-task pending
// set replaces it: an episode asked for within retry_cooldown is skipped, so
// the hours between a grab and the server indexing it do not spend a slot of
// max_per_run or an indexer query on every run. It expires, which a tracker
// record does not, so a failed download or a deleted file comes back.
//
// seasons decides how much of a show's run is in scope. "all" (the default)
// considers every aired season. "from_first_owned" considers seasons from the
// earliest one holding at least one episode onwards, including later seasons
// holding none -- so a show you have from season 2 keeps filling forward into
// season 3 and beyond, while season 1 is left alone. A show the library does
// not have at all is skipped entirely in that mode.
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
//	seasons          - "all" (default) or "from_first_owned"
//	backend          - "plex"/"jellyfin": read what you have from the server
//	                   instead of the tracker
//	url, token       - server address and token; omit both for Plex account
//	                   mode (sign in on the Tools tab)
//	sections         - only read these server libraries, by name
//	exclude_sections - read every library except these
//	library_ttl      - how long the server index is reused (default: "15m")
//	retry_cooldown   - how long an episode already asked for stays out of
//	                   scope (default: "48h"; media-server mode only)
package gaps

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/mediaserver"
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
		Refusal:     plugin.RefusalNone,
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
			{Key: "seasons", Type: plugin.FieldTypeEnum, Enum: []string{"all", "from_first_owned"}, Default: "all", Hint: "Which seasons are in scope: all aired seasons, or from the earliest season you hold an episode of onwards (needs a backend)"},
			{Key: "backend", Type: plugin.FieldTypeString, Hint: "Read what you already have from a media server instead of the download tracker: plex or jellyfin"},
			{Key: "url", Type: plugin.FieldTypeString, Hint: "Media server base URL; omit for Plex account mode (sign in on the Tools tab)"},
			{Key: "token", Type: plugin.FieldTypeString, Hint: "Media server API token; omit for Plex account mode"},
			{Key: "sections", Type: plugin.FieldTypeList, Hint: "Only read these server libraries, by name (e.g. [\"TV Shows\"])"},
			{Key: "exclude_sections", Type: plugin.FieldTypeList, Hint: "Read every server library except these, by name"},
			{Key: "library_ttl", Type: plugin.FieldTypeDuration, Default: "15m", Hint: "How long the media-server index is reused before rescanning"},
			{Key: "retry_cooldown", Type: plugin.FieldTypeDuration, Default: "48h", Hint: "How long an episode already asked for stays out of scope while it downloads and the server indexes it"},
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
	if err := plugin.OptDuration(cfg, "library_ttl", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "retry_cooldown", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "seasons", pluginName, "all", "from_first_owned"); err != nil {
		errs = append(errs, err)
	}
	backend, _ := cfg["backend"].(string)
	if backend != "" && backend != "plex" && backend != "jellyfin" {
		errs = append(errs, fmt.Errorf("%s: backend %q is not plex or jellyfin", pluginName, backend))
	}
	// Catch the misconfiguration at check time rather than at the first run:
	// from_first_owned decides scope from what the library holds, so without
	// a backend there is nothing to decide from.
	if sv, _ := cfg["seasons"].(string); sv == "from_first_owned" && backend == "" {
		errs = append(errs, fmt.Errorf("%s: seasons=\"from_first_owned\" needs a media server; set 'backend'", pluginName))
	}
	if len(plugin.ToStringSlice(cfg["sections"])) > 0 && len(plugin.ToStringSlice(cfg["exclude_sections"])) > 0 {
		errs = append(errs, fmt.Errorf("%s: set 'sections' or 'exclude_sections', not both", pluginName))
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"api_key", "cache_ttl", "include_specials", "include_inactive",
		"pack_threshold", "max_per_run", "seasons", "backend", "url", "token",
		"sections", "exclude_sections", "library_ttl", "retry_cooldown")...)
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
	// seasons decides which seasons are in scope; see the seasons* consts.
	seasons string
	// client, sections and libraryTTL describe the media server consulted
	// for what is actually on disk. client is nil when no backend is set, in
	// which case the tracker alone decides what is missing.
	client     mediaserver.Client
	sections   mediaserver.Sections
	libraryTTL time.Duration
	// retryCooldown is how long an episode asked for stays out of scope
	// while it downloads and the server indexes it. Media-server mode only.
	retryCooldown time.Duration
	// owned is the cached media-server index, rebuilt when older than
	// libraryTTL. ownedErr records why a rebuild failed, so a run can refuse
	// to act on a half-known library instead of guessing.
	mu      sync.Mutex
	owned   *mediaserver.OwnedEpisodes
	ownedAt time.Time
	// now is the reference time for "already aired"; overridable in tests.
	now func() time.Time
}

// Season scopes. A show's episode list from TVDB spans its whole run, so
// something has to say how much of that run is wanted.
const (
	// seasonsAll considers every aired season. The historical behaviour, and
	// still the default, because changing it would silently redefine what
	// existing configs backfill.
	seasonsAll = "all"
	// seasonsFromFirstOwned considers seasons from the earliest one holding
	// at least one episode onwards, including later seasons holding none.
	// This is "finish what I started": a show you have from season 2 keeps
	// filling forward into season 3 and beyond, while season 1 — which you
	// evidently did not want — is left alone.
	seasonsFromFirstOwned = "from_first_owned"
)

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

	seasons := seasonsAll
	if v, _ := cfg["seasons"].(string); v != "" {
		seasons = v
	}
	switch seasons {
	case seasonsAll, seasonsFromFirstOwned:
	default:
		return nil, fmt.Errorf("%s: invalid seasons %q (want %q or %q)",
			pluginName, seasons, seasonsAll, seasonsFromFirstOwned)
	}

	libTTL := 15 * time.Minute
	if v, _ := cfg["library_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid library_ttl %q: %w", pluginName, v, err)
		}
		libTTL = d
	}

	cooldown := 48 * time.Hour
	if v, _ := cfg["retry_cooldown"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid retry_cooldown %q: %w", pluginName, v, err)
		}
		cooldown = d
	}

	var client mediaserver.Client
	if backend, _ := cfg["backend"].(string); backend != "" {
		url, _ := cfg["url"].(string)
		token, _ := cfg["token"].(string)
		var bucket store.Bucket
		if db != nil {
			bucket = db.Bucket(mediaserver.PlexSettingsBucket)
		}
		c, err := mediaserver.Connect(backend, url, token, bucket)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", pluginName, err)
		}
		client = c
	}
	// from_first_owned decides scope from what is on disk, so without a
	// backend it would have to read the tracker instead -- which is what
	// pipeliner grabbed, not what the library holds. Those differ for
	// exactly the shows this option is for, so refuse rather than answer
	// from the wrong set.
	if seasons == seasonsFromFirstOwned && client == nil {
		return nil, fmt.Errorf("%s: seasons=%q needs a media server; set 'backend'",
			pluginName, seasonsFromFirstOwned)
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
		seasons:         seasons,
		client:          client,
		sections: mediaserver.NewSections(
			plugin.ToStringSlice(cfg["sections"]), plugin.ToStringSlice(cfg["exclude_sections"])),
		libraryTTL:    libTTL,
		retryCooldown: cooldown,
		now:           time.Now,
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
	owned, err := p.ownedEpisodes(ctx, tc)
	if err != nil {
		// Acting on a library we could not read would propose re-downloading
		// things that are sitting on disk, and with from_first_owned it
		// would also lose every season floor. Emitting nothing is the safe
		// failure: the next run tries again.
		tc.Logger.Warn(pluginName+": media server unreachable, proposing nothing this run", "err", err)
		return nil, nil
	}

	pending, err := p.loadPending(tc)
	if err != nil {
		return nil, err
	}

	seenShow := map[string]bool{}
	for _, e := range entries {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		cs := p.showCandidates(ctx, tc, e, seenShow, owned, pending)
		candidates = append(candidates, cs...)
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].key() < candidates[j].key()
	})

	emit, err := p.applyCursor(tc, candidates)
	if err != nil {
		return nil, err
	}
	p.recordPending(tc, pending, emit)

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
func (p *gapsPlugin) showCandidates(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry, seenShow map[string]bool, owned *mediaserver.OwnedEpisodes, pending *pendingSet) []*candidate {
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

	return p.diffSeasons(eps, show, trackerName, s.ID, owned, pending)
}

// diffSeasons groups aired episodes by season, finds the ones missing from
// the tracker, and applies the season-pack heuristic: when the missing
// fraction of a season's aired episodes strictly exceeds pack_threshold, one
// pack candidate replaces that season's per-episode candidates.
func (p *gapsPlugin) diffSeasons(eps []itvdb.Episode, show, trackerName, tvdbID string, owned *mediaserver.OwnedEpisodes, pending *pendingSet) []*candidate {
	now := p.now()
	airedBySeason := map[int]int{}
	missingBySeason := map[int][]int{}

	// The season floor, for seasons=from_first_owned: the earliest season
	// holding at least one episode. Seasons below it are out of scope even
	// when episodes are missing, and seasons above it are in scope even when
	// they hold nothing -- which is the whole point. A show the library does
	// not have at all has no floor, so nothing of it is proposed.
	floor, haveFloor := 0, true
	if p.seasons == seasonsFromFirstOwned {
		floor, haveFloor = owned.FirstSeasonWithAny(trackerName, p.includeSpecials)
	}

	for i := range eps {
		ep := &eps[i]
		if !itvdb.EpisodeAired(ep, now, p.includeSpecials) {
			continue
		}
		if !haveFloor || ep.SeasonNumber < floor {
			continue
		}
		airedBySeason[ep.SeasonNumber]++
		epID := series.EpisodeID(&series.Episode{Season: ep.SeasonNumber, Episode: ep.EpisodeNumber})
		if p.have(trackerName, epID, ep.SeasonNumber, ep.EpisodeNumber, owned, pending) {
			continue
		}
		missingBySeason[ep.SeasonNumber] = append(missingBySeason[ep.SeasonNumber], ep.EpisodeNumber)
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

// ownedEpisodes returns the media-server index, rebuilding it when missing or
// older than library_ttl. Nil client means no backend is configured, in which
// case there is nothing to read and the tracker alone decides.
func (p *gapsPlugin) ownedEpisodes(ctx context.Context, tc *plugin.TaskContext) (*mediaserver.OwnedEpisodes, error) {
	if p.client == nil {
		return nil, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.owned != nil && time.Since(p.ownedAt) < p.libraryTTL {
		return p.owned, nil
	}
	// match.Normalize, not series.NormalizeName: the index is looked up by
	// the same trackerName this plugin derives for a show, and those two
	// normalisers do not agree. Keying with the wrong one silently indexes a
	// library nothing can be found in.
	owned, err := mediaserver.BuildOwnedEpisodes(ctx, p.client, p.sections, match.Normalize)
	if err != nil {
		// Keep a previous index rather than nothing: a stale answer about a
		// library that changes slowly beats refusing the whole run.
		if p.owned != nil {
			tc.Logger.Warn(pluginName+": media server unreachable, reusing the previous index", "err", err)
			return p.owned, nil
		}
		return nil, err
	}
	p.owned, p.ownedAt = owned, time.Now()
	tc.Logger.Info(pluginName+": indexed media server",
		"shows", len(owned.Shows()), "episodes", owned.Count())
	return owned, nil
}

// have reports whether an episode should be left alone this run.
//
// With a media server configured it is disk truth that decides, and the
// shared series tracker is deliberately NOT consulted: a record there says
// pipeliner once grabbed the episode, not that you still have it. Consulting
// it would make a deleted episode permanently unreachable -- present in the
// tracker, absent from disk, and never proposed again -- which is the exact
// case this plugin exists to catch.
//
// What the tracker was doing usefully is covered instead by a per-task
// pending set: an episode asked for within retry_cooldown is skipped, so the
// hours between a grab and the server indexing it do not spend a slot of
// max_per_run or an indexer query every run. Unlike a tracker record that
// expires, so a download that failed or a file later deleted comes back.
//
// Without a media server the tracker remains the only thing that knows
// anything, and the behaviour is unchanged from before this option existed.
func (p *gapsPlugin) have(trackerName, epID string, season, episode int, owned *mediaserver.OwnedEpisodes, pending *pendingSet) bool {
	if p.client == nil {
		return p.tracker.IsSeen(trackerName, epID)
	}
	if owned.Has(trackerName, season, episode) {
		return true
	}
	return pending.active(trackerName + "|" + epID)
}

// pendingSet is the per-task record of episodes recently asked for, keyed
// "<show>|<S01E01>". It is a cooldown, not a tracker: entries older than the
// window are ignored, and the bucket is pruned as it is read so it cannot
// grow without bound.
type pendingSet struct {
	at       map[string]time.Time
	cooldown time.Duration
	now      time.Time
}

func (ps *pendingSet) active(key string) bool {
	if ps == nil || ps.cooldown <= 0 {
		return false
	}
	t, ok := ps.at[key]
	return ok && ps.now.Sub(t) < ps.cooldown
}

const pendingKey = "pending"

type pendingRecord struct {
	At map[string]time.Time `json:"at"`
}

// loadPending reads the pending set for this task.
func (p *gapsPlugin) loadPending(tc *plugin.TaskContext) (*pendingSet, error) {
	ps := &pendingSet{at: map[string]time.Time{}, cooldown: p.retryCooldown, now: p.now()}
	if p.client == nil || p.retryCooldown <= 0 {
		return ps, nil
	}
	var rec pendingRecord
	if _, err := p.db.Bucket(pluginName+":"+tc.Name).Get(pendingKey, &rec); err != nil {
		return nil, fmt.Errorf("%s: read pending: %w", pluginName, err)
	}
	for k, t := range rec.At {
		// Drop expired entries on read: they can never make active() true
		// again, and keeping them would grow the record for every episode
		// ever proposed.
		if ps.now.Sub(t) < ps.cooldown {
			ps.at[k] = t
		}
	}
	return ps, nil
}

// recordPending marks the emitted candidates as asked for. Dry runs read the
// set but never write it, so a dry run does not change what the next real
// run would do.
func (p *gapsPlugin) recordPending(tc *plugin.TaskContext, ps *pendingSet, emit []*candidate) {
	if p.client == nil || p.retryCooldown <= 0 || tc.DryRun || len(emit) == 0 {
		return
	}
	now := p.now()
	for _, c := range emit {
		// A season pack stands in for that season's episodes, so it is keyed
		// by season alone; the per-episode keys it replaced are not written.
		if c.pack {
			ps.at[c.norm+"|"+fmt.Sprintf("S%02d", c.season)] = now
			continue
		}
		epID := series.EpisodeID(&series.Episode{Season: c.season, Episode: c.episode})
		ps.at[c.norm+"|"+epID] = now
	}
	if err := p.db.Bucket(pluginName+":"+tc.Name).Put(pendingKey, pendingRecord{At: ps.at}); err != nil {
		tc.Logger.Warn(pluginName+": persist pending failed", "err", err)
	}
}
