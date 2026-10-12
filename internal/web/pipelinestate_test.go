package web

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/store"
)

func newStateTestServer(t *testing.T, live ...string) (*Server, *httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)
	infos := make([]TaskInfo, 0, len(live))
	for _, n := range live {
		infos = append(infos, TaskInfo{Name: n})
	}
	srv.SetTasks(infos)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/pipelines/state", srv.apiPipelineState)
	mux.HandleFunc("POST /api/pipelines/state/move", srv.apiPipelineStateMove)
	mux.HandleFunc("DELETE /api/pipelines/state/{name}", srv.apiPipelineStateForget)
	return srv, httptest.NewServer(mux), db
}

func seedSeen(t *testing.T, db *store.SQLiteStore, bucket, key, title string) {
	t.Helper()
	if err := db.Bucket(bucket).Put(key, store.SeenRecord{
		Fingerprint: key, Title: title, SeenAt: time.Now(),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAPIPipelineStateFlagsOrphans(t *testing.T) {
	_, ts, db := newStateTestServer(t, "alpha")
	defer ts.Close()
	seedSeen(t, db, "seen:alpha", "k1", "one")
	seedSeen(t, db, "seen:ghost", "k1", "two")

	resp, err := ts.Client().Get(ts.URL + "/api/pipelines/state")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out struct {
		Pipelines []struct {
			Pipeline string `json:"pipeline"`
			Orphaned bool   `json:"orphaned"`
			Rows     int    `json:"rows"`
		} `json:"pipelines"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	if len(out.Pipelines) != 2 {
		t.Fatalf("got %d pipelines, want 2", len(out.Pipelines))
	}
	byName := map[string]bool{}
	for _, p := range out.Pipelines {
		byName[p.Pipeline] = p.Orphaned
	}
	if byName["alpha"] {
		t.Error("alpha is in the config and must not be flagged")
	}
	if !byName["ghost"] {
		t.Error("ghost has no pipeline and must be flagged")
	}
}

func TestAPIMoveRenamesState(t *testing.T) {
	_, ts, db := newStateTestServer(t, "new")
	defer ts.Close()
	seedSeen(t, db, "seen:old", "k1", "one")

	resp := postJSON(t, ts.URL+"/api/pipelines/state/move", map[string]any{"from": "old", "to": "new"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rows, err := db.Bucket("seen:new").All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("seen:new has %d records, want 1", len(rows))
	}
}

// TestAPIMoveReportsAConflictAsConflict: a collision that needs a human choice
// is the caller's to resolve, so it must not read as a server fault.
func TestAPIMoveReportsAConflictAsConflict(t *testing.T) {
	_, ts, db := newStateTestServer(t, "dst")
	defer ts.Close()
	if err := db.Bucket("series_gaps:src").Put("cursor", map[string]string{"key": "a"}); err != nil {
		t.Fatal(err)
	}
	if err := db.Bucket("series_gaps:dst").Put("cursor", map[string]string{"key": "b"}); err != nil {
		t.Fatal(err)
	}

	resp := postJSON(t, ts.URL+"/api/pipelines/state/move", map[string]any{"from": "src", "to": "dst"})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
}

func TestAPIMoveDryRunWritesNothing(t *testing.T) {
	_, ts, db := newStateTestServer(t, "new")
	defer ts.Close()
	seedSeen(t, db, "seen:old", "k1", "one")

	resp := postJSON(t, ts.URL+"/api/pipelines/state/move",
		map[string]any{"from": "old", "to": "new", "dry_run": true})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rows, err := db.Bucket("seen:old").All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Errorf("a dry run moved %d records out of the source, want 0 moved", 1-len(rows))
	}
}

// TestAPIForgetRefusesALivePipeline: dropping the state of a pipeline that
// still exists makes it re-download its whole history on the next run, which
// is never what "forget" is for.
func TestAPIForgetRefusesALivePipeline(t *testing.T) {
	_, ts, db := newStateTestServer(t, "alpha")
	defer ts.Close()
	seedSeen(t, db, "seen:alpha", "k1", "one")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		ts.URL+"/api/pipelines/state/alpha", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusConflict {
		t.Fatalf("status = %d, want 409", resp.StatusCode)
	}
	rows, err := db.Bucket("seen:alpha").All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Error("a refused forget deleted records")
	}
}

func TestAPIForgetDeletesOrphanedState(t *testing.T) {
	_, ts, db := newStateTestServer(t, "alpha")
	defer ts.Close()
	seedSeen(t, db, "seen:ghost", "k1", "one")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodDelete,
		ts.URL+"/api/pipelines/state/ghost", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	rows, err := db.Bucket("seen:ghost").All()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Errorf("seen:ghost still has %d records", len(rows))
	}
}

// TestOrphanWarningNamesTheRecoveryPath is what a config save returns after a
// rename. The warning has to be actionable: the records are still there, and
// the person who just renamed the pipeline is the only one who knows whether
// this was a rename or a deletion.
func TestOrphanWarningNamesTheRecoveryPath(t *testing.T) {
	srv, ts, db := newStateTestServer(t, "alpha")
	defer ts.Close()
	seedSeen(t, db, "seen:ghost", "k1", "one")
	seedSeen(t, db, "seen:alpha", "k1", "two")

	warns := srv.orphanStateWarnings()
	if len(warns) != 1 {
		t.Fatalf("got %d warnings, want 1: %v", len(warns), warns)
	}
	for _, want := range []string{"ghost", "seen:ghost", "orphaned", "kept, not deleted"} {
		if !strings.Contains(warns[0], want) {
			t.Errorf("warning does not mention %q: %s", want, warns[0])
		}
	}
	if strings.Contains(warns[0], "alpha") {
		t.Errorf("warning names a live pipeline: %s", warns[0])
	}
}

// TestNoWarningsBeforeAnyConfigIsLoaded: with no tasks the live list is
// unknown, and calling every bucket an orphan would invite deleting all of it.
func TestNoWarningsBeforeAnyConfigIsLoaded(t *testing.T) {
	srv, ts, db := newStateTestServer(t)
	defer ts.Close()
	seedSeen(t, db, "seen:alpha", "k1", "one")

	if warns := srv.orphanStateWarnings(); len(warns) != 0 {
		t.Errorf("got %d warnings with no config loaded, want 0: %v", len(warns), warns)
	}
}
