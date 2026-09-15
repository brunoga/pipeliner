package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

func histGet(t *testing.T, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func newHistoryTestServer(t *testing.T) (*Server, *httptest.Server, string) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	cfgPath := filepath.Join(t.TempDir(), "config.star")
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)
	srv.SetConfigPath(cfgPath)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/config/history", srv.apiConfigHistory)
	mux.HandleFunc("GET /api/config/history/{id}", srv.apiConfigHistoryGet)
	return srv, httptest.NewServer(mux), cfgPath
}

func TestConfigHistorySnapshotAndFetch(t *testing.T) {
	srv, ts, cfgPath := newHistoryTestServer(t)
	defer ts.Close()

	// No file yet → snapshot is a no-op.
	srv.snapshotConfigForHistory([]byte("v1"))
	// Write v1, then "save" v2 → v1 is snapshotted.
	if err := os.WriteFile(cfgPath, []byte("v1 content"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv.snapshotConfigForHistory([]byte("v2 content"))
	// Unchanged content → no snapshot.
	srv.snapshotConfigForHistory([]byte("v1 content"))

	var list struct {
		Versions []struct {
			ID   string `json:"id"`
			Size int    `json:"size"`
		} `json:"versions"`
	}
	resp := histGet(t, ts.URL+"/api/config/history")
	json.NewDecoder(resp.Body).Decode(&list) //nolint:errcheck
	resp.Body.Close()
	if len(list.Versions) != 1 {
		t.Fatalf("want 1 snapshot, got %d", len(list.Versions))
	}
	if list.Versions[0].Size != len("v1 content") {
		t.Errorf("size = %d", list.Versions[0].Size)
	}

	var got struct {
		Content string `json:"content"`
	}
	resp = histGet(t, ts.URL+"/api/config/history/"+list.Versions[0].ID)
	json.NewDecoder(resp.Body).Decode(&got) //nolint:errcheck
	resp.Body.Close()
	if got.Content != "v1 content" {
		t.Errorf("content = %q", got.Content)
	}

	// Unknown id → 404.
	resp = histGet(t, ts.URL+"/api/config/history/nope")
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("want 404, got %d", resp.StatusCode)
	}
}

func TestConfigHistoryPrunes(t *testing.T) {
	srv, ts, cfgPath := newHistoryTestServer(t)
	defer ts.Close()

	for i := 0; i < configHistoryKeep+5; i++ {
		if err := os.WriteFile(cfgPath, []byte(fmt.Sprintf("version %d", i)), 0o600); err != nil {
			t.Fatal(err)
		}
		srv.snapshotConfigForHistory([]byte("next"))
	}

	var list struct {
		Versions []struct{ ID string } `json:"versions"`
	}
	resp := histGet(t, ts.URL+"/api/config/history")
	json.NewDecoder(resp.Body).Decode(&list) //nolint:errcheck
	resp.Body.Close()
	if len(list.Versions) != configHistoryKeep {
		t.Errorf("want %d kept, got %d", configHistoryKeep, len(list.Versions))
	}
}
