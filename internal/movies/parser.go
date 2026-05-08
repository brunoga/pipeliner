// Package movies parses movie metadata from media release names, including
// title, year, video quality, and 3D format detection.
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
	Proper  bool
	Repack  bool
}

var (
	reYear    = regexp.MustCompile(`\b((?:19|20)\d{2})\b`)
	reEpisode = regexp.MustCompile(`(?i)\bS\d+E\d+\b`)

	// Tokens that commonly appear right after the year in scene release names.
	reQualityStart = regexp.MustCompile(
		`(?i)^[.\s_\-]*(4k|2160p|1080p|720p|576p|480p|` +
			`blu[\-\s]?ray|bdrip|bdremux|bd(?:25|50|100)|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip|remux|` +
			`x265|h\.?265|hevc|x264|h\.?264|xvid|divx|av1|mvc|` +
			`bd3d|full[\-]?sbs|full[\-]?ou|fsbs|f[\-]sbs|fou|f[\-]ou|half[\-]?sbs|half[\-]?ou|hsbs|h[\-]sbs|hou|h[\-]ou|sbs|ou|3d|` +
			`extended|theatrical|remaster|proper|repack|` +
			`hdr10[\+]?|hdr|sdr|dolby|` +
			`\[|\()`,
	)

	// reNoise matches quality tokens anywhere in a title. Used both for title
	// cleanup (after year extraction) and as a quality-boundary finder when no
	// year is present.
	reNoise = regexp.MustCompile(
		`(?i)\b(4k|2160p|1080p|720p|576p|480p|` +
			`blu[\-\s]?ray|bdrip|bdremux|bd(?:25|50|100)|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip|remux|` +
			`x265|h\.?265|hevc|x264|h\.?264|xvid|divx|av1|mvc|` +
			`bd3d|full[\-]?sbs|full[\-]?ou|fsbs|f[\-]sbs|fou|f[\-]ou|half[\-]?sbs|half[\-]?ou|hsbs|h[\-]sbs|hou|h[\-]ou|sbs|ou|3d|` +
			`extended|theatrical|remaster|proper|repack|` +
			`hdr10[\+]?|hdr|sdr|dolby|` +
			`\[\w+\]|-\w+$)\b.*`)

	reContainer = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|ts|m2ts|wmv|webm|flv|ogm|divx|asf)$`)

	reProper = regexp.MustCompile(`(?i)\b(PROPER|REPACK|RERIP)\b`)
)

// Parse extracts movie metadata from a release title.
// If a year is found it is used to split title from quality info. As a
// fallback, the first quality marker (e.g. "BluRay", "BD50", "MVC") is used
// as the boundary so year-less releases like "Despicable Me 3 Bluray Complete"
// can still be matched. Year is 0 in that case.
func Parse(title string) (*Movie, bool) {
	m := &Movie{}
	m.Quality = quality.Parse(title)
	if tok := reProper.FindString(title); tok != "" {
		if strings.EqualFold(tok, "repack") {
			m.Repack = true
		} else {
			m.Proper = true
		}
	}

	// Strip file container extension.
	if ext := reContainer.FindString(title); ext != "" {
		title = title[:len(title)-len(ext)]
	}

	// Find all year positions in the title.
	idxs := reYear.FindAllStringIndex(title, -1)
	if len(idxs) == 0 {
		// No year — fall back to first quality marker as the title boundary,
		// but only if the title doesn't look like a TV episode (S01E01 etc.).
		if reEpisode.MatchString(title) {
			return nil, false
		}
		converted := strings.Map(func(r rune) rune {
			if r == '.' || r == '_' {
				return ' '
			}
			return r
		}, title)
		loc := reNoise.FindStringIndex(converted)
		if loc == nil {
			return nil, false
		}
		m.Title = NormalizeTitle(title[:loc[0]])
		if m.Title == "" {
			return nil, false
		}
		return m, true
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
