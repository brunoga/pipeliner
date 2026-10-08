package gaps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

// fixedNow is the reference "now" for aired-date comparisons in all tests.
var fixedNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// mockShow is one show served by the TVDB stub, keyed by search name.
type mockShow struct {
	id       string
	episodes []map[string]any
}

// tvdbMock is a TVDB v4 API stub serving any number of shows, with hit
// counters for the search and episode endpoints so cache tests can assert
// how often the API was actually consulted.
type tvdbMock struct {
	srv         *httptest.Server
	searchHits  atomic.Int32
	episodeHits atomic.Int32
}

func newMockTVDB(t *testing.T, shows map[string]mockShow) *tvdbMock {
	t.Helper()
	m := &tvdbMock{}
	m.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		switch {
		case path == "/v4/login":
			json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]string{"token": "test-jwt"}, "status": "success",
			})
		case path == "/v4/search":
			m.searchHits.Add(1)
			q := r.URL.Query().Get("query")
			for name, s := range shows {
				if strings.EqualFold(name, q) {
					json.NewEncoder(w).Encode(map[string]any{
						"data": []map[string]any{
							{"tvdb_id": s.id, "name": name, "status": "Ended"},
						},
						"status": "success",
					})
					return
				}
			}
			json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "status": "success"})
		case strings.HasSuffix(path, "/episodes/official"):
			m.episodeHits.Add(1)
			id := strings.TrimSuffix(strings.TrimPrefix(path, "/v4/series/"), "/episodes/official")
			for _, s := range shows {
				if s.id == id {
					json.NewEncoder(w).Encode(map[string]any{
						"data":   map[string]any{"episodes": s.episodes},
						"status": "success",
					})
					return
				}
			}
			http.NotFound(w, r)
		case strings.HasPrefix(path, "/v4/series/"):
			id := strings.TrimPrefix(path, "/v4/series/")
			for name, s := range shows {
				if s.id == id {
					json.NewEncoder(w).Encode(map[string]any{
						"data": map[string]any{
							"name": name, "status": map[string]any{"id": 2, "name": "Ended"},
						},
						"status": "success",
					})
					return
				}
			}
			http.NotFound(w, r)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(m.srv.Close)
	return m
}

// openPlugin builds a gapsPlugin against the mock server with a fixed clock,
// returning the plugin and its store for tracker seeding.
func openPlugin(t *testing.T, m *tvdbMock, extra map[string]any) (*gapsPlugin, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	cfg := map[string]any{"api_key": "test-key"}
	maps.Copy(cfg, extra)
	pl, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p := pl.(*gapsPlugin)
	p.resolver.Client.BaseURL = m.srv.URL + "/v4"
	p.now = func() time.Time { return fixedNow }
	return p, db
}

func seedTracker(t *testing.T, db *store.SQLiteStore, name string, episodeIDs ...string) {
	t.Helper()
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	for _, id := range episodeIDs {
		if err := tr.Mark(series.Record{SeriesName: name, EpisodeID: id, DownloadedAt: fixedNow}); err != nil {
			t.Fatalf("Mark: %v", err)
		}
	}
}

func showEntry(title, seriesName string) *entry.Entry {
	e := entry.New(title, "pipeliner://series/"+seriesName)
	e.Set(entry.FieldSeriesName, seriesName)
	return e
}

func run(t *testing.T, p *gapsPlugin, entries ...*entry.Entry) []*entry.Entry {
	t.Helper()
	out, err := p.Process(context.Background(), makeCtx(), entries)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return out
}

func titles(entries []*entry.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Title
	}
	return out
}

// ep builds one mock episode record.
func ep(season, episode int, aired string) map[string]any {
	rec := map[string]any{"id": season*1000 + episode, "seasonNumber": season, "number": episode}
	if aired != "" {
		rec["aired"] = aired
	}
	return rec
}

// season1 is a standard 4-episode aired season plus a future episode, an
// undated episode, and an aired special.
func season1() []map[string]any {
	return []map[string]any{
		ep(1, 1, "2008-01-20"),
		ep(1, 2, "2008-01-27"),
		ep(1, 3, "2008-02-03"),
		ep(1, 4, "2008-02-10"),
		ep(1, 5, "2030-06-01"), // future: not aired yet
		ep(1, 6, ""),           // undated: unscheduled
		ep(0, 1, "2008-05-01"), // special
	}
}

func myShow() map[string]mockShow {
	return map[string]mockShow{"My Show": {id: "100", episodes: season1()}}
}

func TestGapDiffEmitsMissingAiredEpisodes(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, db := openPlugin(t, m, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	out := run(t, p, showEntry("My Show", "my show"))
	// 2 of 4 aired missing = 0.5, not strictly above the 0.5 default
	// threshold, so per-episode entries. Future, undated, and special
	// episodes must not appear.
	want := []string{"My Show S01E03", "My Show S01E04"}
	if got := titles(out); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("titles: got %v, want %v", got, want)
	}
}

func TestEmittedEntryShape(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, db := openPlugin(t, m, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	out := run(t, p, showEntry("My Show", "my show"))
	if len(out) != 2 {
		t.Fatalf("got %d entries, want 2", len(out))
	}
	e := out[0]
	if e.Title != "My Show S01E03" {
		t.Errorf("title: got %q", e.Title)
	}
	if e.URL != "pipeliner://gap/my%20show/S01E03" {
		t.Errorf("url: got %q", e.URL)
	}
	if got := e.GetString(entry.FieldSource); got != "series_gaps:tvdb" {
		t.Errorf("source: got %q", got)
	}
	if got := e.GetString(entry.FieldMediaType); got != entry.MediaTypeSeries {
		t.Errorf("media_type: got %q", got)
	}
	if got := e.GetString(entry.FieldSeriesName); got != "my show" {
		t.Errorf("series_name: got %q", got)
	}
	if got := e.GetInt(entry.FieldSeriesSeason); got != 1 {
		t.Errorf("series_season: got %d", got)
	}
	if got := e.GetInt(entry.FieldSeriesEpisode); got != 3 {
		t.Errorf("series_episode: got %d", got)
	}
	if got := e.GetString(entry.FieldSeriesEpisodeID); got != "S01E03" {
		t.Errorf("series_episode_id: got %q", got)
	}
	if got := e.GetString("tvdb_id"); got != "100" {
		t.Errorf("tvdb_id: got %q", got)
	}
	if !e.IsUndecided() {
		t.Errorf("state: got %v, want undecided", e.State)
	}
}

func TestURLStableAcrossRuns(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, db := openPlugin(t, m, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	first := run(t, p, showEntry("My Show", "my show"))
	second := run(t, p, showEntry("My Show", "my show"))
	if len(first) != len(second) {
		t.Fatalf("run sizes differ: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].URL != second[i].URL {
			t.Errorf("URL drifted between runs: %q vs %q", first[i].URL, second[i].URL)
		}
	}
}

func TestSpecialsExcludedByDefaultIncludedOnOptIn(t *testing.T) {
	// pack_threshold=1 disables packs so the special surfaces per-episode.
	m := newMockTVDB(t, myShow())
	p, db := openPlugin(t, m, map[string]any{"pack_threshold": 1})
	seedTracker(t, db, "my show", "S01E01", "S01E02")
	out := run(t, p, showEntry("My Show", "my show"))
	for _, e := range out {
		if e.GetInt(entry.FieldSeriesSeason) == 0 {
			t.Fatalf("special emitted without include_specials: %q", e.Title)
		}
	}

	m2 := newMockTVDB(t, myShow())
	p2, db2 := openPlugin(t, m2, map[string]any{"pack_threshold": 1, "include_specials": true})
	seedTracker(t, db2, "my show", "S01E01", "S01E02")
	out2 := run(t, p2, showEntry("My Show", "my show"))
	got := titles(out2)
	want := []string{"My Show S00E01", "My Show S01E03", "My Show S01E04"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("titles with specials: got %v, want %v", got, want)
	}
	// Season-0 specials use the tracker's EP-form canonical ID.
	if id := out2[0].GetString(entry.FieldSeriesEpisodeID); id != "EP001" {
		t.Errorf("special episode id: got %q, want EP001", id)
	}
}

// packCase runs the standard 4-aired-episode season with `tracked` episodes
// already in the tracker under the given threshold config value.
func packCase(t *testing.T, threshold any, tracked ...string) []*entry.Entry {
	t.Helper()
	m := newMockTVDB(t, myShow())
	extra := map[string]any{}
	if threshold != nil {
		extra["pack_threshold"] = threshold
	}
	p, db := openPlugin(t, m, extra)
	seedTracker(t, db, "my show", tracked...)
	return run(t, p, showEntry("My Show", "my show"))
}

func TestPackThresholdBoundaries(t *testing.T) {
	// Exactly at the threshold: 2 of 4 missing = 0.5 does NOT exceed 0.5.
	out := packCase(t, 0.5, "S01E01", "S01E02")
	if len(out) != 2 || out[0].Title != "My Show S01E03" {
		t.Errorf("at threshold: got %v, want per-episode entries", titles(out))
	}

	// Above the threshold: 3 of 4 missing = 0.75 > 0.5 → one pack entry.
	out = packCase(t, 0.5, "S01E01")
	if len(out) != 1 || out[0].Title != "My Show S01" {
		t.Fatalf("above threshold: got %v, want [My Show S01]", titles(out))
	}
	pack := out[0]
	if pack.URL != "pipeliner://gap/my%20show/S01" {
		t.Errorf("pack url: got %q", pack.URL)
	}
	if got := pack.GetInt(entry.FieldSeriesSeason); got != 1 {
		t.Errorf("pack series_season: got %d", got)
	}
	if _, ok := pack.Get(entry.FieldSeriesEpisode); ok {
		t.Error("pack entry must not set series_episode")
	}
	if _, ok := pack.Get(entry.FieldSeriesEpisodeID); ok {
		t.Error("pack entry must not set series_episode_id")
	}

	// Below the threshold: 1 of 4 missing = 0.25 → per-episode.
	out = packCase(t, 0.5, "S01E01", "S01E02", "S01E03")
	if len(out) != 1 || out[0].Title != "My Show S01E04" {
		t.Errorf("below threshold: got %v, want [My Show S01E04]", titles(out))
	}

	// Threshold 0: any gap at all becomes a pack.
	out = packCase(t, 0, "S01E01", "S01E02", "S01E03")
	if len(out) != 1 || out[0].Title != "My Show S01" {
		t.Errorf("threshold 0: got %v, want [My Show S01]", titles(out))
	}

	// Threshold 1: even a fully-missing season stays per-episode (1.0 is
	// not strictly greater than 1).
	out = packCase(t, 1)
	if len(out) != 4 || out[0].Title != "My Show S01E01" {
		t.Errorf("threshold 1: got %v, want 4 per-episode entries", titles(out))
	}

	// String threshold (visual editor form) parses too.
	out = packCase(t, "0.5", "S01E01")
	if len(out) != 1 || out[0].Title != "My Show S01" {
		t.Errorf("string threshold: got %v, want [My Show S01]", titles(out))
	}
}

// tenEpisodes is a single 10-episode fully-aired season.
func tenEpisodes() map[string]mockShow {
	eps := make([]map[string]any, 0, 10)
	for i := 1; i <= 10; i++ {
		eps = append(eps, ep(1, i, "2008-01-20"))
	}
	return map[string]mockShow{"My Show": {id: "100", episodes: eps}}
}

func TestCapAndCursorResumeAcrossRuns(t *testing.T) {
	m := newMockTVDB(t, tenEpisodes())
	p, _ := openPlugin(t, m, map[string]any{"pack_threshold": 1, "max_per_run": 4})

	want := [][]string{
		{"My Show S01E01", "My Show S01E02", "My Show S01E03", "My Show S01E04"},
		{"My Show S01E05", "My Show S01E06", "My Show S01E07", "My Show S01E08"},
		// Wrap-around: the tail plus the head again.
		{"My Show S01E09", "My Show S01E10", "My Show S01E01", "My Show S01E02"},
	}
	for i, w := range want {
		out := run(t, p, showEntry("My Show", "my show"))
		if fmt.Sprint(titles(out)) != fmt.Sprint(w) {
			t.Fatalf("run %d: got %v, want %v", i+1, titles(out), w)
		}
	}
}

func TestCursorSurvivesShowDisappearing(t *testing.T) {
	shows := map[string]mockShow{
		"Alpha": {id: "1", episodes: []map[string]any{
			ep(1, 1, "2008-01-20"), ep(1, 2, "2008-01-20"),
			ep(1, 3, "2008-01-20"), ep(1, 4, "2008-01-20"),
		}},
		"Beta": {id: "2", episodes: []map[string]any{
			ep(1, 1, "2008-01-20"), ep(1, 2, "2008-01-20"),
			ep(1, 3, "2008-01-20"), ep(1, 4, "2008-01-20"),
		}},
	}
	m := newMockTVDB(t, shows)
	p, _ := openPlugin(t, m, map[string]any{"pack_threshold": 1, "max_per_run": 3})

	out := run(t, p, showEntry("Alpha", "alpha"), showEntry("Beta", "beta"))
	want := []string{"Alpha S01E01", "Alpha S01E02", "Alpha S01E03"}
	if fmt.Sprint(titles(out)) != fmt.Sprint(want) {
		t.Fatalf("run 1: got %v, want %v", titles(out), want)
	}

	// Alpha disappears from the upstream list; the cursor points into
	// Alpha's key range but the next run must simply continue with the
	// first candidate after it.
	out = run(t, p, showEntry("Beta", "beta"))
	want = []string{"Beta S01E01", "Beta S01E02", "Beta S01E03"}
	if fmt.Sprint(titles(out)) != fmt.Sprint(want) {
		t.Fatalf("run 2: got %v, want %v", titles(out), want)
	}
}

func TestDryRunDoesNotAdvanceCursor(t *testing.T) {
	m := newMockTVDB(t, tenEpisodes())
	p, _ := openPlugin(t, m, map[string]any{"pack_threshold": 1, "max_per_run": 4})

	dryCtx := makeCtx()
	dryCtx.DryRun = true
	first, err := p.Process(context.Background(), dryCtx, []*entry.Entry{showEntry("My Show", "my show")})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	second, err := p.Process(context.Background(), dryCtx, []*entry.Entry{showEntry("My Show", "my show")})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if fmt.Sprint(titles(first)) != fmt.Sprint(titles(second)) {
		t.Fatalf("dry-run advanced the cursor: %v vs %v", titles(first), titles(second))
	}

	// A real run still starts from the beginning.
	out := run(t, p, showEntry("My Show", "my show"))
	if got := titles(out); got[0] != "My Show S01E01" {
		t.Fatalf("first real run after dry-runs: got %v", got)
	}
}

func TestInactiveShowSkippedByDefault(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, db := openPlugin(t, m, nil)
	inactive := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := inactive.Deactivate("my show", "test"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	if out := run(t, p, showEntry("My Show", "my show")); len(out) != 0 {
		t.Fatalf("inactive show emitted %v, want nothing", titles(out))
	}
	if m.searchHits.Load() != 0 {
		t.Error("inactive show must be skipped before any TVDB lookup")
	}

	// include_inactive=true overrides the skip.
	m2 := newMockTVDB(t, myShow())
	p2, db2 := openPlugin(t, m2, map[string]any{"include_inactive": true})
	inactive2 := series.NewInactiveSet(db2.Bucket(series.InactiveBucketName))
	if err := inactive2.Deactivate("my show", "test"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}
	if out := run(t, p2, showEntry("My Show", "my show")); len(out) == 0 {
		t.Fatal("include_inactive=true should emit the show's gaps")
	}
}

func TestResolveByTvdbIDSkipsSearch(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, _ := openPlugin(t, m, nil)

	e := showEntry("My Show", "my show")
	e.Set("tvdb_id", "100")
	out := run(t, p, e)
	if len(out) == 0 {
		t.Fatal("expected gap entries")
	}
	if m.searchHits.Load() != 0 {
		t.Error("search endpoint must not be hit when tvdb_id is present")
	}
}

func TestLookupsAreCachedAcrossRuns(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, _ := openPlugin(t, m, nil)

	run(t, p, showEntry("My Show", "my show"))
	run(t, p, showEntry("My Show", "my show"))
	if got := m.searchHits.Load(); got != 1 {
		t.Errorf("search hits: got %d, want 1 (second run must be cached)", got)
	}
	if got := m.episodeHits.Load(); got != 1 {
		t.Errorf("episode hits: got %d, want 1 (second run must be cached)", got)
	}
}

func TestLookupFailureSkipsShowButNotRun(t *testing.T) {
	// Only Beta resolves; Alpha's search returns no match.
	shows := map[string]mockShow{
		"Beta": {id: "2", episodes: []map[string]any{ep(1, 1, "2008-01-20")}},
	}
	m := newMockTVDB(t, shows)
	p, _ := openPlugin(t, m, nil)

	out := run(t, p, showEntry("Alpha", "alpha"), showEntry("Beta", "beta"))
	want := []string{"Beta S01"} // 1 of 1 missing = 1.0 > 0.5 → pack
	if fmt.Sprint(titles(out)) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", titles(out), want)
	}
}

func TestDuplicateUpstreamShowsScannedOnce(t *testing.T) {
	m := newMockTVDB(t, myShow())
	p, db := openPlugin(t, m, nil)
	seedTracker(t, db, "my show", "S01E01", "S01E02")

	out := run(t, p, showEntry("My Show", "my show"), showEntry("My.Show", ""))
	if len(out) != 2 {
		t.Fatalf("duplicate upstream shows must not duplicate gaps: got %v", titles(out))
	}
}

func TestDescriptorDeclaresReplacesUpstream(t *testing.T) {
	desc, ok := plugin.Lookup(pluginName)
	if !ok {
		t.Fatal("series_gaps not registered")
	}
	if !desc.ReplacesUpstream {
		t.Error("series_gaps must declare ReplacesUpstream so pipeline counters track emitted entries")
	}
	if desc.Role != plugin.RoleProcessor {
		t.Errorf("role: got %v, want processor", desc.Role)
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing api_key should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "cache_ttl": "nope"}); len(errs) == 0 {
		t.Error("bad cache_ttl should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "pack_threshold": 1.5}); len(errs) == 0 {
		t.Error("pack_threshold above 1 should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "pack_threshold": -0.1}); len(errs) == 0 {
		t.Error("negative pack_threshold should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "pack_threshold": "abc"}); len(errs) == 0 {
		t.Error("non-numeric pack_threshold should fail validation")
	}
	if errs := validate(map[string]any{"api_key": "k", "bogus": 1}); len(errs) == 0 {
		t.Error("unknown key should fail validation")
	}
	if errs := validate(map[string]any{
		"api_key": "k", "cache_ttl": "1h", "include_specials": true,
		"include_inactive": true, "pack_threshold": 0.7, "max_per_run": 10,
	}); len(errs) != 0 {
		t.Errorf("valid config should pass, got %v", errs)
	}
}

// ── media-server mode ────────────────────────────────────────────────────────

// stubServer is a mediaserver.Client over a fixed item list.
type stubServer struct {
	items []mediaserver.Item
	err   error
	calls int
}

func (s *stubServer) ListItems(context.Context) ([]mediaserver.Item, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.items, nil
}
func (s *stubServer) Refresh(context.Context) error { return nil }

// owns builds a library item for one episode of "My Show".
func owns(season, episode int) mediaserver.Item {
	return mediaserver.Item{
		Type: "episode", Show: "My Show", Season: season, Episode: episode,
		Section: "TV Shows",
	}
}

// openWithServer builds the plugin in media-server mode over a stub library.
func openWithServer(t *testing.T, m *tvdbMock, srv *stubServer, extra map[string]any) (*gapsPlugin, *store.SQLiteStore) {
	t.Helper()
	cfg := map[string]any{"backend": "plex", "url": "http://x", "token": "t"}
	maps.Copy(cfg, extra)
	p, db := openPlugin(t, m, cfg)
	p.client = srv
	return p, db
}

// twoSeasons is season 1 (4 aired) plus season 2 (3 aired) plus season 3
// (2 aired), so a floor can sit in the middle with seasons on both sides.
func twoSeasons() []map[string]any {
	return []map[string]any{
		ep(1, 1, "2008-01-20"), ep(1, 2, "2008-01-27"),
		ep(2, 1, "2009-01-20"), ep(2, 2, "2009-01-27"), ep(2, 3, "2009-02-03"),
		ep(3, 1, "2010-01-20"), ep(3, 2, "2010-01-27"),
	}
}

func multiSeasonShow() map[string]mockShow {
	return map[string]mockShow{"My Show": {id: "100", episodes: twoSeasons()}}
}

// TestFromFirstOwnedAnchorsOnEarliestHeldSeason is the behaviour asked for:
// holding only S02E01 means season 1 is out of scope, the rest of season 2 is
// in scope, and season 3 is in scope in full even though nothing of it is
// held.
func TestFromFirstOwnedAnchorsOnEarliestHeldSeason(t *testing.T) {
	m := newMockTVDB(t, multiSeasonShow())
	srv := &stubServer{items: []mediaserver.Item{owns(2, 1)}}
	p, _ := openWithServer(t, m, srv, map[string]any{
		"seasons": "from_first_owned", "pack_threshold": 1.0,
	})

	got := titles(run(t, p, showEntry("My Show", "my show")))
	want := []string{
		"My Show S02E02", "My Show S02E03",
		"My Show S03E01", "My Show S03E02",
	}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// TestSeasonsAllIgnoresTheFloor keeps the default honest: with seasons=all a
// library-backed run still proposes season 1.
func TestSeasonsAllIgnoresTheFloor(t *testing.T) {
	m := newMockTVDB(t, multiSeasonShow())
	srv := &stubServer{items: []mediaserver.Item{owns(2, 1)}}
	p, _ := openWithServer(t, m, srv, map[string]any{"pack_threshold": 1.0})

	got := titles(run(t, p, showEntry("My Show", "my show")))
	if len(got) != 6 {
		t.Fatalf("want all 6 missing aired episodes, got %v", got)
	}
	if got[0] != "My Show S01E01" {
		t.Errorf("season 1 should be in scope with seasons=all, got %v", got)
	}
}

// TestDeletedEpisodeBecomesAGapAgain is the case that made consulting the
// shared tracker wrong: the tracker says pipeliner grabbed it, the library
// says it is gone. The library wins, or a deleted episode could never be
// re-fetched.
func TestDeletedEpisodeBecomesAGapAgain(t *testing.T) {
	m := newMockTVDB(t, myShow())
	srv := &stubServer{items: []mediaserver.Item{owns(1, 1)}}
	p, db := openWithServer(t, m, srv, map[string]any{"pack_threshold": 1.0})
	// Grabbed once, since deleted from the library.
	seedTracker(t, db, "my show", "S01E02")

	got := titles(run(t, p, showEntry("My Show", "my show")))
	for _, want := range []string{"My Show S01E02", "My Show S01E03", "My Show S01E04"} {
		found := false
		for _, g := range got {
			if g == want {
				found = true
			}
		}
		if !found {
			t.Errorf("%q missing from %v: a tracked-but-deleted episode must be a gap again", want, got)
		}
	}
	for _, g := range got {
		if g == "My Show S01E01" {
			t.Error("S01E01 is in the library and must not be proposed")
		}
	}
}

// TestPendingCooldownSuppressesThenExpires covers what replaced the tracker:
// an episode just asked for is skipped while it downloads, and comes back
// once the cooldown lapses.
func TestPendingCooldownSuppressesThenExpires(t *testing.T) {
	m := newMockTVDB(t, myShow())
	srv := &stubServer{items: []mediaserver.Item{owns(1, 1)}}
	p, _ := openWithServer(t, m, srv, map[string]any{
		"pack_threshold": 1.0, "retry_cooldown": "48h",
	})

	first := titles(run(t, p, showEntry("My Show", "my show")))
	if len(first) == 0 {
		t.Fatal("first run proposed nothing")
	}
	// Same run again: everything just asked for is on cooldown.
	if second := titles(run(t, p, showEntry("My Show", "my show"))); len(second) != 0 {
		t.Errorf("cooldown not applied, re-proposed %v", second)
	}
	// Past the cooldown it is fair game again — the download may have failed.
	p.now = func() time.Time { return fixedNow.Add(72 * time.Hour) }
	if third := titles(run(t, p, showEntry("My Show", "my show"))); len(third) != len(first) {
		t.Errorf("after cooldown want %d proposals, got %v", len(first), third)
	}
}

// TestFromFirstOwnedSkipsShowsNotInLibrary: with no episode of a show at all
// there is no floor, so nothing of it is proposed. Otherwise the option would
// backfill entire shows you have never watched.
func TestFromFirstOwnedSkipsShowsNotInLibrary(t *testing.T) {
	m := newMockTVDB(t, multiSeasonShow())
	srv := &stubServer{items: []mediaserver.Item{
		{Type: "episode", Show: "Other Show", Season: 1, Episode: 1, Section: "TV Shows"},
	}}
	p, _ := openWithServer(t, m, srv, map[string]any{
		"seasons": "from_first_owned", "pack_threshold": 1.0,
	})
	if got := titles(run(t, p, showEntry("My Show", "my show"))); len(got) != 0 {
		t.Errorf("a show absent from the library must be skipped, got %v", got)
	}
}

// TestUnreachableServerProposesNothing: an empty index is indistinguishable
// from an empty library, and acting on one would re-download everything.
func TestUnreachableServerProposesNothing(t *testing.T) {
	m := newMockTVDB(t, myShow())
	srv := &stubServer{err: errors.New("connection refused")}
	p, _ := openWithServer(t, m, srv, map[string]any{"pack_threshold": 1.0})
	if got := titles(run(t, p, showEntry("My Show", "my show"))); len(got) != 0 {
		t.Errorf("unreachable server should propose nothing, got %v", got)
	}
}

// TestLibraryIndexReusedWithinTTL: one index per ttl, not one per show.
func TestLibraryIndexReusedWithinTTL(t *testing.T) {
	m := newMockTVDB(t, myShow())
	srv := &stubServer{items: []mediaserver.Item{owns(1, 1)}}
	p, _ := openWithServer(t, m, srv, map[string]any{
		"pack_threshold": 1.0, "library_ttl": "15m",
	})
	run(t, p, showEntry("My Show", "my show"))
	run(t, p, showEntry("My Show", "my show"))
	if srv.calls != 1 {
		t.Errorf("ListItems called %d times, want 1 within the ttl", srv.calls)
	}
}

// TestSectionsFilterLibrary: an episode in an excluded library is not "owned".
func TestSectionsFilterLibrary(t *testing.T) {
	m := newMockTVDB(t, myShow())
	item := owns(1, 1)
	item.Section = "Kids TV"
	srv := &stubServer{items: []mediaserver.Item{item}}
	p, _ := openWithServer(t, m, srv, map[string]any{
		"pack_threshold": 1.0, "sections": []any{"TV Shows"},
	})
	got := titles(run(t, p, showEntry("My Show", "my show")))
	saw := false
	for _, g := range got {
		if g == "My Show S01E01" {
			saw = true
		}
	}
	if !saw {
		t.Errorf("an episode outside the configured sections must not count as owned, got %v", got)
	}
}

func TestValidateSeasonsNeedsBackend(t *testing.T) {
	errs := validate(map[string]any{"api_key": "k", "seasons": "from_first_owned"})
	if len(errs) == 0 {
		t.Error("seasons=from_first_owned without a backend should not validate")
	}
	if errs := validate(map[string]any{
		"api_key": "k", "seasons": "from_first_owned", "backend": "plex",
		"url": "http://x", "token": "t",
	}); len(errs) != 0 {
		t.Errorf("valid config rejected: %v", errs)
	}
	if errs := validate(map[string]any{"api_key": "k", "seasons": "sideways"}); len(errs) == 0 {
		t.Error("an unknown seasons value should not validate")
	}
	if errs := validate(map[string]any{"api_key": "k", "backend": "kodi"}); len(errs) == 0 {
		t.Error("an unknown backend should not validate")
	}
}
