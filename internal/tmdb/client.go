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
	VoteCount   int     `json:"vote_count"`
	PosterPath  string  `json:"poster_path"` // e.g. "/abc123.jpg"; prepend ImageBaseURL
}

// ImageBaseURL is the TMDb image CDN prefix for standard poster size.
const ImageBaseURL = "https://image.tmdb.org/t/p/w500"

// MovieDetail contains extended information from the movie detail endpoint,
// including credits, videos, and release dates fetched via append_to_response.
type MovieDetail struct {
	Movie
	Genres             []Genre `json:"genres"`
	Runtime            int     `json:"runtime"`
	Tagline            string  `json:"tagline"`
	Homepage           string  `json:"homepage"`
	ImdbID             string  `json:"imdb_id"`
	OriginalLanguage   string  `json:"original_language"` // ISO 639-1, e.g. "en"
	ProductionCountries []struct {
		Name string `json:"name"`
	} `json:"production_countries"`
	Credits struct {
		Cast []CastMember `json:"cast"`
	} `json:"credits"`
	Videos struct {
		Results []Video `json:"results"`
	} `json:"videos"`
	ReleaseDates struct {
		Results []CountryRelease `json:"results"`
	} `json:"release_dates"`
	AlternativeTitles struct {
		Titles []struct {
			Title string `json:"title"`
		} `json:"titles"`
	} `json:"alternative_titles"`
}

// Genre is a TMDb genre entry.
type Genre struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

// CastMember is a single cast entry from the credits endpoint.
type CastMember struct {
	Name  string `json:"name"`
	Order int    `json:"order"`
}

// Video is a single video entry (trailer, teaser, etc.).
type Video struct {
	Key  string `json:"key"`  // YouTube video ID
	Site string `json:"site"` // "YouTube"
	Type string `json:"type"` // "Trailer", "Teaser", etc.
}

// CountryRelease groups release dates and certifications by country.
type CountryRelease struct {
	ISO   string `json:"iso_3166_1"`
	Dates []struct {
		Certification string `json:"certification"`
		Type          int    `json:"type"` // 3 = theatrical
	} `json:"release_dates"`
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

// GetMovie retrieves detailed movie information by TMDb movie ID, including
// credits, videos, release dates, and alternative titles via append_to_response.
func (c *Client) GetMovie(ctx context.Context, id int) (*MovieDetail, error) {
	u := fmt.Sprintf("%s/movie/%d?api_key=%s&append_to_response=credits,videos,release_dates,alternative_titles", c.BaseURL, id, c.apiKey)
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
