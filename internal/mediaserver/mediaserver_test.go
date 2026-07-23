package mediaserver

import (
	"context"
	"net/http"
	"net/http/httptest"
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
			if r.URL.Query().Get("type") != "4" {
				t.Errorf("show section should request type=4, got %q", r.URL.Query().Get("type"))
			}
			w.Write([]byte(`{"MediaContainer":{"Metadata":[
				{"type":"episode","grandparentTitle":"Breaking Bad","parentIndex":1,"index":2,
				 "Media":[{"videoResolution":"1080"}]}]}}`))
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
		case "/Items":
			w.Write([]byte(`{"Items":[
				{"Type":"Episode","SeriesName":"Severance","ParentIndexNumber":2,"IndexNumber":10,
				 "MediaStreams":[{"Type":"Audio"},{"Type":"Video","Height":716}]},
				{"Type":"Movie","Name":"Dune Part Two","ProductionYear":2024,
				 "MediaStreams":[{"Type":"Video","Height":2160}]}]}`))
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
	// 716px scan lines bucket to 720p (matte-cropped encodes are common).
	if items[0].EpisodeID() != "S02E10" || items[0].Resolution != "720p" {
		t.Errorf("episode: %+v", items[0])
	}
	if items[1].Resolution != "2160p" {
		t.Errorf("movie: %+v", items[1])
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
