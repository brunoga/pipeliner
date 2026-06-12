package bluray

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
)

// FormatFromSlug derives a Format value from a release-page URL slug like
// "Avatar-3D-Blu-ray" or "Avatar-4K-Blu-ray". Unknown slugs default to BD,
// which is the most common form on the site.
func FormatFromSlug(slug string) Format {
	s := strings.ToLower(slug)
	switch {
	case strings.HasSuffix(s, "-3d-blu-ray"):
		return FormatBD3D
	case strings.HasSuffix(s, "-4k-blu-ray"):
		return FormatUHD
	case strings.HasSuffix(s, "-dvd"):
		return FormatDVD
	default:
		return FormatBD
	}
}

// FormatFromTitle is the calendar-row counterpart to FormatFromSlug. Calendar
// records carry the format in the title's trailing token rather than a URL
// slug ("The Killer 4K", "Avatar 3D", "Wanted").
func FormatFromTitle(title string) Format {
	t := strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.HasSuffix(t, " 3d"):
		return FormatBD3D
	case strings.HasSuffix(t, " 4k"):
		return FormatUHD
	default:
		return FormatBD
	}
}

// SlugAndIDFromURL extracts ("Avatar-3D-Blu-ray", "26954") from
// "https://www.blu-ray.com/movies/Avatar-3D-Blu-ray/26954/". Returns empty
// strings if the URL does not match the expected shape.
func SlugAndIDFromURL(rawURL string) (slug, id string) {
	_, tail, ok := strings.Cut(rawURL, "/movies/")
	if !ok {
		return "", ""
	}
	tail = strings.TrimSuffix(tail, "/")
	parts := strings.Split(tail, "/")
	if len(parts) < 2 {
		return "", ""
	}
	return parts[0], parts[1]
}

// ---------- detail page ----------

var (
	// Metadata strip: <a class="grey" ...>Studio</a> | <a class="grey" ...>Year</a>
	// | <span id="runtime" title="...">NNN min</span> | Rated XX |
	// <a class="grey noline" alt="...Release Date <DATE>" title="...">DATE</a>
	reStudio      = regexp.MustCompile(`<a class="grey" href="[^"]*studioid=\d+"[^>]*>([^<]+)</a>`)
	reProdYear    = regexp.MustCompile(`<a class="grey" href="[^"]*year=(\d{4})"`)
	reRuntime     = regexp.MustCompile(`<span id="runtime"[^>]*>([0-9]+)\s*min</span>`)
	reReleaseDate = regexp.MustCompile(`<a class="grey noline"[^>]*title="[^"]*Release Date ([A-Z][a-z]+ \d{1,2}, \d{4})"`)

	// Country: the flag <img> immediately following the <h1> heading carries
	// the country name as title/alt.
	reH1Flag = regexp.MustCompile(`<h1>[^<]*</h1>\s*</a>\s*<img[^>]+title="([^"]+)"`)

	// Video subheading block.
	reCodec       = regexp.MustCompile(`Codec:\s*([^<]+?)<br`)
	reResolution  = regexp.MustCompile(`Resolution:\s*([^<]+?)<br`)
	reAspectRatio = regexp.MustCompile(`Aspect ratio:\s*([^<]+?)<br`)

	// Page <title>: "Avatar 3D Blu-ray (Limited 3D Edition)" — the edition is
	// the parenthesised tail.
	rePageTitle      = regexp.MustCompile(`(?is)<title>([^<]*)</title>`)
	rePageEditionTag = regexp.MustCompile(`^(.*?)\s*\(([^)]+)\)\s*$`)
)

// ParseDetail extracts a Release from a /movies/<slug>/<id>/ page. canonicalURL
// is used to derive ID, slug, and Format — pass the URL the page was fetched
// from. Empty or absent fields are left zero; only an entirely unrecognisable
// page (no metadata strip and no title) is treated as an error.
func ParseDetail(body []byte, canonicalURL string) (*Release, error) {
	s := string(body)
	slug, id := SlugAndIDFromURL(canonicalURL)

	r := &Release{
		ID:     id,
		URL:    canonicalURL,
		Format: FormatFromSlug(slug),
	}

	if m := rePageTitle.FindStringSubmatch(s); len(m) == 2 {
		raw := strings.TrimSpace(html.UnescapeString(m[1]))
		if mm := rePageEditionTag.FindStringSubmatch(raw); len(mm) == 3 {
			r.Title = strings.TrimSpace(mm[1])
			r.Edition = strings.TrimSpace(mm[2])
		} else {
			r.Title = raw
		}
	}

	if m := reStudio.FindStringSubmatch(s); len(m) == 2 {
		r.Studio = strings.TrimSpace(html.UnescapeString(m[1]))
	}
	if m := reProdYear.FindStringSubmatch(s); len(m) == 2 {
		r.Year, _ = strconv.Atoi(m[1])
	}
	if m := reRuntime.FindStringSubmatch(s); len(m) == 2 {
		r.RuntimeMin, _ = strconv.Atoi(m[1])
	}
	if m := reReleaseDate.FindStringSubmatch(s); len(m) == 2 {
		if iso, ok := monthDayYearToISO(m[1]); ok {
			r.ReleaseDate = iso
		}
	}
	if m := reH1Flag.FindStringSubmatch(s); len(m) == 2 {
		r.Country = html.UnescapeString(m[1])
	}
	if m := reCodec.FindStringSubmatch(s); len(m) == 2 {
		r.Codec = strings.TrimSpace(m[1])
	}
	if m := reResolution.FindStringSubmatch(s); len(m) == 2 {
		r.Resolution = strings.TrimSpace(m[1])
	}
	if m := reAspectRatio.FindStringSubmatch(s); len(m) == 2 {
		r.AspectRatio = strings.TrimSpace(m[1])
	}

	if r.Title == "" && r.Studio == "" {
		return nil, fmt.Errorf("bluray: parse detail: page does not look like a release page")
	}
	return r, nil
}

// ---------- search results ----------

// Search results grid: each result is an <a class="hoverlink" ...
// href=".../movies/<slug>/<id>/" title="<Title> (<Year>)"> wrapping a cover img.
var reSearchResult = regexp.MustCompile(
	`<a class="hoverlink"[^>]*?href="(https?://www\.blu-ray\.com/movies/([^/"]+)/(\d+)/)"[^>]*title="([^"]+)"`,
)

// reTitleYear extracts "(YYYY)" or "(YYYY-YYYY)" at the end of a hoverlink title.
var reTitleYear = regexp.MustCompile(`^(.*?)\s*\((\d{4})(?:-\d{4})?\)\s*$`)

// reSearchPage is a stable marker present on every real /search/ response —
// including legitimate zero-result pages. A body that lacks it is either a
// soft block (Cloudflare interstitial, anti-bot challenge, error page returned
// as HTTP 200) or a markup change we have not adapted to. Either way, treating
// it as "no results" and writing the title to the negative cache poisons the
// cache for the negative-TTL window. ParseSearch returns an error in that case
// so callers can surface it as a warning instead of silently caching.
var reSearchPage = regexp.MustCompile(`(?i)<title>\s*Blu-ray\.com\s*-\s*Search\b`)

// ParseSearch extracts result rows from a /search/?quicksearch=1&… results page.
// Returns an empty (but non-nil) slice when the page parses as a real search
// response but contains zero result rows — callers use the empty slice as the
// negative-cache signal. Returns an error when the body does not look like a
// search results page at all, so a transient soft block does not get cached
// as "no such title".
func ParseSearch(body []byte) ([]IndexEntry, error) {
	s := string(body)
	if !reSearchPage.MatchString(s) {
		return nil, fmt.Errorf("bluray: parse search: body does not look like a search results page (length=%d)", len(body))
	}
	matches := reSearchResult.FindAllStringSubmatch(s, -1)
	seen := make(map[string]bool, len(matches))
	out := make([]IndexEntry, 0, len(matches))
	for _, m := range matches {
		if len(m) != 5 {
			continue
		}
		fullURL, slug, id, rawTitle := m[1], m[2], m[3], m[4]
		if seen[id] {
			continue
		}
		seen[id] = true

		title := html.UnescapeString(rawTitle)
		year := 0
		if mm := reTitleYear.FindStringSubmatch(title); len(mm) == 3 {
			title = strings.TrimSpace(mm[1])
			year, _ = strconv.Atoi(mm[2])
		}

		_ = fullURL
		out = append(out, IndexEntry{
			ID:     id,
			Slug:   slug,
			Title:  title,
			Format: FormatFromSlug(slug),
			Year:   year,
		})
	}
	return out, nil
}

// ---------- calendar page ----------

// Calendar rows are emitted as a JS data dump, one per line:
//
//	movies[N] = {id: 12345, casing: '', title: 'X', edition: '...', studio: 'S',
//	             year: '2024', yearend: '2024', releasedate: 'December 01, 2025',
//	             title_keywords: 'X', popularity: 130, ...};
//
// The regex below is intentionally permissive: it pulls each named field out
// of one assignment line and ignores unknown fields. Single-quoted strings
// use \' for embedded apostrophes — handled by reCalString.
var (
	reCalRow      = regexp.MustCompile(`(?m)^movies\[\d+\]\s*=\s*\{(.+?)\};\s*$`)
	reCalID       = regexp.MustCompile(`\bid:\s*(\d+)`)
	reCalTitle    = regexp.MustCompile(`\btitle:\s*'((?:\\.|[^'\\])*)'`)
	reCalKeywords = regexp.MustCompile(`\btitle_keywords:\s*'((?:\\.|[^'\\])*)'`)
	reCalEdition  = regexp.MustCompile(`\bedition:\s*'((?:\\.|[^'\\])*)'`)
	reCalStudio   = regexp.MustCompile(`\bstudio:\s*'((?:\\.|[^'\\])*)'`)
	reCalYear     = regexp.MustCompile(`\byear:\s*'(\d{4})'`)
	reCalDate     = regexp.MustCompile(`\breleasedate:\s*'([A-Z][a-z]+ \d{1,2}, \d{4})'`)
	reCalCasing   = regexp.MustCompile(`\bcasing:\s*'((?:\\.|[^'\\])*)'`)
)

// CalendarEntry is a calendar row before it has been collapsed into either an
// IndexEntry or an entry.Entry. It carries the calendar-only fields (studio,
// releasedate, edition, casing) that are not present in search-result rows.
type CalendarEntry struct {
	IndexEntry
	Edition     string
	Studio      string
	ReleaseDate string // "YYYY-MM-DD"
	Casing      string // e.g. "SteelBook"
}

// ParseCalendar extracts CalendarEntry rows from a /movies/releasedates.php
// page. Empty result is treated as a soft success, not an error — some months
// (especially future-dated) legitimately have nothing.
func ParseCalendar(body []byte) ([]CalendarEntry, error) {
	s := string(body)
	rows := reCalRow.FindAllStringSubmatch(s, -1)
	out := make([]CalendarEntry, 0, len(rows))
	for _, r := range rows {
		if len(r) != 2 {
			continue
		}
		inner := r[1]
		ce := CalendarEntry{}
		if m := reCalID.FindStringSubmatch(inner); len(m) == 2 {
			ce.ID = m[1]
		}
		if ce.ID == "" {
			continue
		}
		if m := reCalTitle.FindStringSubmatch(inner); len(m) == 2 {
			ce.Title = unescapeJSString(m[1])
		}
		if m := reCalKeywords.FindStringSubmatch(inner); len(m) == 2 {
			ce.Slug = buildSlug(unescapeJSString(m[1]), ce.Title)
		}
		if m := reCalEdition.FindStringSubmatch(inner); len(m) == 2 {
			ce.Edition = unescapeJSString(m[1])
		}
		if m := reCalStudio.FindStringSubmatch(inner); len(m) == 2 {
			ce.Studio = unescapeJSString(m[1])
		}
		if m := reCalYear.FindStringSubmatch(inner); len(m) == 2 {
			ce.Year, _ = strconv.Atoi(m[1])
		}
		if m := reCalDate.FindStringSubmatch(inner); len(m) == 2 {
			if iso, ok := monthDayYearToISO(m[1]); ok {
				ce.ReleaseDate = iso
			}
		}
		if m := reCalCasing.FindStringSubmatch(inner); len(m) == 2 {
			ce.Casing = unescapeJSString(m[1])
		}
		ce.Format = FormatFromTitle(ce.Title)
		out = append(out, ce)
	}
	return out, nil
}

// buildSlug constructs the URL slug from the JS `title_keywords` field, which
// already mirrors the slug stem (e.g. "Avatar-Fire-and-Ash-4K"). The full slug
// appends "-Blu-ray". Falls back to dasherising the title if title_keywords is
// empty.
func buildSlug(keywords, title string) string {
	stem := keywords
	if stem == "" {
		stem = strings.ReplaceAll(strings.TrimSpace(title), " ", "-")
	}
	return stem + "-Blu-ray"
}

// ---------- helpers ----------

// monthDayYearToISO converts "October 16, 2012" to "2012-10-16". Returns
// (s, false) on unknown month names.
func monthDayYearToISO(s string) (string, bool) {
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return s, false
	}
	month, ok := monthIndex[parts[0]]
	if !ok {
		return s, false
	}
	rest := strings.TrimSpace(parts[1])
	rest = strings.TrimSuffix(rest, ",")
	day, year, ok := strings.Cut(rest, ", ")
	if !ok {
		return s, false
	}
	d, err := strconv.Atoi(strings.TrimSpace(strings.TrimSuffix(day, ",")))
	if err != nil {
		return s, false
	}
	y, err := strconv.Atoi(strings.TrimSpace(year))
	if err != nil {
		return s, false
	}
	return fmt.Sprintf("%04d-%02d-%02d", y, month, d), true
}

var monthIndex = map[string]int{
	"January": 1, "February": 2, "March": 3, "April": 4, "May": 5, "June": 6,
	"July": 7, "August": 8, "September": 9, "October": 10, "November": 11, "December": 12,
}

// unescapeJSString reverses the small set of escapes blu-ray.com emits inside
// single-quoted JS literals: \', \\, and HTML entities like &#21741;.
func unescapeJSString(s string) string {
	if !strings.ContainsAny(s, "\\&") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\\' && i+1 < len(s) {
			b.WriteByte(s[i+1])
			i++
			continue
		}
		b.WriteByte(s[i])
	}
	return html.UnescapeString(b.String())
}
