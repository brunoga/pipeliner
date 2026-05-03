// Package series parses TV series and episode information from media release titles.
package series

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/brunoga/pipeliner/internal/quality"
)

// Episode holds the parsed metadata for a single episode from a release title.
type Episode struct {
	SeriesName    string
	Season        int
	Episode       int
	DoubleEpisode int  // second episode number for double releases (e.g. S01E01E02 → 2)
	IsDate        bool // true when the episode is identified by air date rather than number
	Year, Month, Day int
	IsSpecial     bool
	Proper, Repack bool
	Service       string // streaming service tag, e.g. "Netflix", "AMZN", "ATVP"
	Container     string // file container, e.g. "mkv", "mp4"
	Quality       quality.Quality
}

// compiled patterns, from most specific to least specific

var (
	// S01E01, S01E01E02, S01E01-E02
	reStandard = regexp.MustCompile(`(?i)S(\d{1,2})E(\d{1,3})(?:[- ]?E(\d{1,3}))?`)
	// 1x01
	reAltNum = regexp.MustCompile(`(?i)\b(\d{1,2})x(\d{2,3})\b`)
	// 2023.11.15 or 2023-11-15
	reDate = regexp.MustCompile(`\b((?:19|20)\d{2})[.\-](\d{2})[.\-](\d{2})\b`)
	// EP123 or E123 (absolute, anime-style)
	reAbsolute = regexp.MustCompile(`(?i)\bE(?:P)?(\d{2,4})\b`)

	reProper = regexp.MustCompile(`(?i)\b(PROPER|REPACK|RERIP)\b`)

	// Streaming service tokens used in scene release names.
	// Ordered longest-first within each group to avoid partial matches.
	reService = regexp.MustCompile(
		`(?i)\b(` +
			`Netflix|NF|` +
			`AmazonPrime|AMZN|` +
			`AppleTV\+?|ATVP|` +
			`DisneyPlus|Disney\+|DSNP|` +
			`HBOMax|HMAX|HBO|` +
			`ParamountPlus|Paramount\+|PMTP|` +
			`Peacock|PCOK|` +
			`Crunchyroll|CR|` +
			`iPlayer|iP|` +
			`HULU|` +
			`STAN|` +
			`CBS|` +
			`FOX` +
			`)\b`)

	// serviceCanonical maps lower-cased service tokens to a display name.
	serviceCanonical = map[string]string{
		"netflix": "Netflix", "nf": "Netflix",
		"amazonprime": "AMZN", "amzn": "AMZN",
		"appletv+": "ATVP", "appletv": "ATVP", "atvp": "ATVP",
		"disneyplus": "DSNP", "disney+": "DSNP", "dsnp": "DSNP",
		"hbomax": "HMAX", "hmax": "HMAX", "hbo": "HBO",
		"paramountplus": "PMTP", "paramount+": "PMTP", "pmtp": "PMTP",
		"peacock": "Peacock", "pcok": "Peacock",
		"crunchyroll": "CR", "cr": "CR",
		"iplayer": "iPlayer", "ip": "iPlayer",
		"hulu": "HULU",
		"stan": "STAN",
		"cbs": "CBS",
		"fox": "FOX",
	}

	// File container extensions.
	reContainer = regexp.MustCompile(`(?i)\.(mkv|mp4|avi|mov|ts|m2ts|wmv|webm|flv|ogm|divx|asf)$`)

	// Noise suffixes to strip when extracting the series name.
	// These are common quality/group markers that appear after the episode identifier.
	reNoiseSuffix = regexp.MustCompile(`(?i)\b(S\d{1,2}E\d{1,3}|` +
		`\d{1,2}x\d{2,3}|` +
		`(?:19|20)\d{2}[.\-]\d{2}[.\-]\d{2}|` +
		`E(?:P)?\d{2,4}|` +
		`4k|2160p|1080p|720p|576p|480p|` +
		`blu[\-\s]?ray|bdrip|bdremux|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip|` +
		`remux|x265|h\.?265|hevc|x264|h\.?264|xvid|divx|av1|` +
		`atmos|truehd|dts|aac|mp3|` +
		`hdr10[\+]?|hdr|sdr|dolby|` +
		`proper|repack|rerip|` +
		// service tags in name prefix
		`netflix|nf|amzn|atvp|dsnp|hmax|pmtp|pcok|hulu|stan|` +
		`\[\w+\]|-\w+$)\b.*`)
)

// Parse extracts episode metadata from a release title.
// Returns (nil, false) if no episode pattern is found.
func Parse(title string) (*Episode, bool) {
	ep := &Episode{}
	ep.Quality = quality.Parse(title)

	// Strip file container extension first so it doesn't confuse other patterns.
	if m := reContainer.FindString(title); m != "" {
		ep.Container = strings.ToLower(strings.TrimPrefix(m, "."))
		title = title[:len(title)-len(m)]
	}

	if m := reProper.FindString(title); m != "" {
		switch strings.ToUpper(m) {
		case "PROPER":
			ep.Proper = true
		case "REPACK", "RERIP":
			ep.Repack = true
		}
	}

	if m := reService.FindString(title); m != "" {
		if canonical, ok := serviceCanonical[strings.ToLower(m)]; ok {
			ep.Service = canonical
		} else {
			ep.Service = m
		}
	}

	// Try patterns from most to least specific.
	if m := reStandard.FindStringSubmatchIndex(title); m != nil {
		sub := reStandard.FindStringSubmatch(title)
		ep.Season, _ = strconv.Atoi(sub[1])
		ep.Episode, _ = strconv.Atoi(sub[2])
		if sub[3] != "" {
			ep.DoubleEpisode, _ = strconv.Atoi(sub[3])
		}
		ep.SeriesName = extractName(title, m[0])
		return ep, true
	}

	if m := reAltNum.FindStringSubmatchIndex(title); m != nil {
		sub := reAltNum.FindStringSubmatch(title)
		ep.Season, _ = strconv.Atoi(sub[1])
		ep.Episode, _ = strconv.Atoi(sub[2])
		ep.SeriesName = extractName(title, m[0])
		return ep, true
	}

	if m := reDate.FindStringSubmatchIndex(title); m != nil {
		sub := reDate.FindStringSubmatch(title)
		ep.IsDate = true
		ep.Year, _ = strconv.Atoi(sub[1])
		ep.Month, _ = strconv.Atoi(sub[2])
		ep.Day, _ = strconv.Atoi(sub[3])
		ep.SeriesName = extractName(title, m[0])
		return ep, true
	}

	if m := reAbsolute.FindStringSubmatchIndex(title); m != nil {
		sub := reAbsolute.FindStringSubmatch(title)
		ep.Episode, _ = strconv.Atoi(sub[1])
		ep.SeriesName = extractName(title, m[0])
		return ep, true
	}

	return nil, false
}

// extractName derives the series name from the portion of the title that
// precedes the episode identifier at position idx.
func extractName(title string, idx int) string {
	prefix := title[:idx]
	return NormalizeName(prefix)
}

// NormalizeName cleans up a raw title or name prefix:
//   - strips trailing noise (year, quality tokens, group tags)
//   - replaces dots and underscores with spaces
//   - collapses repeated whitespace
//   - applies title case
func NormalizeName(raw string) string {
	// Replace separators with spaces.
	s := strings.Map(func(r rune) rune {
		if r == '.' || r == '_' {
			return ' '
		}
		return r
	}, raw)

	// Strip trailing noise.
	s = reNoiseSuffix.ReplaceAllString(s, "")

	// Collapse whitespace and trim.
	fields := strings.FieldsFunc(s, func(r rune) bool {
		return unicode.IsSpace(r) || r == '-'
	})

	// Title-case each word.
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
