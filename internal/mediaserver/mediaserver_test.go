package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPlexListItemsAndRefresh(t *testing.T) {
	refreshed := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") != "tok" {
			t.Errorf("missing plex token on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/library/sections":
			w.Write([]byte(`{"MediaContainer":{"Directory":[
				{"key":"1","type":"show"},{"key":"2","type":"movie"},{"key":"3","type":"photo"}]}}`))
		case "/library/sections/1/all":
			switch r.URL.Query().Get("type") {
			case "2": // the show listing, read for its provider ids
				if r.URL.Query().Get("includeGuids") != "1" {
					t.Errorf("show listing should ask for guids, got %q", r.URL.RawQuery)
				}
				w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"ratingKey":"77","Guid":[{"id":"imdb://tt1"},{"id":"tvdb://81189"}]}]}}`))
			case "4": // the episode leaves
				w.Write([]byte(`{"MediaContainer":{"Metadata":[
					{"type":"episode","grandparentTitle":"Breaking Bad","parentIndex":1,"index":2,
					 "grandparentRatingKey":"77","Media":[{"videoResolution":"1080"}]}]}}`))
			default:
				t.Errorf("show section asked for type=%q", r.URL.Query().Get("type"))
			}
		case "/library/sections/2/all":
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"type":"movie","title":"Dune Part Two","year":2024,"Media":[{"videoResolution":"4k"}]}]}}`))
		case "/library/sections/all/refresh":
			refreshed = true
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c, err := New("plex", ts.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d: %+v", len(items), items)
	}
	ep, mv := items[0], items[1]
	if ep.Type != "episode" || ep.Show != "Breaking Bad" || ep.EpisodeID() != "S01E02" || ep.Resolution != "1080p" {
		t.Errorf("episode: %+v", ep)
	}
	// The SHOW's id, joined via grandparentRatingKey -- not the episode's own.
	if ep.ShowTVDBID != "81189" {
		t.Errorf("ShowTVDBID = %q, want 81189", ep.ShowTVDBID)
	}
	if mv.Type != "movie" || mv.Title != "Dune Part Two" || mv.Year != 2024 || mv.Resolution != "2160p" {
		t.Errorf("movie: %+v", mv)
	}
	if err := c.Refresh(context.Background()); err != nil || !refreshed {
		t.Errorf("refresh: err=%v hit=%v", err, refreshed)
	}
}

func TestJellyfinListItemsAndRefresh(t *testing.T) {
	refreshed := false
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Emby-Token") != "tok" {
			t.Errorf("missing jellyfin token on %s", r.URL.Path)
		}
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			w.Write([]byte(`[{"ItemId":"v1","Name":"Movies"},{"ItemId":"v2","Name":"TV Shows"}]`))
		case "/Items":
			// The series listing, read for its provider ids. Checked before
			// ParentId so it is answered for whichever library asks.
			if r.URL.Query().Get("IncludeItemTypes") == "Series" {
				w.Write([]byte(`{"Items":[
					{"Id":"s9","ProviderIds":{"Imdb":"tt2","Tvdb":"371980"}}]}`))
				return
			}
			// Items are fetched per library, so each carries its Section.
			switch r.URL.Query().Get("ParentId") {
			case "v1":
				w.Write([]byte(`{"Items":[
					{"Type":"Movie","Name":"Dune Part Two","ProductionYear":2024,
					 "MediaStreams":[{"Type":"Video","Height":2160}]}]}`))
			case "v2":
				w.Write([]byte(`{"Items":[
					{"Type":"Episode","SeriesName":"Severance","SeriesId":"s9",
					 "ParentIndexNumber":2,"IndexNumber":10,
					 "MediaStreams":[{"Type":"Audio"},{"Type":"Video","Height":716}]}]}`))
			default:
				t.Errorf("Items without a ParentId: %s", r.URL.RawQuery)
			}
		case "/Library/Refresh":
			if r.Method != http.MethodPost {
				t.Errorf("refresh method: %s", r.Method)
			}
			refreshed = true
			w.WriteHeader(http.StatusNoContent)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c, err := New("jellyfin", ts.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %d", len(items))
	}
	movie, episode := items[0], items[1]
	if movie.Resolution != "2160p" || movie.Section != "Movies" {
		t.Errorf("movie: %+v", movie)
	}
	// 716px scan lines bucket to 720p (matte-cropped encodes are common).
	if episode.EpisodeID() != "S02E10" || episode.Resolution != "720p" || episode.Section != "TV Shows" {
		t.Errorf("episode: %+v", episode)
	}
	// Joined from the series listing via SeriesId; the provider key is matched
	// case-insensitively because Jellyfin has spelt it Tvdb/TVDB/tvdb.
	if episode.ShowTVDBID != "371980" {
		t.Errorf("ShowTVDBID = %q, want 371980", episode.ShowTVDBID)
	}
	if err := c.Refresh(context.Background()); err != nil || !refreshed {
		t.Errorf("refresh: err=%v hit=%v", err, refreshed)
	}
}

func TestUnsupportedBackend(t *testing.T) {
	if _, err := New("emby", "http://x", "t"); err == nil {
		t.Fatal("unsupported backend must error")
	}
}

func TestNormalizeResolution(t *testing.T) {
	cases := map[string]string{"4k": "2160p", "2160": "2160p", "1080": "1080p",
		"1080p": "1080p", "720": "720p", "sd": "480p", "576": "480p", "weird": ""}
	for in, want := range cases {
		if got := normalizeResolution(in); got != want {
			t.Errorf("normalizeResolution(%q) = %q, want %q", in, got, want)
		}
	}
}

// A server that will not list its libraries (older build, restricted token)
// falls back to the flat sweep. Items then carry no Section, which the library
// filter reports as "section filter unusable" rather than silently matching
// nothing.
func TestJellyfinListItemsWithoutVirtualFolders(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/Library/VirtualFolders":
			w.WriteHeader(http.StatusForbidden)
		case "/Items":
			if r.URL.Query().Get("ParentId") != "" {
				t.Errorf("fallback must not scope by ParentId: %s", r.URL.RawQuery)
			}
			w.Write([]byte(`{"Items":[
				{"Type":"Movie","Name":"Dune Part Two","ProductionYear":2024,
				 "MediaStreams":[{"Type":"Video","Height":2160}]}]}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer ts.Close()

	c, err := New("jellyfin", ts.URL, "tok")
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 || items[0].Section != "" {
		t.Errorf("items: %+v", items)
	}
}

// TestPlexMovieProviderIDs: a film's own ids are in its listing row, so asking
// for guids on the movie listing costs a bigger response rather than another
// request.
func TestPlexMovieProviderIDs(t *testing.T) {
	var movieQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{
				"Directory": []map[string]any{{"key": "9", "type": "movie", "title": "Movies"}},
			}})
		case strings.HasPrefix(r.URL.Path, "/library/sections/9/all"):
			movieQuery = r.URL.RawQuery
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{
				"Metadata": []map[string]any{{
					"type": "movie", "title": "Dune: Part Two", "year": 2024,
					"ratingKey": "101",
					"Guid": []map[string]any{
						{"id": "imdb://tt15239678"},
						{"id": "tmdb://693134?lang=en"},
						{"id": "tvdb://373242"},
					},
					"Media": []map[string]any{{"videoResolution": "4k"}},
				}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &plexClient{base: srv.URL, token: "t", http: srv.Client()}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("got %d items, want 1", len(items))
	}
	if !strings.Contains(movieQuery, "includeGuids=1") {
		t.Errorf("movie listing query = %q, want includeGuids=1", movieQuery)
	}
	if got := items[0].MovieTMDBID; got != "693134" {
		t.Errorf("MovieTMDBID = %q, want 693134 (agent suffix stripped)", got)
	}
	if got := items[0].MovieIMDBID; got != "tt15239678" {
		t.Errorf("MovieIMDBID = %q, want tt15239678", got)
	}
}

// TestPlexMovieWithoutGuidsCarriesNoIDs: a server exposing no provider ids
// must leave the fields empty so callers fall back to the title.
func TestPlexMovieWithoutGuidsCarriesNoIDs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{
				"Directory": []map[string]any{{"key": "9", "type": "movie", "title": "Movies"}},
			}})
		case strings.HasPrefix(r.URL.Path, "/library/sections/9/all"):
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{
				"Metadata": []map[string]any{{"type": "movie", "title": "Dune", "year": 2021, "ratingKey": "1"}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &plexClient{base: srv.URL, token: "t", http: srv.Client()}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if items[0].MovieTMDBID != "" || items[0].MovieIMDBID != "" {
		t.Errorf("want no ids, got tmdb=%q imdb=%q", items[0].MovieTMDBID, items[0].MovieIMDBID)
	}
}

// TestJellyfinMovieProviderIDs: ProviderIds comes back on the item, and
// Jellyfin's keys are not case-stable across versions.
func TestJellyfinMovieProviderIDs(t *testing.T) {
	var fields string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/UserViews", "/Users/me/Views":
			json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{
				{"Id": "v1", "Name": "Movies"},
			}})
		case "/Items":
			if r.URL.Query().Get("IncludeItemTypes") == "Series" {
				json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{}})
				return
			}
			fields = r.URL.Query().Get("Fields")
			json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{{
				"Id": "m1", "Type": "Movie", "Name": "Inception", "ProductionYear": 2010,
				"ProviderIds": map[string]string{"TMDB": "27205", "Imdb": "tt1375666"},
			}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := &jellyfinClient{base: srv.URL, token: "t", http: srv.Client()}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatalf("ListItems: %v", err)
	}
	if !strings.Contains(fields, "ProviderIds") {
		t.Errorf("Fields = %q, want it to ask for ProviderIds", fields)
	}
	var movie *Item
	for i := range items {
		if items[i].Type == "movie" {
			movie = &items[i]
		}
	}
	if movie == nil {
		t.Fatalf("no movie in %d items", len(items))
	}
	if movie.MovieTMDBID != "27205" {
		t.Errorf("MovieTMDBID = %q, want 27205 (key case must not matter)", movie.MovieTMDBID)
	}
	if movie.MovieIMDBID != "tt1375666" {
		t.Errorf("MovieIMDBID = %q, want tt1375666", movie.MovieIMDBID)
	}
}
