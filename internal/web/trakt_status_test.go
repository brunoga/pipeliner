package web

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/trakt"
)

func newTraktStatusServer(t *testing.T) (*httptest.Server, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "u", "p")
	srv.SetStore(db)
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/trakt/status", srv.apiTraktStatus)
	return httptest.NewServer(mux), db
}

func TestApiTraktStatus(t *testing.T) {
	hs, db := newTraktStatusServer(t)
	defer hs.Close()

	bucket := db.Bucket(trakt.AuthBucket)
	if err := trakt.SaveToken(bucket, "cid", &trakt.Token{
		AccessToken: "a", RefreshToken: "r", ExpiresIn: 7776000, CreatedAt: time.Now().Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	resp := traceGet(t, hs.URL+"/api/trakt/status")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var out struct {
		Tokens []trakt.TokenStatus `json:"tokens"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Tokens) != 1 {
		t.Fatalf("expected 1 token, got %d", len(out.Tokens))
	}
	tk := out.Tokens[0]
	if tk.ClientID != "cid" || tk.Expired || !tk.Refreshable || tk.NeedsReauth {
		t.Errorf("unexpected status: %+v", tk)
	}
}

func TestApiTraktStatusEmpty(t *testing.T) {
	hs, _ := newTraktStatusServer(t)
	defer hs.Close()
	resp := traceGet(t, hs.URL+"/api/trakt/status")
	defer resp.Body.Close()
	var out struct {
		Tokens []trakt.TokenStatus `json:"tokens"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if len(out.Tokens) != 0 {
		t.Errorf("expected no tokens, got %d", len(out.Tokens))
	}
}
