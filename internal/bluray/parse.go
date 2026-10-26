package bluray

import (
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
)

// EnsureUTF8 converts an ISO-8859-1 / Windows-1252 byte slice to valid UTF-8 if it contains
// non-UTF-8 byte sequences (blu-ray.com serves text/html; charset=ISO-8859-1).
func EnsureUTF8(b []byte) string {
	if utf8.Valid(b) {
		return string(b)
	}
	var sb strings.Builder
	sb.Grow(len(b) + len(b)/10)
	for _, c := range b {
		sb.WriteRune(rune(c))
	}
	return sb.String()
}

// FormatFromSlug derives a Format value from a release-page URL slug like
// "Avatar-3D-Blu-ray" or "Avatar-4K-Blu-ray". Unknown slugs default to BD,
// which is the most common form on the site.
func FormatFromSlug(slug string) Format {
	s := strings.ToLower(slug)
	switch {
	case strings.HasSuffix(s, "-3d-blu-ray") || strings.Contains(s, "-3d-"):
		return FormatBD3D
	case strings.HasSuffix(s, "-4k-blu-ray") || strings.Contains(s, "-4k-"):
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
	case strings.HasSuffix(t, " 3d") || strings.HasSuffix(t, " 3d blu-ray") || strings.HasSuffix(t, " in 3d blu-ray"):
		return FormatBD3D
	case strings.HasSuffix(t, " 4k") || strings.HasSuffix(t, " 4k blu-ray"):
		return FormatUHD
	case strings.HasSuffix(t, " dvd"):
		return FormatDVD
	default:
		return FormatBD
	}
}

// StripFormatTokens removes trailing Blu-ray format suffixes (" Blu-ray", " in 3D", " 3D", " 4K", " DVD")
// from a Blu-ray.com release title so that consumers matching on canonical movie title match cleanly.
func StripFormatTokens(s string) string {
	t := strings.TrimSpace(s)
	for {
		low := strings.ToLower(t)
		stripped := false
		for _, suf := range []string{" blu-ray", " in 3d", " 3d", " 4k", " dvd"} {
			if strings.HasSuffix(low, suf) {
				t = strings.TrimSpace(t[:len(t)-len(suf)])
				stripped = true
				break
			}
		}
		if !stripped {
			break
		}
	}
	return t
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

var (
	reMatchesCount  = regexp.MustCompile(`(?i)\((\d+)\s+match(?:es)?\)`)
	reTheatricalCnt = regexp.MustCompile(`(?i)(\d+)\s+match(?:es)?\s+for\s+`)
	reProductID     = regexp.MustCompile(`/(\d+)/?(?:[?#].*)?$`)
	reYearParens    = regexp.MustCompile(`\((\d{4})\)`)
	reYearBare      = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	reYearRange     = regexp.MustCompile(`^(19\d{2}|20\d{2})(?:-(?:19\d{2}|20\d{2}))?$`)
	reRuntimeMins   = regexp.MustCompile(`(\d+)\s*min`)
	reRatingScore   = regexp.MustCompile(`(?i)(?:Overall:\s*)?(\d+(?:\.\d+)?)\s*of\s*5`)
	reFlagCountry   = regexp.MustCompile(`/flags/([A-Za-z]{2,3})\.png`)
	reFrontImage    = regexp.MustCompile(`src="(https://images[^"]*?/covers/\d+_front\.jpg[^"]*)"`)
	reBackImage     = regexp.MustCompile(`src="(https://images[^"]*?/covers/\d+_back\.jpg[^"]*)"`)
	reSlipImage     = regexp.MustCompile(`src="(https://images[^"]*?/covers/\d+_slip\.jpg[^"]*)"`)
	reMenuAjaxParam = regexp.MustCompile(`menu_ajax\.php\?p=(\d+)`)
	reJSArrayVar    = regexp.MustCompile(`var\s+(\w+)\s*=\s*new\s+Array\((.*?)\);`)
	reWhitespace    = regexp.MustCompile(`\s+`)
)

// cleanText collapses whitespace, trims, and unescapes HTML entities.
func cleanText(s string) string {
	s = html.UnescapeString(s)
	s = strings.ReplaceAll(s, "\u00a0", " ")
	s = reWhitespace.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}

// extractIDFromURL extracts the numeric ID from a blu-ray.com URL like ".../Dredd-3D-Blu-ray/56292/".
func extractIDFromURL(rawURL string) string {
	m := reProductID.FindStringSubmatch(rawURL)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// decodeLinkWrapper unwraps blu-ray.com's link.php wrapper if present.
func decodeLinkWrapper(href string) string {
	const prefix = "https://www.blu-ray.com/link/link.php?url="
	if strings.HasPrefix(href, prefix) {
		return strings.TrimPrefix(href, prefix)
	}
	return href
}

// is3DIndicator checks whether a title, format string, or URL indicates a 3D Blu-ray release.
func is3DIndicator(strs ...string) bool {
	for _, s := range strs {
		lower := strings.ToLower(s)
		if strings.Contains(lower, "3d") || strings.Contains(lower, "mvc") {
			return true
		}
	}
	return false
}

// is4KIndicator checks whether a title, format string, or URL indicates a 4K UHD release.
func is4KIndicator(strs ...string) bool {
	for _, s := range strs {
		lower := strings.ToLower(s)
		if strings.Contains(lower, "4k") || strings.Contains(lower, "2160p") || strings.Contains(lower, "ultra hd") {
			return true
		}
	}
	return false
}

// nodeAttr returns the value of an attribute on an HTML node.
func nodeAttr(n *xhtml.Node, key string) string {
	for _, a := range n.Attr {
		if strings.EqualFold(a.Key, key) {
			return a.Val
		}
	}
	return ""
}

// hasClass checks if an HTML node's class attribute contains cls.
func hasClass(n *xhtml.Node, cls string) bool {
	classes := strings.Fields(nodeAttr(n, "class"))
	for _, c := range classes {
		if strings.EqualFold(c, cls) {
			return true
		}
	}
	return false
}

// nodeText extracts all text content within an HTML node recursively.
func nodeText(n *xhtml.Node) string {
	var sb strings.Builder
	var walk func(*xhtml.Node)
	walk = func(cur *xhtml.Node) {
		if cur.Type == xhtml.TextNode {
			sb.WriteString(cur.Data)
		}
		for c := cur.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return cleanText(sb.String())
}

// findNodes returns all descendant nodes matching predicate.
func findNodes(root *xhtml.Node, pred func(*xhtml.Node) bool) []*xhtml.Node {
	var out []*xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if pred(n) {
			out = append(out, n)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return out
}

// findFirstNode returns the first descendant node matching predicate.
func findFirstNode(root *xhtml.Node, pred func(*xhtml.Node) bool) *xhtml.Node {
	var found *xhtml.Node
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if found != nil {
			return
		}
		if pred(n) {
			found = n
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}

// ParseSearchResults parses an HTML page from /movies/search.php (supporting "full", "thumb", and "simple" layouts).
func ParseSearchResults(rawHTML []byte, defaultCountry Country, currentPage int) (*SearchResultPage, error) {
	htmlStr := EnsureUTF8(rawHTML)
	doc, err := xhtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse search HTML: %w", err)
	}

	page := &SearchResultPage{
		CurrentPage: currentPage,
		Country:     defaultCountry,
	}

	// 1. Parse total matches count and page country from <h2 class="oswaldcollection">Search movies (N matches)...
	h2Nodes := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "h2" && hasClass(n, "oswaldcollection")
	})
	for _, h2 := range h2Nodes {
		txt := nodeText(h2)
		if m := reMatchesCount.FindStringSubmatch(txt); len(m) > 1 {
			page.TotalMatches, _ = strconv.Atoi(m[1])
			img := findFirstNode(h2, func(n *xhtml.Node) bool {
				return n.Type == xhtml.ElementNode && n.Data == "img"
			})
			if img != nil {
				if fm := reFlagCountry.FindStringSubmatch(nodeAttr(img, "src")); len(fm) > 1 {
					page.Country = Country(strings.ToLower(fm[1]))
				}
			}
			break
		}
	}

	// Check for Next page link
	nextLinks := findNodes(doc, func(n *xhtml.Node) bool {
		if n.Type == xhtml.ElementNode && n.Data == "a" {
			txt := strings.ToLower(nodeText(n))
			return strings.Contains(txt, "next")
		}
		return false
	})
	if len(nextLinks) > 0 {
		page.HasNextPage = true
	}

	// 2. Try parsing "full" layout items: tables containing <td width="85%"> with <h3> inside <a class="noline">
	td85Nodes := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "td" && nodeAttr(n, "width") == "85%"
	})
	for _, td := range td85Nodes {
		h3 := findFirstNode(td, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "h3"
		})
		if h3 == nil || h3.Parent == nil || h3.Parent.Data != "a" {
			continue
		}
		link := decodeLinkWrapper(nodeAttr(h3.Parent, "href"))
		if !strings.Contains(link, "/movies/") {
			continue
		}

		itemCountry := defaultCountry
		if itemCountry == CountryAll || itemCountry == "" {
			itemCountry = CountryUS
		}
		item := ReleaseSummary{
			ID:      extractIDFromURL(link),
			Title:   nodeText(h3),
			URL:     link,
			Country: itemCountry,
		}

		// Find sibling <td width="15%"> for cover image
		if td.Parent != nil {
			td15 := findFirstNode(td.Parent, func(n *xhtml.Node) bool {
				return n.Type == xhtml.ElementNode && n.Data == "td" && nodeAttr(n, "width") == "15%"
			})
			if td15 != nil {
				img := findFirstNode(td15, func(n *xhtml.Node) bool {
					return n.Type == xhtml.ElementNode && n.Data == "img" && hasClass(n, "cover")
				})
				if img != nil {
					item.CoverURL = nodeAttr(img, "src")
				}
			}
		}
		if item.ID != "" && (item.CoverURL == "" || strings.HasSuffix(item.CoverURL, "transparent.gif")) {
			item.CoverURL = fmt.Sprintf("https://images.static-bluray.com/movies/covers/%s_small.jpg", item.ID)
		}

		// Check images in td (country flag, rating image)
		imgs := findNodes(td, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "img"
		})
		for _, img := range imgs {
			src := nodeAttr(img, "src")
			if fm := reFlagCountry.FindStringSubmatch(src); len(fm) > 1 {
				item.Country = Country(strings.ToLower(fm[1]))
			}
			titleAttr := nodeAttr(img, "title")
			if rm := reRatingScore.FindStringSubmatch(titleAttr); len(rm) > 1 {
				item.OverallRating, _ = strconv.ParseFloat(rm[1], 64)
			}
		}

		// Format summary in <span class="subheadingsmall">
		formatSpan := findFirstNode(td, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "span" && hasClass(n, "subheadingsmall")
		})
		if formatSpan != nil {
			item.FormatSummary = nodeText(formatSpan)
		}

		// Metadata line in <small style="color: #666666">
		metaSmall := findFirstNode(td, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "small" && strings.Contains(nodeAttr(n, "style"), "666666")
		})
		if metaSmall != nil {
			parseReleaseMetaLine(nodeText(metaSmall), &item)
		}

		// Genres from <a href=".../movies.php?genre=...">
		genreLinks := findNodes(td, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "a" && strings.Contains(nodeAttr(n, "href"), "genre=")
		})
		for _, gl := range genreLinks {
			if g := nodeText(gl); g != "" {
				item.Genres = append(item.Genres, g)
			}
		}

		// Synopsis is the direct text node inside td after <small>
		var synParts []string
		for c := td.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.TextNode {
				t := cleanText(c.Data)
				if t != "" && t != "/" {
					synParts = append(synParts, t)
				}
			}
		}
		if len(synParts) > 0 {
			item.Synopsis = strings.Join(synParts, " ")
		}

		item.Is3D = is3DIndicator(item.Title, item.FormatSummary, item.URL)
		item.Is4K = is4KIndicator(item.Title, item.FormatSummary, item.URL)
		page.Results = append(page.Results, item)
	}

	if len(page.Results) > 0 {
		return page, nil
	}

	// 3. Fallback to "thumb" layout: <a class="hoverlink" data-productid="..." href="https://www.blu-ray.com/movies/...">
	hoverLinks := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "a" && hasClass(n, "hoverlink") && strings.Contains(nodeAttr(n, "href"), "/movies/")
	})
	for _, a := range hoverLinks {
		href := decodeLinkWrapper(nodeAttr(a, "href"))
		title := cleanText(nodeAttr(a, "title"))
		item := ReleaseSummary{
			ID:            nodeAttr(a, "data-productid"),
			GlobalID:      nodeAttr(a, "data-globalproductid"),
			ParentMovieID: nodeAttr(a, "data-globalparentid"),
			Title:         title,
			URL:           href,
			Country:       page.Country,
		}
		if item.ID == "" {
			item.ID = extractIDFromURL(href)
		}
		if ym := reYearParens.FindStringSubmatch(title); len(ym) > 1 {
			item.Year, _ = strconv.Atoi(ym[1])
		}
		img := findFirstNode(a, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "img"
		})
		if img != nil {
			item.CoverURL = nodeAttr(img, "src")
		}
		item.Is3D = is3DIndicator(item.Title, item.URL)
		item.Is4K = is4KIndicator(item.Title, item.URL)
		page.Results = append(page.Results, item)
	}

	if len(page.Results) > 0 {
		return page, nil
	}

	// 4. Fallback to "simple" layout: <table class="bevel"> rows
	bevelTable := findFirstNode(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "table" && hasClass(n, "bevel")
	})
	if bevelTable != nil {
		rows := findNodes(bevelTable, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "tr"
		})
		for _, tr := range rows {
			a := findFirstNode(tr, func(n *xhtml.Node) bool {
				return n.Type == xhtml.ElementNode && n.Data == "a" && strings.Contains(nodeAttr(n, "href"), "/movies/")
			})
			if a == nil {
				continue
			}
			href := decodeLinkWrapper(nodeAttr(a, "href"))
			tds := findNodes(tr, func(n *xhtml.Node) bool {
				return n.Type == xhtml.ElementNode && n.Data == "td"
			})
			item := ReleaseSummary{
				ID:      extractIDFromURL(href),
				Title:   nodeText(a),
				URL:     href,
				Country: page.Country,
			}
			if len(tds) >= 4 {
				if ym := reYearParens.FindStringSubmatch(nodeText(tds[1])); len(ym) > 1 {
					item.Year, _ = strconv.Atoi(ym[1])
				}
				item.Studio = nodeText(tds[2])
				item.ReleaseDate = nodeText(tds[3])
			}
			item.Is3D = is3DIndicator(item.Title, item.URL)
			item.Is4K = is4KIndicator(item.Title, item.URL)
			page.Results = append(page.Results, item)
		}
	}

	return page, nil
}

// parseReleaseMetaLine parses "Lionsgate Films | 2012 | 96 min | Rated R | 2K Blu-ray: Region A (locked) | Jan 08, 2013"
func parseReleaseMetaLine(meta string, item *ReleaseSummary) {
	parts := strings.Split(meta, "|")
	for i, rawPart := range parts {
		part := cleanText(rawPart)
		if part == "" {
			continue
		}
		if ym := reYearRange.FindStringSubmatch(part); len(ym) > 1 {
			item.Year, _ = strconv.Atoi(ym[1])
			continue
		}
		if rm := reRuntimeMins.FindStringSubmatch(part); len(rm) > 1 {
			item.RuntimeMins, _ = strconv.Atoi(rm[1])
			continue
		}
		if strings.HasPrefix(part, "Rated ") || part == "Not rated" || part == "Unrated" {
			item.Rated = part
			continue
		}
		if strings.Contains(strings.ToLower(part), "region") {
			part = strings.ReplaceAll(part, "Region free2K", "Region free / 2K")
			part = strings.ReplaceAll(part, ")2K", ") / 2K")
			item.RegionSummary = part
			continue
		}
		if i == 0 {
			item.Studio = part
			continue
		}
		if i == len(parts)-1 {
			item.ReleaseDate = part
		}
	}
}

// ParseQuickSearch parses the HTML + JS response from POST /search/quicksearch.php.
func ParseQuickSearch(rawHTML []byte) ([]QuickSearchResult, error) {
	htmlStr := EnsureUTF8(rawHTML)
	if strings.TrimSpace(htmlStr) == "" {
		return nil, nil
	}
	doc, err := xhtml.Parse(strings.NewReader(htmlStr))
	if err != nil {
		return nil, fmt.Errorf("failed to parse quicksearch HTML: %w", err)
	}

	// Extract JS arrays: ids, urls, images, ratings, countrycodes
	arrays := make(map[string][]string)
	for _, m := range reJSArrayVar.FindAllStringSubmatch(htmlStr, -1) {
		varName := m[1]
		rawArgs := m[2]
		arrays[varName] = parseJSStringArray(rawArgs)
	}

	liNodes := findNodes(doc, func(n *xhtml.Node) bool {
		return n.Type == xhtml.ElementNode && n.Data == "li" && strings.HasPrefix(nodeAttr(n, "id"), "match")
	})

	var results []QuickSearchResult
	for idx, li := range liNodes {
		var releaseDate string
		span := findFirstNode(li, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "span"
		})
		if span != nil {
			releaseDate = nodeText(span)
			if releaseDate == "-" {
				releaseDate = ""
			}
		}

		var country Country
		img := findFirstNode(li, func(n *xhtml.Node) bool {
			return n.Type == xhtml.ElementNode && n.Data == "img"
		})
		if img != nil {
			if fm := reFlagCountry.FindStringSubmatch(nodeAttr(img, "src")); len(fm) > 1 {
				country = Country(strings.ToLower(fm[1]))
			}
		}

		var titleBuilder strings.Builder
		for c := li.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == xhtml.TextNode {
				titleBuilder.WriteString(c.Data)
			}
		}
		title := cleanText(titleBuilder.String())

		res := QuickSearchResult{
			Title:       title,
			Country:     country,
			ReleaseDate: releaseDate,
		}
		if ym := reYearParens.FindStringSubmatch(title); len(ym) > 1 {
			res.Year, _ = strconv.Atoi(ym[1])
		}
		if idx < len(arrays["ids"]) {
			res.ID = arrays["ids"][idx]
		}
		if idx < len(arrays["urls"]) {
			res.URL = arrays["urls"][idx]
		}
		if idx < len(arrays["images"]) {
			res.CoverURL = arrays["images"][idx]
		}
		if idx < len(arrays["ratings"]) && arrays["ratings"][idx] != "" {
			res.Rating, _ = strconv.ParseFloat(arrays["ratings"][idx], 64)
		}
		if res.Country == "" && idx < len(arrays["countrycodes"]) && arrays["countrycodes"][idx] != "" {
			res.Country = Country(strings.ToLower(arrays["countrycodes"][idx]))
		}
		res.Is3D = is3DIndicator(res.Title, res.URL)
		res.Is4K = is4KIndicator(res.Title, res.URL)
		results = append(results, res)
	}

	return results, nil
}

func parseJSStringArray(raw string) []string {
	var out []string
	var cur strings.Builder
	inQuote := false
	var quoteChar byte
	escaped := false

	for i := 0; i < len(raw); i++ {
		ch := raw[i]
		if escaped {
			cur.WriteByte(ch)
			escaped = false
			continue
		}
		if ch == '\\' {
			escaped = true
			continue
		}
		if !inQuote {
			if ch == '\'' || ch == '"' {
				inQuote = true
				quoteChar = ch
				cur.Reset()
			}
		} else {
			if ch == quoteChar {
				inQuote = false
				out = append(out, cur.String())
			} else {
				cur.WriteByte(ch)
			}
		}
	}
	return out
}

// ---------- /search/?quicksearch=1 results ----------

var reSearchResult = regexp.MustCompile(
	`<a class="hoverlink"[^>]*?href="(https?://www\.blu-ray\.com/movies/([^/"]+)/(\d+)/)"[^>]*title="([^"]+)"`,
)

var reTitleYear = regexp.MustCompile(`^(.*?)\s*\((\d{4})(?:-\d{4})?\)\s*$`)

// reSearchPage is a stable marker present on every valid /search/ response —
// including legitimate zero-result pages. A body that lacks it is an error
// page or an unexpected markup change.
var reSearchPage = regexp.MustCompile(`(?i)<title>\s*Blu-ray\.com\s*-\s*Search\b`)

// ParseSearch extracts result rows from a /search/?quicksearch=1&… results page.
// Returns an empty (but non-nil) slice when the page parses as a real search
// response but contains zero result rows — callers use the empty slice as the
// negative-cache signal. Returns an error when the body does not look like a
// search results page at all, so a transient error page does not get cached
// as "no such title".
func ParseSearch(body []byte) ([]IndexEntry, error) {
	s := EnsureUTF8(body)
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

// ParseCalendar extracts CalendarEntry rows from a /movies/releasedates.php
// page. Empty result is treated as a soft success, not an error — some months
// (especially future-dated) legitimately have nothing.
func ParseCalendar(body []byte) ([]CalendarEntry, error) {
	s := EnsureUTF8(body)
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
			if iso, ok := NormalizeDateISO(m[1]); ok {
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

func buildSlug(keywords, title string) string {
	stem := keywords
	if stem == "" {
		stem = strings.ReplaceAll(strings.TrimSpace(title), " ", "-")
	}
	return stem + "-Blu-ray"
}

// NormalizeDateISO converts full ("October 16, 2012") or abbreviated ("Oct 16, 2012", "Jan 08, 2013")
// Blu-ray.com date strings to "YYYY-MM-DD". If s is already "YYYY-MM-DD", it returns (s, true).
func NormalizeDateISO(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if len(s) == 10 && s[4] == '-' && s[7] == '-' {
		return s, true
	}
	parts := strings.SplitN(s, " ", 2)
	if len(parts) != 2 {
		return s, false
	}
	month, ok := monthIndex[strings.ToLower(strings.TrimSuffix(parts[0], "."))]
	if !ok {
		return s, false
	}
	rest := strings.TrimSpace(parts[1])
	rest = strings.TrimSuffix(rest, ",")
	day, year, ok := strings.Cut(rest, ",")
	if !ok {
		return s, false
	}
	d, err := strconv.Atoi(strings.TrimSpace(day))
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
	"january": 1, "jan": 1,
	"february": 2, "feb": 2,
	"march": 3, "mar": 3,
	"april": 4, "apr": 4,
	"may": 5,
	"june": 6, "jun": 6,
	"july": 7, "jul": 7,
	"august": 8, "aug": 8,
	"september": 9, "sep": 9, "sept": 9,
	"october": 10, "oct": 10,
	"november": 11, "nov": 11,
	"december": 12, "dec": 12,
}

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
