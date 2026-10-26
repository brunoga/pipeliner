package bluray

import (
	"bufio"
	"bytes"
	"compress/gzip"
	_ "embed"
	"encoding/json"
	"strings"
	"sync"

	"github.com/brunoga/pipeliner/internal/match"
)

//go:embed releases_3d_worldwide.jsonl.gz
var embedded3DCatalogGZ []byte

// Catalog3DCutoffYear is the movie release year starting from which the
// embedded 3D catalog snapshot is no longer considered exhaustive (since
// newly announced 3D releases may postdate the snapshot). For movie years in
// [1, Catalog3DCutoffYear-1], a miss in the embedded 3D catalog means no
// 3D Blu-ray release exists.
const Catalog3DCutoffYear = 2026

// Catalog3D indexes the embedded worldwide 3D Blu-ray releases dataset.
type Catalog3D struct {
	all     []ReleaseSummary
	byID    map[string]ReleaseSummary
	byTitle map[string][]ReleaseSummary
}

// IsAuthoritativeForYear reports whether the embedded 3D catalog is
// considered complete for the given movie year (1 <= year < Catalog3DCutoffYear).
func (c *Catalog3D) IsAuthoritativeForYear(year int) bool {
	return c != nil && year > 0 && year < Catalog3DCutoffYear
}

var (
	catalogOnce sync.Once
	catalogInst *Catalog3D
	catalogErr  error
)

// catalogRecord is the compact on-disk representation stored in releases_3d_worldwide.jsonl.gz,
// stripped to only the fields used by pipeliner.
type catalogRecord struct {
	ID          string  `json:"id"`
	Slug        string  `json:"slug,omitempty"`
	Title       string  `json:"title"`
	Edition     string  `json:"edition,omitempty"`
	Country     Country `json:"country,omitempty"`
	Studio      string  `json:"studio,omitempty"`
	Year        int     `json:"year,omitempty"`
	ReleaseDate string  `json:"date,omitempty"`
}

func defaultCatalogSlug(title string) string {
	var b strings.Builder
	b.Grow(len(title))
	prevDash := true
	for i := 0; i < len(title); i++ {
		ch := title[i]
		if (ch >= 'A' && ch <= 'Z') || (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9') {
			b.WriteByte(ch)
			prevDash = false
		} else if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

// Embedded3DCatalog loads and indexes the embedded worldwide 3D Blu-ray catalog once.
func Embedded3DCatalog() (*Catalog3D, error) {
	catalogOnce.Do(func() {
		catalogInst, catalogErr = parseCatalogGZ(embedded3DCatalogGZ)
	})
	return catalogInst, catalogErr
}

func parseCatalogGZ(gzData []byte) (*Catalog3D, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gzData))
	if err != nil {
		return nil, err
	}
	defer zr.Close()

	cat := &Catalog3D{
		all:     make([]ReleaseSummary, 0, 14200),
		byID:    make(map[string]ReleaseSummary, 14200),
		byTitle: make(map[string][]ReleaseSummary, 6000),
	}

	scanner := bufio.NewScanner(zr)
	scanner.Buffer(make([]byte, 0, 64*1024), 512*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec catalogRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		slug := rec.Slug
		if slug == "" {
			slug = defaultCatalogSlug(rec.Title)
		}
		item := ReleaseSummary{
			ID:            rec.ID,
			Title:         rec.Title,
			Edition:       rec.Edition,
			FormatSummary: rec.Edition,
			URL:           "https://www.blu-ray.com/movies/" + slug + "/" + rec.ID + "/",
			Country:       rec.Country,
			Studio:        rec.Studio,
			Year:          rec.Year,
			ReleaseDate:   rec.ReleaseDate,
			Is3D:          true,
		}
		cat.all = append(cat.all, item)
		if item.ID != "" {
			cat.byID[item.ID] = item
		}
		for _, k := range catalogKeysForRelease(item) {
			cat.byTitle[k] = append(cat.byTitle[k], item)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return cat, nil
}

// CatalogKey returns the normalized lookup key for a movie title.
func CatalogKey(title string) string {
	return match.Normalize(StripFormatTokens(title))
}

func catalogKeysForRelease(item ReleaseSummary) []string {
	seen := make(map[string]bool, 4)
	var keys []string
	add := func(s string) {
		k := CatalogKey(s)
		if k != "" && !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}

	clean := StripFormatTokens(item.Title)
	add(clean)

	// Also index without parenthesised suffixes like "(Limited Edition)" or "(Collector's Edition)"
	if idx := strings.Index(clean, "("); idx > 0 {
		add(clean[:idx])
	}
	// FormatSummary often carries alternative titles separated by " / " before the format spec
	if item.FormatSummary != "" {
		for _, part := range strings.Split(item.FormatSummary, "/") {
			p := strings.TrimSpace(part)
			low := strings.ToLower(p)
			if strings.Contains(low, "blu-ray") || strings.Contains(low, "anaglyph") ||
				strings.Contains(low, "edition") || strings.Contains(low, "collection") ||
				strings.Contains(low, "restoration") || strings.Contains(low, "combo") ||
				strings.Contains(low, "steelbook") || strings.Contains(low, "3d") ||
				strings.Contains(low, "4k") || strings.Contains(low, "dvd") {
				continue
			}
			add(p)
		}
	}
	return keys
}

// Len returns the total number of 3D Blu-ray releases in the catalog.
func (c *Catalog3D) Len() int {
	if c == nil {
		return 0
	}
	return len(c.all)
}

// All returns a slice of all ReleaseSummary entries in the catalog, optionally filtered by country.
func (c *Catalog3D) All(country Country) []ReleaseSummary {
	if c == nil {
		return nil
	}
	if country == "" || country == CountryAll {
		out := make([]ReleaseSummary, len(c.all))
		copy(out, c.all)
		return out
	}
	want := Country(strings.ToLower(string(country)))
	var out []ReleaseSummary
	for _, r := range c.all {
		if r.Country == want {
			out = append(out, r)
		}
	}
	return out
}

// LookupID looks up a 3D release by its Blu-ray.com product ID.
func (c *Catalog3D) LookupID(id string) (ReleaseSummary, bool) {
	if c == nil || id == "" {
		return ReleaseSummary{}, false
	}
	r, ok := c.byID[id]
	return r, ok
}

// LookupTitle returns all 3D Blu-ray releases matching title (and optional year and country).
// If year > 0, matches within ±1 year (or entries with Year == 0) are returned.
// If country is empty or CountryAll, editions across all countries are returned.
func (c *Catalog3D) LookupTitle(title string, year int, country Country) []ReleaseSummary {
	if c == nil {
		return nil
	}
	key := CatalogKey(title)
	if key == "" {
		return nil
	}
	candidates := c.byTitle[key]
	if len(candidates) == 0 {
		return nil
	}

	wantCountry := Country(strings.ToLower(string(country)))
	filterCountry := wantCountry != "" && wantCountry != CountryAll

	var out []ReleaseSummary
	for _, r := range candidates {
		if filterCountry && r.Country != "" && r.Country != wantCountry {
			continue
		}
		if year > 0 && r.Year > 0 {
			diff := r.Year - year
			if diff < -1 || diff > 1 {
				continue
			}
		}
		out = append(out, r)
	}
	return out
}
