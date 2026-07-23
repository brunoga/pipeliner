package trakt_list

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
	itrakt "github.com/brunoga/pipeliner/internal/trakt"
)

func makeCtx(dryRun bool) *plugin.TaskContext {
	return &plugin.TaskContext{
		Name:   "test",
		DryRun: dryRun,
		Logger: slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug})),
	}
}

type capture struct {
	hits   atomic.Int64
	path   string
	method string
	auth   string
	body   itrakt.ListItemsBody
}

func mutationServer(t *testing.T, status int, response string) (*httptest.Server, *capture) {
	t.Helper()
	c := &capture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		c.path = r.URL.Path
		c.method = r.Method
		c.auth = r.Header.Get("Authorization")
		_ = json.NewDecoder(r.Body).Decode(&c.body)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(response))
	}))
	t.Cleanup(srv.Close)
	return srv, c
}

func makeSink(t *testing.T, cfg map[string]any) *traktListSink {
	t.Helper()
	base := map[string]any{"client_id": "cid", "access_token": "tok", "list": "watchlist"}
	for k, v := range cfg {
		base[k] = v
	}
	p, err := newPlugin(base, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*traktListSink)
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
	d, ok := plugin.Lookup("trakt_list_update")
	if !ok {
		t.Fatal("trakt_list_update not registered")
	}
	if d.Role != plugin.RoleSink {
		t.Errorf("role: got %v, want sink", d.Role)
	}
	// One OR group with all usable ID fields.
	if len(d.Requires) != 1 {
		t.Fatalf("requires: got %v, want a single OR group", d.Requires)
	}
	want := []string{"trakt_id", "trakt_imdb_id", "video_imdb_id", "trakt_tmdb_id", "tmdb_id", "trakt_tvdb_id", "tvdb_id"}
	got := map[string]bool{}
	for _, f := range d.Requires[0] {
		got[f] = true
	}
	for _, f := range want {
		if !got[f] {
			t.Errorf("requires group missing %q: %v", f, d.Requires[0])
		}
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		cfg     map[string]any
		wantErr string
	}{
		{"missing list", map[string]any{"client_id": "c", "access_token": "t"}, "list"},
		{"missing client_id", map[string]any{"list": "watchlist", "access_token": "t"}, "client_id"},
		{"missing auth", map[string]any{"client_id": "c", "list": "watchlist"}, "client_secret"},
		{"bad action", map[string]any{"client_id": "c", "access_token": "t", "list": "watchlist", "action": "clear"}, "action"},
		{"bad type", map[string]any{"client_id": "c", "access_token": "t", "list": "watchlist", "type": "books"}, "type"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			errs := validate(tc.cfg)
			if len(errs) == 0 {
				t.Fatal("expected a validation error")
			}
			found := false
			for _, err := range errs {
				if strings.Contains(err.Error(), tc.wantErr) {
					found = true
				}
			}
			if !found {
				t.Errorf("no error mentions %q: %v", tc.wantErr, errs)
			}
		})
	}

	if errs := validate(map[string]any{"client_id": "c", "access_token": "t", "list": "watchlist", "action": "remove", "type": "shows"}); len(errs) != 0 {
		t.Errorf("valid config should pass, got %v", errs)
	}
}

func TestFactoryDefaults(t *testing.T) {
	sink := makeSink(t, nil)
	if sink.action != "add" {
		t.Errorf("default action: got %q, want add", sink.action)
	}
	if sink.itemType != "" {
		t.Errorf("default type: got %q, want empty (infer)", sink.itemType)
	}
}

func TestFactoryRequiresAuth(t *testing.T) {
	if _, err := newPlugin(map[string]any{"client_id": "c", "list": "watchlist"}, nil); err == nil {
		t.Fatal("expected error without client_secret or access_token")
	}
}

func TestDryRunMakesNoHTTPCall(t *testing.T) {
	srv, cap := mutationServer(t, http.StatusCreated, `{}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, map[string]any{"action": "remove"})
	e := acceptedEntry("Severance", map[string]any{"trakt_id": 42, entry.FieldMediaType: entry.MediaTypeSeries})
	if err := sink.Consume(context.Background(), makeCtx(true), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if n := cap.hits.Load(); n != 0 {
		t.Errorf("dry-run made %d HTTP calls, want 0", n)
	}
	if !strings.Contains(e.AcceptReason, "would remove") {
		t.Errorf("accept reason: got %q, want a would-remove preview", e.AcceptReason)
	}
}

func TestConsumeBatchesOneRequest(t *testing.T) {
	srv, cap := mutationServer(t, http.StatusCreated,
		`{"added":{"shows":2,"movies":1},"not_found":{}}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, nil) // action=add, type inferred from media_type
	entries := []*entry.Entry{
		acceptedEntry("Show A", map[string]any{"trakt_id": 1, entry.FieldMediaType: entry.MediaTypeSeries}),
		acceptedEntry("Show B", map[string]any{"trakt_tvdb_id": 22, entry.FieldMediaType: entry.MediaTypeSeries}),
		acceptedEntry("Movie C", map[string]any{"tmdb_id": 333, entry.FieldMediaType: entry.MediaTypeMovie}),
	}
	if err := sink.Consume(context.Background(), makeCtx(false), entries); err != nil {
		t.Fatalf("Consume: %v", err)
	}

	if n := cap.hits.Load(); n != 1 {
		t.Fatalf("want exactly 1 batched request, got %d", n)
	}
	if cap.method != http.MethodPost {
		t.Errorf("method: got %q", cap.method)
	}
	if cap.path != "/sync/watchlist" {
		t.Errorf("path: got %q, want /sync/watchlist", cap.path)
	}
	if cap.auth != "Bearer tok" {
		t.Errorf("auth: got %q", cap.auth)
	}
	if len(cap.body.Shows) != 2 {
		t.Errorf("shows: got %+v, want 2 items", cap.body.Shows)
	}
	if len(cap.body.Movies) != 1 {
		t.Errorf("movies: got %+v, want 1 item", cap.body.Movies)
	}
	if cap.body.Shows[0].IDs.Trakt != 1 || cap.body.Shows[1].IDs.TVDB != 22 || cap.body.Movies[0].IDs.TMDB != 333 {
		t.Errorf("ids: shows=%+v movies=%+v", cap.body.Shows, cap.body.Movies)
	}
}

func TestConsumeRemoveUsesRemoveEndpoint(t *testing.T) {
	srv, cap := mutationServer(t, http.StatusOK, `{"deleted":{"shows":1},"not_found":{}}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, map[string]any{"action": "remove", "list": "my-slug", "type": "shows"})
	e := acceptedEntry("Show A", map[string]any{"trakt_id": 1})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if cap.path != "/users/me/lists/my-slug/items/remove" {
		t.Errorf("path: got %q, want /users/me/lists/my-slug/items/remove", cap.path)
	}
}

func TestConsumeFailsEntriesWithoutIDs(t *testing.T) {
	srv, cap := mutationServer(t, http.StatusCreated, `{"added":{"shows":1},"not_found":{}}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, map[string]any{"type": "shows"})
	noID := acceptedEntry("No IDs", map[string]any{"title": "No IDs"})
	ok := acceptedEntry("Show A", map[string]any{"trakt_id": 1})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{noID, ok}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !noID.IsFailed() {
		t.Error("entry without any usable id should be failed")
	}
	if !strings.Contains(noID.FailReason, "no usable") {
		t.Errorf("fail reason: got %q", noID.FailReason)
	}
	if len(cap.body.Shows) != 1 {
		t.Errorf("only the entry with ids should be batched, got %+v", cap.body.Shows)
	}
}

func TestConsumeFailsEntriesWithUnknownMediaType(t *testing.T) {
	srv, cap := mutationServer(t, http.StatusCreated, `{}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, nil) // no type: infer from media_type
	e := acceptedEntry("Mystery", map[string]any{"trakt_id": 1})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !e.IsFailed() {
		t.Error("entry without media_type should be failed when type is not configured")
	}
	if n := cap.hits.Load(); n != 0 {
		t.Errorf("no request should be sent, got %d", n)
	}
}

func TestConsumeFailsNotFoundEntries(t *testing.T) {
	srv, _ := mutationServer(t, http.StatusCreated,
		`{"added":{"shows":1},"not_found":{"shows":[{"ids":{"tvdb":999}}]}}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, map[string]any{"type": "shows"})
	found := acceptedEntry("Found", map[string]any{"trakt_id": 1})
	missing := acceptedEntry("Missing", map[string]any{"trakt_tvdb_id": 999})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{found, missing}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if found.IsFailed() {
		t.Error("matched entry should not be failed")
	}
	if !missing.IsFailed() {
		t.Error("not_found entry should be failed")
	}
}

func TestConsumeFailsAllOnHTTPError(t *testing.T) {
	srv, _ := mutationServer(t, http.StatusInternalServerError, `{}`)
	itrakt.BaseURL = srv.URL

	sink := makeSink(t, map[string]any{"type": "shows"})
	e := acceptedEntry("Show A", map[string]any{"trakt_id": 1})
	if err := sink.Consume(context.Background(), makeCtx(false), []*entry.Entry{e}); err != nil {
		t.Fatalf("Consume: %v", err)
	}
	if !e.IsFailed() {
		t.Error("entry should be failed when the batch request errors")
	}
}

func TestItemIDsFallbacks(t *testing.T) {
	e := entry.New("X", "u")
	e.Set("tvdb_id", "371980") // string form, as set by tvdb_favorites/metainfo_tvdb
	e.Set("video_imdb_id", "tt1234")
	ids := itemIDs(e)
	if ids.TVDB != 371980 {
		t.Errorf("tvdb: got %d, want 371980", ids.TVDB)
	}
	if ids.IMDB != "tt1234" {
		t.Errorf("imdb: got %q, want tt1234", ids.IMDB)
	}
	if ids.Trakt != 0 || ids.TMDB != 0 {
		t.Errorf("unexpected ids: %+v", ids)
	}

	// trakt_* fields take precedence over the generic ones.
	e2 := entry.New("Y", "u")
	e2.Set("trakt_tmdb_id", 7)
	e2.Set("tmdb_id", 8)
	if got := itemIDs(e2).TMDB; got != 7 {
		t.Errorf("tmdb precedence: got %d, want 7", got)
	}
}
