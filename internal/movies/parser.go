// Package movies parses movie title and year information from media release names.
package movies

import (
	"regexp"
	"strings"
	"strconv"
	"unicode"

	"github.com/brunoga/pipeliner/internal/quality"
)

// Movie holds parsed metadata extracted from a release title.
type Movie struct {
	Title   string
	Year    int
	Quality quality.Quality
	Is3D    bool
}

var (
	reYear = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)

	// re3D matches common 3D release markers.
	re3D = regexp.MustCompile(`(?i)\b(3D|HSBS|H-SBS|HALF-SBS|FSBS|F-SBS|FULL-SBS|SBS|HOU|H-OU|HALF-OU|FOU|F-OU|FULL-OU|OU|BD3D)\b`)

	// Tokens that commonly appear right after the year in scene release names.
	reQualityStart = regexp.MustCompile(
		`(?i)^[.\s_\-]*(4k|2160p|1080p|720p|576p|480p|` +
			`blu[\-\s]?ray|bdrip|bdremux|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip|remux|` +
			`x265|h\.?265|hevc|x264|h\.?264|xvid|divx|av1|` +
			`extended|theatrical|remaster|proper|repack|` +
			`hdr10[\+]?|hdr|sdr|dolby|` +
			`\[|\()`,
	)

	reNoise = regexp.MustCompile(
		`(?i)\b(4k|2160p|1080p|720p|576p|480p|` +
			`blu[\-\s]?ray|bdrip|bdremux|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip|remux|` +
			`x265|h\.?265|hevc|x264|h\.?264|xvid|divx|av1|` +
			`extended|theatrical|remaster|proper|repack|` +
			`hdr10[\+]?|hdr|sdr|dolby|` +
			`\[\w+\]|-\w+$)\b.*`)

	reContainer = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|ts|m2ts|wmv|webm|flv|ogm|divx|asf)$`)
)

// Parse extracts movie metadata from a release title.
// Returns (nil, false) if no year can be identified.
func Parse(title string) (*Movie, bool) {
	m := &Movie{}
	m.Quality = quality.Parse(title)
	m.Is3D = re3D.MatchString(title)

	// Strip file container extension.
	if ext := reContainer.FindString(title); ext != "" {
		title = title[:len(title)-len(ext)]
	}

	// Find all year positions in the title.
	idxs := reYear.FindAllStringIndex(title, -1)
	if len(idxs) == 0 {
		return nil, false
	}

	// Prefer the first year that is followed by a quality marker.
	yearIdx := -1
	yearVal := 0
	for _, loc := range idxs {
		y, _ := strconv.Atoi(title[loc[0]:loc[1]])
		rest := title[loc[1]:]
		if reQualityStart.MatchString(rest) {
			yearIdx = loc[0]
			yearVal = y
			break
		}
	}
	// Fall back to the last year occurrence.
	if yearIdx == -1 {
		last := idxs[len(idxs)-1]
		yearIdx = last[0]
		yearVal, _ = strconv.Atoi(title[last[0]:last[1]])
	}

	m.Year = yearVal
	m.Title = NormalizeTitle(title[:yearIdx])
	if m.Title == "" {
		return nil, false
	}
	return m, true
}

// NormalizeTitle cleans up a raw title prefix:
//   - replaces dots and underscores with spaces
//   - strips trailing noise tokens
//   - collapses whitespace, applies title case
func NormalizeTitle(raw string) string {
	s := strings.Map(func(r rune) rune {
		if r == '.' || r == '_' {
			return ' '
		}
		return r
	}, raw)

	s = reNoise.ReplaceAllString(s, "")

	fields := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-'
	})

	for i, f := range fields {
		if len(f) == 0 {
			continue
		}
		runes := []rune(f)
		runes[0] = unicode.ToUpper(runes[0])
		fields[i] = string(runes)
	}

	return strings.Join(fields, " ")
}
