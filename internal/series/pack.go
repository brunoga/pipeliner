package series

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/quality"
)

// Pack is a release that carries a whole season, a run of seasons, or a whole
// series rather than one episode: "Agatha.All.Along.S01.1080p.WEB",
// "Show.Seasons.1-3", "Show.S01-S03", "Show.The.Complete.Series".
type Pack struct {
	SeriesName string
	// Season is the first season covered and LastSeason the last; they are
	// equal for a single season. Both are 0 for a Complete pack that names
	// no seasons.
	Season, LastSeason int
	// Complete is a pack of the whole series. "Show S01 Complete" is a
	// complete SEASON and is not this; only "Complete Series" (or a range,
	// which says so with its seasons) reaches past one season.
	Complete       bool
	SeriesYear     int
	Proper, Repack bool
	Service        string
	Quality        quality.Quality
}

// MultiSeason reports whether the pack covers more than one season, which is
// what decides whether it belongs in a season folder or in the show's.
func (p *Pack) MultiSeason() bool {
	return p.Complete || p.LastSeason > p.Season
}

// Covers reports whether the pack holds the given season.
func (p *Pack) Covers(season int) bool {
	if p.Complete && p.Season == 0 {
		return season > 0
	}
	return season >= p.Season && season <= p.LastSeason
}

// maxPackSeason bounds a season number read from a pack name. Nothing airs
// past it, and it keeps "S2160" and the like out of a name that is not a pack.
const maxPackSeason = 60

var (
	// S01, S01-S03, S01-03. \b on both sides keeps an episode identifier
	// (S01E01) out: there is no word boundary between the season's digits and
	// the E, so it never reaches here -- and Parse claims it first anyway.
	rePackSeason = regexp.MustCompile(`(?i)\bS(\d{1,2})(?:[ .]?-[ .]?S?(\d{1,2}))?\b`)
	// Season 1, Season.01, Seasons 1-3, Season 1 to 3.
	rePackSeasonWord = regexp.MustCompile(`(?i)\bSeasons?[ ._\-]*(\d{1,2})(?:[ ._]*(?:-|to|&)[ ._]*(\d{1,2}))?\b`)
	// The Complete Series. "Complete" alone is not enough: it is a word in
	// film titles ("The Complete Metropolis") and a tag on single seasons.
	rePackComplete = regexp.MustCompile(`(?i)\b(?:The[ ._\-]+)?Complete[ ._\-]+Series\b`)
)

// ParsePack reads a season, multi-season or complete-series pack from a
// release title. A title Parse reads as an episode is not a pack, whatever
// else it says, so this returns false for it.
func ParsePack(title string) (*Pack, bool) {
	if _, ok := Parse(title); ok {
		return nil, false
	}
	p := &Pack{Quality: quality.Parse(title)}
	if m := reContainer.FindString(title); m != "" {
		title = title[:len(title)-len(m)]
	}
	if m := reProper.FindString(title); m != "" {
		p.Proper = strings.EqualFold(m, "PROPER")
		p.Repack = !p.Proper
	}
	if m := reService.FindString(title); m != "" {
		if c, ok := serviceCanonical[strings.ToLower(m)]; ok {
			p.Service = c
		} else {
			p.Service = m
		}
	}

	// The show's name ends at the first pack marker, whichever kind it is.
	cut := -1
	end := -1
	mark := func(m []int) {
		if m != nil && (cut < 0 || m[0] < cut) {
			cut, end = m[0], m[1]
		}
	}
	season := rePackSeason.FindStringSubmatchIndex(title)
	word := rePackSeasonWord.FindStringSubmatchIndex(title)
	complete := rePackComplete.FindStringIndex(title)
	mark(season)
	mark(word)
	mark(complete)
	if cut < 0 {
		return nil, false
	}

	switch {
	case season != nil && (word == nil || season[0] <= word[0]):
		p.Season, p.LastSeason = rangeAt(title, season)
	case word != nil:
		p.Season, p.LastSeason = rangeAt(title, word)
	}
	if p.Season > maxPackSeason || p.LastSeason > maxPackSeason || p.LastSeason < p.Season {
		return nil, false
	}
	if complete != nil {
		p.Complete = true
		// "Show Complete Series S01-S05" names its seasons; one that names
		// none covers the run, whatever it is.
		if p.Season == 0 {
			p.LastSeason = 0
		}
	}
	if p.Season == 0 && !p.Complete {
		// "S00" alone, or a season word with nothing usable after it.
		return nil, false
	}

	p.SeriesName = extractName(title, cut)
	if p.SeriesName == "" {
		return nil, false
	}
	p.SeriesYear = seriesYear(p.SeriesName, title[end:])
	return p, true
}

// rangeAt reads the first and last season from a match of rePackSeason or
// rePackSeasonWord, whose groups 1 and 2 are the two ends.
func rangeAt(title string, m []int) (first, last int) {
	first, _ = strconv.Atoi(title[m[2]:m[3]])
	last = first
	if m[4] >= 0 {
		last, _ = strconv.Atoi(title[m[4]:m[5]])
	}
	return first, last
}

// PackID is a pack's canonical identity within its show, the counterpart of
// EpisodeID: "S01", "S01-S03", or "COMPLETE".
func PackID(p *Pack) string {
	switch {
	case p.Complete && p.Season == 0:
		return "COMPLETE"
	case p.LastSeason > p.Season:
		return "S" + pad2(p.Season) + "-S" + pad2(p.LastSeason)
	default:
		return "S" + pad2(p.Season)
	}
}

func pad2(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}
