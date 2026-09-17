package mediaserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
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

// TestPlexAccountClient: the account client aggregates every owned server's
// items and refreshes them all; a shared (not owned) server is ignored.
func TestPlexAccountClient(t *testing.T) {
	newFakeServer := func(movie string, refreshed *bool) *httptest.Server {
		return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/identity":
				w.WriteHeader(http.StatusOK)
			case "/library/sections":
				json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
					"Directory": []map[string]any{{"key": "1", "type": "movie", "title": "Movies"}},
				}})
			case "/library/sections/1/all":
				json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
					"Metadata": []map[string]any{{"type": "movie", "title": movie, "year": 2020}},
				}})
			case "/library/sections/all/refresh":
				*refreshed = true
				w.WriteHeader(http.StatusOK)
			default:
				http.NotFound(w, r)
			}
		}))
	}
	var r1, r2 bool
	srv1 := newFakeServer("Alpha", &r1)
	defer srv1.Close()
	srv2 := newFakeServer("Beta", &r2)
	defer srv2.Close()

	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{ //nolint:errcheck
			{"name": "S1", "provides": "server", "owned": true, "accessToken": "t1",
				"connections": []map[string]any{{"uri": srv1.URL, "local": false, "relay": false}}},
			{"name": "S2", "provides": "server", "owned": true, "accessToken": "t2",
				"connections": []map[string]any{{"uri": srv2.URL, "local": false, "relay": false}}},
			{"name": "Friend", "provides": "server", "owned": false, "accessToken": "t3",
				"connections": []map[string]any{{"uri": srv1.URL, "local": false, "relay": false}}},
		})
	}))
	defer tv.Close()
	orig := PlexTVBaseURL
	PlexTVBaseURL = tv.URL
	defer func() { PlexTVBaseURL = orig }()

	c := NewPlexAccount(func() string { return "acct" })
	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 2 { // Alpha + Beta; Friend's server not owned
		t.Fatalf("items: %+v", items)
	}
	if err := c.Refresh(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r1 || !r2 {
		t.Errorf("both owned servers should refresh: %v %v", r1, r2)
	}

	// Not signed in → clear error, no plex.tv call needed.
	c2 := NewPlexAccount(func() string { return "" })
	if _, err := c2.ListItems(context.Background()); err == nil {
		t.Error("empty token must error")
	}
}

// TestPlexAccountClientUnreachableOwnedFails: a partial library would make the
// library filter treat a whole server's content as missing — fail instead.
func TestPlexAccountClientUnreachableOwnedFails(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]map[string]any{ //nolint:errcheck
			{"name": "Down", "provides": "server", "owned": true, "accessToken": "t",
				"connections": []map[string]any{{"uri": deadURL, "local": false, "relay": false}}},
		})
	}))
	defer tv.Close()
	orig := PlexTVBaseURL
	PlexTVBaseURL = tv.URL
	defer func() { PlexTVBaseURL = orig }()

	c := NewPlexAccount(func() string { return "acct" })
	if _, err := c.ListItems(context.Background()); err == nil {
		t.Error("unreachable owned server must fail the listing")
	}
}

// TestConnectFallsBackToRelay: a server whose direct connections are all
// unreachable (e.g. a port-forward that only works from inside its LAN) is
// still reachable through Plex's relay, which Connect tries last.
func TestConnectFallsBackToRelay(t *testing.T) {
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/identity" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.NotFound(w, r)
	}))
	defer relay.Close()

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	s := DiscoveredServer{Name: "Hex", Token: "t", conns: []plexConnection{
		{URI: deadURL, Local: false, Relay: false},
		{URI: relay.URL, Local: false, Relay: true},
	}}
	base, err := s.Connect(context.Background())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if base != relay.URL {
		t.Errorf("should land on the relay, got %q", base)
	}
}

// TestDiscoveryRequestsRelay: the resources call must ask plex.tv for relay
// connections, or servers without a working port-forward stay unreachable.
func TestDiscoveryRequestsRelay(t *testing.T) {
	var query string
	tv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.RawQuery
		json.NewEncoder(w).Encode([]map[string]any{}) //nolint:errcheck
	}))
	defer tv.Close()
	orig := PlexTVBaseURL
	PlexTVBaseURL = tv.URL
	defer func() { PlexTVBaseURL = orig }()

	if _, err := DiscoverPlexServers(context.Background(), "tok"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(query, "includeRelay=1") {
		t.Errorf("resources query must include includeRelay=1, got %q", query)
	}
}

// TestPlexListingCarriesCodecAndAudio: the Media-level attributes Plex
// exposes in section listings flow into Item so the library filter can
// grade beyond resolution.
func TestPlexListingCarriesCodecAndAudio(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Directory": []map[string]any{{"key": "1", "type": "movie"}},
			}})
		case "/library/sections/1/all":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{{
					"type": "movie", "title": "Dune", "year": 2021,
					"Media": []map[string]any{{
						"videoResolution": "4k", "videoCodec": "hevc",
						"audioCodec": "truehd", "audioProfile": "dolby truehd + dolby atmos",
					}},
				}},
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
	if len(items) != 1 {
		t.Fatalf("items: %d", len(items))
	}
	it := items[0]
	if it.Resolution != "2160p" || it.VideoCodec != "hevc" || it.AudioCodec != "truehd" ||
		it.AudioProfile != "dolby truehd + dolby atmos" {
		t.Errorf("item: %+v", it)
	}
}

// TestPlexDeepScan: with deep scan enabled, ListItems resolves each item's
// HDR/DV status from stream detail, caches it, and skips the detail call on
// the next listing.
func TestPlexDeepScan(t *testing.T) {
	var detailCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/library/sections":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Directory": []map[string]any{{"key": "1", "type": "movie"}},
			}})
		case "/library/sections/1/all":
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{
					{"type": "movie", "title": "DV Movie", "year": 2024, "ratingKey": "1",
						"Media": []map[string]any{{"videoResolution": "4k"}}},
					{"type": "movie", "title": "SDR Movie", "year": 2020, "ratingKey": "2",
						"Media": []map[string]any{{"videoResolution": "1080"}}},
				},
			}})
		case "/library/metadata/1":
			detailCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{{"Media": []map[string]any{{"Part": []map[string]any{{"Stream": []map[string]any{
					{"streamType": 1, "colorTrc": "smpte2084", "DOVIPresent": true},
				}}}}}}},
			}})
		case "/library/metadata/2":
			detailCalls.Add(1)
			json.NewEncoder(w).Encode(map[string]any{"MediaContainer": map[string]any{ //nolint:errcheck
				"Metadata": []map[string]any{{"Media": []map[string]any{{"Part": []map[string]any{{"Stream": []map[string]any{
					{"streamType": 1, "colorTrc": "bt709"},
				}}}}}}},
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
	cache := &memRangeCache{m: map[string]string{}}
	if !EnableDeepScan(c, cache) {
		t.Fatal("plex client must support deep scan")
	}

	items, err := c.ListItems(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, it := range items {
		got[it.Title] = it.ColorRange
	}
	if got["DV Movie"] != "dolby vision" || got["SDR Movie"] != "sdr" {
		t.Errorf("color ranges: %v", got)
	}
	if detailCalls.Load() != 2 {
		t.Errorf("detail calls: %d, want 2", detailCalls.Load())
	}

	// Second listing: everything served from the cache — zero detail calls.
	if _, err := c.ListItems(context.Background()); err != nil {
		t.Fatal(err)
	}
	if detailCalls.Load() != 2 {
		t.Errorf("cached listing made detail calls: %d", detailCalls.Load())
	}
}

type memRangeCache struct {
	mu sync.Mutex
	m  map[string]string
}

func (c *memRangeCache) Get(k string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.m[k]
	return v, ok
}
func (c *memRangeCache) Set(k, v string) { c.mu.Lock(); defer c.mu.Unlock(); c.m[k] = v }

func TestJellyfinVideoRangeMapping(t *testing.T) {
	cases := map[string]string{
		"DOVI": "dolby vision", "DOVIWITHHDR10": "dolby vision",
		"HDR10": "hdr10", "HDR10Plus": "hdr10",
		"HLG": "hdr", "SDR": "sdr", "": "",
	}
	for in, want := range cases {
		if got := jellyfinVideoRange(in); got != want {
			t.Errorf("%q: got %q, want %q", in, got, want)
		}
	}
}
