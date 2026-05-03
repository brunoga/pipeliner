// Package tmdb provides a minimal TMDb v3 API client.
//
// It uses only stdlib (net/http, encoding/json) and requires no external dependencies.
// Obtain an API key at https://www.themoviedb.org/settings/api
package tmdb

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const defaultBaseURL = "https://api.themoviedb.org/3"

// Client is a TMDb v3 REST API client.
type Client struct {
	apiKey  string
	BaseURL string // overridable for testing; defaults to defaultBaseURL
	http    *http.Client
}

// New creates a Client with the given API key.
func New(apiKey string) *Client {
	return &Client{
		apiKey:  apiKey,
		BaseURL: defaultBaseURL,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// Movie is a summary record returned by the search endpoint.
type Movie struct {
	ID          int     `json:"id"`
	Title       string  `json:"title"`
	OrigTitle   string  `json:"original_title"`
	Overview    string  `json:"overview"`
	ReleaseDate string  `json:"release_date"` // "YYYY-MM-DD"
	Popularity  float64 `json:"popularity"`
	VoteAverage float64 `json:"vote_average"`
}

// MovieDetail contains extended information from the movie detail endpoint.
type MovieDetail struct {
	Movie
	Genres   []Genre  `json:"genres"`
	Runtime  int      `json:"runtime"`
	Tagline  string   `json:"tagline"`
	Homepage string   `json:"homepage"`
	ImdbID   string   `json:"imdb_id"`
}

// Genre is a TMDb genre entry.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// SearchMovie searches for movies by title and optional year.
// Pass year=0 to omit the year filter.
func (c *Client) SearchMovie(ctx context.Context, title string, year int) ([]Movie, error) {
	params := url.Values{
		"api_key": {c.apiKey},
		"query":   {title},
	}
	if year > 0 {
		params.Set("year", strconv.Itoa(year))
	}

	u := c.BaseURL + "/search/movie?" + params.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	var resp struct {
		Results []Movie `json:"results"`
		Page    int     `json:"page"`
		Total   int     `json:"total_results"`
	}
	if err := c.do(req, &resp); err != nil {
		return nil, fmt.Errorf("tmdb: search %q: %w", title, err)
	}
	return resp.Results, nil
}

// GetMovie retrieves detailed movie information by TMDb movie ID.
func (c *Client) GetMovie(ctx context.Context, id int) (*MovieDetail, error) {
	u := fmt.Sprintf("%s/movie/%d?api_key=%s", c.BaseURL, id, c.apiKey)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}

	var m MovieDetail
	if err := c.do(req, &m); err != nil {
		return nil, fmt.Errorf("tmdb: movie %d: %w", id, err)
	}
	return &m, nil
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
