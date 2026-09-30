package series

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/brunoga/pipeliner/internal/match"
)

// reKeyTrailingYear matches a normalized show name ending in a bare year:
// "brothers 2026" → ("brothers", "2026").
var reKeyTrailingYear = regexp.MustCompile(`^(.*\S) ((?:19|20)\d{2})$`)

// Show is the identity of a series independent of how a release or a
// metadata provider happens to spell it. The same show reaches the tracker as
// "Brothers 2026 S01E01", "Brothers S01E01 2026" and TheTVDB's "Brothers" or
// "Brothers (2026)" — TheTVDB adds and drops the year suffix as it
// disambiguates names — so a show is its normalized base name plus an
// optional year, and two shows are the same when their bases match and their
// years do not contradict each other.
type Show struct {
	Base string // normalized name without a trailing year
	Year int    // premiere year; 0 when unknown
}

// NewShow builds a Show from a raw name and an optional year. A trailing year
// in the name is folded into Year when it agrees with the year given (or no
// year is given); a trailing year that contradicts it is kept as part of the
// title ("Class of 1984", premiering in 2018).
func NewShow(name string, year int) Show {
	norm := match.Normalize(name)
	if m := reKeyTrailingYear.FindStringSubmatch(norm); m != nil {
		ty, _ := strconv.Atoi(m[2])
		if year == 0 || match.YearsCompatible(ty, year) {
			if year == 0 {
				year = ty
			}
			return Show{Base: m[1], Year: year}
		}
	}
	return Show{Base: norm, Year: year}
}

// Key is the tracker key for a show with no records yet: the base name, plus
// the year when known.
func (s Show) Key() string {
	if s.Year == 0 {
		return s.Base
	}
	return s.Base + " " + strconv.Itoa(s.Year)
}

// Matches reports whether s and o name the same show. An unknown year on
// either side matches any year.
func (s Show) Matches(o Show) bool {
	return s.Base != "" && s.Base == o.Base && match.YearsCompatible(s.Year, o.Year)
}

// ShowRef locates one show in the tracker.
type ShowRef struct {
	// Key is where new records for the show are written.
	Key string
	// Keys holds every tracker key with records for the show, Key first.
	// Records written before shows were keyed by identity can sit under
	// several spellings ("brothers" and "brothers 2026"); reads consult all
	// of them.
	Keys []string
}

// Resolve finds the tracker keys holding records for the show named name
// (year 0 when unknown). New records go to:
//   - the show's name+year key, when the year is known and that key exists;
//   - otherwise an existing spelling, preferring one that carries a year, so a
//     show keeps growing under a key it already has (no migration needed);
//   - otherwise, for a show with no records, the name+year key (the bare
//     name when the year is unknown).
func (t *Tracker) Resolve(name string, year int) ShowRef {
	show := NewShow(name, year)
	if show.Base == "" {
		return ShowRef{}
	}
	composed := show.Key()
	var existing []string
	if keys, err := t.showKeys(); err == nil {
		for _, k := range keys {
			if NewShow(k, 0).Matches(show) {
				existing = append(existing, k)
			}
		}
	}
	sort.Strings(existing)
	ref := ShowRef{Key: composed}
	if show.Year != 0 && slices.Contains(existing, composed) {
		ref.Keys = existing
		moveToFront(ref.Keys, composed)
		return ref
	}
	if len(existing) > 0 {
		// Prefer a spelling that carries the year, so records converge on
		// name+year even when this release does not name the year.
		ref.Key = existing[0]
		for _, k := range existing {
			if NewShow(k, 0).Year != 0 {
				ref.Key = k
				break
			}
		}
		ref.Keys = existing
		moveToFront(ref.Keys, ref.Key)
		return ref
	}
	ref.Keys = []string{composed}
	return ref
}

// GetAny returns the most recently downloaded record for episodeID across
// every key in seriesNames.
func (t *Tracker) GetAny(seriesNames []string, episodeID string) (*Record, bool) {
	var best *Record
	for _, name := range seriesNames {
		rec, ok := t.Get(name, episodeID)
		if !ok {
			continue
		}
		if best == nil || rec.DownloadedAt.After(best.DownloadedAt) {
			best = rec
		}
	}
	return best, best != nil
}

// showKeys returns the distinct show names that have tracker records.
func (t *Tracker) showKeys() ([]string, error) {
	keys, err := t.bucket.Keys()
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool)
	var out []string
	for _, k := range keys {
		i := strings.LastIndex(k, "|")
		if i <= 0 {
			continue
		}
		name := k[:i]
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out, nil
}

func moveToFront(keys []string, k string) {
	for i, v := range keys {
		if v == k {
			copy(keys[1:i+1], keys[:i])
			keys[0] = k
			return
		}
	}
}
