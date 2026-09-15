package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakePlexTV serves the /api/v2/resources endpoint with two servers: one owned
// (with a dead LAN URI plus a live one) and one shared.
func fakePlexTV(t *testing.T, liveServer string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v2/resources" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Plex-Client-Identifier") == "" {
			http.Error(w, "X-Plex-Client-Identifier is missing", http.StatusBadRequest)
			return
		}
		if r.Header.Get("X-Plex-Token") != "acct-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{ //nolint:errcheck
			{
				"name": "Hex", "provides": "server", "owned": true, "accessToken": "hex-token",
				"connections": []map[string]any{
					{"uri": "https://192-0-2-1.dead.example:32400", "local": true, "relay": false},
					{"uri": liveServer, "local": false, "relay": false},
				},
			},
			{
				"name": "Friend", "provides": "server", "owned": false, "accessToken": "friend-token",
				"connections": []map[string]any{{"uri": liveServer, "local": false, "relay": false}},
			},
			{
				"name": "Player", "provides": "client", "owned": true,
			},
		})
	}))
}

func TestDiscoverPlexServers(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer live.Close()

	tv := fakePlexTV(t, live.URL)
	defer tv.Close()
	orig := PlexTVBaseURL
	PlexTVBaseURL = tv.URL
	defer func() { PlexTVBaseURL = orig }()

	servers, err := DiscoverPlexServers(context.Background(), "acct-token")
	if err != nil {
		t.Fatal(err)
	}
	// The "client" resource is filtered out.
	if len(servers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(servers))
	}
	if servers[0].Name != "Hex" || !servers[0].Owned || servers[0].Token != "hex-token" {
		t.Errorf("Hex: %+v", servers[0])
	}
	if servers[1].Owned {
		t.Error("Friend should not be owned")
	}

	// Connect skips the dead LAN URI and lands on the live one.
	base, err := servers[0].Connect(context.Background())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if base != live.URL {
		t.Errorf("connected to %q, want %q", base, live.URL)
	}
}

func TestConnectNoReachable(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	s := DiscoveredServer{Name: "X", Token: "t",
		conns: []plexConnection{{URI: deadURL}}}
	if _, err := s.Connect(context.Background()); err == nil {
		t.Error("expected error when no connection is reachable")
	}
}

func TestPlexMovieSections(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Directory": []map[string]any{
					{"key": "2", "type": "movie", "title": "3D Movies"},
					{"key": "3", "type": "movie", "title": "Movies"},
					{"key": "4", "type": "show", "title": "TV"},
				},
			}})
		case "/library/sections/2/all":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{{"title": "Avatar", "year": 2009}},
			}})
		case "/library/sections/3/all":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{{"title": "Inception", "year": 2010}, {"title": "Heat", "year": 1995}},
			}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	secs, err := PlexMovieSections(context.Background(), srv.URL, "t")
	if err != nil {
		t.Fatal(err)
	}
	if len(secs) != 2 {
		t.Fatalf("want 2 movie sections (show skipped), got %d", len(secs))
	}
	if secs[0].Name != "3D Movies" || len(secs[0].Items) != 1 || secs[0].Items[0].Title != "Avatar" {
		t.Errorf("3D section: %+v", secs[0])
	}
	if secs[1].Name != "Movies" || len(secs[1].Items) != 2 {
		t.Errorf("Movies section: %+v", secs[1])
	}
}
