package bluray

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// DefaultBaseURL is the production blu-ray.com origin. Override via WithBaseURL
// for tests against an httptest.Server.
const DefaultBaseURL = "https://www.blu-ray.com"

// Option configures a Client.
type Option func(*Client)

// WithTransport sets a custom HTTP/TLS Transport.
func WithTransport(t Transport) Option {
	return func(c *Client) {
		if t != nil {
			c.transport = t
		}
	}
}

// WithCountry sets the default country/region edition filter (e.g., "us", "uk", "all").
func WithCountry[T ~string](country T) Option {
	return func(c *Client) {
		if s := strings.ToLower(strings.TrimSpace(string(country))); s != "" {
			c.country = Country(s)
		}
	}
}

// WithUserAgent overrides the default Chrome User-Agent header.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.profile.UserAgent = ua
		}
	}
}

// WithRequestInterval sets the minimum gap between outgoing requests.
// Zero disables rate limiting (useful in tests).
func WithRequestInterval(d time.Duration) Option {
	return func(c *Client) {
		if d <= 0 {
			c.rateLimiter = nil
		} else {
			c.rateLimiter = NewJitterRateLimiter(d, 0)
		}
	}
}

// WithRateLimiter sets a custom RateLimiter. Pass nil to disable rate limiting.
func WithRateLimiter(rl RateLimiter) Option {
	return func(c *Client) {
		c.rateLimiter = rl
	}
}

// WithRateLimit configures a JitterRateLimiter with minInterval and maxJitter.
func WithRateLimit(minInterval, maxJitter time.Duration) Option {
	return func(c *Client) {
		if minInterval <= 0 && maxJitter <= 0 {
			c.rateLimiter = nil
		} else {
			c.rateLimiter = NewJitterRateLimiter(minInterval, maxJitter)
		}
	}
}

// WithCache sets a ResponseCache (e.g., MemoryCache or DiskCache) to avoid redundant fetches.
func WithCache(cache ResponseCache) Option {
	return func(c *Client) {
		c.cache = cache
	}
}

// WithBrowserProfile sets a custom BrowserProfile (User-Agent and Client Hints).
func WithBrowserProfile(profile BrowserProfile) Option {
	return func(c *Client) {
		c.profile = profile
	}
}

// WithWarmup enables or disables automatic session warm-up (visiting homepage first to acquire session cookies).
func WithWarmup(enabled bool) Option {
	return func(c *Client) {
		c.autoWarmup = enabled
	}
}

// WithBaseURL overrides the base URL (useful for testing against a local mock server).
func WithBaseURL(baseURL string) Option {
	return func(c *Client) {
		c.baseURL = strings.TrimRight(baseURL, "/")
	}
}

// Client fetches and parses Blu-ray.com data using browser simulation.
// Safe for concurrent use.
type Client struct {
	baseURL     string
	transport   Transport
	profile     BrowserProfile
	rateLimiter RateLimiter
	cache       ResponseCache

	mu          sync.Mutex
	country     Country
	autoWarmup  bool
	warmedUp    bool
	lastReferer string
}

// New constructs a Client with browser simulation defaults:
//   - StandardTransport with cookie jar and gzip support
//   - Chrome 131 header profile + Client Hints + Fetch Metadata
//   - JitterRateLimiter (400ms min interval + up to 250ms random jitter)
//   - CountryUS default country filter (and "full" search result layout cookie)
func New(opts ...Option) *Client {
	stdTransport, _ := NewStandardTransport(30 * time.Second)

	c := &Client{
		baseURL:     DefaultBaseURL,
		transport:   stdTransport,
		profile:     DefaultChromeProfile(),
		rateLimiter: NewJitterRateLimiter(400*time.Millisecond, 250*time.Millisecond),
		country:     CountryUS,
		autoWarmup:  false,
	}

	for _, opt := range opts {
		opt(c)
	}

	c.syncCookies(c.country)
	return c
}

// NewClient is an alias for New that returns (*Client, error) for compatibility with bluraycom callers.
func NewClient(opts ...Option) (*Client, error) {
	return New(opts...), nil
}

func (c *Client) syncCookies(country Country) {
	if c.transport == nil {
		return
	}
	u, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return
	}
	domain := u.Hostname()
	if strings.HasSuffix(domain, "blu-ray.com") {
		domain = ".blu-ray.com"
	}
	cookies := []*http.Cookie{
		{Name: "listlayout_7", Value: "full", Path: "/"},   //nolint:gosec // G124: outgoing client cookie jar entry, not a Set-Cookie response header
		{Name: "listlayout_20", Value: "thumb", Path: "/"}, //nolint:gosec // G124: outgoing client cookie jar entry, not a Set-Cookie response header
	}
	if strings.HasSuffix(domain, "blu-ray.com") {
		for _, ck := range cookies {
			ck.Domain = domain
		}
	}
	if country != "" {
		ck := &http.Cookie{ //nolint:gosec // G124: outgoing client cookie jar entry, not a Set-Cookie response header
			Name:  "country",
			Value: strings.ToLower(string(country)),
			Path:  "/",
		}
		if strings.HasSuffix(domain, "blu-ray.com") {
			ck.Domain = domain
		}
		cookies = append(cookies, ck)
	}
	c.transport.SetCookies(u, cookies)
}

// SetCountry updates the active country filter on the client and updates its cookie jar.
func (c *Client) SetCountry(country Country) {
	c.mu.Lock()
	c.country = Country(strings.ToLower(string(country)))
	c.syncCookies(c.country)
	c.mu.Unlock()
}

// Country returns the currently configured Country filter.
func (c *Client) Country() Country {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.country
}

// Warmup visits the blu-ray.com homepage to establish initial session cookies and set the Referer chain.
func (c *Client) Warmup(ctx context.Context) error {
	c.mu.Lock()
	if c.warmedUp {
		c.mu.Unlock()
		return nil
	}
	c.mu.Unlock()

	if c.rateLimiter != nil {
		if err := c.rateLimiter.Wait(ctx); err != nil {
			return err
		}
	}

	homeURL := c.baseURL + "/"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, homeURL, nil)
	if err != nil {
		return fmt.Errorf("warmup request creation failed: %w", err)
	}
	ApplyBrowserHeaders(req, c.profile, "", false)

	resp, err := c.transport.Do(req)
	if err != nil {
		return fmt.Errorf("warmup request failed: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode >= 400 {
		return fmt.Errorf("warmup returned HTTP %d (%s)", resp.StatusCode, resp.Status)
	}

	c.mu.Lock()
	c.warmedUp = true
	c.lastReferer = homeURL
	c.mu.Unlock()
	return nil
}

// wait blocks until the rate-limit gate opens.
func (c *Client) wait(ctx context.Context) error {
	if c.rateLimiter == nil {
		return nil
	}
	return c.rateLimiter.Wait(ctx)
}

// get performs a rate-limited, cached GET request with coherent browser headers and optional country override.
func (c *Client) get(ctx context.Context, targetURL string, opts ...any) ([]byte, error) {
	var countryOverride Country
	var isAJAX bool
	for _, o := range opts {
		switch v := o.(type) {
		case Country:
			countryOverride = v
		case bool:
			isAJAX = v
		}
	}

	effectiveCountry := c.Country()
	if countryOverride != "" {
		effectiveCountry = Country(strings.ToLower(string(countryOverride)))
	}

	cacheKey := fmt.Sprintf("GET|%s|%s", effectiveCountry, targetURL)
	if c.cache != nil {
		if data, ok := c.cache.Get(cacheKey); ok {
			return data, nil
		}
	}

	if c.autoWarmup {
		if err := c.Warmup(ctx); err != nil {
			return nil, err
		}
	}

	if err := c.wait(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()
	prevCountry := c.country
	if effectiveCountry != prevCountry {
		c.syncCookies(effectiveCountry)
	}
	referer := c.lastReferer
	if referer == "" {
		referer = c.baseURL + "/"
	}
	c.mu.Unlock()

	defer func() {
		if effectiveCountry != prevCountry {
			c.mu.Lock()
			c.syncCookies(prevCountry)
			c.mu.Unlock()
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("bluray: build request: %w", err)
	}
	ApplyBrowserHeaders(req, c.profile, referer, isAJAX)

	resp, err := c.transport.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bluray: GET %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bluray: GET %s: HTTP %d", targetURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bluray: read %s: %w", targetURL, err)
	}

	if !isAJAX {
		c.mu.Lock()
		c.lastReferer = targetURL
		c.mu.Unlock()
	}

	if c.cache != nil {
		c.cache.Set(cacheKey, body)
	}

	return body, nil
}

// postForm performs a rate-limited, cached POST request with form data and XHR browser headers.
func (c *Client) postForm(ctx context.Context, targetURL string, form url.Values) ([]byte, error) {
	encoded := form.Encode()
	cacheKey := fmt.Sprintf("POST|%s|%s", targetURL, encoded)
	if c.cache != nil {
		if data, ok := c.cache.Get(cacheKey); ok {
			return data, nil
		}
	}

	if c.autoWarmup {
		if err := c.Warmup(ctx); err != nil {
			return nil, err
		}
	}

	if err := c.wait(ctx); err != nil {
		return nil, err
	}

	c.mu.Lock()
	referer := c.lastReferer
	if referer == "" {
		referer = c.baseURL + "/"
	}
	c.mu.Unlock()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, targetURL, strings.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("bluray: build POST request: %w", err)
	}
	ApplyBrowserHeaders(req, c.profile, referer, true)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded; charset=UTF-8")
	req.Header.Set("Origin", c.baseURL)

	resp, err := c.transport.Do(req)
	if err != nil {
		return nil, fmt.Errorf("bluray: POST %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("bluray: POST %s: HTTP %d", targetURL, resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("bluray: read POST %s: %w", targetURL, err)
	}

	if c.cache != nil {
		c.cache.Set(cacheKey, body)
	}

	return body, nil
}

// GetRelease fetches and parses a Blu-ray release detail page given its numeric ID
// (and optional slug) or full URL.
func (c *Client) GetRelease(ctx context.Context, idOrURL string, slug ...string) (*Release, error) {
	idOrURL = strings.TrimSpace(idOrURL)
	if idOrURL == "" {
		return nil, fmt.Errorf("bluray: GetRelease: empty id")
	}
	var s string
	if len(slug) > 0 {
		s = slug[0]
	}
	canonical := c.releaseURL(idOrURL, s)
	body, err := c.get(ctx, canonical)
	if err != nil {
		return nil, err
	}
	return ParseReleaseDetails(body, canonical)
}

func (c *Client) releaseURL(idOrURL, slug string) string {
	if strings.HasPrefix(idOrURL, "http://") || strings.HasPrefix(idOrURL, "https://") {
		return idOrURL
	}
	if strings.HasPrefix(idOrURL, "/") {
		return c.baseURL + idOrURL
	}
	if slug == "" {
		slug = "_"
	}
	return fmt.Sprintf("%s/movies/%s/%s/", c.baseURL, slug, strings.Trim(idOrURL, "/"))
}

// ListMonth fetches the release calendar for a given (year, month) and returns
// every release row found, in document order.
func (c *Client) ListMonth(ctx context.Context, year, month int) ([]CalendarEntry, error) {
	return c.listMonth(ctx, "/movies/releasedates.php", year, month, "")
}

// List3DMonth fetches the 3D-specific release calendar for (year, month).
// Every returned CalendarEntry has Format=FormatBD3D.
func (c *Client) List3DMonth(ctx context.Context, year, month int) ([]CalendarEntry, error) {
	return c.listMonth(ctx, "/3d/releasedates.php", year, month, FormatBD3D)
}

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
// every result row found. year is used as a result-filter hint after the fact.
func (c *Client) SearchTitle(ctx context.Context, title string, year int) ([]IndexEntry, error) {
	if strings.TrimSpace(title) == "" {
		return nil, fmt.Errorf("bluray: SearchTitle: empty title")
	}
	countryParam := strings.ToUpper(string(c.Country()))
	if strings.EqualFold(countryParam, "all") {
		countryParam = "all"
	}
	q := url.Values{}
	q.Set("quicksearch", "1")
	q.Set("quicksearch_keyword", title)
	q.Set("section", string(SectionBluRay))
	q.Set("quicksearch_country", countryParam)
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

// QuickSearch queries Blu-ray.com's instant autocomplete endpoint (/search/quicksearch.php)
// for a given section (e.g., Section3D, SectionBluRay, SectionTheatrical) and keyword.
func (c *Client) QuickSearch(ctx context.Context, section SearchSection, keyword string, countryOverride ...Country) ([]QuickSearchResult, error) {
	country := c.Country()
	if len(countryOverride) > 0 && countryOverride[0] != "" {
		country = countryOverride[0]
	}
	countryParam := strings.ToUpper(string(country))
	if strings.EqualFold(countryParam, "all") {
		countryParam = "all"
	}

	form := url.Values{
		"section": {string(section)},
		"userid":  {"-1"},
		"country": {countryParam},
		"keyword": {keyword},
	}

	body, err := c.postForm(ctx, c.baseURL+"/search/quicksearch.php", form)
	if err != nil {
		return nil, err
	}
	return ParseQuickSearch(body)
}

// Search executes an advanced Blu-ray search query against /movies/search.php and returns a single page of results.
func (c *Client) Search(ctx context.Context, q SearchQuery) (*SearchResultPage, error) {
	params := url.Values{}
	params.Set("action", "search")

	if q.Keyword != "" {
		params.Set("keyword", q.Keyword)
	}
	if q.UPC != "" {
		params.Set("upc", strings.TrimSpace(q.UPC))
	}
	if q.EAN != "" {
		params.Set("ean", strings.TrimSpace(q.EAN))
	}
	if q.ASIN != "" {
		params.Set("asin", strings.TrimSpace(q.ASIN))
	}
	if q.BluRay3D {
		params.Set("other_bluray3d", "1")
	}
	if q.Anaglyph3D {
		params.Set("other_anaglyph3d", "1")
	}
	if q.UltraHD4K {
		params.Set("ultrahd", "1")
	}
	if q.HDR10 {
		params.Set("hdr10", "1")
	}
	if q.DolbyVision {
		params.Set("dolbyvision", "1")
	}
	if q.YearFrom > 0 {
		params.Set("yearfrom", strconv.Itoa(q.YearFrom))
	}
	if q.YearTo > 0 {
		params.Set("yearto", strconv.Itoa(q.YearTo))
	}
	if q.ReleaseYear > 0 {
		params.Set("releaseyear", strconv.Itoa(q.ReleaseYear))
	}
	if q.Audio != "" {
		params.Set("audio", q.Audio)
	}
	if q.Subtitles != "" {
		params.Set("subtitles", q.Subtitles)
	}
	if q.Synopsis != "" {
		params.Set("synopsis", q.Synopsis)
	}
	if q.BoxSet {
		params.Set("boxset", "1")
	}
	if q.Television {
		params.Set("television", "1")
	}
	for _, g := range q.Genres {
		if g != "" {
			params.Set(g, "1")
		}
	}
	if q.SortBy != "" {
		params.Set("sortby", string(q.SortBy))
	}
	if q.Page > 0 {
		params.Set("page", strconv.Itoa(q.Page))
	}
	params.Set("submit", "Search")

	effectiveCountry := c.Country()
	if q.Country != "" {
		effectiveCountry = q.Country
	}

	searchURL := c.baseURL + "/movies/search.php?" + params.Encode()
	body, err := c.get(ctx, searchURL, effectiveCountry, false)
	if err != nil {
		return nil, err
	}

	return ParseSearchResults(body, effectiveCountry, q.Page)
}

// SearchByBarcode searches for Blu-ray releases matching a 12-digit UPC or 13-digit EAN barcode.
func (c *Client) SearchByBarcode(ctx context.Context, barcode string, countryOverride ...Country) ([]ReleaseSummary, error) {
	clean := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, barcode)

	var q SearchQuery
	if len(clean) == 13 {
		q.EAN = clean
	} else {
		q.UPC = clean
	}
	if len(countryOverride) > 0 && countryOverride[0] != "" {
		q.Country = countryOverride[0]
	} else {
		q.Country = CountryAll
	}

	page, err := c.Search(ctx, q)
	if err != nil {
		return nil, err
	}
	return page.Results, nil
}

// List3DReleases fetches a single page (0-indexed) of 3D Blu-ray releases for the given country
// sorted by sortBy (defaults to SortPopularity).
func (c *Client) List3DReleases(ctx context.Context, page int, sortBy SortBy, countryOverride ...Country) (*SearchResultPage, error) {
	if sortBy == "" {
		sortBy = SortPopularity
	}
	q := SearchQuery{
		BluRay3D: true,
		SortBy:   sortBy,
		Page:     page,
	}
	if len(countryOverride) > 0 && countryOverride[0] != "" {
		q.Country = countryOverride[0]
	}
	return c.Search(ctx, q)
}

// ListAll3DReleases iterates through pages of 3D Blu-ray releases up to maxPages (if maxPages <= 0,
// it fetches all available pages) and returns the combined slice of 3D Blu-ray releases and total match count.
func (c *Client) ListAll3DReleases(ctx context.Context, maxPages int, sortBy SortBy, countryOverride ...Country) ([]ReleaseSummary, int, error) {
	var all []ReleaseSummary
	totalMatches := 0

	for pageNum := 0; ; pageNum++ {
		if maxPages > 0 && pageNum >= maxPages {
			break
		}
		resPage, err := c.List3DReleases(ctx, pageNum, sortBy, countryOverride...)
		if err != nil {
			return all, totalMatches, err
		}
		if pageNum == 0 {
			totalMatches = resPage.TotalMatches
		}
		if len(resPage.Results) == 0 {
			break
		}
		all = append(all, resPage.Results...)
		if !resPage.HasNextPage || (totalMatches > 0 && len(all) >= totalMatches) {
			break
		}
	}

	return all, totalMatches, nil
}

// GetMovie fetches and parses a master theatrical movie page given its URL or numeric movie ID.
func (c *Client) GetMovie(ctx context.Context, urlOrID string) (*MovieDetails, error) {
	targetURL := c.resolveMovieURL(urlOrID)
	body, err := c.get(ctx, targetURL)
	if err != nil {
		return nil, err
	}
	return ParseMovieDetails(body, targetURL)
}

func (c *Client) resolveMovieURL(urlOrID string) string {
	urlOrID = strings.TrimSpace(urlOrID)
	if strings.HasPrefix(urlOrID, "http://") || strings.HasPrefix(urlOrID, "https://") {
		return urlOrID
	}
	if strings.HasPrefix(urlOrID, "/") {
		return c.baseURL + urlOrID
	}
	return fmt.Sprintf("%s/Movie/%s/", c.baseURL, strings.Trim(urlOrID, "/"))
}

// GetMovieReleases fetches all releases across all countries and formats for a master movie ID (or movie URL)
// via Blu-ray.com's /products/menu_ajax.php?p=<MovieID>&c=20&action=showreleasesall endpoint.
func (c *Client) GetMovieReleases(ctx context.Context, movieIDOrURL string) (*MovieReleases, error) {
	movieID := strings.TrimSpace(movieIDOrURL)
	if strings.Contains(movieID, "/") {
		movieID = extractIDFromURL(movieID)
	}
	if movieID == "" {
		return nil, fmt.Errorf("invalid movie ID or URL: %q", movieIDOrURL)
	}

	ajaxURL := fmt.Sprintf("%s/products/menu_ajax.php?p=%s&c=20&action=showreleasesall", c.baseURL, url.QueryEscape(movieID))
	body, err := c.get(ctx, ajaxURL, CountryAll, true)
	if err != nil {
		return nil, err
	}
	return ParseMovieReleasesAll(body, movieID)
}

// SearchTheatricalMovies searches for master theatrical movie entries matching keyword.
func (c *Client) SearchTheatricalMovies(ctx context.Context, keyword string) ([]MovieDetails, int, error) {
	params := url.Values{
		"quicksearch":         {"1"},
		"quicksearch_country": {"all"},
		"quicksearch_keyword": {keyword},
		"section":             {string(SectionTheatrical)},
	}
	targetURL := c.baseURL + "/search/?" + params.Encode()
	body, err := c.get(ctx, targetURL, CountryAll, false)
	if err != nil {
		return nil, 0, err
	}
	return ParseTheatricalSearch(body)
}

// CheckMovieHas3D checks whether a movie (specified by title keyword, theatrical movie URL/ID,
// or Blu-ray release URL) has any 3D Blu-ray releases (optionally filtered by country, or across all countries if CountryAll/empty).
// It combines master movie edition lookups with direct 3D search fallback so that both theatrical titles
// and country-specific 3D releases are accurately discovered.
func (c *Client) CheckMovieHas3D(ctx context.Context, titleOrURL string, countryFilter ...Country) (*Movie3DCheckResult, error) {
	filterCountry := CountryAll
	if len(countryFilter) > 0 && countryFilter[0] != "" {
		filterCountry = Country(strings.ToLower(string(countryFilter[0])))
	}

	result := &Movie3DCheckResult{
		Query:     titleOrURL,
		CheckedAt: time.Now(),
	}

	q := strings.TrimSpace(titleOrURL)
	var movieID string

	// Case 1: Caller passed a Blu-ray release URL (/movies/...)
	if strings.Contains(q, "/movies/") {
		rel, err := c.GetRelease(ctx, q)
		if err != nil {
			return nil, err
		}
		movieID = rel.ParentMovieID
		if movieID == "" && (rel.Video.Is3D || rel.Discs.Is3D) {
			result.Has3D = true
			result.Releases3D = []ReleaseSummary{{
				ID:            rel.ID,
				Title:         rel.Title,
				FormatSummary: rel.Subtitle,
				URL:           rel.URL,
				Country:       rel.CountryCode,
				Studio:        rel.Studio,
				Year:          rel.Year,
				RuntimeMins:   rel.RuntimeMin,
				Rated:         rel.Rated,
				ReleaseDate:   rel.ReleaseDate,
				CoverURL:      rel.CoverURL,
				Is3D:          true,
				Is4K:          rel.Video.Is4K || rel.Discs.Is4K,
			}}
			return result, nil
		}
	} else if strings.HasPrefix(q, "http://") || strings.HasPrefix(q, "https://") {
		movieID = extractIDFromURL(q)
	} else if isDigitsOnly(q) {
		movieID = q
	} else {
		movies, _, err := c.SearchTheatricalMovies(ctx, q)
		if err == nil && len(movies) > 0 {
			best := selectBestMovieMatch(movies, q)
			movieID = best.ID
			result.Movie = &best
		}
	}

	if movieID != "" {
		if result.Movie == nil || len(result.Movie.TechnicalDetails) == 0 {
			if m, err := c.GetMovie(ctx, movieID); err == nil {
				if m.Rating == 0 && result.Movie != nil {
					m.Rating = result.Movie.Rating
				}
				result.Movie = m
			}
		}
		if releases, err := c.GetMovieReleases(ctx, movieID); err == nil {
			for _, rel := range releases.BluRay3D {
				if filterCountry == CountryAll || rel.Country == "" || strings.EqualFold(string(rel.Country), string(filterCountry)) {
					result.Releases3D = append(result.Releases3D, rel)
				}
			}
		}
	}

	if len(result.Releases3D) == 0 && !strings.HasPrefix(q, "http") && !isDigitsOnly(q) {
		page, err := c.Search(ctx, SearchQuery{
			Keyword:  q,
			BluRay3D: true,
			Country:  filterCountry,
		})
		if err == nil {
			normQ := normalizeTitle(q)
			for _, rel := range page.Results {
				if strings.Contains(normalizeTitle(rel.Title), normQ) {
					result.Releases3D = append(result.Releases3D, rel)
				}
			}
		}
	}

	result.Has3D = len(result.Releases3D) > 0
	return result, nil
}

func isDigitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

func normalizeTitle(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if m := reYearParens.FindStringIndex(s); m != nil {
		s = strings.TrimSpace(s[:m[0]])
	}
	var sb strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == ' ' {
			sb.WriteRune(r)
		}
	}
	return cleanText(sb.String())
}

func selectBestMovieMatch(movies []MovieDetails, query string) MovieDetails {
	var queryYear int
	if ym := reYearParens.FindStringSubmatch(query); len(ym) > 1 {
		queryYear, _ = strconv.Atoi(ym[1])
	}
	normQuery := normalizeTitle(query)

	if queryYear > 0 {
		for _, m := range movies {
			if normalizeTitle(m.Title) == normQuery && m.Year == queryYear {
				return m
			}
		}
	}

	for _, m := range movies {
		if normalizeTitle(m.Title) == normQuery {
			return m
		}
	}

	return movies[0]
}
