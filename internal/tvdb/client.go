// Package tvdb provides a minimal TheTVDB v4 API client.
//
// It uses only stdlib (net/http, encoding/json) and requires no external
// dependencies. Obtain an API key at https://thetvdb.com/api-information
//
// The flexString type handles the TheTVDB API's inconsistency of returning
// tvdb_id as a quoted string in search results but as a bare integer in series
// detail endpoints — both unmarshal cleanly into a Go string.
package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

const defaultBaseURL = "https://api4.thetvdb.com/v4"

// Client is a TheTVDB v4 REST API client.
type Client struct {
	apiKey  string
	userPin string // optional; enables user-specific endpoints (favorites)
	BaseURL string // overridable for testing; defaults to defaultBaseURL
	token   string
	expires time.Time
	http    *http.Client
}

// New creates a Client for public TheTVDB endpoints.
func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		BaseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// NewWithPin creates a Client that can access user-specific endpoints (favorites).
func NewWithPin(apiKey, userPin string) *Client {
	c := New(apiKey)
	c.userPin = userPin
	return c
}

// Series is a summary record returned by the search endpoint.
// Fields like Genres, Network, and FirstAired are populated when the search
// result includes them; they may be absent for some entries.
type Series struct {
	ID         string   `json:"tvdb_id"`
	Name       string   `json:"name"`
	Overview   string   `json:"overview"`
	Year       string   `json:"year"`
	Slug       string   `json:"slug"`
	Genres     []string `json:"genres"`           // genre names, e.g. ["Drama","Crime"]
	Network    string   `json:"network"`           // originating network name
	Language   string   `json:"originalLanguage"`  // original language code, e.g. "eng"
	Country    string   `json:"country"`           // country of origin, e.g. "usa"
	ImageURL   string   `json:"image_url"`         // poster image URL
	FirstAired string   `json:"first_air_time"`    // ISO-8601 from search; may be empty
	Score      float64  `json:"score"`             // popularity score
}

// Episode represents a single episode from the series episodes endpoint.
type Episode struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"number"`
	Overview      string `json:"overview"`
	AirDate       string `json:"aired"`
	Runtime       int    `json:"runtime"` // minutes
	Image         string `json:"image"`   // episode still/thumbnail URL
}

// SearchSeries searches for TV series by name.
func (c *Client) SearchSeries(ctx context.Context, name string) ([]Series, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	u := c.BaseURL + "/search?query=" + url.QueryEscape(name) + "&type=series"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	var resp struct {
		Data   []Series `json:"data"`
		Status string   `json:"status"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("tvdb: search %q: %w", name, err)
	}
	return resp.Data, nil
}

// GetEpisodes retrieves episodes for a series (official ordering) using the series TVDB ID.
func (c *Client) GetEpisodes(ctx context.Context, seriesID string) ([]Episode, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/series/%s/episodes/official?page=0", c.BaseURL, seriesID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	var resp struct {
		Data struct {
			Episodes []Episode `json:"episodes"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("tvdb: episodes %s: %w", seriesID, err)
	}
	return resp.Data.Episodes, nil
}

// GetFavorites returns the TVDB series IDs from the authenticated user's favorites list.
// Requires a user pin (created via NewWithPin).
func (c *Client) GetFavorites(ctx context.Context) ([]int, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}
	if c.userPin == "" {
		return nil, fmt.Errorf("tvdb: GetFavorites requires a user_pin")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/user/favorites", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	var resp struct {
		Data struct {
			Series []int `json:"series"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("tvdb: favorites: %w", err)
	}
	return resp.Data.Series, nil
}

// SeriesExtended holds richer metadata from the /series/{id}/extended endpoint.
// The search endpoint inconsistently omits genres and language; this endpoint
// is the authoritative source for both.
// Trailer holds a single trailer entry from the extended series endpoint.
type Trailer struct {
	URL      string `json:"url"`
	Name     string `json:"name"`
	Language string `json:"language"`
	Runtime  int    `json:"runtime"` // seconds
}

// ContentRating holds a content rating entry (e.g. TV-MA, PG-13).
type ContentRating struct {
	Name    string `json:"name"`
	Country string `json:"country"`
}

// Alias holds an alternative title for a series.
type Alias struct {
	Language string `json:"language"`
	Name     string `json:"name"`
}

// Character holds a cast or crew member from the extended series endpoint.
type Character struct {
	Name       string `json:"name"`       // character name
	PersonName string `json:"personName"` // actor/person real name
	Type       int    `json:"type"`       // 3 = Actor, 4 = Director, etc.
	Image      string `json:"image"`      // headshot URL
	Sort       int    `json:"sort"`       // display order
}

type SeriesExtended struct {
	Language        string          `json:"originalLanguage"` // e.g. "eng"
	OriginalCountry string          `json:"originalCountry"`  // e.g. "usa"
	FirstAired      string          `json:"firstAired"`       // YYYY-MM-DD
	LastAired       string          `json:"lastAired"`        // YYYY-MM-DD
	NextAired       string          `json:"nextAired"`        // YYYY-MM-DD
	Score           float64         `json:"score"`
	Status          struct {
		Name string `json:"name"`
	} `json:"status"`
	Genres []struct {
		Name string `json:"name"`
	} `json:"genres"`
	Trailers       []Trailer       `json:"trailers"`
	ContentRatings []ContentRating `json:"contentRatings"`
	Aliases        []Alias         `json:"aliases"`
	Characters     []Character     `json:"characters"`
}

// GenreNames returns the genre names as a plain string slice.
func (s *SeriesExtended) GenreNames() []string {
	names := make([]string, 0, len(s.Genres))
	for _, g := range s.Genres {
		if g.Name != "" {
			names = append(names, g.Name)
		}
	}
	return names
}

// AliasNames returns alias names as a plain string slice.
func (s *SeriesExtended) AliasNames() []string {
	names := make([]string, 0, len(s.Aliases))
	for _, a := range s.Aliases {
		if a.Name != "" {
			names = append(names, a.Name)
		}
	}
	return names
}

// TrailerURLs returns all trailer URLs in order.
func (s *SeriesExtended) TrailerURLs() []string {
	urls := make([]string, 0, len(s.Trailers))
	for _, t := range s.Trailers {
		if t.URL != "" {
			urls = append(urls, t.URL)
		}
	}
	return urls
}

// ContentRatingName returns the first content rating name (e.g. "TV-MA"), or "".
func (s *SeriesExtended) ContentRatingName() string {
	for _, cr := range s.ContentRatings {
		if cr.Name != "" {
			return cr.Name
		}
	}
	return ""
}

// ActorNames returns the real names of actors (type 3), sorted by their
// display order.
func (s *SeriesExtended) ActorNames() []string {
	const typeActor = 3
	type entry struct {
		name string
		sort int
	}
	var actors []entry
	for _, c := range s.Characters {
		if c.Type == typeActor && c.PersonName != "" {
			actors = append(actors, entry{c.PersonName, c.Sort})
		}
	}
	// Sort by display order.
	for i := 1; i < len(actors); i++ {
		for j := i; j > 0 && actors[j].sort < actors[j-1].sort; j-- {
			actors[j], actors[j-1] = actors[j-1], actors[j]
		}
	}
	names := make([]string, len(actors))
	for i, a := range actors {
		names[i] = a.name
	}
	return names
}

// GetSeriesExtended fetches the extended series record for the given TVDB ID.
// It returns language and genres which are unreliable in search results.
func (c *Client) GetSeriesExtended(ctx context.Context, id string) (*SeriesExtended, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/series/%s/extended?meta=actors", c.BaseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	var resp struct {
		Data   SeriesExtended `json:"data"`
		Status string         `json:"status"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("tvdb: series %s extended: %w", id, err)
	}
	return &resp.Data, nil
}

// GetSeriesByID fetches a series record by its TVDB ID.
func (c *Client) GetSeriesByID(ctx context.Context, id int) (*Series, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/series/%d", c.BaseURL, id)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.token)

	var resp struct {
		Data   Series `json:"data"`
		Status string `json:"status"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("tvdb: series %d: %w", id, err)
	}
	return &resp.Data, nil
}

// ensureToken acquires a JWT if the current one is absent or expiring within 5 minutes.
func (c *Client) ensureToken(ctx context.Context) error {
	if c.token != "" && time.Until(c.expires) > 5*time.Minute {
		return nil
	}
	return c.login(ctx)
}

func (c *Client) login(ctx context.Context) error {
	body := map[string]string{"apikey": c.apiKey}
	if c.userPin != "" {
		body["pin"] = c.userPin
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/login", bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := c.do(req, &resp); err != nil {
		return fmt.Errorf("tvdb: login: %w", err)
	}
	if resp.Data.Token == "" {
		return fmt.Errorf("tvdb: login: empty token")
	}
	c.token = resp.Data.Token
	// TVDB JWTs are valid for 1 month; use a conservative 23-hour expiry.
	c.expires = time.Now().Add(23 * time.Hour)
	return nil
}

func (c *Client) do(req *http.Request, dest any) error {
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}
