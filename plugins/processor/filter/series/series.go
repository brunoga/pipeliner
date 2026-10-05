// Package series provides a TV series processor that accepts episodes from a
// configured show list and tracks downloads across runs.
//
// Episode metadata is read from entry fields populated upstream by
// metainfo_file (or any equivalent metainfo source). The plugin does not
// parse the entry title itself; the upstream requirement is declared via
// Descriptor.Requires so the DAG validator catches misconfigured pipelines
// at load time.
//
// The plugin matches the parsed series name against the configured show list,
// enforces optional quality and ordering constraints, and persists downloads
// via CommitPlugin so only entries that survive all downstream sinks are
// recorded. Multiple quality variants of the same episode are accepted so the
// dedup processor can pick the best copy.
//
// The show list may be provided statically via 'static', dynamically via 'list'
// (source plugins whose entry titles are used as show names), or both.
// Dynamic lists are cached for the configured ttl (default: 1h).
package series

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/downloads"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/settle"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/untrack"
	"github.com/brunoga/pipeliner/quality"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "series",
		Description: "accept episodes for configured shows; track downloads across runs",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalPerItem,
		// Episode metadata must be populated upstream — by metainfo_file in
		// the common case, or by any other plugin that sets these fields.
		// series_season and series_episode are part of the same parsed-episode
		// bundle as series_episode_id (metainfo_file always sets them
		// together); they support follow-mode season-floor logic and
		// double-episode part marking on commit. Declaring them keeps the
		// contract symmetric with the premiere plugin and documents the
		// expected upstream shape.
		// FieldQuality (the typed quality.Quality struct read via e.Quality())
		// is required so spec matching and upgrade detection work; without it
		// quality features silently degrade to no-op.
		Requires: plugin.RequireAll(
			entry.FieldTitle,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldQuality,
		),
		// Every entry exiting this filter is a series episode by
		// construction (the filter only accepts entries that match a known
		// show). Setting media_type here makes the classification Certain
		// for downstream nodes like dedup, instead of relying on
		// metainfo_file's conditional MayProduce.
		Produces: []string{
			entry.FieldMediaType,
		},
		Factory:     newPlugin,
		Validate:    validate,
		AcceptsList: true,
		Schema: []plugin.FieldSchema{
			{Key: "static", Type: plugin.FieldTypeList, Hint: "Optional static list of show names to accept; omit to accept every classified episode"},
			{Key: "list", Type: plugin.FieldTypeDict, Hint: "Optional dynamic show list from a source plugin (e.g. tvdb_favorites, trakt_list); omit to accept every classified episode"},
			{Key: "tracking", Type: plugin.FieldTypeEnum, Enum: []string{"strict", "backfill", "follow"}, Default: "strict", Hint: "Episode ordering mode"},
			{Key: "ttl", Type: plugin.FieldTypeDuration, Default: "1h", Hint: "Cache TTL for dynamic lists"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject episodes not classified as series upstream; when a list is configured, also reject episodes whose show isn't in the list"},
			{Key: "upgrade_window", Type: plugin.FieldTypeDuration, Hint: "Accept quality upgrades only within this window after the first download (e.g. 7d as 168h; default: unlimited)"},
			{Key: "retry_cooldown", Type: plugin.FieldTypeDuration, Default: "6h", Hint: "Hold off this long before grabbing another release of an episode whose last grab was marked failed by a janitor pipeline (0 = retry on the next run)"},
		},
		Caches: []plugin.CacheInfo{
			{Name: "cache_series_list", Display: "Series Title List Cache"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	// static and list are both optional. With neither set, the filter accepts
	// every classified episode that passes the quality spec and tracker checks
	// — useful for "download every 720p+ episode I find" pipelines.
	if err := plugin.OptDuration(cfg, "ttl", "series"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptEnum(cfg, "tracking", "series", "strict", "backfill", "follow"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "upgrade_window", "series"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "settle", "series"); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "retry_cooldown", "series"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "series", "static", "list", "ttl", "tracking", "reject_unmatched", "upgrade_window", "settle", "retry_cooldown")...)
	return errs
}

// tracking controls how episode ordering is enforced.
type tracking string

const (
	trackingStrict   tracking = "strict"   // reject if episode number skips > 1 ahead of latest
	trackingBackfill tracking = "backfill" // accept any episode not yet downloaded
	trackingFollow   tracking = "follow"   // accept all on first encounter; thereafter reject episodes from seasons older than the highest tracked episode
)

// seriesTrackerName is the entry field used to carry the normalized matched
// show name from filter() to persist(). The constant lives in the entry
// package because the torrent sinks' grab records also read it (failed-grab
// recovery needs the tracker key to un-track an episode).
const seriesTrackerName = entry.FieldSeriesTrackerName

type seriesPlugin struct {
	staticShows     []match.TitleEntry // show names from config (year=0 for plain strings)
	listSources     []plugin.SourcePlugin
	listCache       *cache.Cache[[]match.TitleEntry]
	tracking        tracking
	tracker         *series.Tracker
	inactive        *series.InactiveSet
	downloadLog     *downloads.Log
	rejectUnmatched bool
	upgradeWindow   time.Duration // 0 = upgrades accepted forever
	settle          time.Duration // 0 = grab on sight
	settleTracker   *settle.Tracker
	retryCooldown   time.Duration // 0 = retry on the next run
	untrackStore    *untrack.Store
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	raw := plugin.ToStringSlice(cfg["static"])
	staticShows := make([]match.TitleEntry, len(raw))
	for i, s := range raw {
		staticShows[i] = match.NewTitleEntry(s, 0) // static show names have no year
	}

	listRaw, _ := cfg["list"].([]any)
	var listSources []plugin.SourcePlugin
	for _, item := range listRaw {
		src, err := plugin.MakeListPlugin(item, db)
		if err != nil {
			return nil, fmt.Errorf("series: list: %w", err)
		}
		listSources = append(listSources, src)
	}

	ttl := time.Hour
	if v, _ := cfg["ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("series: invalid ttl %q: %w", v, err)
		}
		ttl = d
	}

	tracker := series.NewTracker(db.Bucket(series.TrackerBucketName))

	tr := trackingStrict
	if t, _ := cfg["tracking"].(string); t != "" {
		switch tracking(t) {
		case trackingStrict, trackingBackfill, trackingFollow:
			tr = tracking(t)
		default:
			return nil, fmt.Errorf("series: unknown tracking mode %q (strict|backfill|follow)", t)
		}
	}

	rejectUnmatched := plugin.OptBool(cfg, "reject_unmatched", true)

	var upgradeWindow time.Duration
	if v, _ := cfg["upgrade_window"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("series: invalid upgrade_window %q: %w", v, err)
		}
		upgradeWindow = d
	}

	var settleWindow time.Duration
	if v, _ := cfg["settle"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("series: invalid settle %q: %w", v, err)
		}
		settleWindow = d
	}

	retryCooldown := defaultRetryCooldown
	if v, _ := cfg["retry_cooldown"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("series: invalid retry_cooldown %q: %w", v, err)
		}
		retryCooldown = d
	}

	return &seriesPlugin{
		settle:          settleWindow,
		retryCooldown:   retryCooldown,
		untrackStore:    untrack.NewStore(db.Bucket(untrack.BucketName)),
		settleTracker:   settle.New(db.Bucket(settle.SeriesBucketName)),
		staticShows:     staticShows,
		listSources:     listSources,
		listCache:       cache.NewPersistent[[]match.TitleEntry](ttl, db.Bucket("cache_series_list")),
		tracking:        tr,
		tracker:         tracker,
		inactive:        series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
		downloadLog:     downloads.New(db.Bucket(downloads.BucketName)),
		rejectUnmatched: rejectUnmatched,
		upgradeWindow:   upgradeWindow,
	}, nil
}

// defaultRetryCooldown is how long an episode is held after a janitor pipeline
// marked its last grab failed. Non-zero by default: alternating to another
// release of an episode whose torrents keep dying is correct, but doing it on
// every scheduled run burns through every release of it in a few hours.
const defaultRetryCooldown = 6 * time.Hour

func (p *seriesPlugin) Name() string { return "series" }

// hasList reports whether the filter has any source of show names — either a
// static list or one or more dynamic list source plugins. When false, the
// filter accepts every classified entry that passes the quality / tracker
// checks instead of matching against a list.
func (p *seriesPlugin) hasList() bool {
	return len(p.staticShows) > 0 || len(p.listSources) > 0
}

func (p *seriesPlugin) filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	epID := e.GetString(entry.FieldSeriesEpisodeID)
	if epID == "" {
		if p.rejectUnmatched {
			e.Reject("series: entry has no series_episode_id (not classified as series upstream)")
		}
		return nil
	}

	parsedName := e.GetString(entry.FieldTitle)
	year := entry.ReleaseYear(e)
	// When no list is configured the filter operates in accept-all mode:
	// every classified episode passes the upstream Requires + quality/tracker
	// checks, with no title matching. The show is identified by the parsed
	// name so dedup and upgrade detection still work across runs.
	showName := parsedName
	if p.hasList() {
		show, ok := matchShow(parsedName, year, p.resolveShows(ctx, tc))
		if !ok {
			if p.rejectUnmatched {
				e.Reject("series: show not in list")
			}
			return nil
		}
		showName = show.Norm
		if show.Year != 0 {
			year = show.Year
		}
	}

	// The same show is spelled with and without its year by releases and by
	// TheTVDB over time, so it is resolved to every tracker key already
	// holding its episodes, and to the one new records go to.
	ref := p.tracker.Resolve(showName, year)
	if ref.Key == "" {
		return nil
	}
	matchedShow := ref.Key

	// Stamp the full tracker key for persist() to read back at commit time, so
	// we don't have to re-resolve the show list there — and, for the episode
	// identity, so the record is written under the key this decision was made
	// with. metainfo_tvdb re-parses the release and rewrites series_episode_id
	// (see entry.FieldSeriesTrackerEpisodeID), and commit runs after it.
	e.Set(seriesTrackerName, matchedShow)
	e.Set(entry.FieldSeriesTrackerEpisodeID, epID)
	e.Set(entry.FieldSeriesTrackerSeason, e.GetInt(entry.FieldSeriesSeason))
	e.Set(entry.FieldSeriesTrackerEpisode, e.GetInt(entry.FieldSeriesEpisode))
	e.Set(entry.FieldSeriesTrackerDouble, e.GetInt(entry.FieldSeriesDoubleEpisode))

	// A janitor pipeline marked this episode's last grab failed very recently.
	// Hold off instead of immediately grabbing the next release: when every
	// release of an episode is dead, retrying once per scheduled run just
	// churns. Rejection is per-run and uncommitted, so the entry is
	// re-evaluated on the next run and passes once the window elapses.
	if left, held := p.untrackStore.Remaining(
		untrack.EpisodeKey(matchedShow, epID), p.retryCooldown, time.Now()); held {
		e.Reject(fmt.Sprintf("series: %s %s last grab failed, retrying in %s",
			matchedShow, epID, left.Round(time.Minute)))
		return nil
	}

	// Deactivated shows (series_tracker_update sink, typically after a
	// series_lifecycle "complete" classification) are rejected before any
	// quality or tracker checks — searching for them is wasted work.
	for _, key := range ref.Keys {
		if rec, ok := p.inactive.Get(key); ok {
			reason := rec.Reason
			if reason == "" {
				reason = "deactivated"
			}
			e.Reject(fmt.Sprintf("series: %s inactive (%s)", key, reason))
			return nil
		}
	}

	incomingQuality, _ := e.Quality()

	if stored, ok := p.tracker.GetAny(ref.Keys, epID); ok {
		// Outside the upgrade window a better copy no longer replaces the
		// downloaded one — an episode grabbed and watched weeks ago should not
		// re-download because a remux appeared.
		if p.upgradeWindow > 0 && !stored.DownloadedAt.IsZero() &&
			time.Since(stored.DownloadedAt) > p.upgradeWindow {
			e.Reject(fmt.Sprintf("series: %s %s already downloaded (upgrade window expired)", matchedShow, epID))
			return nil
		}
		properOrRepack := e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack)
		switch quality.Decide(incomingQuality, stored.Quality, properOrRepack, stored.Repack) {
		case quality.UpgradeQuality:
			if p.holdForSettle(tc, e, matchedShow, epID) {
				return nil
			}
			e.Accept(fmt.Sprintf("series: %s %s quality upgrade", matchedShow, epID))
			return nil
		case quality.UpgradeProperRepack:
			if p.holdForSettle(tc, e, matchedShow, epID) {
				return nil
			}
			e.Accept(fmt.Sprintf("series: %s %s proper/repack accepted", matchedShow, epID))
			return nil
		}
		e.Reject(fmt.Sprintf("series: %s %s already downloaded", matchedShow, epID))
		return nil
	}

	if p.tracking == trackingStrict {
		if latest, ok := p.tracker.Latest(ref.Keys...); ok {
			if err := enforceStrict(tc.Logger, epID, latest); err != nil {
				e.Reject(err.Error())
				return nil
			}
		}
	}

	if p.tracking == trackingFollow {
		// On first encounter (no episodes tracked yet) accept everything —
		// handles binge dumps where a full season lands in a single run.
		// Once tracking is established, use the highest tracked episode as
		// the season floor: reject episodes from older seasons, accept
		// everything from the floor season onwards (including unseen episodes
		// within the floor season, e.g. mid-season gaps filled on a later run).
		// Using the highest episode (not earliest) prevents stale old-season
		// records from pulling the floor back to an earlier season.
		// For date-based shows fall back to comparing the full episode ID
		// string lexicographically.
		if highest, ok := p.tracker.HighestEpisode(ref.Keys...); ok {
			incomingSeason := e.GetInt(entry.FieldSeriesSeason)
			floorSeason := seasonFromEpisodeID(highest.EpisodeID)
			if incomingSeason > 0 && floorSeason > 0 {
				if incomingSeason < floorSeason {
					e.Reject(fmt.Sprintf("series: %s S%02d predates tracking window (at S%02d)",
						matchedShow, incomingSeason, floorSeason))
					return nil
				}
			} else if epID < highest.EpisodeID {
				e.Reject(fmt.Sprintf("series: %s %s predates tracking window (at %s)",
					matchedShow, epID, highest.EpisodeID))
				return nil
			}
		}
	}

	if p.holdForSettle(tc, e, matchedShow, epID) {
		return nil
	}
	e.Accept(fmt.Sprintf("series: %s %s matched", matchedShow, epID))
	return nil
}

func (p *seriesPlugin) persist(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		// Only persist entries that were accepted by all downstream nodes.
		// The executor passes every entry the series node produced to Commit,
		// including those later rejected by dedup — we must filter them here
		// so the stored quality reflects the entry that was actually downloaded.
		if !e.IsAccepted() {
			continue
		}
		// The key the filter decided with, not a fresh read of
		// series_episode_id — metainfo_tvdb rewrites that field and commit
		// runs after it (see entry.FieldSeriesTrackerEpisodeID).
		key, ok := e.SeriesTrackerKey()
		if !ok {
			continue
		}
		matchedShow, epID := key.Show, key.EpisodeID
		q, _ := e.Quality()
		rec := series.Record{
			SeriesName:   matchedShow,
			DisplayName:  e.GetString(entry.FieldTitle),
			EpisodeID:    epID,
			Quality:      q,
			DownloadedAt: time.Now(),
			Repack:       e.GetBool(entry.FieldVideoProper) || e.GetBool(entry.FieldVideoRepack),
		}
		// Build a minimal Episode for MarkWithParts so double-episode releases
		// also mark each individual part. MarkWithParts only consults Season,
		// Episode, and DoubleEpisode; date-based IDs (which can't be doubles)
		// naturally fall through with DoubleEpisode == 0.
		ep := &series.Episode{
			Season:        key.Season,
			Episode:       key.Episode,
			DoubleEpisode: key.DoubleEpisode,
		}
		if err := p.tracker.MarkWithParts(rec, ep); err != nil {
			return fmt.Errorf("series: mark %s %s: %w", matchedShow, epID, err)
		}
		// The wave produced a download; the next one starts a fresh timer.
		p.settleTracker.Clear(settle.SeriesKey(tc.Name, matchedShow, epID))
		// Append to the download history audit log (best-effort — the tracker
		// is the source of truth; the log is for reporting re-downloads and
		// quality upgrades over time). Optional: nil in tests that build the
		// plugin struct directly.
		if p.downloadLog == nil {
			continue
		}
		if err := p.downloadLog.Append(downloads.Event{
			MediaType:    "series",
			Name:         matchedShow,
			DisplayName:  rec.DisplayName,
			EpisodeID:    epID,
			Quality:      q,
			Repack:       rec.Repack,
			DownloadedAt: rec.DownloadedAt,
			Settled:      e.GetBool(entry.FieldSettled),
			Revived:      e.GetBool(entry.FieldSettledRevived),
			Task:         tc.Name,
		}); err != nil {
			tc.Logger.Warn("series: append download log", "series", matchedShow, "episode", epID, "err", err)
		}
	}
	return nil
}

func (p *seriesPlugin) resolveShows(ctx context.Context, tc *plugin.TaskContext) []match.TitleEntry {
	return plugin.ResolveDynamicList(ctx, tc, p.listSources, p.staticShows,
		func(src string) ([]match.TitleEntry, bool) { return p.listCache.Get(src) },
		func(src string, v []match.TitleEntry) { p.listCache.Set(src, v) },
	)
}

// matchShow returns the configured show that parsed (with the year the
// release names, 0 when none) belongs to. A title match wins outright — shows
// air over multiple years, so a release year is no reason to refuse one.
// Failing that, the names are compared without a trailing year, which is how
// releases and TheTVDB variously spell it: "Brothers 2026" and "Brothers"
// (listed with year 2026) are one show, unless the years contradict each
// other.
func matchShow(parsed string, year int, shows []match.TitleEntry) (match.TitleEntry, bool) {
	norm := match.Normalize(parsed)
	for _, s := range shows {
		if match.Fuzzy(norm, s.Norm) {
			return s, true
		}
	}
	release := series.NewShow(parsed, year)
	for _, s := range shows {
		if series.NewShow(s.Norm, s.Year).Matches(release) {
			return s, true
		}
	}
	return match.TitleEntry{}, false
}

// enforceStrict rejects episodes that skip more than one ahead of the latest
// downloaded episode (standard / absolute episode numbering only; date episodes
// skip this check because their IDs do not encode comparable season/episode
// numbers).
func enforceStrict(log *slog.Logger, epID string, latest *series.Record) error {
	incomingSeason, incomingEpisode, ok := series.ParseEpisodeID(epID)
	if !ok {
		return nil // date or unparseable: skip strict comparison
	}
	latestSeason, latestEpisode, ok := series.ParseEpisodeID(latest.EpisodeID)
	if !ok {
		log.Warn("series: strict tracking: stored episode ID did not parse, skipping strict check",
			"series", latest.SeriesName, "episode_id", latest.EpisodeID)
		return nil
	}
	if incomingSeason != latestSeason {
		return nil
	}
	gap := incomingEpisode - latestEpisode
	if gap > 1 {
		return fmt.Errorf("series: strict tracking: %s skips %d episodes ahead of latest %s",
			epID, gap-1, latest.EpisodeID)
	}
	return nil
}

// seasonFromEpisodeID extracts the season number from a zero-padded episode ID
// such as "S02E05" → 2. Returns 0 for date-based ("2023-11-15") or absolute
// ("EP123") IDs that carry no season number.
func seasonFromEpisodeID(epID string) int {
	if len(epID) >= 3 && (epID[0] == 'S' || epID[0] == 's') {
		var s int
		fmt.Sscanf(epID[1:], "%d", &s) //nolint:errcheck
		return s
	}
	return 0
}

func (p *seriesPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		// Series classifier: every entry that reaches this filter is a
		// series episode (Requires guarantees series_episode_id upstream).
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("series filter error", "entry", e.Title, "err", err)
		}
	}
	entries = append(entries, p.releaseSettled(ctx, tc, entries)...)
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists episode tracking records
// for all entries that were accepted by Process and not subsequently failed by
// any downstream sink. This ensures we only mark episodes as downloaded when
// the full pipeline (including download/output) succeeded.
func (p *seriesPlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}

// holdForSettle rejects an otherwise-downloadable entry while its episode's
// settle window is still running, and reports whether it did. Holding every
// release in a wave means they all become eligible in the same run once the
// window elapses, so the downstream dedup picks one best release instead of
// the pipeline grabbing each improvement as it appears. Rejection is per-run
// and uncommitted, so the entry is re-evaluated on the next run.
func (p *seriesPlugin) holdForSettle(tc *plugin.TaskContext, e *entry.Entry, show, epID string) bool {
	key := settle.SeriesKey(tc.Name, show, epID)
	left := p.settleTracker.Offer(key, settle.CandidateOf(e), p.settle, time.Now())
	if left <= 0 {
		if p.settle > 0 {
			e.Set(entry.FieldSettled, true)
		}
		return false
	}
	e.Reject(fmt.Sprintf("series: %s %s settling for %s more (holding the best release seen so far)",
		show, epID, left.Round(time.Minute)))
	return true
}

// releaseSettled downloads winners whose window elapsed but that are no
// longer advertised by any source — indexer feeds hold only a few hours of
// items, so a wave can scroll out before a longer window expires. Entries
// rebuilt here flow through filter() like any other, so the tracker records
// them at commit time.
func (p *seriesPlugin) releaseSettled(ctx context.Context, tc *plugin.TaskContext, batch []*entry.Entry) []*entry.Entry {
	if p.settle <= 0 {
		return nil
	}
	// Skip a candidate the feed is still advertising, matched by release name
	// — the only stable handle a release has here. Its URL is re-encrypted by
	// the indexer on every search, and the settle key identifies the item
	// rather than the release, so neither can tell one release of a film from
	// another. Without this the run would carry both the live copy and a
	// rebuilt one, and dedup could crown the rebuilt one and fetch from a link
	// that no longer resolves.
	present := make(map[string]bool, len(batch))
	for _, e := range batch {
		if n := settle.NormalizeTitle(e.Title); n != "" {
			present[n] = true
		}
	}
	var revived []*entry.Entry
	for _, exp := range p.settleTracker.Expired(tc.Name, p.settle, time.Now()) {
		if present[settle.NormalizeTitle(exp.Release.Title)] {
			continue // the live copy carries it
		}
		e := exp.Release.Rebuild()
		e.Set(entry.FieldSettledRevived, true)
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("series: settled release", "entry", e.Title, "err", err)
			continue
		}
		if !e.IsAccepted() {
			continue
		}
		tc.Logger.Info("series: releasing settled candidate no longer in the feed",
			"entry", e.Title, "quality", exp.Release.Quality.String())
		revived = append(revived, e)
	}
	return revived
}
