// Package trakt provides a minimal Trakt.tv v2 API client.
//
// Public endpoints (trending, popular, watched) require only a Client ID.
// User-private endpoints (watchlist, ratings, collection) additionally require
// an OAuth access token obtained outside this package.
//
// API docs: https://trakt.docs.apiary.io/
package trakt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// BaseURL is the Trakt API base URL. Exported so tests can override it.
var BaseURL = "https://api.trakt.tv"

// IDs holds the various external identifiers for a Trakt item.
type IDs struct {
	Trakt int    `json:"trakt"`
	Slug  string `json:"slug"`
	IMDB  string `json:"imdb"`
	TMDB  int    `json:"tmdb"`
	TVDB  int    `json:"tvdb"`
}

// Item represents a movie or show retrieved from any Trakt list or search.
type Item struct {
	Title      string   `json:"title"`
	Year       int      `json:"year"`
	IDs        IDs      `json:"ids"`
	Overview   string   `json:"overview"`
	Rating     float64  `json:"rating"`
	Votes      int      `json:"votes"`
	Genres     []string `json:"genres"`
	UserRating int      // rating given by the user (ratings list only)
}

// Client is a Trakt.tv v2 REST API client.
type Client struct {
	ClientID    string
	AccessToken string // required for private endpoints (watchlist, ratings, collection)
	http        *http.Client
}

// New creates a Client for public Trakt endpoints.
func New(clientID string) *Client {
	return &Client{
		ClientID: clientID,
		http:     &http.Client{Timeout: 15 * time.Second},
	}
}

// NewWithToken creates a Client that can access private user endpoints.
func NewWithToken(clientID, accessToken string) *Client {
	c := New(clientID)
	c.AccessToken = accessToken
	return c
}

// GetList fetches a named list from Trakt and returns the items.
//
// itemType is "shows" or "movies".
// list is one of: "trending", "popular", "watched", "watchlist", "ratings", "collection".
// limit is the maximum number of results (ignored for user-private lists which return all).
func (c *Client) GetList(ctx context.Context, itemType, list string, limit int) ([]Item, error) {
	var endpoint string
	private := false
	switch list {
	case "trending":
		endpoint = fmt.Sprintf("%s/%s/trending?extended=full&limit=%d", BaseURL, itemType, limit)
	case "popular":
		endpoint = fmt.Sprintf("%s/%s/popular?extended=full&limit=%d", BaseURL, itemType, limit)
	case "watched":
		endpoint = fmt.Sprintf("%s/%s/watched/weekly?extended=full&limit=%d", BaseURL, itemType, limit)
	case "watchlist":
		endpoint = fmt.Sprintf("%s/users/me/watchlist/%s?extended=full", BaseURL, itemType)
		private = true
	case "ratings":
		endpoint = fmt.Sprintf("%s/users/me/ratings/%s?extended=full", BaseURL, itemType)
		private = true
	case "collection":
		endpoint = fmt.Sprintf("%s/users/me/collection/%s?extended=full", BaseURL, itemType)
		private = true
	default:
		return nil, fmt.Errorf("trakt: unknown list %q", list)
	}

	if private && c.AccessToken == "" {
		return nil, fmt.Errorf("trakt: list %q requires an access_token", list)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, private)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: get %s/%s: %w", itemType, list, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trakt: HTTP %d for %s/%s", resp.StatusCode, itemType, list)
	}

	var raw []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("trakt: decode %s/%s: %w", itemType, list, err)
	}

	singular := itemType[:len(itemType)-1] // "shows" → "show", "movies" → "movie"
	return parseListItems(raw, singular, list)
}

// Search searches Trakt for movies or shows matching query.
// itemType is "show" or "movie" (singular).
func (c *Client) Search(ctx context.Context, itemType, query string) ([]Item, error) {
	u := fmt.Sprintf("%s/search/%s?query=%s&extended=full", BaseURL, itemType, url.QueryEscape(query))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, false)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: search %s %q: %w", itemType, query, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trakt: search HTTP %d", resp.StatusCode)
	}

	var results []struct {
		Type  string          `json:"type"`
		Score float64         `json:"score"`
		Item  json.RawMessage // key matches itemType, decoded below
	}
	// The JSON has either a "show" or "movie" key; decode generically then extract.
	var rawResults []json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&rawResults); err != nil {
		return nil, fmt.Errorf("trakt: search decode: %w", err)
	}
	_ = results

	var items []Item
	for _, raw := range rawResults {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			continue
		}
		itemRaw, ok := wrapper[itemType]
		if !ok {
			continue
		}
		var it Item
		if err := json.Unmarshal(itemRaw, &it); err != nil {
			continue
		}
		items = append(items, it)
	}
	return items, nil
}

func (c *Client) setHeaders(req *http.Request, withAuth bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-key", c.ClientID)
	req.Header.Set("trakt-api-version", "2")
	if withAuth && c.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)
	}
}

// parseListItems converts raw JSON list responses (which differ by list type)
// to a uniform []Item slice.
func parseListItems(raw []json.RawMessage, singular, list string) ([]Item, error) {
	var items []Item
	for _, r := range raw {
		var wrapper map[string]json.RawMessage
		if err := json.Unmarshal(r, &wrapper); err != nil {
			continue
		}

		// Nested item (trending / watched / watchlist / ratings / collection)
		if itemRaw, ok := wrapper[singular]; ok {
			var it Item
			if err := json.Unmarshal(itemRaw, &it); err != nil {
				continue
			}
			// Capture user rating when available
			if list == "ratings" {
				var ratingWrapper struct {
					Rating int `json:"rating"`
				}
				json.Unmarshal(r, &ratingWrapper) //nolint:errcheck
				it.UserRating = ratingWrapper.Rating
			}
			items = append(items, it)
			continue
		}

		// Popular list: the wrapper IS the item
		var it Item
		if err := json.Unmarshal(r, &it); err != nil {
			continue
		}
		if it.Title != "" {
			items = append(items, it)
		}
	}
	return items, nil
}
