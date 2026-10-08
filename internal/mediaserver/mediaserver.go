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
	// Section is the library the item lives in, as the server names it
	// ("Movies", "3D Movies", "TV Shows"). Consumers need it because one
	// server commonly holds several movie libraries whose contents must not be
	// pooled: a film owned only in 3D would otherwise look like a 2D copy.
	Section string
	// ColorRange is a release-vocabulary token: "dolby vision", "hdr10",
	// "hdr", "sdr", or "" when unknown. Jellyfin fills it from listings;
	// Plex needs deep scanning (per-item detail calls).
	ColorRange string
	// ShowTVDBID is the TheTVDB id of the SHOW an episode belongs to, as the
	// server publishes it; "" when the server exposes none. Episodes only.
	//
	// Deliberately the show's id and not the episode's. Plex publishes both --
	// an episode's own Guid list carries a tvdb:// id for the episode -- and
	// the one worth having here is the show's, because titles are not stable
	// identity: TheTVDB renames series, and a server may disambiguate a remake
	// with a year the provider does not use ("Brothers" against
	// "Brothers (2026)"). Matching on it lets a caller find a show it would
	// otherwise miss by name.
	ShowTVDBID string
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
				Key   string `json:"key"`
				Type  string `json:"type"` // "show" or "movie"
				Title string `json:"title"`
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
					GrandparentKey   string `json:"grandparentRatingKey"`
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
		// The show listing is the only place the SHOW's external ids appear:
		// an episode's own Guid list holds the episode's ids, and its
		// grandparentGuid is a plex:// id, not a provider one. One extra call
		// per show section (hundreds of rows, not thousands of episodes).
		var showTVDB map[string]string
		if d.Type == "show" {
			showTVDB = c.showTVDBIDs(ctx, d.Key)
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
					Season: m.ParentIndex, Episode: m.Index, Resolution: res, Section: d.Title,
					VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: m.RatingKey, Version: ver,
					ShowTVDBID: showTVDB[m.GrandparentKey]})
			case "movie":
				items = append(items, Item{Type: "movie", Title: m.Title, Year: m.Year, Resolution: res, Section: d.Title,
					VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: m.RatingKey, Version: ver})
			}
		}
	}
	if c.deep {
		c.fillColorRanges(ctx, items)
	}
	return items, nil
}

// showTVDBIDs maps a show section's ratingKey -> TheTVDB series id, read from
// the show listing's Guid entries.
//
// Best effort: a server that does not answer, or answers without guids, yields
// an empty map and callers fall back to matching shows by title. An id is an
// improvement on the title, never a precondition for listing a library, so a
// failure here must not fail ListItems.
func (c *plexClient) showTVDBIDs(ctx context.Context, sectionKey string) map[string]string {
	var shows struct {
		MediaContainer struct {
			Metadata []struct {
				RatingKey string `json:"ratingKey"`
				Guid      []struct {
					ID string `json:"id"`
				} `json:"Guid"`
			} `json:"Metadata"`
		} `json:"MediaContainer"`
	}
	url := fmt.Sprintf("%s/library/sections/%s/all?type=2&includeGuids=1", c.base, sectionKey)
	if err := getJSON(ctx, c.http, url, c.header(), &shows); err != nil {
		return nil
	}
	out := make(map[string]string, len(shows.MediaContainer.Metadata))
	for _, m := range shows.MediaContainer.Metadata {
		if id := tvdbIDFromGuids(m.Guid); id != "" && m.RatingKey != "" {
			out[m.RatingKey] = id
		}
	}
	return out
}

// tvdbIDFromGuids picks the TheTVDB id out of a Plex Guid list, which also
// carries imdb:// and tmdb:// entries in no guaranteed order.
func tvdbIDFromGuids(guids []struct {
	ID string `json:"id"`
}) string {
	for _, g := range guids {
		if rest, ok := strings.CutPrefix(g.ID, "tvdb://"); ok {
			// Plex appends an agent suffix on some entries ("tvdb://123?lang=en").
			if i := strings.IndexAny(rest, "?/"); i >= 0 {
				rest = rest[:i]
			}
			if rest != "" {
				return rest
			}
		}
	}
	return ""
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
		Items []jellyfinItem `json:"Items"`
	}
	// One request per library rather than one flat Recursive sweep, so each
	// item can be attributed to the library it came from. Consumers need that:
	// a server commonly holds both "Movies" and "3D Movies", and pooling them
	// makes a film owned only in 3D look like a 2D copy.
	views, err := c.views(ctx)
	if err != nil {
		return nil, err
	}
	var items []Item
	for _, v := range views {
		out.Items = nil
		url := c.base + "/Items?Recursive=true&IncludeItemTypes=Episode,Movie&Fields=MediaStreams"
		if v.ID != "" {
			url += "&ParentId=" + v.ID
		}
		if err := getJSON(ctx, c.http, url, c.header(), &out); err != nil {
			return nil, fmt.Errorf("jellyfin: list items in %q: %w", v.Name, err)
		}
		// Provider ids live on the series, not on its episodes, so they need
		// their own listing -- one call per library, against hundreds of shows.
		items = append(items, c.itemsFrom(out.Items, v.Name, c.seriesTVDBIDs(ctx, v.ID))...)
	}
	return items, nil
}

// seriesTVDBIDs maps a library's series ids -> TheTVDB id, from the series
// listing's ProviderIds.
//
// Best effort, like its Plex counterpart: an error or a server without provider
// ids yields an empty map and callers fall back to matching shows by title.
func (c *jellyfinClient) seriesTVDBIDs(ctx context.Context, viewID string) map[string]string {
	var out struct {
		Items []struct {
			ID          string            `json:"Id"`
			ProviderIDs map[string]string `json:"ProviderIds"`
		} `json:"Items"`
	}
	url := c.base + "/Items?Recursive=true&IncludeItemTypes=Series&Fields=ProviderIds"
	if viewID != "" {
		url += "&ParentId=" + viewID
	}
	if err := getJSON(ctx, c.http, url, c.header(), &out); err != nil {
		return nil
	}
	ids := make(map[string]string, len(out.Items))
	for _, it := range out.Items {
		// Jellyfin's provider keys are not case-stable across versions
		// ("Tvdb", "TVDB", "tvdb"), so match without regard to case.
		for k, v := range it.ProviderIDs {
			if strings.EqualFold(k, "tvdb") && v != "" && it.ID != "" {
				ids[it.ID] = v
				break
			}
		}
	}
	return ids
}

// jellyfinView is one of the server's libraries.
type jellyfinView struct {
	ID   string
	Name string
}

// views lists the server's libraries. A server that does not answer the
// endpoint (older builds, restricted tokens) yields a single unnamed view, so
// ListItems falls back to the flat sweep it used to do — items then carry no
// Section and a section filter is reported as unusable rather than silently
// matching nothing.
func (c *jellyfinClient) views(ctx context.Context) ([]jellyfinView, error) {
	var out struct {
		Items []struct {
			ID   string `json:"ItemId"`
			Name string `json:"Name"`
		}
	}
	if err := getJSON(ctx, c.http, c.base+"/Library/VirtualFolders", c.header(), &out.Items); err != nil {
		return []jellyfinView{{}}, nil //nolint:nilerr // fall back to the flat sweep
	}
	views := make([]jellyfinView, 0, len(out.Items))
	for _, v := range out.Items {
		if v.ID != "" {
			views = append(views, jellyfinView{ID: v.ID, Name: v.Name})
		}
	}
	if len(views) == 0 {
		return []jellyfinView{{}}, nil
	}
	return views, nil
}

// jellyfinItem is one row of a Jellyfin /Items listing. Named rather than
// repeated inline, since both the listing and itemsFrom must agree on it.
type jellyfinItem struct {
	ID                string `json:"Id"`
	Type              string `json:"Type"`
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
	// SeriesId is the server's id for an episode's SHOW, used to join
	// against the series listing that carries the provider ids.
	SeriesID string `json:"SeriesId"`
}

func (c *jellyfinClient) itemsFrom(raw []jellyfinItem, section string, seriesTVDB map[string]string) []Item {
	items := make([]Item, 0, len(raw))
	for _, it := range raw {
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
				Season: it.ParentIndexNumber, Episode: it.IndexNumber, Resolution: res, Section: section,
				VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: it.ID, ColorRange: cr,
				ShowTVDBID: seriesTVDB[it.SeriesID]})
		case "Movie":
			items = append(items, Item{Type: "movie", Title: it.Name,
				Year: it.ProductionYear, Resolution: res, Section: section,
				VideoCodec: vc, AudioCodec: ac, AudioProfile: ap, ID: it.ID, ColorRange: cr})
		}
	}
	return items
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
