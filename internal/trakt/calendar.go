package trakt

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// CalendarEntry is one upcoming episode from the authenticated user's
// "my shows" calendar.
type CalendarEntry struct {
	FirstAired time.Time `json:"first_aired"`
	Episode    struct {
		Season int    `json:"season"`
		Number int    `json:"number"`
		Title  string `json:"title"`
	} `json:"episode"`
	Show struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
		IDs   struct {
			Trakt int    `json:"trakt"`
			TVDB  int    `json:"tvdb"`
			TMDB  int    `json:"tmdb"`
			IMDB  string `json:"imdb"`
		} `json:"ids"`
	} `json:"show"`
}

// MyCalendarShows returns the authenticated user's upcoming episodes starting
// at start for the given number of days (Trakt caps days at 33).
func (c *Client) MyCalendarShows(ctx context.Context, start time.Time, days int) ([]CalendarEntry, error) {
	if c.AccessToken == "" {
		return nil, fmt.Errorf("trakt: calendar requires an access token")
	}
	url := fmt.Sprintf("%s/calendars/my/shows/%s/%d", BaseURL, start.Format("2006-01-02"), days)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	c.setHeaders(req, true)
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("trakt: calendar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("trakt: calendar: http %d", resp.StatusCode)
	}
	var out []CalendarEntry
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("trakt: calendar: decode: %w", err)
	}
	return out, nil
}
