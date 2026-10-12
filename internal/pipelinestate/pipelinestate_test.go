package pipelinestate

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

func testStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func put(t *testing.T, db *store.SQLiteStore, bucket, key string, v any) {
	t.Helper()
	if err := db.Bucket(bucket).Put(key, v); err != nil {
		t.Fatalf("put %s/%s: %v", bucket, key, err)
	}
}

func rows(t *testing.T, db *store.SQLiteStore, bucket string) map[string][]byte {
	t.Helper()
	all, err := db.Bucket(bucket).All()
	if err != nil {
		t.Fatalf("all %s: %v", bucket, err)
	}
	return all
}

func seenRec(task, title string) store.SeenRecord {
	return store.SeenRecord{Fingerprint: title, Title: title, Task: task, SeenAt: time.Now()}
}

func movieRec(title string, year int, q string) imovies.Record {
	return imovies.Record{Title: title, Year: year, Quality: quality.Parse(q), DownloadedAt: time.Now()}
}

// --- scanning ---

func TestScanGroupsBucketsByPipelineAndMarksOrphans(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:alpha", "k1", seenRec("alpha", "one"))
	put(t, db, "discover:alpha", "dog man", map[string]any{"last_searched": time.Now()})
	put(t, db, "seen:ghost", "k1", seenRec("ghost", "two"))
	// Not per-pipeline state: a global bucket and a cache must not be grouped.
	put(t, db, "seen", "k1", seenRec("", "global"))
	put(t, db, "cache_metainfo_tmdb", "k1", map[string]any{"x": 1})

	got, err := Scan(db, []string{"alpha"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d pipelines, want 2: %+v", len(got), got)
	}
	if got[0].Pipeline != "alpha" || got[0].Orphaned {
		t.Errorf("alpha = %+v, want present and not orphaned", got[0])
	}
	if len(got[0].Buckets) != 2 || got[0].Rows() != 2 {
		t.Errorf("alpha buckets = %+v, rows = %d, want 2 and 2", got[0].Buckets, got[0].Rows())
	}
	if got[1].Pipeline != "ghost" || !got[1].Orphaned {
		t.Errorf("ghost = %+v, want orphaned", got[1])
	}
}

// TestOrphansWithoutLiveNamesJudgesNothing: a caller that could not load the
// config must not be told every pipeline is orphaned, which would invite
// deleting all of it.
func TestOrphansWithoutLiveNamesJudgesNothing(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:alpha", "k1", seenRec("alpha", "one"))

	got, err := Orphans(db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d orphans for a nil live list, want 0", len(got))
	}
}

func TestOrphansWithEmptyLiveListReportsEverything(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:alpha", "k1", seenRec("alpha", "one"))

	got, err := Orphans(db, []string{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].Pipeline != "alpha" {
		t.Errorf("got %+v, want alpha orphaned", got)
	}
}

// --- rename: an empty destination ---

func TestMoveToEmptyDestinationIsARename(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:old", "k1", seenRec("old", "one"))
	put(t, db, "seen:old", "k2", seenRec("old", "two"))
	put(t, db, "movies:old", "dune|2024|", movieRec("dune", 2024, "2160p BluRay"))

	rep, err := Move(db, "old", "new", MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if n := rep.Total(func(s Step) int { return s.Moved }); n != 3 {
		t.Errorf("moved %d records, want 3", n)
	}
	if n := rep.Total(func(s Step) int { return s.Conflicts }); n != 0 {
		t.Errorf("conflicts = %d, want 0", n)
	}
	if got := len(rows(t, db, "seen:new")); got != 2 {
		t.Errorf("seen:new has %d records, want 2", got)
	}
	if got := len(rows(t, db, "seen:old")); got != 0 {
		t.Errorf("seen:old still has %d records, want 0", got)
	}
	if got := len(rows(t, db, "movies:new")); got != 1 {
		t.Errorf("movies:new has %d records, want 1", got)
	}
}

// TestRenameRetagsTheDenormalisedTaskName: a seen record repeats its pipeline
// name, and the database tab shows the record verbatim. A moved record that
// still names its old pipeline is a lie about where it lives.
func TestRenameRetagsTheDenormalisedTaskName(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:old", "k1", seenRec("old", "one"))

	if _, err := Move(db, "old", "new", MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	var rec store.SeenRecord
	found, err := db.Bucket("seen:new").Get("k1", &rec)
	if err != nil || !found {
		t.Fatalf("get: found=%v err=%v", found, err)
	}
	if rec.Task != "new" {
		t.Errorf("record task = %q, want %q", rec.Task, "new")
	}
	if rec.Title != "one" {
		t.Errorf("retag damaged the record: title = %q", rec.Title)
	}
}

// TestRetagPreservesUnknownFields guards the reason retagging rewrites one key
// instead of round-tripping through the struct: a record written by a newer
// build can hold fields this one does not know, and a struct round trip would
// drop them.
func TestRetagPreservesUnknownFields(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:old", "k1", map[string]any{
		"task": "old", "title": "one", "future_field": "keep me",
	})

	if _, err := Move(db, "old", "new", MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	var obj map[string]any
	if _, err := db.Bucket("seen:new").Get("k1", &obj); err != nil {
		t.Fatal(err)
	}
	if obj["task"] != "new" {
		t.Errorf("task = %v, want new", obj["task"])
	}
	if obj["future_field"] != "keep me" {
		t.Errorf("future_field = %v, want it preserved", obj["future_field"])
	}
}

// --- adoption: state on both sides ---

// TestAdoptSeenIsAUnion is the case that makes folding two pipelines into one
// safe: measured on the live install, seen:movies-3d and
// seen:movies-3d-discover shared no keys at all, so every record has to
// survive or the merged pipeline re-downloads what the other already had.
func TestAdoptSeenIsAUnion(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:src", "a", seenRec("src", "a"))
	put(t, db, "seen:src", "shared", seenRec("src", "from source"))
	put(t, db, "seen:dst", "b", seenRec("dst", "b"))
	put(t, db, "seen:dst", "shared", seenRec("dst", "from dest"))

	rep, err := Move(db, "src", "dst", MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(rows(t, db, "seen:dst")); got != 3 {
		t.Errorf("seen:dst has %d records, want 3 (union)", got)
	}
	if rep.Steps[0].Moved != 1 || rep.Steps[0].Conflicts != 1 {
		t.Errorf("step = %+v, want 1 moved and 1 conflict", rep.Steps[0])
	}
	// Both records mean "this was seen", so the destination's stands.
	var rec store.SeenRecord
	if _, err := db.Bucket("seen:dst").Get("shared", &rec); err != nil {
		t.Fatal(err)
	}
	if rec.Title != "from dest" {
		t.Errorf("conflict kept %q, want the destination's record", rec.Title)
	}
}

// TestAdoptMoviesKeepsTheBetterCopy: the record states what was downloaded, so
// keeping the worse of two would invite a re-grab or a downgrade of a film
// already owned.
func TestAdoptMoviesKeepsTheBetterCopy(t *testing.T) {
	db := testStore(t)
	put(t, db, "movies:src", "dune|2024|", movieRec("dune", 2024, "2160p BluRay"))
	put(t, db, "movies:dst", "dune|2024|", movieRec("dune", 2024, "1080p WEB-DL"))

	rep, err := Move(db, "src", "dst", MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var rec imovies.Record
	if _, err := db.Bucket("movies:dst").Get("dune|2024|", &rec); err != nil {
		t.Fatal(err)
	}
	if got := rec.Quality.String(); got != quality.Parse("2160p BluRay").String() {
		t.Errorf("kept %q, want the better copy (2160p BluRay)", got)
	}
	if rep.Steps[0].SourceWon != 1 {
		t.Errorf("source_won = %d, want 1", rep.Steps[0].SourceWon)
	}
}

func TestAdoptMoviesKeepsTheDestinationWhenItIsBetter(t *testing.T) {
	db := testStore(t)
	put(t, db, "movies:src", "dune|2024|", movieRec("dune", 2024, "1080p WEB-DL"))
	put(t, db, "movies:dst", "dune|2024|", movieRec("dune", 2024, "2160p BluRay"))

	rep, err := Move(db, "src", "dst", MoveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var rec imovies.Record
	if _, err := db.Bucket("movies:dst").Get("dune|2024|", &rec); err != nil {
		t.Fatal(err)
	}
	if got := rec.Quality.String(); got != quality.Parse("2160p BluRay").String() {
		t.Errorf("kept %q, want the destination's better copy", got)
	}
	if rep.Steps[0].SourceWon != 0 {
		t.Errorf("source_won = %d, want 0", rep.Steps[0].SourceWon)
	}
}

// TestAdoptDiscoverKeepsTheNewerSearch is the 989-conflict case: on the live
// install discover:3d-mvc-harvest and discover:movies-3d-discover share every
// one of their 989 keys, because both walk the same watchlist.
func TestAdoptDiscoverKeepsTheNewerSearch(t *testing.T) {
	db := testStore(t)
	older := time.Now().Add(-48 * time.Hour)
	newer := time.Now()
	put(t, db, "discover:src", "dog man", map[string]any{"last_searched": newer, "results": []string{"new"}})
	put(t, db, "discover:dst", "dog man", map[string]any{"last_searched": older, "results": []string{"old"}})

	if _, err := Move(db, "src", "dst", MoveOptions{}); err != nil {
		t.Fatal(err)
	}
	var obj map[string]json.RawMessage
	if _, err := db.Bucket("discover:dst").Get("dog man", &obj); err != nil {
		t.Fatal(err)
	}
	var ts time.Time
	if err := json.Unmarshal(obj["last_searched"], &ts); err != nil {
		t.Fatal(err)
	}
	if !ts.Equal(newer.UTC()) && ts.Sub(newer).Abs() > time.Second {
		t.Errorf("last_searched = %v, want the newer %v", ts, newer)
	}
}

// --- the cursor that cannot be merged ---

// TestUnmergeableCollisionRefusesWithoutAChoice: two gap-scan cursors have no
// union. A merged cursor would be a scan position neither pipeline was ever
// at, so this has to be the user's call.
func TestUnmergeableCollisionRefusesWithoutAChoice(t *testing.T) {
	db := testStore(t)
	put(t, db, "series_gaps:src", "cursor", map[string]any{"key": "american dad|0002|0008"})
	put(t, db, "series_gaps:dst", "cursor", map[string]any{"key": "the office|0001|0003"})

	_, err := Move(db, "src", "dst", MoveOptions{})
	if !errors.Is(err, ErrNeedPrefer) {
		t.Fatalf("err = %v, want ErrNeedPrefer", err)
	}
}

// TestRefusalWritesNothing is the reason Move plans before it applies: the
// collision is found at the gap-scan bucket, by which point a bucket-at-a-time
// implementation would already have moved the seen records and left the
// pipeline half renamed.
func TestRefusalWritesNothing(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:src", "k1", seenRec("src", "one"))
	put(t, db, "series_gaps:src", "cursor", map[string]any{"key": "a|0002|0008"})
	put(t, db, "series_gaps:dst", "cursor", map[string]any{"key": "b|0001|0003"})

	if _, err := Move(db, "src", "dst", MoveOptions{}); !errors.Is(err, ErrNeedPrefer) {
		t.Fatalf("err = %v, want ErrNeedPrefer", err)
	}
	if got := len(rows(t, db, "seen:src")); got != 1 {
		t.Errorf("seen:src has %d records after a refused move, want 1 (untouched)", got)
	}
	if got := len(rows(t, db, "seen:dst")); got != 0 {
		t.Errorf("seen:dst has %d records after a refused move, want 0", got)
	}
}

func TestPreferSourceReplacesTheCursor(t *testing.T) {
	db := testStore(t)
	put(t, db, "series_gaps:src", "cursor", map[string]any{"key": "src-position"})
	put(t, db, "series_gaps:dst", "cursor", map[string]any{"key": "dst-position"})

	rep, err := Move(db, "src", "dst", MoveOptions{Prefer: PreferSource})
	if err != nil {
		t.Fatal(err)
	}
	var obj map[string]string
	if _, err := db.Bucket("series_gaps:dst").Get("cursor", &obj); err != nil {
		t.Fatal(err)
	}
	if obj["key"] != "src-position" {
		t.Errorf("cursor = %q, want the source's", obj["key"])
	}
	if got := len(rows(t, db, "series_gaps:src")); got != 0 {
		t.Errorf("source still has %d records, want 0", got)
	}
	if rep.Steps[0].Note == "" {
		t.Error("a chosen-not-merged bucket must say so in its note")
	}
}

func TestPreferDestDropsTheSourceCursor(t *testing.T) {
	db := testStore(t)
	put(t, db, "series_gaps:src", "cursor", map[string]any{"key": "src-position"})
	put(t, db, "series_gaps:dst", "cursor", map[string]any{"key": "dst-position"})

	if _, err := Move(db, "src", "dst", MoveOptions{Prefer: PreferDest}); err != nil {
		t.Fatal(err)
	}
	var obj map[string]string
	if _, err := db.Bucket("series_gaps:dst").Get("cursor", &obj); err != nil {
		t.Fatal(err)
	}
	if obj["key"] != "dst-position" {
		t.Errorf("cursor = %q, want the destination's", obj["key"])
	}
	if got := len(rows(t, db, "series_gaps:src")); got != 0 {
		t.Errorf("source still has %d records, want 0", got)
	}
}

// TestUnmergeableMovesFreelyWhenTheDestinationHasNone: a cursor with nothing
// to collide with is an ordinary rename and must not demand a choice.
func TestUnmergeableMovesFreelyWhenTheDestinationHasNone(t *testing.T) {
	db := testStore(t)
	put(t, db, "series_gaps:src", "cursor", map[string]any{"key": "src-position"})

	if _, err := Move(db, "src", "dst", MoveOptions{}); err != nil {
		t.Fatalf("a cursor with no counterpart should move without a choice: %v", err)
	}
	if got := len(rows(t, db, "series_gaps:dst")); got != 1 {
		t.Errorf("series_gaps:dst has %d records, want 1", got)
	}
}

// --- dry run, forget, and guards ---

func TestDryRunReportsWithoutWriting(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:src", "a", seenRec("src", "a"))
	put(t, db, "seen:src", "shared", seenRec("src", "s"))
	put(t, db, "seen:dst", "shared", seenRec("dst", "s"))

	rep, err := Move(db, "src", "dst", MoveOptions{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || rep.Steps[0].Moved != 1 || rep.Steps[0].Conflicts != 1 {
		t.Errorf("report = %+v, want 1 moved and 1 conflict", rep.Steps[0])
	}
	if got := len(rows(t, db, "seen:src")); got != 2 {
		t.Errorf("a dry run emptied the source: %d records left, want 2", got)
	}
	if got := len(rows(t, db, "seen:dst")); got != 1 {
		t.Errorf("a dry run wrote to the destination: %d records, want 1", got)
	}
}

func TestForgetDeletesEveryBucketForAPipeline(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:ghost", "a", seenRec("ghost", "a"))
	put(t, db, "discover:ghost", "t", map[string]any{"last_searched": time.Now()})
	put(t, db, "seen:keep", "a", seenRec("keep", "a"))

	rep, err := Forget(db, "ghost", false)
	if err != nil {
		t.Fatal(err)
	}
	if n := rep.Total(func(s Step) int { return s.Deleted }); n != 2 {
		t.Errorf("deleted %d records, want 2", n)
	}
	if got := len(rows(t, db, "seen:ghost")); got != 0 {
		t.Errorf("seen:ghost has %d records, want 0", got)
	}
	if got := len(rows(t, db, "seen:keep")); got != 1 {
		t.Errorf("forget touched another pipeline: seen:keep has %d records, want 1", got)
	}
}

func TestForgetDryRunWritesNothing(t *testing.T) {
	db := testStore(t)
	put(t, db, "seen:ghost", "a", seenRec("ghost", "a"))

	rep, err := Forget(db, "ghost", true)
	if err != nil {
		t.Fatal(err)
	}
	if n := rep.Total(func(s Step) int { return s.Deleted }); n != 1 {
		t.Errorf("reported %d deletions, want 1", n)
	}
	if got := len(rows(t, db, "seen:ghost")); got != 1 {
		t.Errorf("a dry run deleted records: %d left, want 1", got)
	}
}

func TestMoveRejectsBadNames(t *testing.T) {
	db := testStore(t)
	for _, tc := range []struct{ from, to string }{
		{"", "x"}, {"x", ""}, {"same", "same"},
	} {
		if _, err := Move(db, tc.from, tc.to, MoveOptions{}); err == nil {
			t.Errorf("Move(%q, %q) succeeded, want an error", tc.from, tc.to)
		}
	}
}

// TestEveryPerPipelineBucketIsRegistered pins the registry to the prefixes the
// rest of the tree actually namespaces by pipeline name. A new one added
// without registering it here is state a rename would silently lose, so this
// failing is the reminder to add it.
func TestEveryPerPipelineBucketIsRegistered(t *testing.T) {
	want := []string{"seen", "movies", "discover", "series_gaps"}
	got := map[string]bool{}
	for _, k := range Kinds() {
		got[k.Prefix] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("prefix %q is not registered", w)
		}
	}
	if len(got) != len(want) {
		t.Errorf("registry holds %d kinds, want %d — if you added one, add it to this test too", len(got), len(want))
	}
}

func TestOnlyTheGapCursorIsUnmergeable(t *testing.T) {
	for _, k := range Kinds() {
		if k.Prefix == "series_gaps" {
			if k.Mergeable() {
				t.Error("the gap cursor must not be mergeable")
			}
			continue
		}
		if !k.Mergeable() {
			t.Errorf("%q has no merge rule, so adopting it would be impossible", k.Prefix)
		}
	}
}
