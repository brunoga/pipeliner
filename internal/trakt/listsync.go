package trakt

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
)

// ItemIDs identifies one movie or show in a list mutation request. At least
// one ID must be set; Trakt matches on whichever are present.
type ItemIDs struct {
	Trakt int    `json:"trakt,omitempty"`
	IMDB  string `json:"imdb,omitempty"`
	TMDB  int    `json:"tmdb,omitempty"`
	TVDB  int    `json:"tvdb,omitempty"`
}

// IsZero reports whether no ID is set.
func (i ItemIDs) IsZero() bool {
	return i.Trakt == 0 && i.IMDB == "" && i.TMDB == 0 && i.TVDB == 0
}

// ListItem wraps ItemIDs in the shape Trakt's sync endpoints expect.
type ListItem struct {
	IDs ItemIDs `json:"ids"`
}

// ListItemsBody is the request body for list add/remove endpoints.
type ListItemsBody struct {
	Movies []ListItem `json:"movies,omitempty"`
	Shows  []ListItem `json:"shows,omitempty"`
}

// Empty reports whether the body contains no items.
func (b ListItemsBody) Empty() bool { return len(b.Movies) == 0 && len(b.Shows) == 0 }

// SyncResponse is Trakt's response to list add/remove requests. Added is
// populated by add requests, Deleted by remove requests. NotFound lists the
// submitted IDs Trakt could not match to any item.
type SyncResponse struct {
	Added    map[string]int `json:"added"`
	Deleted  map[string]int `json:"deleted"`
	Existing map[string]int `json:"existing"`
	NotFound ListItemsBody  `json:"not_found"`
}

// AddListItems adds items to a list owned by the authenticated user.
// list is either "watchlist" (POST /sync/watchlist) or a personal list slug
// (POST /users/me/lists/{slug}/items). Requires an access token.
func (c *Client) AddListItems(ctx context.Context, list string, body ListItemsBody) (*SyncResponse, error) {
	return c.mutateList(ctx, list, false, body)
}

// RemoveListItems removes items from a list owned by the authenticated user.
// list is either "watchlist" (POST /sync/watchlist/remove) or a personal list
// slug (POST /users/me/lists/{slug}/items/remove). Requires an access token.
func (c *Client) RemoveListItems(ctx context.Context, list string, body ListItemsBody) (*SyncResponse, error) {
	return c.mutateList(ctx, list, true, body)
}

func (c *Client) mutateList(ctx context.Context, list string, remove bool, body ListItemsBody) (*SyncResponse, error) {
	if c.AccessToken == "" {
		return nil, fmt.Errorf("trakt: list mutation requires an access token")
	}
	if body.Empty() {
		return nil, fmt.Errorf("trakt: list mutation requires at least one item")
	}

	var endpoint string
	if list == "watchlist" {
		endpoint = BaseURL + "/sync/watchlist"
	} else {
		endpoint = fmt.Sprintf("%s/users/me/lists/%s/items", BaseURL, url.PathEscape(list))
	}
	if remove {
		endpoint += "/remove"
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, true)

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: update list %q: %w", list, err)
	}
	defer resp.Body.Close()
	// Trakt returns 201 for adds and 200 for removes.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		return nil, fmt.Errorf("trakt: HTTP %d updating list %q", resp.StatusCode, list)
	}

	var out SyncResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("trakt: decode list update response: %w", err)
	}
	return &out, nil
}
