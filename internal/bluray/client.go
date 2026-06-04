package bluray

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// defaultBaseURL is the production blu-ray.com origin. Override via WithBaseURL
// for tests against an httptest.Server.
const defaultBaseURL = "https://www.blu-ray.com"

// defaultUserAgent is a generic Chrome UA. The site returns no-index pages to
// our project UA, so the default has to look like a browser. Override via
// WithUserAgent if needed.
const defaultUserAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 " +
	"(KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"

// Client fetches and parses blu-ray.com pages. Safe for concurrent use.
type Client struct {
	baseURL   string
	country   string // lowercase ISO short, e.g. "us", "uk"
	userAgent string
	http      *http.Client

	gateMu sync.Mutex
	next   time.Time // earliest time the next request may go out
	gap    time.Duration
}

// Option configures a Client.
type Option func(*Client)

// WithBaseURL overrides the origin. Pass an httptest.Server URL in tests.
func WithBaseURL(u string) Option { return func(c *Client) { c.baseURL = u } }

// WithCountry sets the locale cookie sent with every request, e.g. "us", "uk",
// "ca", "de". Defaults to "us".
func WithCountry(c string) Option {
	return func(cl *Client) { cl.country = strings.ToLower(strings.TrimSpace(c)) }
}

// WithUserAgent overrides the default Chrome UA.
func WithUserAgent(ua string) Option {
	return func(c *Client) { c.userAgent = ua }
}

// WithRequestInterval sets the minimum gap between outgoing requests. Defaults
// to 1s. Zero disables rate limiting (useful in tests).
func WithRequestInterval(d time.Duration) Option {
	return func(c *Client) { c.gap = d }
}

// New constructs a Client with the default polite-scraper settings, then
// applies opts.
func New(opts ...Option) *Client {
	c := &Client{
		baseURL:   defaultBaseURL,
		country:   "us",
		userAgent: defaultUserAgent,
		http:      &http.Client{Timeout: 30 * time.Second},
		gap:       1 * time.Second,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// wait blocks until the rate-limit gate opens, then advances it.
func (c *Client) wait(ctx context.Context) error {
	if c.gap <= 0 {
		return nil
	}
	c.gateMu.Lock()
	now := time.Now()
	sleep := max(time.Until(c.next), 0)
	c.next = now.Add(sleep + c.gap)
	c.gateMu.Unlock()

	if sleep <= 0 {
		return nil
	}
	t := time.NewTimer(sleep)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// get performs a rate-limited GET and returns the response body. Non-2xx is
// returned as an error.
func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	if err := c.wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bluray: build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.5")
	if c.country != "" {
		req.AddCookie(&http.Cookie{Name: "country", Value: c.country})
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bluray: GET %s: %w", rawURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bluray: GET %s: HTTP %d", rawURL, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// GetRelease fetches a release detail page by its numeric ID. If slug is
// supplied (typically from an IndexEntry) the canonical URL is used directly;
// otherwise the function probes `/movies/_/<id>/` which the site redirects to
// the canonical slug.
func (c *Client) GetRelease(ctx context.Context, id, slug string) (*Release, error) {
	if id == "" {
		return nil, fmt.Errorf("bluray: GetRelease: empty id")
	}
	canonical := c.releaseURL(id, slug)
	body, err := c.get(ctx, canonical)
	if err != nil {
		return nil, err
	}
	return ParseDetail(body, canonical)
}

// releaseURL constructs a canonical detail-page URL. Slug is preferred; with
// an empty slug we fall back to a placeholder that the site is willing to
// redirect.
func (c *Client) releaseURL(id, slug string) string {
	if slug == "" {
		slug = "_"
	}
	return fmt.Sprintf("%s/movies/%s/%s/", c.baseURL, slug, id)
}

// ListMonth fetches the release calendar for a given (year, month) and returns
// every release row found, in document order. Months in the future have no
// "releasedate" yet — those rows are skipped.
func (c *Client) ListMonth(ctx context.Context, year, month int) ([]CalendarEntry, error) {
	return c.listMonth(ctx, "/movies/releasedates.php", year, month, "")
}

// List3DMonth fetches the 3D-specific release calendar for (year, month).
// Uses the same movies[N] = {…} JS-array markup as ListMonth — but the rows
// are pre-filtered to BD3D releases server-side. Every returned CalendarEntry
// has Format=FormatBD3D regardless of what FormatFromTitle would have inferred,
// which is the source of truth for edge-case titles like "Alita: Battle Angel
// 4K and 3D" that don't end with " 3D".
func (c *Client) List3DMonth(ctx context.Context, year, month int) ([]CalendarEntry, error) {
	return c.listMonth(ctx, "/3d/releasedates.php", year, month, FormatBD3D)
}

// listMonth is the shared implementation behind ListMonth / List3DMonth. If
// forceFormat is non-empty, every parsed CalendarEntry has its Format
// overwritten — used by List3DMonth to honour the server-side BD3D filter.
func (c *Client) listMonth(ctx context.Context, path string, year, month int, forceFormat Format) ([]CalendarEntry, error) {
	if year <= 0 || month < 1 || month > 12 {
		return nil, fmt.Errorf("bluray: listMonth: invalid (year=%d, month=%d)", year, month)
	}
	q := url.Values{}
	q.Set("year", fmt.Sprintf("%04d", year))
	q.Set("month", fmt.Sprintf("%02d", month))
	body, err := c.get(ctx, c.baseURL+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	rows, err := ParseCalendar(body)
	if err != nil {
		return nil, err
	}
	if forceFormat != "" {
		for i := range rows {
			rows[i].Format = forceFormat
		}
	}
	return rows, nil
}

// SearchTitle performs a quicksearch in the bluraymovies section and returns
// every result row found. year is currently used only as a result-filter hint
// after the fact — the site does not accept a year query param.
//
// This endpoint is Disallow'd by blu-ray.com's robots.txt; callers are
// expected to cache results aggressively (see plugins/processor/metainfo/bluray).
func (c *Client) SearchTitle(ctx context.Context, title string, year int) ([]IndexEntry, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("bluray: SearchTitle: empty title")
	}
	q := url.Values{}
	q.Set("quicksearch", "1")
	q.Set("quicksearch_keyword", title)
	q.Set("section", "bluraymovies")
	q.Set("quicksearch_country", strings.ToUpper(c.country))
	body, err := c.get(ctx, c.baseURL+"/search/?"+q.Encode())
	if err != nil {
		return nil, err
	}
	results, err := ParseSearch(body)
	if err != nil {
		return nil, err
	}
	if year > 0 {
		filtered := results[:0]
		for _, r := range results {
			if r.Year == 0 || r.Year == year {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}
	return results, nil
}
