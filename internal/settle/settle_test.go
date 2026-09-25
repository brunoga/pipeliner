package settle

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/quality"
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
	if len(exp) != 1 || exp[0].Key != "t|old" || exp[0].Best.URL != "u-old" {
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
	if len(got) != 1 || got[0].Best.URL != "u-2d" {
		t.Errorf("the owning task must see its own record, got %+v", got)
	}
	// An empty task name must never match everything.
	if got := tr.Expired("", w, now); len(got) != 0 {
		t.Errorf("empty task must match nothing, got %+v", got)
	}
}
