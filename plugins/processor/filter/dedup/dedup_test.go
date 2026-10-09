package dedup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/quality"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func accepted(title string) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	e.Accept()
	return e
}

// TestDedupCaseInsensitiveSeriesName is the regression test for the "FROM" vs
// "From" bug: entries for the same show parsed with different letter casing
// must collapse to a single dedup group.
func TestDedupCaseInsensitiveSeriesName(t *testing.T) {
	p := &dedupPlugin{}
	entries := []*entry.Entry{
		accepted("FROM S04E05 What A Long Strange Trip 2160p AMZN WEB-DL H.265-Kitsune"),
		accepted("From S04E05 1080p WEB h264-GRACE"),
		accepted("FROM S04E05 720p HEVC x265-MeGusta"),
	}
	for _, e := range entries {
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.Set(entry.FieldSeriesEpisodeID, "S04E05")
	}

	out, err := p.Process(context.Background(), tc(), entries)
	if err != nil {
		t.Fatal(err)
	}

	accepted := 0
	for _, e := range out {
		if e.IsAccepted() {
			accepted++
		}
	}
	if accepted != 1 {
		t.Errorf("want exactly 1 accepted entry (best quality), got %d", accepted)
	}
}

// TestDedupSeriesYearSpellings: releases name a show with its year before the
// episode identifier, after it, or not at all; all are copies of one episode.
func TestDedupSeriesYearSpellings(t *testing.T) {
	p := &dedupPlugin{}
	entries := []*entry.Entry{
		accepted("Brothers 2026 S01E01 On the Road 2160p ATVP WEB-DL DDP5 1 Atmos DV HDR H 265-RAWR"),
		accepted("Brothers S01E01 2026 1080p ATVP WEB-DL H 264 DDP5 1 Atmos-HHWEB"),
		accepted("Brothers S01E01 720p WEB H264-JFF"),
	}
	for _, e := range entries {
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.Set(entry.FieldSeriesEpisodeID, "S01E01")
	}

	out, err := p.Process(context.Background(), tc(), entries)
	if err != nil {
		t.Fatal(err)
	}

	var kept []string
	for _, e := range out {
		if e.IsAccepted() {
			kept = append(kept, e.Title)
		}
	}
	if len(kept) != 1 || !strings.Contains(kept[0], "RAWR") {
		t.Errorf("want only the 2160p copy kept, got %q", kept)
	}
}

func TestDedupKeepsBestResolution(t *testing.T) {
	p := &dedupPlugin{}
	entries := []*entry.Entry{
		accepted("Show S01E01 720p WEB-DL"),
		accepted("Show S01E01 1080p WEB-DL"),
		accepted("Show S01E01 480p WEB-DL"),
	}
	for _, e := range entries {
		e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
		e.Set(entry.FieldSeriesEpisodeID, "S01E01")
	}

	out, _ := p.Process(context.Background(), tc(), entries)
	var winner *entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winner = e
		}
	}
	if winner == nil {
		t.Fatal("no accepted entry")
	}
	if winner.Title != "Show S01E01 1080p WEB-DL" {
		t.Errorf("want 1080p winner, got %q", winner.Title)
	}
}

// TestDedupMoviesByMediaTypeAndTitle exercises the post-deprecation path:
// dedup must group movies by media_type + title rather than the deprecated
// movie_title field. Two copies of the same movie at different qualities
// should collapse to one accepted entry.
func TestDedupMoviesByMediaTypeAndTitle(t *testing.T) {
	p := &dedupPlugin{}
	low := accepted("Superman.2025.1080p.WEB-DL")
	high := accepted("Superman.2025.2160p.UHD.BluRay")
	for _, e := range []*entry.Entry{low, high} {
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.Set(entry.FieldTitle, "Superman")
	}

	out, err := p.Process(context.Background(), tc(), []*entry.Entry{low, high})
	if err != nil {
		t.Fatal(err)
	}

	var winners []*entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winners = append(winners, e)
		}
	}
	if len(winners) != 1 {
		t.Fatalf("want 1 winner, got %d", len(winners))
	}
	if winners[0] != high {
		t.Errorf("want 2160p winner, got %q", winners[0].Title)
	}
}

// TestDedupSkipsSeriesWithoutMediaType pins the parallel behavior for series:
// an entry with series_episode_id but no media_type is no longer treated as
// a series for dedup purposes. Pipelines that want episode dedup must run
// metainfo_file (or another classifier) upstream.
func TestDedupSkipsSeriesWithoutMediaType(t *testing.T) {
	p := &dedupPlugin{}
	a := accepted("Show S01E01 720p WEB-DL")
	b := accepted("Show S01E01 1080p WEB-DL")
	for _, e := range []*entry.Entry{a, b} {
		// Only series_episode_id set; no media_type.
		e.Set(entry.FieldSeriesEpisodeID, "S01E01")
	}

	out, _ := p.Process(context.Background(), tc(), []*entry.Entry{a, b})
	var winners []*entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winners = append(winners, e)
		}
	}
	if len(winners) != 2 {
		t.Errorf("entries without media_type must pass through unduplicated, got %d", len(winners))
	}
}

// TestDedupSkipsMovieWithoutMediaType verifies that an entry carrying only the
// legacy movie_title (no media_type) is NOT deduped by movie title under the
// new logic. This documents the intentional behavior change — pipelines that
// want movie dedup must run metainfo_file or metainfo_tmdb upstream.
func TestDedupSkipsMovieWithoutMediaType(t *testing.T) {
	p := &dedupPlugin{}
	a := accepted("Superman.2025.1080p.WEB-DL")
	b := accepted("Superman.2025.2160p.UHD.BluRay")
	for _, e := range []*entry.Entry{a, b} {
		// Only movie_title set; no media_type or title field.
		e.Set(entry.FieldMovieTitle, "Superman")
	}

	out, _ := p.Process(context.Background(), tc(), []*entry.Entry{a, b})
	var winners []*entry.Entry
	for _, e := range out {
		if e.IsAccepted() {
			winners = append(winners, e)
		}
	}
	if len(winners) != 2 {
		t.Errorf("entries without media_type must pass through unduplicated, got %d", len(winners))
	}
}

func TestDedupPassesThroughEntriesWithNoKey(t *testing.T) {
	p := &dedupPlugin{}
	e := accepted("Some article with no media key")
	out, _ := p.Process(context.Background(), tc(), []*entry.Entry{e})
	if len(out) != 1 || !out[0].IsAccepted() {
		t.Error("entry without dedup key should pass through")
	}
}

// TestDedupRequiresErrorsWhenMediaTypeUnreachable verifies that placing dedup
// in a pipeline with no upstream producing media_type yields a validator
// error. This is the "you probably forgot metainfo_file/tmdb upstream" signal.
func TestDedupRequiresErrorsWhenMediaTypeUnreachable(t *testing.T) {
	desc, ok := plugin.Lookup("dedup")
	if !ok {
		t.Fatal("dedup plugin not registered")
	}
	src := &plugin.Descriptor{
		PluginName: "src", Role: plugin.RoleSource,
		// title is reachable, but media_type is not.
		Produces: []string{entry.FieldTitle, entry.FieldSource},
	}
	g := dag.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.AddNode(&dag.Node{ID: "a", PluginName: "src"}))
	must(g.AddNode(&dag.Node{ID: "b", PluginName: "dedup", Upstreams: []dag.NodeID{"a"}}))

	reg := func(name string) (*plugin.Descriptor, bool) {
		switch name {
		case "src":
			return src, true
		case "dedup":
			return desc, true
		}
		return nil, false
	}
	errs, _ := dag.Validate(g, reg)
	found := false
	for _, e := range errs {
		if strings.Contains(e.Error(), "media_type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected an error mentioning media_type, got: %v", errs)
	}
}

// TestDedupRequiresWarnsWhenMediaTypeOnlyReachable verifies that when an
// upstream MayProduces media_type (as metainfo_file does), dedup gets a
// warning rather than an error — accurate: dedup will silently skip entries
// that weren't classified.
func TestDedupRequiresWarnsWhenMediaTypeOnlyReachable(t *testing.T) {
	desc, ok := plugin.Lookup("dedup")
	if !ok {
		t.Fatal("dedup plugin not registered")
	}
	src := &plugin.Descriptor{
		PluginName: "src", Role: plugin.RoleSource,
		Produces:   []string{entry.FieldTitle, entry.FieldSource},
		MayProduce: []string{entry.FieldMediaType, entry.FieldSeriesEpisodeID},
	}
	g := dag.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(g.AddNode(&dag.Node{ID: "a", PluginName: "src"}))
	must(g.AddNode(&dag.Node{ID: "b", PluginName: "dedup", Upstreams: []dag.NodeID{"a"}}))

	reg := func(name string) (*plugin.Descriptor, bool) {
		switch name {
		case "src":
			return src, true
		case "dedup":
			return desc, true
		}
		return nil, false
	}
	errs, warnings := dag.Validate(g, reg)
	if len(errs) > 0 {
		t.Fatalf("expected no errors, got: %v", errs)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Error(), "media_type") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a warning mentioning media_type, got: %v", warnings)
	}
}

// TestDedupNoMediaTypeWarningWhenClassifierFilterUpstream verifies the payoff
// of having series/movies filters Produce media_type: when dedup follows one
// of them, media_type is Certain (not merely MayProduced from metainfo_file)
// so the "may not be present on all entries" warning disappears.
func TestDedupNoMediaTypeWarningWhenClassifierFilterUpstream(t *testing.T) {
	dedupDesc, ok := plugin.Lookup("dedup")
	if !ok {
		t.Fatal("dedup plugin not registered")
	}
	// Stand-in for a classifier filter: Produces media_type + the key data
	// (title) as Certain — same shape series/movies advertise.
	classifier := &plugin.Descriptor{
		PluginName: "classifier", Role: plugin.RoleProcessor,
		Produces: []string{entry.FieldMediaType, entry.FieldTitle},
	}
	g := dag.New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	src := &plugin.Descriptor{
		PluginName: "src", Role: plugin.RoleSource,
		Produces: []string{entry.FieldSource},
	}
	must(g.AddNode(&dag.Node{ID: "a", PluginName: "src"}))
	must(g.AddNode(&dag.Node{ID: "b", PluginName: "classifier", Upstreams: []dag.NodeID{"a"}}))
	must(g.AddNode(&dag.Node{ID: "c", PluginName: "dedup", Upstreams: []dag.NodeID{"b"}}))

	reg := func(name string) (*plugin.Descriptor, bool) {
		switch name {
		case "src":
			return src, true
		case "classifier":
			return classifier, true
		case "dedup":
			return dedupDesc, true
		}
		return nil, false
	}
	errs, warnings := dag.Validate(g, reg)
	if len(errs) > 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, w := range warnings {
		if strings.Contains(w.Error(), "media_type") {
			t.Fatalf("did not expect a media_type warning, got: %v", w)
		}
	}
}

// --- identity: a shared name is not a shared item ---

func movieEntry(title string, year int, url, res string) *entry.Entry {
	e := entry.New(title+"."+fmt.Sprint(year)+"."+res, url)
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	e.Set(entry.FieldTitle, title)
	e.Set(entry.FieldVideoYear, year)
	e.Set(entry.FieldTorrentSeeds, 10)
	return e
}

func episodeEntry(title, epID, url string) *entry.Entry {
	e := entry.New(title, url)
	e.Set(entry.FieldMediaType, entry.MediaTypeSeries)
	e.Set(entry.FieldSeriesEpisodeID, epID)
	e.Set(entry.FieldTorrentSeeds, 10)
	return e
}

func dedupRun(t *testing.T, entries ...*entry.Entry) []*entry.Entry {
	t.Helper()
	p := &dedupPlugin{}
	out, err := p.Process(context.Background(), tc(), entries)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return out
}

// TestTwoFilmsSharingATitleAreNotCopies is the defect: the title was the whole
// movie key, so the 1984 Dune was rejected as "a better copy" of the 2021 one
// and the request for it came back with the wrong film.
func TestTwoFilmsSharingATitleAreNotCopies(t *testing.T) {
	old := movieEntry("Dune", 1984, "http://x/1", "720p")
	new2021 := movieEntry("Dune", 2021, "http://x/2", "2160p")

	out := dedupRun(t, old, new2021)
	if len(out) != 2 {
		t.Fatalf("kept %d entries, want both films", len(out))
	}
	if old.IsRejected() || new2021.IsRejected() {
		t.Errorf("neither film is a copy of the other: 1984 rejected=%v (%s), 2021 rejected=%v (%s)",
			old.IsRejected(), old.RejectReason, new2021.IsRejected(), new2021.RejectReason)
	}
}

// TestFilmsSeparatedByTheirIDs: the year is not always there to separate them,
// but by the time dedup runs a metainfo plugin has usually resolved an id.
func TestFilmsSeparatedByTheirIDs(t *testing.T) {
	a := movieEntry("Michael", 0, "http://x/1", "1080p")
	a.Set("tmdb_id", 24913)
	b := movieEntry("Michael", 0, "http://x/2", "2160p")
	b.Set("tmdb_id", 1156593)

	out := dedupRun(t, a, b)
	if len(out) != 2 || a.IsRejected() || b.IsRejected() {
		t.Errorf("two tmdb ids are two films: kept %d, a rejected=%v, b rejected=%v",
			len(out), a.IsRejected(), b.IsRejected())
	}
}

// TestSameFilmStillDedups pins the behaviour that must not regress: two
// releases of one film collapse to the better one.
func TestSameFilmStillDedups(t *testing.T) {
	worse := movieEntry("Dune", 2021, "http://x/1", "1080p")
	better := movieEntry("Dune", 2021, "http://x/2", "2160p")

	out := dedupRun(t, worse, better)
	if len(out) != 1 || out[0] != better {
		t.Fatalf("want only the 2160p copy, got %d entries", len(out))
	}
	if !worse.IsRejected() {
		t.Error("the 1080p copy should be rejected")
	}
}

// TestSameFilmWithAnOffByOneYear: release years disagree by one across
// regional windows, which is why the comparison tolerates ±1 — the same
// tolerance match.YearsCompatible applies everywhere else.
func TestSameFilmWithAnOffByOneYear(t *testing.T) {
	a := movieEntry("Mother", 2017, "http://x/1", "1080p")
	b := movieEntry("Mother", 2018, "http://x/2", "2160p")

	out := dedupRun(t, a, b)
	if len(out) != 1 {
		t.Errorf("an off-by-one year is the same film: kept %d entries", len(out))
	}
}

// TestOneCopyWithAnIDAndOneWithout still dedups. Most releases publish no id
// at all, and a metainfo plugin leaves an entry unenriched rather than guess —
// so within one run a copy with an id and a copy without are routine, and
// splitting on absence would stop dedup working for exactly those.
func TestOneCopyWithAnIDAndOneWithout(t *testing.T) {
	withID := movieEntry("Dune", 2021, "http://x/1", "1080p")
	withID.Set("tmdb_id", 438631)
	without := movieEntry("Dune", 2021, "http://x/2", "2160p")

	out := dedupRun(t, withID, without)
	if len(out) != 1 || out[0] != without {
		t.Fatalf("want one entry (the 2160p copy), got %d", len(out))
	}
}

// TestIDsFromDifferentNamespacesDoNotSplit: one copy identified by TMDB and
// another by the indexer's IMDb id say nothing about each other.
func TestIDsFromDifferentNamespacesDoNotSplit(t *testing.T) {
	a := movieEntry("Inception", 2010, "http://x/1", "1080p")
	a.Set("tmdb_id", 27205)
	b := movieEntry("Inception", 2010, "http://x/2", "2160p")
	b.Set("jackett_imdb_id", "tt1375666")

	out := dedupRun(t, a, b)
	if len(out) != 1 {
		t.Errorf("unrelated namespaces must not split a film: kept %d entries", len(out))
	}
}

// TestTwoShowsSharingABaseNameAreNotCopies: the series name is keyed with the
// year stripped on purpose, so the id is the only thing left that can tell two
// same-named shows apart.
func TestTwoShowsSharingABaseNameAreNotCopies(t *testing.T) {
	a := episodeEntry("Brothers.2026.S01E01.1080p.WEB", "S01E01", "http://x/1")
	a.Set("tvdb_id", "448114")
	b := episodeEntry("Brothers.S01E01.2160p.WEB", "S01E01", "http://x/2")
	b.Set("tvdb_id", "500001")

	out := dedupRun(t, a, b)
	if len(out) != 2 || a.IsRejected() || b.IsRejected() {
		t.Errorf("two tvdb ids are two shows: kept %d, a rejected=%v, b rejected=%v",
			len(out), a.IsRejected(), b.IsRejected())
	}
}

// TestOneShowSpelledTwoWaysStillDedups is the case the year-stripped name key
// exists for, and it must survive the id check: same id, two spellings, one
// episode.
func TestOneShowSpelledTwoWaysStillDedups(t *testing.T) {
	a := episodeEntry("Brothers.2026.S01E01.1080p.WEB", "S01E01", "http://x/1")
	a.Set("tvdb_id", "448114")
	b := episodeEntry("Brothers.S01E01.2160p.WEB", "S01E01", "http://x/2")
	b.Set("tvdb_id", "448114")

	out := dedupRun(t, a, b)
	if len(out) != 1 {
		t.Fatalf("one show, two spellings: kept %d entries, want 1", len(out))
	}
	if !a.IsRejected() {
		t.Error("the 1080p copy should be rejected")
	}
}

// TestRejectionReasonNamesTheGroup keeps the log line usable: it should say
// which item the better copy was for.
func TestRejectionReasonNamesTheGroup(t *testing.T) {
	worse := movieEntry("Dune", 2021, "http://x/1", "1080p")
	better := movieEntry("Dune", 2021, "http://x/2", "2160p")
	dedupRun(t, worse, better)
	if !strings.Contains(worse.RejectReason, "movie:dune") {
		t.Errorf("reason = %q, want it to name movie:dune", worse.RejectReason)
	}
}

// --- the whole quality ladder, not resolution alone ---

// qualityEntry builds a movie entry whose typed quality is parsed from its
// release name, the way metainfo_file sets it upstream.
func qualityEntry(release string, seeds int, url string) *entry.Entry {
	e := entry.New(release, url)
	e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
	e.Set(entry.FieldTitle, "Supergirl")
	e.Set(entry.FieldVideoYear, 2026)
	e.Set(entry.FieldTorrentSeeds, seeds)
	e.SetQuality(quality.Parse(release))
	return e
}

// TestRemuxBeatsDiscImageAtEqualResolution is the live case: both releases are
// 2160p with 6 seeds, so resolution and seeds both tie and the 90 GB disc
// image won on indexer order. The ladder already ranks Remux above BluRay.
func TestRemuxBeatsDiscImageAtEqualResolution(t *testing.T) {
	iso := qualityEntry("Supergirl 2026 Complete 4K UHD Blu Ray ISO File [RoB]", 6, "http://x/iso")
	remux := qualityEntry("Supergirl 2026 2160p UHD BluRay REMUX DV HDR TrueHD 7 1 Atmos Multi-d3g", 6, "http://x/remux")

	// The disc image first, as the indexer listed it.
	out := dedupRun(t, iso, remux)
	if len(out) != 1 {
		t.Fatalf("want 1 entry, got %d", len(out))
	}
	if out[0] != remux {
		t.Errorf("kept %q, want the remux", out[0].Title)
	}
	if !iso.IsRejected() {
		t.Error("the disc image should be rejected")
	}
}

// TestLadderDimensionsAllCount: every rung the comparator knows about now
// decides a dedup that resolution alone could not.
func TestLadderDimensionsAllCount(t *testing.T) {
	for _, tc := range []struct{ name, worse, better string }{
		{"source: web-dl under bluray",
			"Film 2026 1080p WEB-DL H 264", "Film 2026 1080p BluRay H 264"},
		{"source: bluray under remux",
			"Film 2026 1080p BluRay AVC", "Film 2026 1080p BluRay REMUX AVC"},
		{"color range: sdr under dolby vision",
			"Film 2026 2160p BluRay REMUX HDR", "Film 2026 2160p BluRay REMUX DV"},
		{"audio: dd under atmos",
			"Film 2026 2160p BluRay REMUX DV DD 5 1", "Film 2026 2160p BluRay REMUX DV TrueHD Atmos"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := qualityEntry(tc.worse, 10, "http://x/worse")
			b := qualityEntry(tc.better, 10, "http://x/better")
			out := dedupRun(t, w, b)
			if len(out) != 1 || out[0] != b {
				t.Errorf("kept %q, want %q", out[0].Title, tc.better)
			}
		})
	}
}

// TestResolutionStillOutranksEverything: the ladder is ordered, so a better
// source at a lower resolution does not win.
func TestResolutionStillOutranksEverything(t *testing.T) {
	remux1080 := qualityEntry("Film 2026 1080p BluRay REMUX AVC Atmos", 10, "http://x/1080")
	web2160 := qualityEntry("Film 2026 2160p WEB-DL H 265", 10, "http://x/2160")
	out := dedupRun(t, remux1080, web2160)
	if len(out) != 1 || out[0] != web2160 {
		t.Errorf("kept %q, want the 2160p release", out[0].Title)
	}
}

// TestSeedTierStillComesFirst: the best copy you cannot get is not the best
// copy, so a single-seeder release loses to a healthy one whatever its tags.
func TestSeedTierStillComesFirst(t *testing.T) {
	lonely := qualityEntry("Film 2026 2160p BluRay REMUX DV TrueHD Atmos", 1, "http://x/lonely")
	healthy := qualityEntry("Film 2026 1080p WEB-DL H 264", 25, "http://x/healthy")
	out := dedupRun(t, lonely, healthy)
	if len(out) != 1 || out[0] != healthy {
		t.Errorf("kept %q, want the well-seeded release", out[0].Title)
	}
}

// TestSeedsBreakAQualityTie pins the last rung: identical quality, more seeds.
func TestSeedsBreakAQualityTie(t *testing.T) {
	few := qualityEntry("Film 2026 1080p BluRay x264-AAA", 4, "http://x/few")
	many := qualityEntry("Film 2026 1080p BluRay x264-BBB", 40, "http://x/many")
	out := dedupRun(t, few, many)
	if len(out) != 1 || out[0] != many {
		t.Errorf("kept %q, want the better-seeded copy", out[0].Title)
	}
}

// TestFallsBackToParsingTheTitle: dedup may sit in a pipeline where nothing
// set the typed field, and it still has to rank.
func TestFallsBackToParsingTheTitle(t *testing.T) {
	worse := entry.New("Film 2026 1080p WEB-DL H 264", "http://x/worse")
	better := entry.New("Film 2026 1080p BluRay REMUX AVC", "http://x/better")
	for _, e := range []*entry.Entry{worse, better} {
		e.Set(entry.FieldMediaType, entry.MediaTypeMovie)
		e.Set(entry.FieldTitle, "Film")
		e.Set(entry.FieldVideoYear, 2026)
		e.Set(entry.FieldTorrentSeeds, 10)
	}
	out := dedupRun(t, worse, better)
	if len(out) != 1 || out[0] != better {
		t.Errorf("kept %q, want the remux", out[0].Title)
	}
}
