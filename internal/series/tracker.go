package series

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/brunoga/pipeliner/internal/untrack"
	"github.com/brunoga/pipeliner/quality"
)

// TrackerBucketName is the store bucket holding per-episode download records.
// It is deliberately not namespaced by task: every series/premiere filter and
// the series_tracker source across all pipelines share one tracker, so a show
// downloaded by one pipeline is recognised by every other.
const TrackerBucketName = "series"

// Record is persisted for each downloaded episode.
type Record struct {
	SeriesName   string          `json:"series_name"`
	DisplayName  string          `json:"display_name,omitempty"` // canonical title from the standard "title" field
	EpisodeID    string          `json:"episode_id"`
	DownloadedAt time.Time       `json:"downloaded_at"`
	Quality      quality.Quality `json:"quality"`
	// Repack is true when the stored release was itself a PROPER/REPACK. It
	// blocks chaining REPACK→REPACK at the same quality forever: without it,
	// the same REPACK torrent reappearing in a feed would be accepted on
	// every run. Pre-existing records (written before this field existed)
	// default to false, which costs one extra re-download in the worst case.
	Repack bool `json:"repack,omitempty"`

	// Prev is the record this one replaced: one level of undo, so a failed
	// grab can be rolled back to the download that preceded it instead of
	// erasing the episode's history. Nil for a first download.
	Prev *Previous `json:"prev,omitempty"`
}

// Previous is a superseded download, kept on the record that replaced it so
// UntrackGrab can restore it. Only the fields the upgrade decision reads are
// retained — the show and episode are the key and cannot differ.
type Previous struct {
	DisplayName  string          `json:"display_name,omitempty"`
	Repack       bool            `json:"repack,omitempty"`
	DownloadedAt time.Time       `json:"downloaded_at"`
	Quality      quality.Quality `json:"quality"`
}

// bucket is the minimal key-value interface that Tracker requires.
// store.Bucket satisfies this interface automatically.
type bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
	Delete(key string) error
	Keys() ([]string, error)
	All() (map[string][]byte, error)
}

// Tracker tracks which episodes of which series have been downloaded.
// It is backed by a bucket so state persists across runs.
type Tracker struct {
	bucket bucket
}

// NewTracker wraps a bucket as a series Tracker.
func NewTracker(b bucket) *Tracker {
	return &Tracker{bucket: b}
}

// Get returns the stored record for the given episode if it has been downloaded.
func (t *Tracker) Get(seriesName, episodeID string) (*Record, bool) {
	var rec Record
	found, _ := t.bucket.Get(recordKey(seriesName, episodeID), &rec)
	if !found {
		return nil, false
	}
	return &rec, true
}

// IsSeen returns true if the given episode has already been downloaded.
func (t *Tracker) IsSeen(seriesName, episodeID string) bool {
	_, ok := t.Get(seriesName, episodeID)
	return ok
}

// Mark records that an episode has been downloaded. Any record already stored
// under the same key is carried along as r.Prev so UntrackGrab can roll back
// to it; only one level is kept, which is all failed-grab recovery needs (it
// always refers to the most recent grab of a release).
func (t *Tracker) Mark(r Record) error {
	key := recordKey(r.SeriesName, r.EpisodeID)
	if r.Prev == nil {
		var old Record
		if found, _ := t.bucket.Get(key, &old); found {
			r.Prev = &Previous{
				DisplayName:  old.DisplayName,
				Repack:       old.Repack,
				DownloadedAt: old.DownloadedAt,
				Quality:      old.Quality,
			}
		}
	}
	return t.bucket.Put(key, r)
}

// MarkWithParts records that an episode has been downloaded and, for double
// episodes (e.g. S01E01E02), also marks each individual part so that a later
// single-episode release for either part is recognised as already downloaded.
func (t *Tracker) MarkWithParts(r Record, ep *Episode) error {
	if err := t.Mark(r); err != nil {
		return err
	}
	if ep.DoubleEpisode > 0 {
		ep1 := *ep
		ep1.DoubleEpisode = 0
		ep2 := *ep
		ep2.Episode = ep.DoubleEpisode
		ep2.DoubleEpisode = 0
		for _, partID := range []string{EpisodeID(&ep1), EpisodeID(&ep2)} {
			partRec := r
			partRec.EpisodeID = partID
			if err := t.Mark(partRec); err != nil {
				return err
			}
		}
	}
	return nil
}

// Forget removes the download record for an episode outright, history and all,
// so the series filter stops considering it downloaded. For double-episode IDs
// (S01E01E02) the individual part records written by MarkWithParts are removed
// too. No-op for unknown records.
//
// This is the explicit user-driven un-track (CLI, web tools); failed-grab
// recovery wants UntrackGrab instead.
func (t *Tracker) Forget(seriesName, episodeID string) error {
	for _, key := range episodeKeys(seriesName, episodeID) {
		if err := t.bucket.Delete(key); err != nil {
			return err
		}
	}
	return nil
}

// episodeKeys returns every bucket key MarkWithParts may have written for one
// episode id: the id itself, plus each part of a double episode.
func episodeKeys(seriesName, episodeID string) []string {
	keys := []string{recordKey(seriesName, episodeID)}
	var season, ep1, ep2 int
	if n, err := fmt.Sscanf(episodeID, "S%dE%dE%d", &season, &ep1, &ep2); err == nil && n == 3 {
		for _, part := range []int{ep1, ep2} {
			partID := EpisodeID(&Episode{Season: season, Episode: part})
			keys = append(keys, recordKey(seriesName, partID))
		}
	}
	return keys
}

// UntrackGrab rolls the tracker back after a grab turned out to be dead.
//
// Plain deletion is wrong here: each download overwrites the episode's record,
// so deleting it also erases the memory of whatever was downloaded before —
// and the next run then re-downloads an episode that is already in the
// library, often at worse quality than the copy it has. Instead the record is
// rolled back to the download it replaced, and only deleted when the failed
// grab is the only one on record. A double episode's part records move with
// the combined one, since MarkWithParts wrote them together.
//
// failed is the quality of the grab that died, as recorded on its grab record.
// When it does not match the stored record the record belongs to a later,
// different download and is left untouched. Pass hasQuality=false for grab
// records written before the quality was captured: the rollback still happens,
// but the staleness check cannot run.
func (t *Tracker) UntrackGrab(seriesName, episodeID string, failed quality.Quality, hasQuality bool) (untrack.Outcome, error) {
	keys := episodeKeys(seriesName, episodeID)

	// The combined key decides for the whole set: the parts were written from
	// the same release, so rolling one back and not the others would leave the
	// episode half-tracked.
	var rec Record
	found, err := t.bucket.Get(keys[0], &rec)
	if err != nil {
		return untrack.NoRecord, err
	}
	if !found {
		return untrack.NoRecord, nil
	}
	if hasQuality && rec.Quality != failed {
		return untrack.Stale, nil
	}

	outcome := untrack.Deleted
	if rec.Prev != nil {
		outcome = untrack.Restored
	}
	for _, key := range keys {
		var cur Record
		if found, _ := t.bucket.Get(key, &cur); !found {
			continue
		}
		if cur.Prev == nil {
			if err := t.bucket.Delete(key); err != nil {
				return outcome, err
			}
			continue
		}
		restored := Record{
			SeriesName:   cur.SeriesName,
			DisplayName:  cur.Prev.DisplayName,
			EpisodeID:    cur.EpisodeID,
			DownloadedAt: cur.Prev.DownloadedAt,
			Quality:      cur.Prev.Quality,
			Repack:       cur.Prev.Repack,
		}
		if restored.DisplayName == "" {
			restored.DisplayName = cur.DisplayName
		}
		if err := t.bucket.Put(key, restored); err != nil {
			return outcome, err
		}
	}
	return outcome, nil
}

// HighestEpisode returns the episode with the lexicographically greatest EpisodeID
// for the given series. Because episode IDs are zero-padded (S01E01, 2023-11-15,
// EP001), lexicographic order matches episode order. This represents the furthest
// progress point and is used by "follow" tracking mode as the season floor:
// episodes from seasons older than the highest tracked season are rejected.
// Several names (the keys of one ShowRef) are searched together.
func (t *Tracker) HighestEpisode(seriesNames ...string) (*Record, bool) {
	all, err := t.bucket.All()
	if err != nil {
		return nil, false
	}
	var highest *Record
	for k, raw := range all {
		rec, ok := recordFor(k, raw, seriesNames)
		if !ok {
			continue
		}
		if highest == nil || rec.EpisodeID > highest.EpisodeID {
			r := rec
			highest = &r
		}
	}
	return highest, highest != nil
}

// Latest returns the most recently downloaded episode for the given series,
// determined by DownloadedAt timestamp. Uses All() to fetch all records in
// a single query rather than Keys() + N×Get(). Several names (the keys of one
// ShowRef) are searched together.
func (t *Tracker) Latest(seriesNames ...string) (*Record, bool) {
	all, err := t.bucket.All()
	if err != nil {
		return nil, false
	}
	var latest *Record
	for k, raw := range all {
		rec, ok := recordFor(k, raw, seriesNames)
		if !ok {
			continue
		}
		if latest == nil ||
			rec.DownloadedAt.After(latest.DownloadedAt) ||
			(rec.DownloadedAt.Equal(latest.DownloadedAt) && rec.EpisodeID > latest.EpisodeID) {
			r := rec
			latest = &r
		}
	}
	return latest, latest != nil
}

// ShowSummary aggregates the tracker's per-episode records for one show.
type ShowSummary struct {
	// Name is the normalized show name used as the tracker key.
	Name string
	// DisplayName is the canonical title from the most recently downloaded
	// record that carries one. Empty when no record has a display name.
	DisplayName string
	// EpisodeCount is the number of episode records for the show.
	EpisodeCount int
	// NewestEpisodeID is the lexicographically greatest episode ID (zero-padded
	// IDs make lexicographic order match episode order).
	NewestEpisodeID string
	// LastDownloadedAt is the most recent DownloadedAt across all records.
	LastDownloadedAt time.Time
}

// Summaries returns one ShowSummary per tracked show, sorted by Name.
// It fetches all records in a single query.
func (t *Tracker) Summaries() ([]ShowSummary, error) {
	all, err := t.bucket.All()
	if err != nil {
		return nil, err
	}
	byName := make(map[string]*ShowSummary)
	// displayAt tracks the DownloadedAt of the record that supplied each
	// show's DisplayName, so the most recent non-empty one wins.
	displayAt := make(map[string]time.Time)
	for _, raw := range all {
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.SeriesName == "" {
			continue
		}
		s, ok := byName[rec.SeriesName]
		if !ok {
			s = &ShowSummary{Name: rec.SeriesName}
			byName[rec.SeriesName] = s
		}
		s.EpisodeCount++
		if rec.EpisodeID > s.NewestEpisodeID {
			s.NewestEpisodeID = rec.EpisodeID
		}
		if rec.DownloadedAt.After(s.LastDownloadedAt) {
			s.LastDownloadedAt = rec.DownloadedAt
		}
		if rec.DisplayName != "" && (s.DisplayName == "" || rec.DownloadedAt.After(displayAt[rec.SeriesName])) {
			s.DisplayName = rec.DisplayName
			displayAt[rec.SeriesName] = rec.DownloadedAt
		}
	}
	out := make([]ShowSummary, 0, len(byName))
	for _, s := range byName {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// EpisodeID returns the canonical episode identifier for an Episode.
// For standard episodes: "S01E01" or "S01E01E02".
// For date episodes: "2023-11-15".
// For absolute episodes: "EP123".
func EpisodeID(ep *Episode) string {
	if ep.IsDate {
		return fmt.Sprintf("%04d-%02d-%02d", ep.Year, ep.Month, ep.Day)
	}
	if ep.Season > 0 {
		if ep.DoubleEpisode > 0 {
			return fmt.Sprintf("S%02dE%02dE%02d", ep.Season, ep.Episode, ep.DoubleEpisode)
		}
		return fmt.Sprintf("S%02dE%02d", ep.Season, ep.Episode)
	}
	return fmt.Sprintf("EP%03d", ep.Episode)
}

// recordFor decodes the record stored under key k when it belongs to one of
// seriesNames.
func recordFor(k string, raw []byte, seriesNames []string) (Record, bool) {
	for _, name := range seriesNames {
		if !hasPrefix(k, name+"|") {
			continue
		}
		var rec Record
		if err := json.Unmarshal(raw, &rec); err != nil || rec.SeriesName != name {
			return Record{}, false
		}
		return rec, true
	}
	return Record{}, false
}

func recordKey(seriesName, episodeID string) string {
	return seriesName + "|" + episodeID
}

func hasPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
