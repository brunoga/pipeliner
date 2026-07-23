package tvdb_calendar

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	itvdb "github.com/brunoga/pipeliner/internal/tvdb"
)

// fixedNow keeps the window math deterministic.
var fixedNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func day(offset int) string {
	return fixedNow.AddDate(0, 0, offset).Format("2006-01-02")
}

func newTVDBServer(t *testing.T) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/login":
			w.Write([]byte(`{"data":{"token":"tok"}}`))
		case r.URL.Path == "/search":
			w.Write([]byte(`{"data":[{"tvdb_id":"42","name":"Severance","type":"series"}]}`))
		case r.URL.Path == "/series/42/episodes/official":
			w.Write([]byte(fmt.Sprintf(`{"data":{"episodes":[
				{"id":1,"seasonNumber":2,"number":1,"aired":%q,"name":"yesterday"},
				{"id":2,"seasonNumber":2,"number":2,"aired":%q,"name":"today"},
				{"id":3,"seasonNumber":2,"number":3,"aired":%q,"name":"tomorrow"},
				{"id":4,"seasonNumber":2,"number":4,"aired":%q,"name":"beyond window"},
				{"id":5,"seasonNumber":0,"number":1,"aired":%q,"name":"special"},
				{"id":6,"seasonNumber":2,"number":5,"aired":"","name":"undated"}
			]}}`, day(-1), day(0), day(1), day(10), day(0))))
		default:
			t.Errorf("unexpected tvdb path: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
}

func newTestPlugin(t *testing.T, ts *httptest.Server, includeInactive bool) (*calendarPlugin, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	client := itvdb.New("key")
	client.BaseURL = ts.URL
	return &calendarPlugin{
		resolver: itvdb.NewResolver(client, time.Hour,
			db.Bucket("cache_tvdb_calendar"), db.Bucket("cache_tvdb_calendar_eps")),
		tracker:         series.NewTracker(db.Bucket(series.TrackerBucketName)),
		inactive:        series.NewInactiveSet(db.Bucket(series.InactiveBucketName)),
		window:          48 * time.Hour,
		includeInactive: includeInactive,
		now:             func() time.Time { return fixedNow },
	}, db
}

func track(t *testing.T, db *store.SQLiteStore, name, display string) {
	t.Helper()
	tr := series.NewTracker(db.Bucket(series.TrackerBucketName))
	if err := tr.Mark(series.Record{SeriesName: name, DisplayName: display,
		EpisodeID: "S01E01", DownloadedAt: fixedNow.AddDate(0, 0, -30)}); err != nil {
		t.Fatal(err)
	}
}

func TestCalendarEmitsUpcomingWithinWindow(t *testing.T) {
	ts := newTVDBServer(t)
	defer ts.Close()
	p, db := newTestPlugin(t, ts, false)
	track(t, db, "severance", "Severance")

	out, err := p.Generate(context.Background(), &plugin.TaskContext{Name: "t", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	// today + tomorrow only: yesterday is past, day+10 beyond the 48h
	// window, specials and undated episodes are skipped.
	if len(out) != 2 {
		for _, e := range out {
			t.Logf("got: %s", e.Title)
		}
		t.Fatalf("want 2 upcoming episodes, got %d", len(out))
	}
	e := out[0]
	if e.Title != "Severance S02E02" || e.GetString(entry.FieldSeriesEpisodeID) != "S02E02" {
		t.Errorf("first: %q %+v", e.Title, e.Fields)
	}
	if _, ok := e.Fields[entry.FieldSeriesAirDate].(time.Time); !ok {
		t.Error("series_air_date must be a time.Time")
	}
	if out[1].GetString(entry.FieldSeriesEpisodeID) != "S02E03" {
		t.Errorf("second: %+v", out[1].Fields)
	}
}

func TestCalendarSkipsInactiveShows(t *testing.T) {
	ts := newTVDBServer(t)
	defer ts.Close()
	p, db := newTestPlugin(t, ts, false)
	track(t, db, "severance", "Severance")
	if err := series.NewInactiveSet(db.Bucket(series.InactiveBucketName)).Deactivate("severance", "done"); err != nil {
		t.Fatal(err)
	}
	out, err := p.Generate(context.Background(), &plugin.TaskContext{Name: "t", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("inactive show must be skipped, got %d entries", len(out))
	}

	p2, _ := newTestPlugin(t, ts, true)
	p2.tracker = series.NewTracker(db.Bucket(series.TrackerBucketName))
	p2.inactive = series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	out2, err := p2.Generate(context.Background(), &plugin.TaskContext{Name: "t", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out2) != 2 {
		t.Fatalf("include_inactive must override the skip, got %d", len(out2))
	}
}

func TestCalendarValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing api_key must fail")
	}
	if errs := validate(map[string]any{"api_key": "k", "window": "nope"}); len(errs) == 0 {
		t.Error("bad window must fail")
	}
	if errs := validate(map[string]any{"api_key": "k", "window": "24h"}); len(errs) != 0 {
		t.Errorf("valid config: %v", errs)
	}
}
