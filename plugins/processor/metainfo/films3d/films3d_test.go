package films3d

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

const page = `====2015====
{| class="wikitable sortable"
|-
|''[[Star Wars: The Force Awakens]]''
|December 18, 2015
|United States
|Filmed in 2D
|3D conversion by Stereo D.
|}
====2020====
{| class="wikitable sortable"
|-
|''[[Mulan (2020 film)|Mulan]]''
|September 11, 2020
|United States
|Filmed in 2D
|3D conversion by [[DNEG]].
|}
====2016====
{| class="wikitable sortable"
|-
|''[[Star Trek Beyond]]''
|July 22, 2016
|United States
|Filmed in 2D
|
|}
`

type wiki struct {
	srv   *httptest.Server
	hits  atomic.Int32
	fail  atomic.Bool
}

func newWiki(t *testing.T) *wiki {
	w := &wiki{}
	w.srv = httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		w.hits.Add(1)
		if w.fail.Load() {
			http.Error(rw, "down", http.StatusServiceUnavailable)
			return
		}
		_, _ = rw.Write([]byte(page))
	}))
	t.Cleanup(w.srv.Close)
	return w
}

func newProc(t *testing.T, db *store.SQLiteStore, w *wiki, cfg map[string]any) *processorPlugin {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	p, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatal(err)
	}
	pp := p.(*processorPlugin)
	pp.baseURL = w.srv.URL
	pp.pages = []string{"List"}
	pp.client = w.srv.Client()
	return pp
}

func openDB(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))}
}

func run(t *testing.T, p *processorPlugin, es ...*entry.Entry) {
	t.Helper()
	if _, err := p.Process(context.Background(), tc(), es); err != nil {
		t.Fatal(err)
	}
}

func official(e *entry.Entry) (bool, bool) {
	v, ok := e.Fields[entry.FieldMovie3DOfficial].(bool)
	return v, ok
}

func TestMarksStudio3DAndFanConversions(t *testing.T) {
	p := newProc(t, openDB(t), newWiki(t), nil)

	beyond := entry.New("Star Trek Beyond 2016 BD25 1080p 3D Blu ray Re Encoded MVC DTS HD MA 7 1 munk", "u1")
	fight := entry.New("Fight Club (1999) BD50   MVC", "u2")
	// The release name numbers the episode; TMDb's title is what is listed.
	force := entry.New("Star Wars Episode VII The Force Awakens 3D 2015 1080p Bluray AVC", "u3")
	force.Set(entry.FieldMovieTitle, "Star Wars: The Force Awakens")
	force.Set(entry.FieldVideoYear, 2015)
	// No TMDb title: the episode number is dropped from the name instead.
	forceName := entry.New("Star Wars Episode VII The Force Awakens 2015 1080p BluRay 3D MVC", "u5")
	forceBare := entry.New("Star Wars VII The Force Awakens 2015 1080p 3D BluRay MVC", "u6")
	// Same title as a listed film, different film.
	mulan98 := entry.New("Mulan 1998 1080p 3D BluRay Half-SBS", "u4")
	mulan98.Set(entry.FieldVideoYear, 1998)

	run(t, p, beyond, fight, force, mulan98, forceName, forceBare)

	for _, c := range []struct {
		e    *entry.Entry
		want bool
	}{{beyond, true}, {fight, false}, {force, true}, {mulan98, false}, {forceName, true}, {forceBare, true}} {
		got, set := official(c.e)
		if !set || got != c.want {
			t.Errorf("%s: movie_3d_official = %v (set %v), want %v", c.e.Title, got, set, c.want)
		}
	}
	if native, _ := beyond.Fields[entry.FieldMovie3DNative].(bool); native {
		t.Error("Star Trek Beyond is listed as filmed in 2D")
	}
	if _, set := fight.Fields[entry.FieldMovie3DNative]; set {
		t.Error("movie_3d_native is only set on listed films")
	}
}

func TestListIsFetchedOncePerRefresh(t *testing.T) {
	w := newWiki(t)
	db := openDB(t)
	run(t, newProc(t, db, w, nil), entry.New("Mulan 2020 3D 1080p", "u"))
	// A second plugin instance (the next run, or another pipeline) reads the
	// stored list instead of fetching again.
	run(t, newProc(t, db, w, nil), entry.New("Mulan 2020 3D 1080p", "u"))
	if n := w.hits.Load(); n != 1 {
		t.Errorf("fetched %d times, want 1", n)
	}
}

func TestStaleListUsedWhenRefreshFails(t *testing.T) {
	w := newWiki(t)
	db := openDB(t)
	run(t, newProc(t, db, w, map[string]any{"refresh": "1ms"}), entry.New("Mulan 2020 3D 1080p", "u"))
	time.Sleep(5 * time.Millisecond)
	w.fail.Store(true)

	e := entry.New("Mulan 2020 3D 1080p", "u")
	run(t, newProc(t, db, w, map[string]any{"refresh": "1ms"}), e)
	if got, set := official(e); !set || !got {
		t.Errorf("movie_3d_official = %v (set %v); the expired list should still answer", got, set)
	}
}

func TestNoListLeavesFieldUnset(t *testing.T) {
	w := newWiki(t)
	w.fail.Store(true)
	e := entry.New("Fight Club (1999) BD50 MVC", "u")
	run(t, newProc(t, openDB(t), w, nil), e)
	if _, set := official(e); set {
		t.Error("with no list ever read, movie_3d_official must stay unset so a gate refuses")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{"refresh": "nope"}); len(errs) == 0 {
		t.Error("bad duration accepted")
	}
	if errs := validate(map[string]any{"pages": []string{"x"}}); len(errs) == 0 {
		t.Error("unknown key accepted")
	}
	if errs := validate(map[string]any{"refresh": "24h"}); len(errs) != 0 {
		t.Errorf("valid config refused: %v", errs)
	}
}

func TestRefreshNeverForgetsAFilm(t *testing.T) {
	w := newWiki(t)
	db := openDB(t)
	run(t, newProc(t, db, w, map[string]any{"refresh": "1ms"}), entry.New("x", "u"))
	time.Sleep(5 * time.Millisecond)

	// The page now lists something else entirely.
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		_, _ = rw.Write([]byte("====2013====\n{|\n|-\n|''[[Gravity (2013 film)|Gravity]]''\n|October 4, 2013\n|}\n"))
	}))
	t.Cleanup(srv.Close)
	p := newProc(t, db, w, map[string]any{"refresh": "1ms"})
	p.baseURL, p.client = srv.URL, srv.Client()

	mulan, gravity := entry.New("Mulan 2020 3D 1080p", "u1"), entry.New("Gravity 2013 3D 1080p", "u2")
	run(t, p, mulan, gravity)
	for _, e := range []*entry.Entry{mulan, gravity} {
		if got, _ := official(e); !got {
			t.Errorf("%s: movie_3d_official = false after a refresh", e.Title)
		}
	}
}
