package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/downloads"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

func newDownloadsServer(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetStore(db)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/downloads", srv.apiDownloads)
	return httptest.NewServer(mux), db
}

func TestApiDownloadsGroupsAndFlagsTracked(t *testing.T) {
	hs, db := newDownloadsServer(t)
	defer hs.Close()

	base := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	log := downloads.New(db.Bucket(downloads.BucketName))
	log.Append(downloads.Event{MediaType: "series", Name: "snw", DisplayName: "SNW", EpisodeID: "S04E05",
		Quality: quality.Parse("720p web"), DownloadedAt: base, Task: "tv"})
	log.Append(downloads.Event{MediaType: "series", Name: "snw", DisplayName: "SNW", EpisodeID: "S04E05",
		Quality: quality.Parse("1080p web"), DownloadedAt: base.Add(time.Hour), Task: "tv"})
	// Currently tracked.
	series.NewTracker(db.Bucket(series.TrackerBucketName)).Mark(series.Record{
		SeriesName: "snw", EpisodeID: "S04E05", DownloadedAt: base.Add(time.Hour)})

	resp := traceGet(t, hs.URL+"/api/downloads?q=snw")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Items []struct {
			Count            int    `json:"count"`
			EpisodeID        string `json:"episode_id"`
			CurrentlyTracked bool   `json:"currently_tracked"`
		} `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Items) != 1 {
		t.Fatalf("expected 1 grouped item, got %d", len(out.Items))
	}
	if out.Items[0].Count != 2 {
		t.Errorf("count = %d, want 2", out.Items[0].Count)
	}
	if !out.Items[0].CurrentlyTracked {
		t.Errorf("expected currently_tracked=true")
	}
}

func TestApiDownloadsUntracked(t *testing.T) {
	hs, db := newDownloadsServer(t)
	defer hs.Close()
	downloads.New(db.Bucket(downloads.BucketName)).Append(downloads.Event{
		MediaType: "movie", Name: "old movie", DisplayName: "Old Movie", Year: 2000,
		Quality: quality.Parse("1080p"), DownloadedAt: time.Now(), Task: "movies"})

	resp := traceGet(t, hs.URL+"/api/downloads?q=old+movie")
	defer resp.Body.Close()
	var out struct {
		Items []struct {
			CurrentlyTracked bool `json:"currently_tracked"`
		} `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Items) != 1 || out.Items[0].CurrentlyTracked {
		t.Errorf("expected 1 untracked item, got %+v", out.Items)
	}
}

func TestApiDownloadsEmpty(t *testing.T) {
	hs, _ := newDownloadsServer(t)
	defer hs.Close()
	resp := traceGet(t, hs.URL+"/api/downloads?q=nothing")
	defer resp.Body.Close()
	var out struct {
		Items []any `json:"items"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Items) != 0 {
		t.Errorf("expected no items, got %d", len(out.Items))
	}
}
