// Package mediaserver provides minimal Plex and Jellyfin clients for the two
// operations pipeliner needs: listing library items (episodes and movies with
// their video resolution) and triggering a library rescan. Both clients speak
// JSON and authenticate with the server's API token.
//
// Listings expose resolution, video/audio codec, and the audio profile
// (which names Atmos), so the library filter grades server copies on those;
// source (BluRay/WEB) and HDR are not available from listings and stay
// unknown.
package mediaserver

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
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
	// Codec/audio metadata, straight from the server's listing where exposed
	// (Plex media attributes, Jellyfin media streams). Empty when unknown.
	VideoCodec   string // e.g. "hevc", "h264", "av1"
	AudioCodec   string // e.g. "truehd", "eac3", "dca"
	AudioProfile string // e.g. "dolby truehd + dolby atmos"
	// ID is the server's item identifier (Plex ratingKey, Jellyfin Id);
	// used by deep scanning to fetch stream-level detail.
	ID string
	// Version is a server-provided change marker for the item's media (Plex
	// updatedAt). Deep-scan cache keys include it, so replacing the file
	// under the same item — a quality upgrade — invalidates instantly
	// instead of waiting out a TTL.
	Version string
	// ColorRange is a release-vocabulary token: "dolby vision", "hdr10",
	// "hdr", "sdr", or "" when unknown. Jellyfin fills it from listings;
	// Plex needs deep scanning (per-item detail calls).
	ColorRange string
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

	// Deep scanning (HDR/DV detection): when enabled, ListItems fetches each
	// item's stream detail to read colorTrc/DOVIPresent — one extra request
	// per item, so results are remembered in rangeCache across runs (media
	// files rarely change). rangeScope keys the cache stably (the account
	// client uses the server name; a direct client its base URL).
	deep       bool
	rangeCache RangeCache
	rangeScope string
}

// RangeCache remembers per-item color-range results across runs. Implemented
// by the library filter over a persistent store bucket; nil-safe usage is the
// caller's concern (EnableDeepScan accepts nil for in-memory-only scanning).
type RangeCache interface {
	Get(key string) (string, bool)
	Set(key, val string)
}

// EnableDeepScan turns on stream-level HDR/DV detection for clients that
// support it, returning false for backends that don't need it (Jellyfin
// exposes the video range in listings; the filesystem backend parses names).
// cache may be nil to scan without cross-run memory.
func EnableDeepScan(c Client, cache RangeCache) bool {
	type scanner interface {
		enableDeepScan(cache RangeCache, scope string)
	}
	if sc, ok := c.(scanner); ok {
		sc.enableDeepScan(cache, "")
		return true
	}
	return false
}

func (c *plexClient) enableDeepScan(cache RangeCache, scope string) {
	c.deep = true
	c.rangeCache = cache
	if scope == "" {
		scope = c.base
	}
	c.rangeScope = scope
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
					RatingKey        string `json:"ratingKey"`
					UpdatedAt        int64  `json:"updatedAt"`
					Media            []struct {
						VideoResolution string `json:"videoResolution"`
						VideoCodec      string `json:"videoCodec"`
						AudioCodec      string `json:"audioCodec"`
						AudioProfile    string `json:"audioProfile"`
					} `json:"Media"`
				} `json:"Metadata"`
			} `json:"MediaContainer"`
		}
		url := fmt.Sprintf("%s/library/sections/%s/all?type=%s", c.base, d.Key, contentType)
		if err := getJSON(ctx, c.http, url, c.header(), &content); err != nil {
			return nil, fmt.Errorf("plex: section %s: %w", d.Key, err)
		}
		for _, m := range content.MediaContainer.Metadata {
			var res, vc, ac, ap string
			if len(m.Media) > 0 {
				res = normalizeResolution(m.Media[0].VideoResolution)
				vc = m.Media[0].VideoCodec
				ac = m.Media[0].AudioCodec
				ap = m.Media[0].AudioProfile
			}
			ver := ""
			if m.UpdatedAt > 0 {
				ver = strconv.FormatInt(m.UpdatedAt, 10)
			}
			switch m.Type {
			case "episode":
				items = append(items, Item{Type: "episode", Show: m.GrandparentTitle,
					Season: m.ParentIndex, Episode: m.Index, Resolution: res,
					VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: m.RatingKey, Version: ver})
			case "movie":
				items = append(items, Item{Type: "movie", Title: m.Title, Year: m.Year, Resolution: res,
					VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: m.RatingKey, Version: ver})
			}
		}
	}
	if c.deep {
		c.fillColorRanges(ctx, items)
	}
	return items, nil
}

// maxDeepScanWorkers bounds parallel per-item detail fetches.
const maxDeepScanWorkers = 8

// fillColorRanges resolves each item's HDR/DV status from its stream detail,
// consulting the cache first so only never-seen items cost a request.
func (c *plexClient) fillColorRanges(ctx context.Context, items []Item) {
	var wg sync.WaitGroup
	sem := make(chan struct{}, maxDeepScanWorkers)
	for i := range items {
		if items[i].ID == "" {
			continue
		}
		// Version (updatedAt) in the key makes a file replacement — a quality
		// upgrade under the same item — miss the cache and rescan immediately.
		key := c.rangeScope + "|" + items[i].ID + "|" + items[i].Version
		if c.rangeCache != nil {
			if v, ok := c.rangeCache.Get(key); ok {
				items[i].ColorRange = v
				continue
			}
		}
		wg.Add(1)
		go func(i int, key string) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cr, err := c.itemColorRange(ctx, items[i].ID)
			if err != nil || cr == "" {
				return // unknown; retried next index build
			}
			items[i].ColorRange = cr
			if c.rangeCache != nil {
				c.rangeCache.Set(key, cr)
			}
		}(i, key)
	}
	wg.Wait()
}

// itemColorRange reads the item's first video stream and maps its color
// metadata onto release vocabulary: DOVIPresent → "dolby vision",
// colorTrc smpte2084 → "hdr10", arib-std-b67 (HLG) → "hdr", else "sdr".
func (c *plexClient) itemColorRange(ctx context.Context, ratingKey string) (string, error) {
	var out struct {
		MediaContainer struct {
			Metadata []struct {
				Media []struct {
					Part []struct {
						Stream []struct {
							StreamType  int    `json:"streamType"`
							ColorTrc    string `json:"colorTrc"`
							DOVIPresent bool   `json:"DOVIPresent"`
						} `json:"Stream"`
					} `json:"Part"`
				} `json:"Media"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	url := c.base + "/library/metadata/" + url.PathEscape(ratingKey)
	if err := getJSON(ctx, c.http, url, c.header(), &out); err != nil {
		return "", err
	}
	for _, m := range out.MediaContainer.Metadata {
		for _, media := range m.Media {
			for _, part := range media.Part {
				for _, st := range part.Stream {
					if st.StreamType != 1 {
						continue
					}
					switch {
					case st.DOVIPresent:
						return "dolby vision", nil
					case st.ColorTrc == "smpte2084":
						return "hdr10", nil
					case st.ColorTrc == "arib-std-b67":
						return "hdr", nil
					default:
						return "sdr", nil
					}
				}
			}
		}
	}
	return "", nil
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
			ID                string `json:"Id"`
			Type              string `json:"Type"` // "Episode" or "Movie"
			Name              string `json:"Name"`
			SeriesName        string `json:"SeriesName"`
			ParentIndexNumber int    `json:"ParentIndexNumber"`
			IndexNumber       int    `json:"IndexNumber"`
			ProductionYear    int    `json:"ProductionYear"`
			MediaStreams      []struct {
				Type           string `json:"Type"`
				Height         int    `json:"Height"`
				Codec          string `json:"Codec"`
				Profile        string `json:"Profile"`
				VideoRangeType string `json:"VideoRangeType"`
			} `json:"MediaStreams"`
		} `json:"Items"`
	}
	url := c.base + "/Items?Recursive=true&IncludeItemTypes=Episode,Movie&Fields=MediaStreams"
	if err := getJSON(ctx, c.http, url, c.header(), &out); err != nil {
		return nil, fmt.Errorf("jellyfin: list items: %w", err)
	}
	items := make([]Item, 0, len(out.Items))
	for _, it := range out.Items {
		var res, vc, ac, ap, cr string
		for _, s := range it.MediaStreams {
			switch s.Type {
			case "Video":
				if res == "" && s.Height > 0 {
					res = normalizeResolution(fmt.Sprint(heightBucket(s.Height)))
					vc = s.Codec
					cr = jellyfinVideoRange(s.VideoRangeType)
				}
			case "Audio":
				if ac == "" {
					ac = s.Codec
					ap = s.Profile
				}
			}
		}
		switch it.Type {
		case "Episode":
			items = append(items, Item{Type: "episode", Show: it.SeriesName,
				Season: it.ParentIndexNumber, Episode: it.IndexNumber, Resolution: res,
				VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: it.ID, ColorRange: cr})
		case "Movie":
			items = append(items, Item{Type: "movie", Title: it.Name,
				Year: it.ProductionYear, Resolution: res,
				VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: it.ID, ColorRange: cr})
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

// jellyfinVideoRange maps Jellyfin's VideoRangeType onto release vocabulary.
func jellyfinVideoRange(v string) string {
	switch strings.ToUpper(v) {
	case "DOVI", "DOVIWITHHDR10", "DOVIWITHSDR", "DOVIWITHHLG":
		return "dolby vision"
	case "HDR10", "HDR10PLUS":
		return "hdr10"
	case "HLG", "HDR":
		return "hdr"
	case "SDR":
		return "sdr"
	}
	return ""
}
