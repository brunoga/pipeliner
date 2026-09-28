package settle

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/quality"
)

type memBucket struct{ data map[string][]byte }

func newMemBucket() *memBucket { return &memBucket{data: map[string][]byte{}} }

func (b *memBucket) Put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.data[key] = raw
	return nil
}
func (b *memBucket) Get(key string, dest any) (bool, error) {
	raw, ok := b.data[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dest)
}
func (b *memBucket) Delete(key string) error { delete(b.data, key); return nil }
func (b *memBucket) Keys() ([]string, error) {
	out := make([]string, 0, len(b.data))
	for k := range b.data {
		out = append(out, k)
	}
	return out, nil
}

func cand(url, title, q string) Candidate {
	return Candidate{URL: url, Title: title, Quality: quality.Parse(q)}
}

// The tracker keeps the best candidate of a wave regardless of arrival order,
// so the winner is known without the releases still being advertised.
func TestOfferKeepsBestRegardlessOfOrder(t *testing.T) {
	for _, order := range [][]Candidate{
		{cand("u1", "a", "1080p WEB-DL"), cand("u2", "b", "2160p WEB-DL"), cand("u3", "c", "2160p BluRay TrueHD Atmos")},
		{cand("u3", "c", "2160p BluRay TrueHD Atmos"), cand("u2", "b", "2160p WEB-DL"), cand("u1", "a", "1080p WEB-DL")},
		{cand("u2", "b", "2160p WEB-DL"), cand("u3", "c", "2160p BluRay TrueHD Atmos"), cand("u1", "a", "1080p WEB-DL")},
	} {
		tr := New(newMemBucket())
		now := time.Now()
		for _, c := range order {
			tr.Offer("t|k", c, 12*time.Hour, now)
		}
		best, ok := tr.Best("t|k")
		if !ok || best.URL != "u3" {
			t.Errorf("best = %q, want u3 (the Atmos release)", best.URL)
		}
	}
}

func TestOfferWindowCountdownAndExpiry(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	w := 12 * time.Hour

	if left := tr.Offer("t|k", cand("u1", "a", "1080p"), w, now); left != w {
		t.Errorf("first offer: got %v, want %v", left, w)
	}
	if left := tr.Offer("t|k", cand("u2", "b", "2160p"), w, now.Add(4*time.Hour)); left != 8*time.Hour {
		t.Errorf("mid-window: got %v, want 8h", left)
	}
	if left := tr.Offer("t|k", cand("u3", "c", "2160p BluRay"), w, now.Add(13*time.Hour)); left > 0 {
		t.Errorf("after window: got %v, want <= 0", left)
	}
}

// Expired surfaces winners whose window elapsed — this is what lets a caller
// download a release the feed has already forgotten.
func TestExpiredListsWinners(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	w := 6 * time.Hour
	tr.Offer("t|old", cand("u-old", "old", "2160p BluRay"), w, now.Add(-7*time.Hour))
	tr.Offer("t|fresh", cand("u-fresh", "fresh", "2160p BluRay"), w, now.Add(-1*time.Hour))

	exp := tr.Expired("t", w, now)
	if len(exp) != 1 || exp[0].Key != "t|old" || exp[0].Release.URL != "u-old" {
		t.Fatalf("expired = %+v, want only the elapsed window", exp)
	}
	tr.Clear("t|old")
	if len(tr.Expired("t", w, now)) != 0 {
		t.Error("cleared window must not reappear")
	}
}

func TestDisabledAndNilSafe(t *testing.T) {
	tr := New(newMemBucket())
	if left := tr.Offer("t|k", cand("u", "a", "1080p"), 0, time.Now()); left != 0 {
		t.Errorf("zero window disables settling, got %v", left)
	}
	var nilTracker *Tracker
	if left := nilTracker.Offer("t|k", cand("u", "a", "1080p"), time.Hour, time.Now()); left != 0 {
		t.Errorf("nil tracker must be safe, got %v", left)
	}
	if _, ok := nilTracker.Best("t|k"); ok {
		t.Error("nil tracker has no best")
	}
	nilTracker.Clear("t|k")
	if got := nilTracker.Expired("t", time.Hour, time.Now()); got != nil {
		t.Error("nil tracker has nothing expired")
	}
}

func TestMovieKeyMatchesTrackerShape(t *testing.T) {
	if got := MovieKey("movies", "The Matrix", 1999, false); got != "movies|the matrix|1999" {
		t.Errorf("got %q", got)
	}
	if got := MovieKey("movies", "The Matrix", 1999, true); got != "movies|the matrix|1999|3d" {
		t.Errorf("got %q", got)
	}
	if got := SeriesKey("tv", "Breaking Bad", "s01e01"); got != "tv|breaking bad|S01E01" {
		t.Errorf("got %q", got)
	}
}

// Expired must only ever return the calling task's own records: a revived
// winner is injected straight into that pipeline's filter, skipping every
// upstream node, so another pipeline's pending release would bypass gates it
// never passed.
func TestExpiredIsScopedToTask(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	w := 6 * time.Hour
	tr.Offer(MovieKey("movies", "Irresistible", 2020, false),
		cand("u-2d", "Irresistible 2160p WEB-DL", "2160p WEB-DL"), w, now.Add(-7*time.Hour))

	if got := tr.Expired("movies-3d", w, now); len(got) != 0 {
		t.Errorf("another pipeline's record leaked: %+v", got)
	}
	got := tr.Expired("movies", w, now)
	if len(got) != 1 || got[0].Release.URL != "u-2d" {
		t.Errorf("the owning task must see its own record, got %+v", got)
	}
	// An empty task name must never match everything.
	if got := tr.Expired("", w, now); len(got) != 0 {
		t.Errorf("empty task must match nothing, got %+v", got)
	}
}

// ── the window keeps the whole wave ──────────────────────────────────────────

// Every distinct release of a wave is kept, so the caller can release them all
// and let the pipeline's gates and dedup decide. The old design kept a running
// maximum, which meant choosing on quality tags before any node that could
// refuse a release had run.
func TestOfferKeepsEveryDistinctRelease(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	wave := []Candidate{
		cand("u1", "Film 1080p WEB-DL", "1080p WEB-DL"),
		cand("u2", "Film 2160p WEB-DL", "2160p WEB-DL"),
		cand("u3", "Film 2160p BluRay Atmos", "2160p BluRay TrueHD Atmos"),
	}
	for _, c := range wave {
		tr.Offer("t|film", c, 6*time.Hour, now)
	}
	got := tr.Candidates("t|film")
	if len(got) != 3 {
		t.Fatalf("wave holds %d releases, want 3", len(got))
	}
	// Arrival order is preserved; the pipeline, not the tracker, ranks them.
	for i, want := range []string{"u1", "u2", "u3"} {
		if got[i].URL != want {
			t.Errorf("candidate %d = %q, want %q (arrival order)", i, got[i].URL, want)
		}
	}
}

// The same release offered twice — which happens on every run while a window
// is open — must not accumulate.
func TestOfferDeduplicatesByReleaseName(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	c := cand("u1", "Film 2160p BluRay Atmos", "2160p BluRay TrueHD Atmos")
	for i := 0; i < 5; i++ {
		// Fresh URL each time, as an indexer would hand out.
		c.URL = "u1-rotated-" + string(rune('a'+i))
		tr.Offer("t|film", c, 6*time.Hour, now)
	}
	if got := tr.Candidates("t|film"); len(got) != 1 {
		t.Errorf("wave holds %d copies of one release, want 1: %v", len(got), got)
	}
}

// Expired hands back every candidate of an elapsed window, not just the best.
func TestExpiredReturnsTheWholeWave(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	for _, c := range []Candidate{
		cand("u1", "Film 1080p WEB-DL", "1080p WEB-DL"),
		cand("u2", "Film 2160p BluRay Atmos", "2160p BluRay TrueHD Atmos"),
	} {
		tr.Offer("t|film", c, 6*time.Hour, now)
	}
	exp := tr.Expired("t", 6*time.Hour, now.Add(7*time.Hour))
	if len(exp) != 2 {
		t.Fatalf("expired returned %d releases, want the whole wave (2)", len(exp))
	}
	for _, e := range exp {
		if e.Key != "t|film" {
			t.Errorf("release %q carries key %q", e.Release.URL, e.Key)
		}
	}
}

func TestCandidateListIsCapped(t *testing.T) {
	tr := New(newMemBucket())
	now := time.Now()
	for i := 0; i < maxCandidates*2; i++ {
		tr.Offer("t|film", cand(fmt.Sprintf("u%d", i), fmt.Sprintf("Film release %d", i), "1080p WEB-DL"), 6*time.Hour, now)
	}
	got := tr.Candidates("t|film")
	if len(got) != maxCandidates {
		t.Fatalf("wave grew to %d, cap is %d", len(got), maxCandidates)
	}
	// The cap drops the oldest, so the newest release is still present.
	last := fmt.Sprintf("u%d", maxCandidates*2-1)
	if got[len(got)-1].URL != last {
		t.Errorf("newest release was evicted: last kept is %q, want %q", got[len(got)-1].URL, last)
	}
}

// Records written before the window kept the whole wave carry a single "best".
// They must keep working: the winner is folded into the wave on first touch and
// the legacy field is not written again.
func TestMigratesSingleWinnerRecord(t *testing.T) {
	b := newMemBucket()
	tr := New(b)
	now := time.Now()
	legacy := cand("u-legacy", "Film 2160p BluRay Atmos", "2160p BluRay TrueHD Atmos")
	if err := b.Put("t|film", Record{FirstSeen: now.Add(-7 * time.Hour), Best: &legacy}); err != nil {
		t.Fatal(err)
	}

	// Reading it surfaces the legacy winner as a candidate.
	got := tr.Candidates("t|film")
	if len(got) != 1 || got[0].URL != "u-legacy" {
		t.Fatalf("legacy winner not migrated: %v", got)
	}
	// It is still released when the window has elapsed.
	if exp := tr.Expired("t", 6*time.Hour, now); len(exp) != 1 || exp[0].Release.URL != "u-legacy" {
		t.Fatalf("legacy winner not released: %v", exp)
	}
	// A further offer joins it rather than replacing it, and the legacy field
	// is gone from what gets written.
	tr.Offer("t|film", cand("u-new", "Film 1080p WEB-DL", "1080p WEB-DL"), 6*time.Hour, now)
	if got := tr.Candidates("t|film"); len(got) != 2 {
		t.Errorf("wave after offer holds %d, want 2 (legacy + new)", len(got))
	}
	var raw Record
	if _, err := b.Get("t|film", &raw); err != nil {
		t.Fatal(err)
	}
	if raw.Best != nil {
		t.Error("the legacy best field was written back; it should be dropped")
	}
}

// A record whose wave is empty must not be released, or the caller would
// rebuild entries from nothing on every run.
func TestExpiredSkipsRecordWithNoCandidates(t *testing.T) {
	b := newMemBucket()
	tr := New(b)
	now := time.Now()
	if err := b.Put("t|film", Record{FirstSeen: now.Add(-7 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
	if exp := tr.Expired("t", 6*time.Hour, now); len(exp) != 0 {
		t.Errorf("expired returned %d releases for an empty wave, want 0", len(exp))
	}
}
