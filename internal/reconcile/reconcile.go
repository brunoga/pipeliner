// Package reconcile compares pipeliner's download trackers against the actual
// media library so tracker/reality divergence is visible and fixable. The
// motivating incident: torrents removed by the janitor without their movies
// being un-tracked (a missing grab record) left dozens of titles marked as
// downloaded that were never kept — silently blocking every retry.
//
// Matching is deliberately forgiving in the same ways the manual forensic
// pass was: titles are compared as normalized token sets ("&" equals "and",
// punctuation dropped) with exact-set or subset-either-way equality, and
// years must be within ±1 (0 acts as a wildcard). A miss therefore means
// "really not in the library", not "spelled differently".
package reconcile

import (
	"sort"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/mediaserver"
	imovies "github.com/brunoga/pipeliner/internal/movies"
)

// MissingMovie is a tracker record with no matching library item.
type MissingMovie struct {
	Key          string    `json:"key"` // raw tracker key (title|year[|3d])
	Title        string    `json:"title"`
	Year         int       `json:"year"`
	Is3D         bool      `json:"is_3d"`
	Quality      string    `json:"quality"`
	DownloadedAt time.Time `json:"downloaded_at"`
}

// TrackerRecord pairs a raw tracker key with its decoded record.
type TrackerRecord struct {
	Key    string
	Record imovies.Record
}

// Movies returns the tracker records absent from the library sections.
// 3D records are only matched against 3D sections (name contains "3d") and
// non-3D records only against the rest, so owning the 2D copy never masks a
// missing 3D one or vice versa.
func Movies(records []TrackerRecord, sections []mediaserver.MovieSection) []MissingMovie {
	var lib3D, lib2D []mediaserver.Item
	for _, sec := range sections {
		is3D := strings.Contains(strings.ToLower(sec.Name), "3d")
		for _, it := range sec.Items {
			if is3D {
				lib3D = append(lib3D, it)
			} else {
				lib2D = append(lib2D, it)
			}
		}
	}

	idx3D := buildIndex(lib3D)
	idx2D := buildIndex(lib2D)

	var missing []MissingMovie
	for _, tr := range records {
		idx := idx2D
		if tr.Record.Is3D {
			idx = idx3D
		}
		if idx.has(tr.Record.Title, tr.Record.Year) {
			continue
		}
		missing = append(missing, MissingMovie{
			Key:          tr.Key,
			Title:        tr.Record.Title,
			Year:         tr.Record.Year,
			Is3D:         tr.Record.Is3D,
			Quality:      tr.Record.Quality.String(),
			DownloadedAt: tr.Record.DownloadedAt,
		})
	}
	sort.Slice(missing, func(i, j int) bool {
		if missing[i].Is3D != missing[j].Is3D {
			return !missing[i].Is3D
		}
		return missing[i].Key < missing[j].Key
	})
	return missing
}

// libIndex holds tokenized library items for matching.
type libIndex struct {
	items []libItem
}

type libItem struct {
	toks map[string]bool
	year int
}

func buildIndex(items []mediaserver.Item) libIndex {
	idx := libIndex{items: make([]libItem, 0, len(items))}
	for _, it := range items {
		idx.items = append(idx.items, libItem{toks: tokenSet(it.Title), year: it.Year})
	}
	return idx
}

// has reports whether any library item matches the title+year: token sets
// equal or one a subset of the other, years within ±1 (0 = wildcard).
func (x libIndex) has(title string, year int) bool {
	want := tokenSet(title)
	if len(want) == 0 {
		return false
	}
	for _, it := range x.items {
		if !yearsCompatible(year, it.year) {
			continue
		}
		if subset(want, it.toks) || subset(it.toks, want) {
			return true
		}
	}
	return false
}

func yearsCompatible(a, b int) bool {
	if a == 0 || b == 0 {
		return true
	}
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= 1
}

func subset(a, b map[string]bool) bool {
	if len(a) == 0 || len(a) > len(b) {
		return false
	}
	for t := range a {
		if !b[t] {
			return false
		}
	}
	return true
}

// tokenSet normalizes a title into a token set: lowercase, "&" equals "and",
// apostrophes dropped, all other punctuation treated as separators.
func tokenSet(s string) map[string]bool {
	s = strings.ToLower(s)
	s = strings.ReplaceAll(s, "&", " and ")
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '\'' || r == '’':
			// dropped with no separator ("marvel's" → "marvels")
		case r == ' ' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
		default:
			b.WriteRune(' ')
		}
	}
	out := make(map[string]bool)
	for _, t := range strings.Fields(b.String()) {
		out[t] = true
	}
	return out
}
