package bluray

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	ibluray "github.com/brunoga/pipeliner/internal/bluray"
	"github.com/brunoga/pipeliner/internal/entry"
)

// recordingServer counts requests and answers none of them, so a test can
// assert the embedded catalog answered alone.
func recordingServer(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, &n
}

func run(t *testing.T, pp *processorPlugin, es ...*entry.Entry) {
	t.Helper()
	if _, err := pp.Process(context.Background(), taskCtx(), es); err != nil {
		t.Fatal(err)
	}
}

func movie(name, title string, year int) *entry.Entry {
	e := entry.New(name, "https://example.com/"+name)
	if title != "" {
		e.Set(entry.FieldMovieTitle, title)
	}
	e.Set(entry.FieldVideoYear, year)
	return e
}

func TestCatalogAnswersWithoutRequests(t *testing.T) {
	srv, hits := recordingServer(t)
	pp := newProcessor(t, srv.URL)

	// Listed as "Eternals 4K + 3D Blu-ray".
	listed := movie("Eternals 2021 1080p BluRay 3D", "Eternals", 2021)
	// A fan conversion: no 3D Blu-ray was ever released.
	fan := movie("Fight Club (1999) BD50 MVC", "", 1999)
	run(t, pp, listed, fan)

	if n := hits.Load(); n != 0 {
		t.Errorf("made %d requests; the catalog is complete for these years", n)
	}
	if v, ok := listed.Fields[entry.FieldBluray3DRelease].(bool); !ok || !v {
		t.Errorf("Eternals: bluray_3d_release = %v (set %v), want true", v, ok)
	}
	if id, _ := listed.Fields[entry.FieldBlurayID].(string); id == "" {
		t.Error("Eternals: bluray_id not set from the catalog")
	}
	if v, ok := fan.Fields[entry.FieldBluray3DRelease].(bool); !ok || v {
		t.Errorf("Fight Club: bluray_3d_release = %v (set %v), want an explicit false", v, ok)
	}
}

func TestCatalogMatchesReleaseTitleForms(t *testing.T) {
	srv, _ := recordingServer(t)
	pp := newProcessor(t, srv.URL)
	for _, c := range []struct {
		title string
		year  int
	}{
		{"Thunderbolts", 2025},                     // listed as "Thunderbolts*"
		{"The Fantastic 4: First Steps", 2025},     // listed as "The Fantastic Four: …"
		{"Star Wars: The Rise of Skywalker", 2019}, // listed with "Episode IX -"
		{"Deadpool & Wolverine", 2024},             // listed as "… 4K + 3D Blu-ray"
	} {
		e := movie(c.title, c.title, c.year)
		run(t, pp, e)
		if v, _ := e.Fields[entry.FieldBluray3DRelease].(bool); !v {
			t.Errorf("%s (%d): no 3D Blu-ray found in the catalog", c.title, c.year)
		}
	}
}

func TestCatalogKeepsTheEntrysOwnRelease(t *testing.T) {
	srv, hits := recordingServer(t)
	pp := newProcessor(t, srv.URL)
	cat, err := ibluray.Embedded3DCatalog()
	if err != nil {
		t.Fatal(err)
	}
	rels := cat.LookupTitle("Avatar", 2009, ibluray.CountryAll)
	if len(rels) < 2 {
		t.Fatalf("want several Avatar 3D releases in the catalog, got %d", len(rels))
	}
	own := rels[len(rels)-1].ID
	e := movie("Avatar 2009 3D", "Avatar", 2009)
	e.Set(entry.FieldBlurayID, own)
	run(t, pp, e)
	if got, _ := e.Fields[entry.FieldBlurayID].(string); got != own {
		t.Errorf("bluray_id = %q, want the entry's own %q", got, own)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("made %d requests for a release the catalog holds", n)
	}
}

func TestOfflineLeavesNewerFilmsUnset(t *testing.T) {
	srv, hits := recordingServer(t)
	pp := newProcessor(t, srv.URL)
	pp.offline = true
	pp.details = true // offline wins

	newer := movie("Some Film 3D", "Some Film", ibluray.Catalog3DCutoffYear)
	older := movie("Avatar 2009 3D", "Avatar", 2009)
	run(t, pp, newer, older)

	if n := hits.Load(); n != 0 {
		t.Errorf("offline made %d requests", n)
	}
	if _, set := newer.Fields[entry.FieldBluray3DRelease]; set {
		t.Error("a film past the catalog's complete years must stay unset offline")
	}
	if v, _ := older.Fields[entry.FieldBluray3DRelease].(bool); !v {
		t.Error("Avatar: bluray_3d_release not answered from the catalog offline")
	}
}

func TestValidateDetailsAndOffline(t *testing.T) {
	if errs := validate(map[string]any{"details": true, "offline": true}); len(errs) != 0 {
		t.Errorf("details/offline refused: %v", errs)
	}
}
