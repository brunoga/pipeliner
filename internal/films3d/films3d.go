// Package films3d knows which films were released in 3D by their studio. It
// reads Wikipedia's "List of 3D films" pages, which list every film shown in
// stereoscopic 3D in cinemas: shot in 3D, or converted to 3D by the studio for
// release (Titanic in 2012, the Star Wars prequels, Mulan in 2020).
//
// A 3D copy of a film that is not on the list did not come from the studio: it
// is a fan conversion of the 2D film, however it is labelled. That is the
// question this package answers. It does not say whether a 3D Blu-ray exists;
// some studio 3D versions never reached disc (Mulan) and only circulate as 3D
// web streams.
package films3d

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/internal/match"
)

// DefaultPages are the Wikipedia articles that together list every 3D film.
var DefaultPages = []string{
	"List of 3D films (1914–2004)",
	"List of 3D films (2005–present)",
}

// DefaultBaseURL is the wiki that serves DefaultPages.
const DefaultBaseURL = "https://en.wikipedia.org"

// userAgent identifies the client, as Wikipedia's robot policy asks.
const userAgent = "pipeliner (https://github.com/brunoga/pipeliner)"

// Film is one row of a 3D film list.
type Film struct {
	// Title is the title as the list shows it.
	Title string `json:"title"`
	// Keys are the normalised titles the film is matched by: the shown title,
	// the article name, and their parts on either side of a colon.
	Keys []string `json:"keys"`
	// Years are the years the film is matched at: the 3D release, the
	// original release for a 3D re-release, and the year in the article name.
	Years []int `json:"years"`
	// Native is true when the list says the film was shot in 3D, false when it
	// says the 3D is a conversion or does not say.
	Native bool `json:"native,omitempty"`
}

// Fetch downloads the wikitext of each page from baseURL and parses it.
func Fetch(ctx context.Context, client *http.Client, baseURL string, pages []string) ([]Film, error) {
	var films []Film
	for _, page := range pages {
		body, err := fetchRaw(ctx, client, baseURL, page)
		if err != nil {
			return nil, err
		}
		got := Parse(body)
		if len(got) == 0 {
			return nil, fmt.Errorf("films3d: %q lists no films; the page layout may have changed", page)
		}
		films = append(films, got...)
	}
	return films, nil
}

func fetchRaw(ctx context.Context, client *http.Client, baseURL, page string) (string, error) {
	q := url.Values{"title": {page}, "action": {"raw"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(baseURL, "/")+"/w/index.php?"+q.Encode(), nil)
	if err != nil {
		return "", fmt.Errorf("films3d: build request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("films3d: fetch %q: %w", page, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("films3d: fetch %q: HTTP %d", page, resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("films3d: read %q: %w", page, err)
	}
	return string(b), nil
}

var (
	reRef       = regexp.MustCompile(`(?s)<ref[^>/]*/>|<ref[^>]*>.*?</ref>|<!--.*?-->`)
	reHeading   = regexp.MustCompile(`(?m)^=+\s*(\d{4})\s*=+\s*$`)
	reFirstLink = regexp.MustCompile(`\[\[([^\]|]+)(?:\|([^\]]+))?\]\]`)
	reYear      = regexp.MustCompile(`\b(1[89]\d\d|20\d\d)\b`)
	reOriginal  = regexp.MustCompile(`(?i)originally released[^.]{0,40}?\b(1[89]\d\d|20\d\d)\b`)
	reFilmYear  = regexp.MustCompile(`\((\d{4})\b[^)]*\)\s*$`)
	reDisambig  = regexp.MustCompile(`\s*\([^)]*\)\s*$`)
	rePossess   = regexp.MustCompile(`^(?:[A-Z][\w.\-]*\s){0,3}?[A-Z][\w.\-]*['’]s\s+(.+)$`)
)

// Parse reads the films out of a 3D-film list's wikitext. Each table row whose
// first cell links an article is a film; rows without one (notes, headers) are
// skipped.
func Parse(wikitext string) []Film {
	text := reRef.ReplaceAllString(wikitext, "")
	var films []Film
	section := 0
	for _, row := range strings.Split(text, "\n|-") {
		if f, ok := parseRow(row, section); ok {
			films = append(films, f)
		}
		// A heading inside a chunk follows the chunk's own film (the last row
		// of the previous year's table), so it applies from the next chunk on.
		if hs := reHeading.FindAllStringSubmatch(row, -1); len(hs) > 0 {
			section, _ = strconv.Atoi(hs[len(hs)-1][1])
		}
	}
	return films
}

func parseRow(row string, section int) (Film, bool) {
	first, rest, _ := strings.Cut(strings.TrimLeft(row, "\n"), "\n")
	if !strings.HasPrefix(first, "|") || strings.HasPrefix(first, "|}") || strings.HasPrefix(first, "|+") {
		return Film{}, false
	}
	m := reFirstLink.FindStringSubmatch(first)
	if m == nil || strings.Contains(m[1], ":") && strings.HasPrefix(strings.ToLower(m[1]), "file:") {
		return Film{}, false
	}
	target, shown := strings.TrimSpace(m[1]), strings.TrimSpace(m[2])
	if shown == "" {
		shown = target
	}
	// Article anchors ("Avatar (2009 film)#Extended…") name a section, not
	// a different film.
	target, _, _ = strings.Cut(target, "#")

	f := Film{Title: shown}
	addYear := func(y int) {
		if y < 1890 || y > 2100 {
			return
		}
		for _, have := range f.Years {
			if have == y {
				return
			}
		}
		f.Years = append(f.Years, y)
	}
	// The release date is the next cell; its year is the 3D release.
	if y := reYear.FindString(firstLines(rest, 2)); y != "" {
		n, _ := strconv.Atoi(y)
		addYear(n)
	}
	addYear(section)
	if o := reOriginal.FindStringSubmatch(row); o != nil {
		n, _ := strconv.Atoi(o[1])
		addYear(n)
	}
	if fy := reFilmYear.FindStringSubmatch(target); fy != nil {
		n, _ := strconv.Atoi(fy[1])
		addYear(n)
	}
	if len(f.Years) == 0 {
		return Film{}, false
	}

	lower := strings.ToLower(row)
	f.Native = strings.Contains(lower, "filmed in 3d") ||
		(!strings.Contains(lower, "filmed in 2d") && !strings.Contains(lower, "conversion") &&
			(strings.Contains(lower, "fusion camera") || strings.Contains(lower, "stereoscopic camera")))

	seen := map[string]bool{}
	for _, t := range titleForms(shown, reDisambig.ReplaceAllString(target, "")) {
		if k := Key(t); k != "" && !seen[k] {
			seen[k] = true
			f.Keys = append(f.Keys, k)
		}
	}
	return f, len(f.Keys) > 0
}

// titleForms expands the titles a film is listed under into every form a
// release may use: as listed, either side of a colon ("Rogue One: A Star Wars
// Story", "Star Wars: The Force Awakens"), and without a possessive credit
// ("Tim Burton's The Nightmare Before Christmas").
func titleForms(titles ...string) []string {
	var out []string
	for _, t := range titles {
		t = strings.Trim(strings.TrimSpace(t), "'")
		out = append(out, t)
		if a, b, ok := strings.Cut(t, ":"); ok {
			out = append(out, a, b)
		}
		if m := rePossess.FindStringSubmatch(t); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

var romans = map[string]string{
	"ii": "2", "iii": "3", "iv": "4", "v": "5", "vi": "6",
	"vii": "7", "viii": "8", "ix": "9", "x": "10",
}

// Key normalises a title for matching: match.Normalize, "&" as "and", no
// leading "The" ("Darkest Hour" is listed as "The Darkest Hour"), and a Roman
// numeral after the first word read as a number, so "Frozen II" and the
// list's "Frozen 2" agree.
func Key(title string) string {
	words := strings.Fields(match.Normalize(strings.ReplaceAll(title, "&", " and ")))
	if len(words) > 1 && words[0] == "the" {
		words = words[1:]
	}
	for i := 1; i < len(words); i++ {
		if n, ok := romans[words[i]]; ok {
			words[i] = n
		}
	}
	return strings.Join(words, " ")
}

func firstLines(s string, n int) string {
	lines := strings.SplitN(s, "\n", n+1)
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// Merge returns the union of two film lists: every film in next, plus every
// film in prev that next no longer lists. A film is the same film when it
// shares a key and a year with one in next.
func Merge(prev, next []Film) []Film {
	ix := NewIndex(next)
	out := append([]Film(nil), next...)
	for _, f := range prev {
		found := false
		for _, y := range f.Years {
			if _, ok := ix.Lookup(f.Keys, y); ok {
				found = true
				break
			}
		}
		if !found {
			out = append(out, f)
		}
	}
	return out
}

// Index matches titles against a film list.
type Index struct {
	byKey map[string][]*Film
}

// NewIndex indexes films by every key. Keys are normalised again here, so a
// list stored by an older build matches the way this one looks titles up.
func NewIndex(films []Film) *Index {
	ix := &Index{byKey: map[string][]*Film{}}
	for i := range films {
		f := &films[i]
		for _, k := range f.Keys {
			k = Key(k)
			ix.byKey[k] = append(ix.byKey[k], f)
		}
	}
	return ix
}

// Len is the number of distinct keys indexed.
func (ix *Index) Len() int { return len(ix.byKey) }

// yearSlack is how far a title's year may sit from a listed year. Release
// names carry a festival premiere ("Battle for Terra 2007", released in 3D in
// 2009) or a wide-release year a year off the list's.
const yearSlack = 2

// Lookup returns the listed film one of titles names, released within
// yearSlack years of year. A zero year matches on title alone. The year check
// keeps remakes apart: the list has "Mulan" in 2020, which must not vouch for
// the 1998 film.
//
// A title that only starts with a listed one ("Warcraft The Beginning", the
// international title of the listed "Warcraft") matches at the same year and
// no other, so a sequel a year later ("Wicked For Good" after "Wicked") is not
// vouched for by the first film.
func (ix *Index) Lookup(titles []string, year int) (*Film, bool) {
	for _, t := range titles {
		if f, ok := ix.lookupKey(Key(t), year, yearSlack); ok {
			return f, true
		}
	}
	if year == 0 {
		return nil, false
	}
	for _, t := range titles {
		words := strings.Fields(Key(t))
		for n := len(words) - 1; n >= 1; n-- {
			if f, ok := ix.lookupKey(strings.Join(words[:n], " "), year, 0); ok {
				return f, true
			}
		}
	}
	return nil, false
}

func (ix *Index) lookupKey(key string, year, slack int) (*Film, bool) {
	for _, f := range ix.byKey[key] {
		if year == 0 {
			return f, true
		}
		for _, y := range f.Years {
			if y-year <= slack && year-y <= slack {
				return f, true
			}
		}
	}
	return nil, false
}
