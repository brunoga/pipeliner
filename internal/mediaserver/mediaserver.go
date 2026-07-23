// Package mediaserver provides minimal Plex and Jellyfin clients for the two
// operations pipeliner needs: listing library items (episodes and movies with
// their video resolution) and triggering a library rescan. Both clients speak
// JSON and authenticate with the server's API token.
//
// Resolution is the only quality signal these APIs expose reliably, so the
// library filter's server backends compare resolution alone (the filesystem
// backend, which parses release names, also sees source/codec).
package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// Item is one library entry, normalized across servers.
type Item struct {
	Type       string // "episode" or "movie"
	Show       string // series title (episodes only)
	Season     int    // episodes only
	Episode    int    // episodes only
	Title      string // movie title (movies only)
	Year       int    // movies only
	Resolution string // normalized: "2160p", "1080p", "720p", "480p", or "" when unknown
}

// EpisodeID returns the SxxEyy identifier for episode items.
func (i Item) EpisodeID() string {
	return fmt.Sprintf("S%02dE%02d", i.Season, i.Episode)
}

// Client is the interface the library filter and library_refresh sink use.
type Client interface {
	// ListItems returns every episode and movie in the server's libraries.
	ListItems(ctx context.Context) ([]Item, error)
	// Refresh asks the server to rescan its libraries.
	Refresh(ctx context.Context) error
}

// New returns a client for the given backend ("plex" or "jellyfin").
func New(backend, baseURL, token string) (Client, error) {
	hc := &http.Client{Timeout: 30 * time.Second}
	base := strings.TrimRight(baseURL, "/")
	switch backend {
	case "plex":
		return &plexClient{base: base, token: token, http: hc}, nil
	case "jellyfin":
		return &jellyfinClient{base: base, token: token, http: hc}, nil
	default:
		return nil, fmt.Errorf("mediaserver: unsupported backend %q (supported: plex, jellyfin)", backend)
	}
}

// normalizeResolution maps the servers' assorted resolution spellings
// ("4k", "2160", 1080, "sd", …) onto the quality parser's vocabulary.
func normalizeResolution(v string) string {
	switch strings.ToLower(strings.TrimSuffix(v, "p")) {
	case "4k", "2160":
		return "2160p"
	case "1080":
		return "1080p"
	case "720":
		return "720p"
	case "480", "576", "sd":
		return "480p"
	}
	return ""
}

func getJSON(ctx context.Context, hc *http.Client, url string, header http.Header, dest any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for k, vs := range header {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("http %d from %s", resp.StatusCode, url)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

// ── Plex ─────────────────────────────────────────────────────────────────────

type plexClient struct {
	base  string
	token string
	http  *http.Client
}

func (c *plexClient) header() http.Header {
	return http.Header{"X-Plex-Token": {c.token}}
}

func (c *plexClient) ListItems(ctx context.Context) ([]Item, error) {
	var sections struct {
		MediaContainer struct {
			Directory []struct {
				Key  string `json:"key"`
				Type string `json:"type"` // "show" or "movie"
			} `json:"Directory"`
		} `json:"MediaContainer"`
	}
	if err := getJSON(ctx, c.http, c.base+"/library/sections", c.header(), &sections); err != nil {
		return nil, fmt.Errorf("plex: list sections: %w", err)
	}

	var items []Item
	for _, d := range sections.MediaContainer.Directory {
		var contentType string
		switch d.Type {
		case "show":
			contentType = "4" // episode leaves
		case "movie":
			contentType = "1"
		default:
			continue
		}
		var content struct {
			MediaContainer struct {
				Metadata []struct {
					Type             string `json:"type"`
					Title            string `json:"title"`
					GrandparentTitle string `json:"grandparentTitle"`
					ParentIndex      int    `json:"parentIndex"`
					Index            int    `json:"index"`
					Year             int    `json:"year"`
					Media            []struct {
						VideoResolution string `json:"videoResolution"`
					} `json:"Media"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		url := fmt.Sprintf("%s/library/sections/%s/all?type=%s", c.base, d.Key, contentType)
		if err := getJSON(ctx, c.http, url, c.header(), &content); err != nil {
			return nil, fmt.Errorf("plex: section %s: %w", d.Key, err)
		}
		for _, m := range content.MediaContainer.Metadata {
			res := ""
			if len(m.Media) > 0 {
				res = normalizeResolution(m.Media[0].VideoResolution)
			}
			switch m.Type {
			case "episode":
				items = append(items, Item{Type: "episode", Show: m.GrandparentTitle,
					Season: m.ParentIndex, Episode: m.Index, Resolution: res})
			case "movie":
				items = append(items, Item{Type: "movie", Title: m.Title, Year: m.Year, Resolution: res})
			}
		}
	}
	return items, nil
}

func (c *plexClient) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/library/sections/all/refresh", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("plex: refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("plex: refresh: http %d", resp.StatusCode)
	}
	return nil
}

// ── Jellyfin ─────────────────────────────────────────────────────────────────

type jellyfinClient struct {
	base  string
	token string
	http  *http.Client
}

func (c *jellyfinClient) header() http.Header {
	return http.Header{"X-Emby-Token": {c.token}}
}

func (c *jellyfinClient) ListItems(ctx context.Context) ([]Item, error) {
	var out struct {
		Items []struct {
			Type              string `json:"Type"` // "Episode" or "Movie"
			Name              string `json:"Name"`
			SeriesName        string `json:"SeriesName"`
			ParentIndexNumber int    `json:"ParentIndexNumber"`
			IndexNumber       int    `json:"IndexNumber"`
			ProductionYear    int    `json:"ProductionYear"`
			MediaStreams      []struct {
				Type   string `json:"Type"`
				Height int    `json:"Height"`
			} `json:"MediaStreams"`
		} `json:"Items"`
	}
	url := c.base + "/Items?Recursive=true&IncludeItemTypes=Episode,Movie&Fields=MediaStreams"
	if err := getJSON(ctx, c.http, url, c.header(), &out); err != nil {
		return nil, fmt.Errorf("jellyfin: list items: %w", err)
	}
	items := make([]Item, 0, len(out.Items))
	for _, it := range out.Items {
		res := ""
		for _, s := range it.MediaStreams {
			if s.Type == "Video" && s.Height > 0 {
				res = normalizeResolution(fmt.Sprint(heightBucket(s.Height)))
				break
			}
		}
		switch it.Type {
		case "Episode":
			items = append(items, Item{Type: "episode", Show: it.SeriesName,
				Season: it.ParentIndexNumber, Episode: it.IndexNumber, Resolution: res})
		case "Movie":
			items = append(items, Item{Type: "movie", Title: it.Name,
				Year: it.ProductionYear, Resolution: res})
		}
	}
	return items, nil
}

// heightBucket maps a video height in pixels onto the nearest standard
// resolution class (2160/1080/720/480).
func heightBucket(h int) int {
	switch {
	case h >= 1800:
		return 2160
	case h >= 900:
		return 1080
	case h >= 620:
		return 720
	default:
		return 480
	}
}

func (c *jellyfinClient) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/Library/Refresh", nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Emby-Token", c.token)
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("jellyfin: refresh: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("jellyfin: refresh: http %d", resp.StatusCode)
	}
	return nil
}
