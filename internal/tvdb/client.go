// Package tvdb provides a minimal TheTVDB v4 API client.
//
// It uses only stdlib (net/http, encoding/json) and requires no external dependencies.
// Obtain an API key at https://thetvdb.com/api-information
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
	ID         int      `json:"tvdb_id"`
	Name       string   `json:"name"`
	Overview   string   `json:"overview"`
	Year       string   `json:"year"`
	Slug       string   `json:"slug"`
	Status     string   `json:"status"`
	Genres     []string `json:"genres"`      // genre names, e.g. ["Drama","Crime"]
	Network    string   `json:"network"`     // originating network name
	FirstAired string   `json:"first_air_time"` // ISO-8601 from search; may be empty
}

// Episode represents a single episode from the series episodes endpoint.
type Episode struct {
	ID            int    `json:"id"`
	Name          string `json:"name"`
	SeasonNumber  int    `json:"seasonNumber"`
	EpisodeNumber int    `json:"number"`
	Overview      string `json:"overview"`
	AirDate       string `json:"aired"`
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
func (c *Client) GetEpisodes(ctx context.Context, seriesID int) ([]Episode, error) {
	if err := c.ensureToken(ctx); err != nil {
		return nil, err
	}

	u := fmt.Sprintf("%s/series/%d/episodes/official?page=0", c.BaseURL, seriesID)
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
		return nil, fmt.Errorf("tvdb: episodes %d: %w", seriesID, err)
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
