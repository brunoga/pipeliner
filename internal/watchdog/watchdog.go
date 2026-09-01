// Package watchdog detects "stuck" favorites: shows or movies on a user's list
// whose candidates keep appearing in runs but never get accepted. This inverts
// the failure mode behind the Star Trek: Strange New Worlds incident — an
// episode silently failing to download for weeks with nothing to flag it.
//
// Detection associates each candidate seen in the run traces with its nearest
// favorite by edit distance (so a normalization gap like a stray colon still
// links the release to the intended favorite), then reports favorites that were
// seen across enough runs but never accepted. A NearestDistance of 0 means the
// favorite matched but every candidate was rejected downstream (quality,
// tracking, dedup); a positive distance means candidates nearly match the
// favorite but don't — the fingerprint of a matching/normalization problem.
package watchdog

import (
	"sort"
	"strconv"
	"time"

	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/traces"
)

// DefaultMinRuns is the default number of distinct runs a favorite must appear
// in (never accepted) before it is reported as stuck.
const DefaultMinRuns = 3

// DefaultMaxDistance is the default edit-distance ceiling for associating a
// candidate title with a favorite. Beyond this a candidate is treated as
// unrelated feed noise rather than a near-miss of a favorite.
const DefaultMaxDistance = 6

// StuckFavorite is one favorite whose candidates were seen but never accepted.
type StuckFavorite struct {
	// Favorite is the normalized favorite name the candidates associated with.
	Favorite string `json:"favorite"`
	// Runs is the number of distinct runs a candidate for this favorite was
	// seen in without ever being accepted.
	Runs int `json:"runs"`
	// Occurrences is the total number of candidate entries seen.
	Occurrences int `json:"occurrences"`
	// NearestDistance is the smallest edit distance between an associated
	// candidate's show name and this favorite. 0 means the favorite matched
	// (so the block is downstream — quality/tracking); >0 means candidates only
	// nearly match, suggesting a matching/normalization problem.
	NearestDistance int `json:"nearest_distance"`
	// LastSeen, LastReason, LastState, LastTask describe the most recent
	// candidate occurrence.
	LastSeen   time.Time `json:"last_seen"`
	LastReason string    `json:"last_reason,omitempty"`
	LastState  string    `json:"last_state,omitempty"`
	LastTask   string    `json:"last_task,omitempty"`
	// ExampleTitle is a representative candidate release title.
	ExampleTitle string `json:"example_title,omitempty"`
}

// showName derives a group name from a release title: the parsed series name
// when it parses as an episode, otherwise the whole title. The result is not
// normalized (Probe normalizes it).
func showName(title string) string {
	if ep, ok := series.Parse(title); ok && ep.SeriesName != "" {
		return ep.SeriesName
	}
	return title
}

// favAssoc is the memoized outcome of associating one show name with the
// favorite list: which favorite it maps to, at what distance, and whether it
// was close enough to count.
type favAssoc struct {
	favNorm string
	dist    int
	ok      bool
}

// dedupeFavorites removes duplicate favorites (same normalized name and year),
// which arise when the series and movies list caches are unioned and the same
// title appears in more than one pipeline. Deduping shrinks the inner Probe
// loop and keeps the candidate ranking clean.
func dedupeFavorites(favorites []match.TitleEntry) []match.TitleEntry {
	seen := make(map[string]bool, len(favorites))
	out := favorites[:0:0]
	for _, f := range favorites {
		key := f.Norm + "\x00" + strconv.Itoa(f.Year)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

type favAgg struct {
	runs         map[string]bool
	occ          int
	accepted     bool
	minDistance  int
	lastAt       time.Time
	lastReason   string
	lastState    string
	lastTask     string
	exampleTitle string
}

// Detect reports favorites whose candidates were seen across at least minRuns
// distinct runs but never accepted. Candidates are associated with a favorite
// only when within maxDistance edits of it (0 distance = exact/glob match).
// Occurrences that match no favorite closely enough are ignored as feed noise.
//
// minRuns <= 0 and maxDistance <= 0 fall back to the package defaults.
func Detect(occ []traces.Occurrence, favorites []match.TitleEntry, minRuns, maxDistance int) []StuckFavorite {
	if minRuns <= 0 {
		minRuns = DefaultMinRuns
	}
	if maxDistance <= 0 {
		maxDistance = DefaultMaxDistance
	}
	favorites = dedupeFavorites(favorites)
	if len(favorites) == 0 {
		return nil
	}

	// Probing every occurrence against every favorite is O(occ × fav × len²),
	// which blows past the web timeout on a real trace store (tens of thousands
	// of occurrences × hundreds of favorites). The same shows recur across the
	// kept runs, so memoize the association by show name: the expensive Probe
	// runs once per distinct show, not once per occurrence.
	nameCache := map[string]string{}   // raw title → showName (avoids re-parsing)
	assocCache := map[string]favAssoc{} // showName → nearest-favorite association
	byFav := map[string]*favAgg{}
	for _, o := range occ {
		name, named := nameCache[o.Entry.Title]
		if !named {
			name = showName(o.Entry.Title)
			nameCache[o.Entry.Title] = name
		}
		a, cached := assocCache[name]
		if !cached {
			p := match.Probe(name, 0, favorites)
			favNorm, dist, ok := nearestFavorite(p, maxDistance)
			a = favAssoc{favNorm: favNorm, dist: dist, ok: ok}
			assocCache[name] = a
		}
		if !a.ok {
			continue
		}
		favNorm, dist := a.favNorm, a.dist
		g := byFav[favNorm]
		if g == nil {
			g = &favAgg{runs: map[string]bool{}, minDistance: dist}
			byFav[favNorm] = g
		}
		g.occ++
		g.runs[o.RunID] = true
		if dist < g.minDistance {
			g.minDistance = dist
		}
		if o.Entry.Final == "accepted" {
			g.accepted = true
		}
		if o.At.After(g.lastAt) {
			g.lastAt = o.At
			g.lastReason = o.Entry.Reason
			g.lastState = o.Entry.Final
			g.lastTask = o.Task
			g.exampleTitle = o.Entry.Title
		}
	}

	var out []StuckFavorite
	for favNorm, g := range byFav {
		if g.accepted || len(g.runs) < minRuns {
			continue
		}
		out = append(out, StuckFavorite{
			Favorite:        favNorm,
			Runs:            len(g.runs),
			Occurrences:     g.occ,
			NearestDistance: g.minDistance,
			LastSeen:        g.lastAt,
			LastReason:      g.lastReason,
			LastState:       g.lastState,
			LastTask:        g.lastTask,
			ExampleTitle:    g.exampleTitle,
		})
	}
	// Most runs first, then most recently seen — the loudest problems on top.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Runs != out[j].Runs {
			return out[i].Runs > out[j].Runs
		}
		return out[i].LastSeen.After(out[j].LastSeen)
	})
	return out
}

// nearestFavorite picks the favorite a probe result associates with: the
// matched favorite (distance 0) when the title matched, otherwise the nearest
// candidate if it is within maxDistance. ok is false when nothing is close
// enough.
func nearestFavorite(p match.ProbeResult, maxDistance int) (favNorm string, dist int, ok bool) {
	if len(p.Candidates) == 0 {
		return "", 0, false
	}
	best := p.Candidates[0] // matches sort first, then nearest by distance
	if p.Matched {
		return best.Norm, 0, true
	}
	if best.Distance <= maxDistance {
		return best.Norm, best.Distance, true
	}
	return "", 0, false
}
