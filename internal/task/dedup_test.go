package task

import (
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func nopLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

func ep(title, series, epID string, seeds int) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	e.Accept()
	e.Set("series_name", series)
	e.Set("series_episode_id", epID)
	if seeds > 0 {
		e.Set("torrent_seeds", seeds)
	}
	return e
}

func movie(title, movieTitle string, seeds int) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	e.Accept()
	e.Set("movie_title", movieTitle)
	if seeds > 0 {
		e.Set("torrent_seeds", seeds)
	}
	return e
}

func TestDedupKeepsOnlyBestPerEpisode(t *testing.T) {
	// 1080p/2 seeds vs 720p/50 seeds → 1080p wins (alive tier + quality)
	a := ep("Show.S01E01.1080p.WEB-DL", "Show", "S01E01", 2)
	b := ep("Show.S01E01.720p.HDTV", "Show", "S01E01", 50)
	deduplicate([]*entry.Entry{a, b}, nopLogger())
	if !a.IsAccepted() {
		t.Error("1080p/2 seeds should win over 720p/50 seeds")
	}
	if !b.IsRejected() {
		t.Error("720p/50 seeds should be rejected")
	}
}

func TestDedupAliveBeatsBarely(t *testing.T) {
	// 720p/5 seeds vs 1080p/1 seed → 720p wins (alive tier beats barely-alive)
	a := ep("Show.S01E01.720p.WEB-DL", "Show", "S01E01", 5)
	b := ep("Show.S01E01.1080p.WEB-DL", "Show", "S01E01", 1)
	deduplicate([]*entry.Entry{b, a}, nopLogger())
	if !a.IsAccepted() {
		t.Error("720p/5 seeds should win over 1080p/1 seed")
	}
	if !b.IsRejected() {
		t.Error("1080p/1 seed should be rejected")
	}
}

func TestDedupSameQualityMoreSeedsWins(t *testing.T) {
	// 1080p/10 seeds vs 1080p/2 seeds → more seeds wins
	a := ep("Show.S01E01.1080p.WEB-DL.NTb", "Show", "S01E01", 10)
	b := ep("Show.S01E01.1080p.WEB-DL.FGT", "Show", "S01E01", 2)
	deduplicate([]*entry.Entry{b, a}, nopLogger())
	if !a.IsAccepted() {
		t.Error("1080p/10 seeds should win over 1080p/2 seeds")
	}
	if !b.IsRejected() {
		t.Error("1080p/2 seeds should be rejected")
	}
}

func TestDedupBothBarelyAliveQualityWins(t *testing.T) {
	// 1080p/1 seed vs 720p/1 seed → 1080p wins (same tier, quality decides)
	a := ep("Show.S01E01.1080p.WEB-DL", "Show", "S01E01", 1)
	b := ep("Show.S01E01.720p.HDTV", "Show", "S01E01", 1)
	deduplicate([]*entry.Entry{b, a}, nopLogger())
	if !a.IsAccepted() {
		t.Error("1080p/1 seed should win over 720p/1 seed")
	}
	if !b.IsRejected() {
		t.Error("720p/1 seed should be rejected")
	}
}

func TestDedupDifferentEpisodesUnaffected(t *testing.T) {
	a := ep("Show.S01E01.720p", "Show", "S01E01", 5)
	b := ep("Show.S01E02.720p", "Show", "S01E02", 5)
	deduplicate([]*entry.Entry{a, b}, nopLogger())
	if !a.IsAccepted() || !b.IsAccepted() {
		t.Error("different episodes should both remain accepted")
	}
}

func TestDedupMovieDuplicates(t *testing.T) {
	// 1080p/2 seeds vs 720p/20 seeds → 1080p wins
	a := movie("Dune.2021.1080p.WEB-DL", "Dune", 2)
	b := movie("Dune.2021.720p.BluRay", "Dune", 20)
	deduplicate([]*entry.Entry{a, b}, nopLogger())
	if !a.IsAccepted() {
		t.Error("1080p movie should win over 720p")
	}
	if !b.IsRejected() {
		t.Error("720p movie duplicate should be rejected")
	}
}

func TestDedupMovieAliveBeatsBarely(t *testing.T) {
	// 720p/5 seeds vs 1080p/1 seed → 720p wins
	a := movie("Dune.2021.720p.WEB-DL", "Dune", 5)
	b := movie("Dune.2021.1080p.WEB-DL", "Dune", 1)
	deduplicate([]*entry.Entry{b, a}, nopLogger())
	if !a.IsAccepted() {
		t.Error("720p/5 seeds should beat 1080p/1 seed for movie")
	}
}

func TestDedupNoSeriesOrMovieFieldUnaffected(t *testing.T) {
	e := entry.New("Some.Random.File.mkv", "http://example.com/file")
	e.Accept()
	deduplicate([]*entry.Entry{e}, nopLogger())
	if !e.IsAccepted() {
		t.Error("entry without series or movie fields should not be affected")
	}
}

// --- End-to-end: all three plugins accept multiple entries; dedup picks best ---

func TestDedupEndToEndEpisodes(t *testing.T) {
	// Three qualities of the same episode all accepted; dedup keeps 2160p/2seeds
	// over 1080p/5seeds (alive tier + resolution wins) and 720p/1seed (barely alive).
	entries := []*entry.Entry{
		ep("Show.S01E01.720p.HDTV", "Show", "S01E01", 1),    // barely alive, low quality
		ep("Show.S01E01.1080p.WEB-DL", "Show", "S01E01", 5), // alive, medium quality
		ep("Show.S01E01.2160p.BluRay", "Show", "S01E01", 2), // alive, high quality
	}
	deduplicate(entries, nopLogger())

	if !entries[2].IsAccepted() {
		t.Errorf("2160p/2seeds should win; got rejected: %s", entries[2].RejectReason)
	}
	if !entries[0].IsRejected() {
		t.Error("720p/1seed (barely alive) should be rejected")
	}
	if !entries[1].IsRejected() {
		t.Error("1080p/5seeds should lose to 2160p/2seeds (same alive tier, lower resolution)")
	}
}

func TestDedupEndToEndMovies(t *testing.T) {
	// Two qualities of the same movie; 720p/5seeds beats 1080p/1seed (alive tier).
	entries := []*entry.Entry{
		movie("Dune.2021.1080p.WEB-DL", "Dune", 1),
		movie("Dune.2021.720p.HDTV", "Dune", 5),
	}
	deduplicate(entries, nopLogger())

	if !entries[1].IsAccepted() {
		t.Errorf("720p/5seeds should beat 1080p/1seed; got: %s", entries[1].RejectReason)
	}
	if !entries[0].IsRejected() {
		t.Error("1080p/1seed (barely alive) should be rejected")
	}
}

func TestDedupEndToEndMixed(t *testing.T) {
	// Episode and movie in same batch — each deduped independently, unrelated entries untouched.
	ep1 := ep("Show.S01E01.720p.WEB-DL", "Show", "S01E01", 3)
	ep2 := ep("Show.S01E01.1080p.WEB-DL", "Show", "S01E01", 3)
	mov1 := movie("Dune.2021.720p.HDTV", "Dune", 5)
	mov2 := movie("Dune.2021.1080p.WEB-DL", "Dune", 2)
	other := entry.New("Some.Random.File.mkv", "http://x.com/file")
	other.Accept()

	deduplicate([]*entry.Entry{ep1, ep2, mov1, mov2, other}, nopLogger())

	if !ep2.IsAccepted() {
		t.Error("1080p episode should win over 720p (same alive tier, higher resolution)")
	}
	if !ep1.IsRejected() {
		t.Error("720p episode should be deduped away")
	}
	if !mov2.IsAccepted() {
		t.Error("1080p/2seeds movie should beat 720p/5seeds (same alive tier, resolution wins)")
	}
	if !mov1.IsRejected() {
		t.Error("720p/5seeds movie should be deduped away in favour of 1080p/2seeds")
	}
	if !other.IsAccepted() {
		t.Error("entry without series/movie fields should be untouched")
	}
}

func TestDedupRejectedEntriesIgnored(t *testing.T) {
	a := ep("Show.S01E01.1080p", "Show", "S01E01", 5)
	b := ep("Show.S01E01.720p", "Show", "S01E01", 5)
	b.Reject("already rejected")
	deduplicate([]*entry.Entry{a, b}, nopLogger())
	if !a.IsAccepted() {
		t.Error("accepted entry should remain accepted")
	}
	if !b.IsRejected() {
		t.Error("pre-rejected entry should stay rejected")
	}
}
