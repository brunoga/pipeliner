package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

// newDBTestServer builds a Server with an in-memory SQLite store and a mux
// exposing only the database API endpoints.
func newDBTestServer(t *testing.T) (*Server, *httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/db/buckets", srv.apiDBBuckets)
	mux.HandleFunc("GET /api/db/buckets/{name}", srv.apiDBGetBucket)
	mux.HandleFunc("DELETE /api/db/buckets/{name}", srv.apiDBClearBucket)
	mux.HandleFunc("DELETE /api/db/entries/{name}", srv.apiDBDeleteEntry)
	mux.HandleFunc("DELETE /api/db/series/show", srv.apiDBDeleteSeriesShow)

	return srv, httptest.NewServer(mux), db
}

func deleteReq(t *testing.T, url string, body []byte) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete, url,
		bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

// ── GET /api/db/buckets ───────────────────────────────────────────────────────

// TestDBBucketsEmpty verifies the response stays well-formed when the store
// has zero rows. Plugin-declared caches still appear (count=0) so the user
// can pre-emptively clear a cache they expect to exist — and importantly,
// none of them are written, so every returned bucket has count=0 and
// category=cache.
func TestDBBucketsEmpty(t *testing.T) {
	_, ts, _ := newDBTestServer(t)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Buckets []struct {
			Name     string `json:"name"`
			Count    int    `json:"count"`
			Category string `json:"category"`
		} `json:"buckets"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	for _, b := range out.Buckets {
		if b.Count != 0 {
			t.Errorf("bucket %q count: got %d, want 0 (no buckets seeded)", b.Name, b.Count)
		}
		if b.Category != "cache" {
			t.Errorf("bucket %q category: got %q, want cache (only registry-derived caches surface in an empty store)", b.Name, b.Category)
		}
	}
}

func TestDBBucketsListed(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	// Seed two buckets.
	db.Bucket("series").Put("key1", "val1")
	db.Bucket("cache_tvdb").Put("key2", "val2")
	db.Bucket("cache_tvdb").Put("key3", "val3")

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()

	var out struct {
		Buckets []struct {
			Name     string `json:"name"`
			Count    int    `json:"count"`
			Category string `json:"category"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The response contains the seeded buckets PLUS every plugin-declared
	// cache (at count=0) from blank-imported plugin packages in the test
	// binary. Assert on the seeded ones specifically so the test stays stable
	// as plugins gain or lose Descriptor.Caches entries.
	byName := map[string]int{}
	byCategory := map[string]string{}
	for _, b := range out.Buckets {
		byName[b.Name] = b.Count
		byCategory[b.Name] = b.Category
	}
	if byName["series"] != 1 {
		t.Errorf("series count: got %d, want 1", byName["series"])
	}
	if byName["cache_tvdb"] != 2 {
		t.Errorf("cache_tvdb count: got %d, want 2", byName["cache_tvdb"])
	}
	if byCategory["series"] != "tracker" {
		t.Errorf("series category: got %q, want tracker", byCategory["series"])
	}
	if byCategory["cache_tvdb"] != "cache" {
		t.Errorf("cache_tvdb category: got %q, want cache", byCategory["cache_tvdb"])
	}
}

// TestDBBucketsRegistryMergesEmptyCaches confirms the database tab surfaces a
// plugin-declared cache before the plugin first writes to it. The web-package
// test binary blank-imports several plugins via playwright_test.go, so we can
// pick a known one (cache_bluray_index, declared by both metainfo_bluray and
// bluray_releases) and assert it appears at count=0 with the registered
// display name even though nothing has been written.
//
// This is the regression guard for the "where's my bluray cache?" confusion
// on fresh installs that motivated this change.
func TestDBBucketsRegistryMergesEmptyCaches(t *testing.T) {
	_, ts, _ := newDBTestServer(t)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()

	var out struct {
		Buckets []struct {
			Name     string `json:"name"`
			Display  string `json:"display"`
			Count    int    `json:"count"`
			Category string `json:"category"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// We assert on the bluray caches because bluray_releases is guaranteed
	// to be blank-imported by playwright_test.go in this test binary. Series
	// and movies filter plugins are not imported here, so we don't check
	// their caches — TestDBBucketsRegistryCountReflectsActualWrites would
	// catch a broken merge for those if it mattered.
	want := map[string]string{
		"cache_bluray_index":      "Blu-ray.com Title Index",
		"cache_bluray_search_neg": "Blu-ray.com Negative Search Cache",
		"cache_bluray_detail":     "Blu-ray.com Release Detail Cache",
	}
	got := map[string]string{}
	gotCounts := map[string]int{}
	gotCategory := map[string]string{}
	for _, b := range out.Buckets {
		got[b.Name] = b.Display
		gotCounts[b.Name] = b.Count
		gotCategory[b.Name] = b.Category
	}
	for name, display := range want {
		if got[name] != display {
			t.Errorf("display name for %q: got %q, want %q", name, got[name], display)
		}
		if gotCounts[name] != 0 {
			t.Errorf("count for %q: got %d, want 0 (registry merge for never-written bucket)", name, gotCounts[name])
		}
		if gotCategory[name] != "cache" {
			t.Errorf("category for %q: got %q, want cache", name, gotCategory[name])
		}
	}
}

// TestDBBucketsRegistryCountReflectsActualWrites confirms that once a plugin
// HAS written to one of its declared caches, the response reports the real
// count (not the synthetic 0 from the registry merge). Otherwise the merge
// would mask actual data.
func TestDBBucketsRegistryCountReflectsActualWrites(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("cache_bluray_index").Put("avatar", "x")
	db.Bucket("cache_bluray_index").Put("inception", "y")

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()

	var out struct {
		Buckets []struct {
			Name  string `json:"name"`
			Count int    `json:"count"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, b := range out.Buckets {
		if b.Name == "cache_bluray_index" {
			if b.Count != 2 {
				t.Errorf("cache_bluray_index count: got %d, want 2 (real SQL count must win over registry default)", b.Count)
			}
			return
		}
	}
	t.Error("cache_bluray_index not in bucket list")
}

// ── GET /api/db/buckets/{name} ────────────────────────────────────────────────

func TestDBGetBucketEntries(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("mylist").Put("foo", "bar")
	db.Bucket("mylist").Put("baz", "qux")

	resp := get(t, ts.URL+"/api/db/buckets/mylist")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Entries []struct {
			Key string `json:"key"`
		} `json:"entries"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Entries) != 2 {
		t.Errorf("want 2 entries, got %d", len(out.Entries))
	}
}

func TestDBGetSeriesBucketGrouped(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	// Write two series tracker records.
	type rec struct {
		SeriesName   string `json:"series_name"`
		EpisodeID    string `json:"episode_id"`
		DownloadedAt string `json:"downloaded_at"`
		Quality      struct {
			Str string `json:"string"`
		} `json:"quality"`
	}
	db.Bucket("series").Put("Breaking Bad|S01E01", rec{SeriesName: "Breaking Bad", EpisodeID: "S01E01"})
	db.Bucket("series").Put("Breaking Bad|S01E02", rec{SeriesName: "Breaking Bad", EpisodeID: "S01E02"})
	db.Bucket("series").Put("Dark|S01E01", rec{SeriesName: "Dark", EpisodeID: "S01E01"})

	resp := get(t, ts.URL+"/api/db/buckets/series")
	defer resp.Body.Close()

	var out struct {
		Grouped []struct {
			Name     string `json:"name"`
			Episodes []any  `json:"episodes"`
		} `json:"grouped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Grouped) != 2 {
		t.Fatalf("want 2 shows, got %d", len(out.Grouped))
	}
	// Grouped is sorted alphabetically: Breaking Bad, Dark.
	if out.Grouped[0].Name != "Breaking Bad" {
		t.Errorf("first show: got %q, want Breaking Bad", out.Grouped[0].Name)
	}
	if len(out.Grouped[0].Episodes) != 2 {
		t.Errorf("Breaking Bad episodes: got %d, want 2", len(out.Grouped[0].Episodes))
	}
}

// TestDBSeriesGroupedExposesStoredKeys is the regression guard for the silent
// no-op delete: the tracker stores normalized (lowercased) series names in
// the key while the record carries a DisplayName with the original casing.
// The grouped response must expose the exact stored key per episode and the
// normalized series_name per show, and deleting by the returned key must
// actually remove the row — reconstructing "DisplayName|episode" client-side
// matches nothing.
func TestDBSeriesGroupedExposesStoredKeys(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	type rec struct {
		SeriesName  string `json:"series_name"`
		DisplayName string `json:"display_name"`
		EpisodeID   string `json:"episode_id"`
	}
	// Stored key uses the normalized (lowercase) name; DisplayName differs in case.
	db.Bucket("series").Put("breaking bad|S01E01",
		rec{SeriesName: "breaking bad", DisplayName: "Breaking Bad", EpisodeID: "S01E01"})

	resp := get(t, ts.URL+"/api/db/buckets/series")
	defer resp.Body.Close()
	var out struct {
		Grouped []struct {
			Name       string `json:"name"`
			SeriesName string `json:"series_name"`
			Episodes   []struct {
				Key       string `json:"key"`
				EpisodeID string `json:"episode_id"`
			} `json:"episodes"`
		} `json:"grouped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out.Grouped) != 1 {
		t.Fatalf("want 1 show, got %d", len(out.Grouped))
	}
	show := out.Grouped[0]
	if show.Name != "Breaking Bad" {
		t.Errorf("display name: got %q, want Breaking Bad", show.Name)
	}
	if show.SeriesName != "breaking bad" {
		t.Errorf("series_name: got %q, want normalized breaking bad", show.SeriesName)
	}
	if len(show.Episodes) != 1 || show.Episodes[0].Key != "breaking bad|S01E01" {
		t.Fatalf("episode key: got %+v, want breaking bad|S01E01", show.Episodes)
	}

	// Deleting by the returned key must remove the row even though the
	// display name differs in case from the stored key.
	body, _ := json.Marshal(map[string]string{"key": show.Episodes[0].Key})
	dresp := deleteReq(t, ts.URL+"/api/db/entries/series", body)
	defer dresp.Body.Close()
	if dresp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status: %d", dresp.StatusCode)
	}
	keys, _ := db.Bucket("series").Keys()
	if len(keys) != 0 {
		t.Errorf("row survived delete-by-returned-key: %v", keys)
	}
}

// TestDBSeriesGroupedUnrecognizedEntries verifies that entries whose value
// fails to parse as a series record still surface — grouped by key prefix
// when the key has one, and under "(unrecognized entries)" otherwise — so
// the data stays visible and deletable instead of vanishing or rendering as
// a nameless group.
func TestDBSeriesGroupedUnrecognizedEntries(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	// Values are plain strings — json.Unmarshal into the record struct fails.
	db.Bucket("series").Put("orphan-no-separator", "junk")
	db.Bucket("series").Put("dark|S01E01", "junk")

	resp := get(t, ts.URL+"/api/db/buckets/series")
	defer resp.Body.Close()
	var out struct {
		Grouped []struct {
			Name       string `json:"name"`
			SeriesName string `json:"series_name"`
			Episodes   []struct {
				Key       string `json:"key"`
				EpisodeID string `json:"episode_id"`
			} `json:"episodes"`
		} `json:"grouped"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	byName := map[string]int{}
	for _, g := range out.Grouped {
		byName[g.Name] = len(g.Episodes)
	}
	if byName["(unrecognized entries)"] != 1 {
		t.Errorf("unrecognized group: got %v, want 1 episode under (unrecognized entries)", byName)
	}
	if byName["dark"] != 1 {
		t.Errorf("keyed junk entry: got %v, want 1 episode under dark (grouped by key prefix)", byName)
	}
	for _, g := range out.Grouped {
		for _, ep := range g.Episodes {
			if ep.Key == "" {
				t.Errorf("episode in group %q has empty key — not deletable", g.Name)
			}
		}
	}
}

// ── DELETE /api/db/series/show ────────────────────────────────────────────────

// TestDBDeleteSeriesShow seeds more shows than one UI page (20) and deletes a
// show that only appears beyond page 1. The old client-side implementation
// fetched page 1 and filtered keys locally, so such a show survived deletion.
func TestDBDeleteSeriesShow(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	type rec struct {
		SeriesName string `json:"series_name"`
		EpisodeID  string `json:"episode_id"`
	}
	// 25 shows (show-00 … show-24), 2 episodes each. With the default page
	// size of 20, show-24 is on page 2.
	for i := 0; i < 25; i++ {
		name := fmt.Sprintf("show-%02d", i)
		for _, ep := range []string{"S01E01", "S01E02"} {
			db.Bucket("series").Put(name+"|"+ep, rec{SeriesName: name, EpisodeID: ep})
		}
	}

	body, _ := json.Marshal(map[string]string{"series_name": "show-24"})
	resp := deleteReq(t, ts.URL+"/api/db/series/show", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("series").Keys()
	if len(keys) != 48 {
		t.Errorf("want 48 keys after deleting one 2-episode show, got %d", len(keys))
	}
	for _, k := range keys {
		if strings.HasPrefix(k, "show-24|") {
			t.Errorf("show-24 episode survived: %s", k)
		}
	}
}

// TestDBDeleteSeriesShowExactPrefixMatch guards against prefix bleed: deleting
// "dark" must not touch "darker".
func TestDBDeleteSeriesShowExactPrefixMatch(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("series").Put("dark|S01E01", `{"series_name":"dark","episode_id":"S01E01"}`)
	db.Bucket("series").Put("darker|S01E01", `{"series_name":"darker","episode_id":"S01E01"}`)

	body, _ := json.Marshal(map[string]string{"series_name": "dark"})
	resp := deleteReq(t, ts.URL+"/api/db/series/show", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("series").Keys()
	if len(keys) != 1 || keys[0] != "darker|S01E01" {
		t.Errorf("want only darker|S01E01 remaining, got %v", keys)
	}
}

// TestDBDeleteSeriesShowUnrecognizedGroup: an empty series_name targets the
// "(unrecognized entries)" group — keys with no '|' separator.
func TestDBDeleteSeriesShowUnrecognizedGroup(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("series").Put("orphan-no-separator", "junk")
	db.Bucket("series").Put("dark|S01E01", `{"series_name":"dark","episode_id":"S01E01"}`)

	body, _ := json.Marshal(map[string]string{"series_name": ""})
	resp := deleteReq(t, ts.URL+"/api/db/series/show", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("series").Keys()
	if len(keys) != 1 || keys[0] != "dark|S01E01" {
		t.Errorf("want only dark|S01E01 remaining, got %v", keys)
	}
}

func TestDBDeleteSeriesShowMissingName(t *testing.T) {
	_, ts, _ := newDBTestServer(t)
	defer ts.Close()

	resp := deleteReq(t, ts.URL+"/api/db/series/show", []byte(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing series_name, got %d", resp.StatusCode)
	}
}

// TestDBBucketDisplayLegacyCaches: buckets written by older pipeliner
// versions that no current plugin declares still get a friendly sidebar
// label instead of the raw bucket name.
func TestDBBucketDisplayLegacyCaches(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("cache_tvdb").Put("k", "v")
	db.Bucket("cache_tmdb").Put("k", "v")

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()
	var out struct {
		Buckets []struct {
			Name    string `json:"name"`
			Display string `json:"display"`
		} `json:"buckets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := map[string]string{}
	for _, b := range out.Buckets {
		got[b.Name] = b.Display
	}
	if got["cache_tvdb"] != "TVDB Lookup Cache" {
		t.Errorf("cache_tvdb display: got %q, want TVDB Lookup Cache", got["cache_tvdb"])
	}
	if got["cache_tmdb"] != "TMDb Lookup Cache" {
		t.Errorf("cache_tmdb display: got %q, want TMDb Lookup Cache", got["cache_tmdb"])
	}
}

// ── DELETE /api/db/buckets/{name} ─────────────────────────────────────────────

func TestDBClearBucket(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("cache_tvdb").Put("k1", "v1")
	db.Bucket("cache_tvdb").Put("k2", "v2")

	resp := deleteReq(t, ts.URL+"/api/db/buckets/cache_tvdb", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("cache_tvdb").Keys()
	if len(keys) != 0 {
		t.Errorf("bucket should be empty after clear, got %d keys", len(keys))
	}
}

// ── DELETE /api/db/entries/{name} ─────────────────────────────────────────────

func TestDBDeleteEntry(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	db.Bucket("series").Put("Breaking Bad|S01E01", "rec1")
	db.Bucket("series").Put("Breaking Bad|S01E02", "rec2")

	body, _ := json.Marshal(map[string]string{"key": "Breaking Bad|S01E01"})
	resp := deleteReq(t, ts.URL+"/api/db/entries/series", body)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status: %d", resp.StatusCode)
	}

	keys, _ := db.Bucket("series").Keys()
	if len(keys) != 1 || keys[0] != "Breaking Bad|S01E02" {
		t.Errorf("expected only S01E02 remaining, got %v", keys)
	}
}

func TestDBDeleteEntryMissingKey(t *testing.T) {
	_, ts, _ := newDBTestServer(t)
	defer ts.Close()

	resp := deleteReq(t, ts.URL+"/api/db/entries/series", []byte(`{}`))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for missing key, got %d", resp.StatusCode)
	}
}

func TestDBNotAvailableWithoutStore(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/db/buckets", srv.apiDBBuckets)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp := get(t, ts.URL+"/api/db/buckets")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("expected 501 when store not set, got %d", resp.StatusCode)
	}
}

// ── cursor pagination ─────────────────────────────────────────────────────────

func TestDBPaginationFirstPage(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		db.Bucket("movies").Put(k, `{"title":"`+k+`"}`)
	}

	resp := get(t, ts.URL+"/api/db/buckets/movies?limit=2")
	defer resp.Body.Close()
	var out struct {
		Entries []struct {
			Key string `json:"key"`
		} `json:"entries"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
		Total      int    `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out.Entries))
	}
	if out.Entries[0].Key != "a" || out.Entries[1].Key != "b" {
		t.Errorf("unexpected keys: %v", out.Entries)
	}
	if !out.HasMore {
		t.Error("want has_more=true")
	}
	if out.NextCursor != "b" {
		t.Errorf("next_cursor: got %q, want b", out.NextCursor)
	}
	if out.Total != 5 {
		t.Errorf("total: got %d, want 5", out.Total)
	}
}

func TestDBPaginationNextPage(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()
	for _, k := range []string{"a", "b", "c", "d", "e"} {
		db.Bucket("movies").Put(k, `{"title":"`+k+`"}`)
	}

	resp := get(t, ts.URL+"/api/db/buckets/movies?limit=2&after=b")
	defer resp.Body.Close()
	var out struct {
		Entries []struct {
			Key string `json:"key"`
		} `json:"entries"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Entries) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out.Entries))
	}
	if out.Entries[0].Key != "c" || out.Entries[1].Key != "d" {
		t.Errorf("unexpected keys: %v", out.Entries)
	}
	if !out.HasMore {
		t.Error("want has_more=true for page 2 of 5")
	}
}

func TestDBPaginationLastPage(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()
	for _, k := range []string{"a", "b", "c"} {
		db.Bucket("movies").Put(k, `{"title":"`+k+`"}`)
	}

	resp := get(t, ts.URL+"/api/db/buckets/movies?limit=2&after=b")
	defer resp.Body.Close()
	var out struct {
		HasMore bool `json:"has_more"`
		Entries []struct {
			Key string `json:"key"`
		} `json:"entries"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.HasMore {
		t.Error("want has_more=false on last page")
	}
	if len(out.Entries) != 1 || out.Entries[0].Key != "c" {
		t.Errorf("unexpected entries: %v", out.Entries)
	}
}

func TestDBPaginationFilter(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()
	db.Bucket("movies").Put("avatar|2009", `{"title":"avatar"}`)
	db.Bucket("movies").Put("batman|2022", `{"title":"batman"}`)
	db.Bucket("movies").Put("avatar|2022", `{"title":"avatar 2"}`)

	resp := get(t, ts.URL+"/api/db/buckets/movies?q=avatar")
	defer resp.Body.Close()
	var out struct {
		Entries []struct {
			Key string `json:"key"`
		} `json:"entries"`
		Total int `json:"total"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if out.Total != 2 {
		t.Errorf("total: got %d, want 2", out.Total)
	}
	if len(out.Entries) != 2 {
		t.Errorf("entries: got %d, want 2", len(out.Entries))
	}
}

func TestDBSeriesPaginationByShow(t *testing.T) {
	_, ts, db := newDBTestServer(t)
	defer ts.Close()

	type rec struct {
		SeriesName string `json:"series_name"`
		EpisodeID  string `json:"episode_id"`
	}
	for _, ep := range []struct{ show, ep string }{
		{"Breaking Bad", "S01E01"}, {"Breaking Bad", "S01E02"},
		{"Dark", "S01E01"},
		{"Mindhunter", "S01E01"}, {"Mindhunter", "S01E02"},
	} {
		db.Bucket("series").Put(ep.show+"|"+ep.ep, rec{SeriesName: ep.show, EpisodeID: ep.ep})
	}

	// Page 1: limit=2 shows
	resp := get(t, ts.URL+"/api/db/buckets/series?limit=2")
	defer resp.Body.Close()
	var out struct {
		Grouped []struct {
			Name string `json:"name"`
		} `json:"grouped"`
		NextCursor string `json:"next_cursor"`
		HasMore    bool   `json:"has_more"`
		Total      int    `json:"total"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Grouped) != 2 {
		t.Fatalf("want 2 shows on page 1, got %d", len(out.Grouped))
	}
	if out.Grouped[0].Name != "Breaking Bad" || out.Grouped[1].Name != "Dark" {
		t.Errorf("unexpected shows: %v", out.Grouped)
	}
	if !out.HasMore {
		t.Error("want has_more=true")
	}
	if out.NextCursor != "Dark" {
		t.Errorf("next_cursor: got %q, want Dark", out.NextCursor)
	}
	if out.Total != 3 {
		t.Errorf("total shows: got %d, want 3", out.Total)
	}

	// Page 2: after Dark
	resp2 := get(t, ts.URL+"/api/db/buckets/series?limit=2&after=Dark")
	defer resp2.Body.Close()
	var out2 struct {
		Grouped []struct {
			Name string `json:"name"`
		} `json:"grouped"`
		HasMore bool `json:"has_more"`
	}
	json.NewDecoder(resp2.Body).Decode(&out2)
	if len(out2.Grouped) != 1 || out2.Grouped[0].Name != "Mindhunter" {
		t.Errorf("page 2 shows: %v", out2.Grouped)
	}
	if out2.HasMore {
		t.Error("want has_more=false on last page")
	}
}
