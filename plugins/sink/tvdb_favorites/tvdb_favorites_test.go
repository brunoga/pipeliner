package tvdb_favorites

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx(dryRun bool) *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		DryRun: dryRun,
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

// favServer mimics the TVDB favorites endpoints. It counts every request and
// records the series IDs POSTed to /user/favorites.
func favServer(t *testing.T, existing []int) (*httptest.Server, *atomic.Int64, *[]int) {
	t.Helper()
	var hits atomic.Int64
	var added []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok"}, "status": "success"}) //nolint:errcheck
		case r.Method == http.MethodGet && r.URL.Path == "/user/favorites":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"series": existing}, "status": "success"}) //nolint:errcheck
		case r.Method == http.MethodPost && r.URL.Path == "/user/favorites":
			var body map[string]int
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad body", http.StatusBadRequest)
				return
			}
			added = append(added, body["series"])
			w.WriteHeader(http.StatusCreated)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &hits, &added
}

func makeSink(t *testing.T, srvURL string) *tvdbFavoritesSink {
	t.Helper()
	p, err := newPlugin(map[string]any{"api_key": "key", "user_pin": "pin"}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	sink := p.(*tvdbFavoritesSink)
	sink.client.BaseURL = srvURL
	return sink
}

func acceptedEntry(title string, fields map[string]any) *entry.Entry {
	e := entry.New(title, "https://example.com/"+title)
	for k, v := range fields {
		e.Set(k, v)
	}
	e.Accept("test")
	return e
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("tvdb_favorites_add")
	if !ok {
		t.Fatal("tvdb_favorites_add not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("role: got %v, want sink", d.Role)
	}
	if len(d.Requires) != 1 || len(d.Requires[0]) != 1 || d.Requires[0][0] != "tvdb_id" {
		t.Errorf("requires: got %v, want [[tvdb_id]]", d.Requires)
	}
}

func TestValidateRequiredKeys(t *testing.T) {
	errs := validate(map[string]any{})
	if len(errs) < 2 {
		t.Fatalf("expected errors for missing api_key and user_pin, got %v", errs)
	}
}

func TestValidateRejectsRemoveAction(t *testing.T) {
	errs := validate(map[string]any{"api_key": "k", "user_pin": "p", "action": "remove"})
	if len(errs) != 1 {
		t.Fatalf("expected exactly one error, got %v", errs)
	}
	msg := errs[0].Error()
	if !strings.Contains(msg, "no favorites-removal endpoint") {
		t.Errorf("error should explain the API limitation, got %q", msg)
	}
	if !strings.Contains(msg, "trakt_list_update") || !strings.Contains(msg, "list_add") {
		t.Errorf("error should point at the local-list/trakt alternatives, got %q", msg)
	}
}

func TestValidateAcceptsAddAction(t *testing.T) {
	if errs := validate(map[string]any{"api_key": "k", "user_pin": "p", "action": "add"}); len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
}

func TestFactoryRejectsRemoveAction(t *testing.T) {
	if _, err := newPlugin(map[string]any{"api_key": "k", "user_pin": "p", "action": "remove"}, nil); err == nil {
		t.Fatal("expected factory error for action=remove")
	}
}

func TestDryRunMakesNoHTTPCall(t *testing.T) {
	srv, hits, _ := favServer(t, nil)
	sink := makeSink(t, srv.URL)

	e := acceptedEntry("Severance", map[string]any{"tvdb_id": "371980"})
	if err := sink.Consume(context.Background(), makeCtx(true), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("dry-run made %d HTTP calls, want 0", n)
	}
	if !strings.Contains(e.AcceptReason, "would add") {
		t.Errorf("accept reason: got %q, want a would-add preview", e.AcceptReason)
	}
}

func TestConsumeAddsFavorite(t *testing.T) {
	srv, _, added := favServer(t, []int{111})
	sink := makeSink(t, srv.URL)

	e := acceptedEntry("Severance", map[string]any{"tvdb_id": "371980"})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(*added) != 1 || (*added)[0] != 371980 {
		t.Errorf("added: got %v, want [371980]", *added)
	}
	if !e.IsAccepted() || e.IsConsumed() {
		t.Errorf("entry should stay accepted and unconsumed, state=%v consumed=%v", e.State, e.IsConsumed())
	}
}

func TestConsumeSkipsAlreadyFavorited(t *testing.T) {
	srv, _, added := favServer(t, []int{371980})
	sink := makeSink(t, srv.URL)

	e := acceptedEntry("Severance", map[string]any{"tvdb_id": "371980"})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if len(*added) != 0 {
		t.Errorf("no add should be posted, got %v", *added)
	}
	if !e.IsConsumed() {
		t.Error("already-favorited entry should be consumed")
	}
}

func TestConsumeFailsEntriesWithoutID(t *testing.T) {
	srv, _, added := favServer(t, nil)
	sink := makeSink(t, srv.URL)

	noID := acceptedEntry("Mystery Show", nil)
	badID := acceptedEntry("Bad ID", map[string]any{"tvdb_id": "not-a-number"})
	ok := acceptedEntry("Severance", map[string]any{"tvdb_id": 371980}) // int form tolerated
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{noID, badID, ok}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !noID.IsFailed() {
		t.Error("entry without tvdb_id should be failed")
	}
	if !badID.IsFailed() {
		t.Error("entry with unparsable tvdb_id should be failed")
	}
	if noID.FailReason == "" || !strings.Contains(noID.FailReason, "tvdb_id") {
		t.Errorf("fail reason: got %q", noID.FailReason)
	}
	if len(*added) != 1 || (*added)[0] != 371980 {
		t.Errorf("added: got %v, want [371980]", *added)
	}
}

func TestConsumeFailsEntryOnServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/login":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"token": "tok"}, "status": "success"}) //nolint:errcheck
		case r.Method == http.MethodGet && r.URL.Path == "/user/favorites":
			json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"series": []int{}}, "status": "success"}) //nolint:errcheck
		default:
			http.Error(w, "boom", http.StatusInternalServerError)
		}
	}))
	t.Cleanup(srv.Close)
	sink := makeSink(t, srv.URL)

	e := acceptedEntry("Severance", map[string]any{"tvdb_id": "371980"})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !e.IsFailed() {
		t.Error("entry should be failed when the add call errors")
	}
}
