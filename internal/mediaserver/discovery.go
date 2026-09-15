// Plex account discovery: enumerate the account's servers via plex.tv using
// only the account token, so tools and filters can reach every owned server
// without per-server URLs. Each discovered server carries its candidate
// connection URIs; Connect probes them and returns a working base URL.
package mediaserver

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
)

// PlexTVBaseURL is the plex.tv API root; a package variable so tests can point
// discovery at a local fake.
var PlexTVBaseURL = "https://plex.tv"

// plexClientID identifies pipeliner to plex.tv (the resources API rejects
// requests without an X-Plex-Client-Identifier).
const plexClientID = "pipeliner"

// DiscoveredServer is one Plex server the account can reach.
type DiscoveredServer struct {
	Name  string
	Owned bool
	// Token is the per-server access token from plex.tv. For shared servers it
	// differs from the account token; for owned servers either works.
	Token string
	// connections in preference order: non-relay before relay.
	conns []plexConnection
}

type plexConnection struct {
	URI   string
	Local bool
	Relay bool
}

// DiscoverPlexServers enumerates the account's servers via plex.tv.
func DiscoverPlexServers(ctx context.Context, accountToken string) ([]DiscoveredServer, error) {
	hc := &http.Client{Timeout: 15 * time.Second}
	var resources []struct {
		Name        string `json:"name"`
		Provides    string `json:"provides"`
		Owned       bool   `json:"owned"`
		AccessToken string `json:"accessToken"`
		Connections []struct {
			URI   string `json:"uri"`
			Local bool   `json:"local"`
			Relay bool   `json:"relay"`
		} `json:"connections"`
	}
	header := http.Header{
		"X-Plex-Token":             {accountToken},
		"X-Plex-Client-Identifier": {plexClientID},
		"X-Plex-Product":           {"pipeliner"},
	}
	url := PlexTVBaseURL + "/api/v2/resources?includeHttps=1"
	if err := getJSON(ctx, hc, url, header, &resources); err != nil {
		return nil, fmt.Errorf("plex.tv resources: %w", err)
	}

	var servers []DiscoveredServer
	for _, r := range resources {
		if !strings.Contains(r.Provides, "server") {
			continue
		}
		s := DiscoveredServer{Name: r.Name, Owned: r.Owned, Token: r.AccessToken}
		if s.Token == "" {
			s.Token = accountToken
		}
		for _, c := range r.Connections {
			s.conns = append(s.conns, plexConnection{URI: strings.TrimRight(c.URI, "/"), Local: c.Local, Relay: c.Relay})
		}
		// Prefer direct connections; relays are a slow last resort.
		sort.SliceStable(s.conns, func(i, j int) bool {
			return !s.conns[i].Relay && s.conns[j].Relay
		})
		servers = append(servers, s)
	}
	return servers, nil
}

// Connect probes the server's connection URIs in parallel and returns the base
// URL of the first (most preferred) one that answers /identity. Unreachable
// candidates — e.g. LAN addresses probed from a remote host — are skipped.
func (s DiscoveredServer) Connect(ctx context.Context) (string, error) {
	if len(s.conns) == 0 {
		return "", fmt.Errorf("plex server %q has no connections", s.Name)
	}
	probeCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	hc := &http.Client{Timeout: 6 * time.Second}
	ok := make([]bool, len(s.conns))
	var wg sync.WaitGroup
	for i, c := range s.conns {
		wg.Add(1)
		go func(i int, uri string) {
			defer wg.Done()
			req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, uri+"/identity", nil)
			if err != nil {
				return
			}
			req.Header.Set("X-Plex-Token", s.Token)
			resp, err := hc.Do(req)
			if err != nil {
				return
			}
			resp.Body.Close()
			ok[i] = resp.StatusCode == http.StatusOK
		}(i, c.URI)
	}
	wg.Wait()

	for i := range s.conns { // conns are already in preference order
		if ok[i] {
			return s.conns[i].URI, nil
		}
	}
	return "", fmt.Errorf("plex server %q: no reachable connection (%d candidates)", s.Name, len(s.conns))
}

// MovieSection is one movie library on a Plex server, with its items. Tools
// that need section-level granularity use this instead of Client.ListItems —
// e.g. telling a "3D Movies" library apart from the flat movie library.
type MovieSection struct {
	Name  string
	Items []Item
}

// PlexMovieSections lists each movie section on the server with its items.
func PlexMovieSections(ctx context.Context, baseURL, token string) ([]MovieSection, error) {
	c := &plexClient{base: strings.TrimRight(baseURL, "/"), token: token,
		http: &http.Client{Timeout: 60 * time.Second}}

	var sections struct {
		MediaContainer struct {
			Directory []struct {
				Key   string `json:"key"`
				Type  string `json:"type"`
				Title string `json:"title"`
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := getJSON(ctx, c.http, c.base+"/library/sections", c.header(), &sections); err != nil {
		return nil, fmt.Errorf("plex: list sections: %w", err)
	}

	var out []MovieSection
	for _, d := range sections.MediaContainer.Directory {
		if d.Type != "movie" {
			continue
		}
		var content struct {
			MediaContainer struct {
				Metadata []struct {
					Title string `json:"title"`
					Year  int    `json:"year"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		url := fmt.Sprintf("%s/library/sections/%s/all?type=1", c.base, d.Key)
		if err := getJSON(ctx, c.http, url, c.header(), &content); err != nil {
			return nil, fmt.Errorf("plex: section %q: %w", d.Title, err)
		}
		sec := MovieSection{Name: d.Title}
		for _, m := range content.MediaContainer.Metadata {
			sec.Items = append(sec.Items, Item{Type: "movie", Title: m.Title, Year: m.Year})
		}
		out = append(out, sec)
	}
	return out, nil
}
