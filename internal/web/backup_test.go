package web

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

// TestDBBackupDownload takes a live snapshot and verifies it is a valid
// SQLite database containing the store's data.
func TestDBBackupDownload(t *testing.T) {
	dir := t.TempDir()
	db, err := store.OpenSQLite(filepath.Join(dir, "live.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Bucket("movies").Put("inception|2010", map[string]any{"title": "inception"}); err != nil {
		t.Fatal(err)
	}

	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetStore(db)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/db/backup", srv.apiDBBackup)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/db/backup", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("http %d", resp.StatusCode)
	}
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "pipeliner-") || !strings.Contains(cd, ".db") {
		t.Errorf("content-disposition: %q", cd)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(data) == 0 || !strings.HasPrefix(string(data[:16]), "SQLite format 3") {
		t.Fatalf("payload is not a SQLite database (%d bytes)", len(data))
	}

	// The snapshot must open as a store and contain the row.
	snap := filepath.Join(dir, "snap.db")
	if err := os.WriteFile(snap, data, 0o600); err != nil {
		t.Fatal(err)
	}
	db2, err := store.OpenSQLite(snap)
	if err != nil {
		t.Fatalf("snapshot does not open as a store: %v", err)
	}
	defer db2.Close()
	var rec map[string]any
	if found, _ := db2.Bucket("movies").Get("inception|2010", &rec); !found {
		t.Error("snapshot is missing the tracker row")
	}
}

func TestDBBackupNoStore(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/db/backup", srv.apiDBBackup)
	ts := httptest.NewServer(mux)
	defer ts.Close()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+"/api/db/backup", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Errorf("want 501 without a store, got %d", resp.StatusCode)
	}
}
