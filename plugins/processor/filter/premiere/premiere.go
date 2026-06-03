// Package premiere provides a filter that accepts premiere episodes of
// previously unseen series, enabling automatic "try new shows" pipelines.
//
// All qualifying entries for an unseen series are accepted — multiple
// quality variants from different sources pass through so the dedup processor
// can keep the best copy. A series is marked "seen" via CommitPlugin.Commit,
// which runs only after all downstream sinks confirm success. If the download
// fails, the premiere is not recorded and will be retried on the next run.
//
// Episode metadata is read from entry fields populated upstream by
// metainfo_file (or any equivalent metainfo source that sets the required
// fields). The plugin does not parse the entry title itself; the upstream
// requirement is declared via Descriptor.Requires so the DAG validator catches
// misconfigured pipelines at load time.
//
// Config keys:
//
//	episode - episode number to treat as premiere (default: 1)
//	season  - season number to match; 0 means any season (default: 1)
//	quality - quality spec the entry must satisfy (e.g. "720p+ webrip+")
package premiere

import (
	"context"
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

// premiereTrackerName is the entry field used to carry the normalized show
// name from filter() to persist(). It is internal to this plugin.
const premiereTrackerName = "_premiere_tracker_name"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "premiere",
		Description: "accept only the first episode of series not previously seen (series premiere detection)",
		Role:        plugin.RoleProcessor,
		// Episode metadata must be populated upstream — by metainfo_file in
		// the common case, or by any other plugin that sets these fields.
		// FieldQuality (the typed quality.Quality struct read via e.Quality())
		// is required so spec matching works and the persisted Record stores a
		// non-empty quality for future upgrade comparisons.
		Requires: plugin.RequireAll(
			entry.FieldTitle,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldQuality,
		),
		// Every entry exiting this filter is a series episode by
		// construction (Requires guarantees series_episode_id upstream).
		Produces: []string{
			entry.FieldMediaType,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "quality", Type: plugin.FieldTypeString, Hint: "Quality spec, e.g. 720p+ (floor), 720p (exact), 720p-1080p (range)"},
			{Key: "episode", Type: plugin.FieldTypeInt, Default: 1, Hint: "Episode number to treat as premiere"},
			{Key: "season", Type: plugin.FieldTypeInt, Default: 1, Hint: "Season number to match (0 = any season)"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject entries that lack series_episode_id (i.e. not classified as a series episode upstream)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if q, _ := cfg["quality"].(string); q != "" {
		if _, err := quality.ParseSpec(q); err != nil {
			errs = append(errs, fmt.Errorf("premiere: invalid quality spec: %w", err))
		}
	}
	if v, ok := cfg["episode"]; ok {
		if n := plugin.IntVal(v, -1); n < 0 {
			errs = append(errs, fmt.Errorf("premiere: \"episode\" must be a non-negative integer"))
		}
	}
	if v, ok := cfg["season"]; ok {
		if n := plugin.IntVal(v, -1); n < 0 {
			errs = append(errs, fmt.Errorf("premiere: \"season\" must be a non-negative integer"))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "premiere", "episode", "season", "quality", "reject_unmatched")...)
	return errs
}

type premierePlugin struct {
	episode         int
	season          int // 0 = any season
	spec            quality.Spec
	tracker         *series.Tracker
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	episode := plugin.IntVal(cfg["episode"], 1)
	season := plugin.IntVal(cfg["season"], 1)

	var spec quality.Spec
	if q, _ := cfg["quality"].(string); q != "" {
		s, err := quality.ParseSpec(q)
		if err != nil {
			return nil, fmt.Errorf("premiere: invalid quality spec: %w", err)
		}
		spec = s
	}

	rejectUnmatched := plugin.OptBool(cfg, "reject_unmatched", true)

	return &premierePlugin{
		episode:         episode,
		season:          season,
		spec:            spec,
		tracker:         series.NewTracker(db.Bucket("series")),
		rejectUnmatched: rejectUnmatched,
	}, nil
}

func (p *premierePlugin) Name() string { return "premiere" }

func (p *premierePlugin) filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	epID := e.GetString(entry.FieldSeriesEpisodeID)
	if epID == "" {
		if p.rejectUnmatched {
			e.Reject("premiere: entry has no series_episode_id (not classified as series upstream)")
		}
		return nil
	}

	season := e.GetInt(entry.FieldSeriesSeason)
	episode := e.GetInt(entry.FieldSeriesEpisode)
	displayName := e.GetString(entry.FieldTitle)

	// Tracker keys are normalized so they match those written by the series
	// plugin (which also normalizes). Stamp the normalized name so persist()
	// can read it back without re-normalizing.
	normalizedName := match.Normalize(displayName)
	e.Set(premiereTrackerName, normalizedName)

	if p.season != 0 && season != p.season {
		e.Reject(fmt.Sprintf("premiere: season %d does not match premiere season %d", season, p.season))
		return nil
	}

	if episode != p.episode {
		e.Reject(fmt.Sprintf("premiere: episode %d is not premiere episode %d", episode, p.episode))
		return nil
	}

	if (p.spec != quality.Spec{}) {
		q, _ := e.Quality()
		if !p.spec.Matches(q) {
			e.Reject(fmt.Sprintf("premiere: %s %s quality %s does not match spec", displayName, epID, q.String()))
			return nil
		}
	}

	if p.tracker.IsSeen(normalizedName, epID) {
		e.Reject(fmt.Sprintf("premiere: %s %s already downloaded", displayName, epID))
		return nil
	}

	// Accept all matching entries — dedup will keep the best one.
	e.Accept(fmt.Sprintf("premiere: %s %s matched", displayName, epID))
	return nil
}

// persist records accepted premiere entries in the series tracker.
// Multiple entries for the same series may be accepted in the same run
// (different qualities/sources); place dedup after premiere so only the
// best-quality copy is persisted.
func (p *premierePlugin) persist(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) error {
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		// normalizedName was stamped by filter(); reading it back avoids
		// re-normalizing at commit time and ensures consistent tracker keys.
		normalizedName := e.GetString(premiereTrackerName)
		if normalizedName == "" {
			continue
		}
		epID := e.GetString(entry.FieldSeriesEpisodeID)
		if epID == "" {
			continue
		}
		q, _ := e.Quality()
		rec := series.Record{
			SeriesName:   normalizedName,
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
			Season:        e.GetInt(entry.FieldSeriesSeason),
			Episode:       e.GetInt(entry.FieldSeriesEpisode),
			DoubleEpisode: e.GetInt(entry.FieldSeriesDoubleEpisode),
		}
		if err := p.tracker.MarkWithParts(rec, ep); err != nil {
			return fmt.Errorf("premiere: mark %s %s: %w", normalizedName, epID, err)
		}
	}
	return nil
}

func (p *premierePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		// Premiere is a series classifier — every processed entry is an
		// episode by Requires.
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("premiere filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists premiere tracking records
// for all entries that were accepted by Process and not subsequently failed by
// any downstream sink. This ensures we only mark episodes as seen when the full
// pipeline (including download/output) succeeded. Failed downloads are retried
// on the next run.
func (p *premierePlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}
