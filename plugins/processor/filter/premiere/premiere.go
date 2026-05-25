// Package premiere provides a filter that accepts premiere episodes of
// previously unseen series, enabling automatic "try new shows" pipelines.
//
// All qualifying entries for an unseen series are accepted — multiple
// quality variants from different sources pass through so the dedup processor
// can keep the best copy. A series is marked "seen" via CommitPlugin.Commit,
// which runs only after all downstream sinks confirm success. If the download
// fails, the premiere is not recorded and will be retried on the next run.
//
// Episode metadata is parsed directly from the entry title, so metainfo/series
// is not required. The parsed series fields are set on the entry for use by
// downstream plugins.
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
		// Only set when the entry title parses as a series episode.
		MayProduce: []string{
			entry.FieldTitle,
			entry.FieldSeriesSeason,
			entry.FieldSeriesEpisode,
			entry.FieldSeriesEpisodeID,
			entry.FieldSeriesDoubleEpisode,
			entry.FieldSeriesProper,
			entry.FieldSeriesRepack,
			entry.FieldSeriesService,
			"series_container",
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "quality", Type: plugin.FieldTypeString, Hint: "Quality spec the entry must satisfy"},
			{Key: "episode", Type: plugin.FieldTypeInt, Default: 1, Hint: "Episode number to treat as premiere"},
			{Key: "season", Type: plugin.FieldTypeInt, Default: 1, Hint: "Season number to match (0 = any season)"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool, Default: true, Hint: "Reject entries that do not parse as episodes"},
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
		if n := intVal(v, -1); n < 0 {
			errs = append(errs, fmt.Errorf("premiere: \"episode\" must be a non-negative integer"))
		}
	}
	if v, ok := cfg["season"]; ok {
		if n := intVal(v, -1); n < 0 {
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
	episode := intVal(cfg["episode"], 1)
	season := intVal(cfg["season"], 1)

	var spec quality.Spec
	if q, _ := cfg["quality"].(string); q != "" {
		s, err := quality.ParseSpec(q)
		if err != nil {
			return nil, fmt.Errorf("premiere: invalid quality spec: %w", err)
		}
		spec = s
	}

	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}

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
	ep, ok := series.Parse(e.Title)
	if !ok {
		if p.rejectUnmatched {
			e.Reject("premiere: title did not parse as episode")
		}
		return nil
	}

	epID := series.EpisodeID(ep)
	// Normalize the show name so tracker keys match those written by the series
	// plugin, which also uses match.Normalize for all bucket operations.
	normalizedName := match.Normalize(ep.SeriesName)

	// Stamp normalized name for persist() to read back at commit time.
	e.Set(premiereTrackerName, normalizedName)
	e.SetSeriesInfo(entry.SeriesInfo{
		VideoInfo:     entry.VideoInfo{GenericInfo: entry.GenericInfo{Title: ep.SeriesName}},
		Season:        ep.Season,
		Episode:       ep.Episode,
		EpisodeID:     epID,
		DoubleEpisode: ep.DoubleEpisode,
		Proper:        ep.Proper,
		Repack:        ep.Repack,
		Service:       ep.Service,
	})
	if ep.Container != "" {
		e.Set("series_container", ep.Container)
	}

	// Check season constraint.
	if p.season != 0 && ep.Season != p.season {
		e.Reject(fmt.Sprintf("premiere: season %d does not match premiere season %d", ep.Season, p.season))
		return nil
	}

	// Check episode number.
	if ep.Episode != p.episode {
		e.Reject(fmt.Sprintf("premiere: episode %d is not premiere episode %d", ep.Episode, p.episode))
		return nil
	}

	if (p.spec != quality.Spec{}) && !p.spec.Matches(ep.Quality) {
		e.Reject(fmt.Sprintf("premiere: %s %s quality %s does not match spec", ep.SeriesName, epID, ep.Quality.String()))
		return nil
	}

	// Reject if this episode is already in the shared series tracker.
	if p.tracker.IsSeen(normalizedName, epID) {
		e.Reject(fmt.Sprintf("premiere: %s %s already downloaded", ep.SeriesName, epID))
		return nil
	}

	// Accept all matching entries — dedup will keep the best one.
	e.Accept()
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
		ep, ok := series.Parse(e.Title)
		if !ok {
			continue
		}
		epID := series.EpisodeID(ep)
		rec := series.Record{
			SeriesName:   normalizedName,
			DisplayName:  e.GetString(entry.FieldTitle),
			EpisodeID:    epID,
			Quality:      ep.Quality,
			DownloadedAt: time.Now(),
		}
		if err := p.tracker.Mark(rec); err != nil {
			return fmt.Errorf("premiere: mark %s %s: %w", normalizedName, epID, err)
		}
		// For double episodes, also mark each part individually so a later
		// single-episode release is recognised as already downloaded.
		if ep.DoubleEpisode > 0 {
			ep1 := *ep
			ep1.DoubleEpisode = 0
			ep2 := *ep
			ep2.Episode = ep.DoubleEpisode
			ep2.DoubleEpisode = 0
			for _, partID := range []string{series.EpisodeID(&ep1), series.EpisodeID(&ep2)} {
				partRec := rec
				partRec.EpisodeID = partID
				if err := p.tracker.Mark(partRec); err != nil {
					return fmt.Errorf("premiere: mark %s %s: %w", normalizedName, partID, err)
				}
			}
		}
	}
	return nil
}

func intVal(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case float64:
		return int(t)
	case int64:
		return int(t)
	}
	return def
}

func (p *premierePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
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
