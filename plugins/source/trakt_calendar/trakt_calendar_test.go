package trakt_calendar

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

var fixedNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func TestCalendarWindowAndFields(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/calendars/my/shows/2026-07-24/") {
			t.Errorf("path: %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer tok" {
			t.Errorf("auth header: %q", r.Header.Get("Authorization"))
		}
		in := func(h time.Duration) string { return fixedNow.Add(h).Format(time.RFC3339) }
		fmt.Fprintf(w, `[
			{"first_aired":%q,"episode":{"season":2,"number":10,"title":"tonight"},
			 "show":{"title":"Severance","year":2022,"ids":{"trakt":1,"tvdb":371980}}},
			{"first_aired":%q,"episode":{"season":1,"number":1,"title":"past"},
			 "show":{"title":"Old","ids":{}}},
			{"first_aired":%q,"episode":{"season":3,"number":1,"title":"beyond"},
			 "show":{"title":"Far","ids":{}}}
		]`, in(6*time.Hour), in(-2*time.Hour), in(80*time.Hour))
	}))
	defer ts.Close()
	old := itrakt.BaseURL
	itrakt.BaseURL = ts.URL
	defer func() { itrakt.BaseURL = old }()

	pl, err := newPlugin(map[string]any{"client_id": "cid", "access_token": "tok", "window": "24h"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := pl.(*calendarPlugin)
	p.now = func() time.Time { return fixedNow }

	out, err := p.Generate(context.Background(), &plugin.TaskContext{Name: "t", Logger: slog.Default()})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want 1 upcoming, got %d", len(out))
	}
	e := out[0]
	if e.Title != "Severance S02E10" || e.Fields["tvdb_id"] != 371980 || e.Fields["trakt_id"] != 1 {
		t.Errorf("entry: %q %+v", e.Title, e.Fields)
	}
	if e.GetString(entry.FieldDescription) != "tonight" {
		t.Errorf("description: %+v", e.Fields)
	}
}

func TestCalendarValidate(t *testing.T) {
	if errs := validate(map[string]any{"client_id": "x"}); len(errs) == 0 {
		t.Error("missing auth must fail")
	}
	if errs := validate(map[string]any{"client_id": "x", "access_token": "t", "window": "24h"}); len(errs) != 0 {
		t.Errorf("valid: %v", errs)
	}
	if errs := validate(map[string]any{"access_token": "t"}); len(errs) == 0 {
		t.Error("missing client_id must fail")
	}
}
