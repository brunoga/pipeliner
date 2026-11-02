package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestFrameResolution(t *testing.T) {
	cases := []struct {
		w, h int
		want string
	}{
		{3840, 1080, "1080p"}, // full side-by-side 1080p
		{3840, 800, "1080p"},  // full side-by-side 1080p, scope
		{7680, 2160, "2160p"}, // full side-by-side 2160p
		{1920, 2160, "1080p"}, // full over-under 1080p
		{1920, 1608, "1080p"}, // full over-under 1080p, scope
		{2880, 1080, "1080p"}, // full side-by-side of a 4:3 film: under 3:1, still 1080p by width
		{1920, 1080, "1080p"}, // half side-by-side, and plain 1080p
		{1920, 800, "1080p"},  // scope 1080p: the height alone says 720p
		{3840, 1600, "2160p"}, // scope 2160p
		{3840, 2160, "2160p"},
		{1280, 536, "720p"},
		{720, 576, "480p"},
		{640, 480, "480p"},
		{0, 1080, ""},
		{1920, 0, ""},
	}
	for _, c := range cases {
		if got := frameResolution(c.w, c.h); got != c.want {
			t.Errorf("frameResolution(%d, %d) = %q, want %q", c.w, c.h, got, c.want)
		}
	}
}

// A full-SBS 1080p file is 3840 wide, which Plex reports as "4k". Graded that
// way, the 3D library read as 2160p and refused every 1080p 3D release of a
// film it held ("already in library at 2160p").
func TestPlexGradesFramePackedFilesPerEye(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Directory": []map[string]any{{"key": "1", "type": "movie", "title": "3D Movies"}},
			}})
		case "/library/sections/1/all":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{
					{"type": "movie", "title": "The Bad Guys", "year": 2022,
						"Media": []map[string]any{{"videoResolution": "4k", "width": 3840, "height": 1080}}},
					{"type": "movie", "title": "Avatar", "year": 2009,
						"Media": []map[string]any{{"videoResolution": "4k", "width": 3840, "height": 2160}}},
					{"type": "movie", "title": "Dune", "year": 2021,
						"Media": []map[string]any{{"videoResolution": "1080", "width": 1920, "height": 800}}},
					{"type": "movie", "title": "No Size", "year": 2020,
						"Media": []map[string]any{{"videoResolution": "4k"}}},
				},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c, err := New("plex", srv.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{"The Bad Guys": "1080p", "Avatar": "2160p", "Dune": "1080p", "No Size": "2160p"}
	if len(items) != len(want) {
		t.Fatalf("items: %d, want %d", len(items), len(want))
	}
	for _, it := range items {
		if it.Resolution != want[it.Title] {
			t.Errorf("%s: Resolution = %q, want %q", it.Title, it.Resolution, want[it.Title])
		}
	}
}

func TestJellyfinGradesFramePackedFilesPerEye(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/UserViews", "/Users/me/Views":
			json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{ //nolint:errcheck
				{"Id": "v1", "Name": "3D Movies"},
			}})
		case "/Items":
			if r.URL.Query().Get("IncludeItemTypes") == "Series" {
				json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{}}) //nolint:errcheck
				return
			}
			video := func(w, h int) []map[string]any {
				return []map[string]any{{"Type": "Video", "Width": w, "Height": h}}
			}
			json.NewEncoder(w).Encode(map[string]any{"Items": []map[string]any{ //nolint:errcheck
				{"Id": "m1", "Type": "Movie", "Name": "Over Under", "ProductionYear": 2022, "MediaStreams": video(1920, 2160)},
				{"Id": "m2", "Type": "Movie", "Name": "Scope", "ProductionYear": 2021, "MediaStreams": video(1920, 800)},
				{"Id": "m3", "Type": "Movie", "Name": "Height Only", "ProductionYear": 2020, "MediaStreams": video(0, 2160)},
			}})
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
	want := map[string]string{"Over Under": "1080p", "Scope": "1080p", "Height Only": "2160p"}
	got := map[string]string{}
	for _, it := range items {
		if it.Type == "movie" {
			got[it.Title] = it.Resolution
		}
	}
	for title, res := range want {
		if got[title] != res {
			t.Errorf("%s: Resolution = %q, want %q", title, got[title], res)
		}
	}
}
