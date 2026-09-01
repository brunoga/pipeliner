// Package watchdog detects "stuck" favorites: shows or movies on a user's list
// whose candidates keep appearing in runs but never get accepted. This inverts
// the failure mode behind the Star Trek: Strange New Worlds incident — an
// episode silently failing to download for weeks with nothing to flag it.
//
// Detection associates each candidate seen in the run traces with its nearest
// favorite (an exact/glob match, or a genuine near-miss — a punctuation-only
// normalization gap or a small fraction of the title length, so unrelated titles
// that merely fall within a few edits are not linked), then reports favorites
// whose candidates were blocked across enough runs and never succeeded. A
// NearestDistance of 0 means the favorite matched but every candidate was
// rejected downstream (quality, tracking); a positive distance means candidates
// nearly match but don't — the fingerprint of a matching/normalization problem.
//
// Only a terminal block counts as evidence: an "undecided" occurrence never
// reached an accept/reject (the shape of a discover/search pipeline, which emits
// a result per favorite every run) and is ignored, and an accept, a silent
// consume, or an "already downloaded"/"already seen" rejection all mark a
// favorite healthy — proof the pipeline grabbed it, not that it is stuck.
package watchdog

import (
	"sort"
	"strconv"
	"strings"
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

// dedupeFavorites removes duplicate favorites (same normalized name, year, and
// kind), which arise when the series and movies list caches are unioned and the
// same title appears in more than one pipeline. Deduping shrinks the inner Probe
// loop and keeps the candidate ranking clean. A title present as both a series
// and a movie favorite is kept once per kind so each stays correctly scoped.
func dedupeFavorites(favorites []Favorite) []Favorite {
	seen := make(map[string]bool, len(favorites))
	out := favorites[:0:0]
	for _, f := range favorites {
		key := f.Entry.Norm + "\x00" + strconv.Itoa(f.Entry.Year) + "\x00" + strconv.Itoa(int(f.Kind))
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, f)
	}
	return out
}

// favoritesForMask returns the favorites whose kind intersects mask, as a plain
// TitleEntry list for Probe. The result is memoized per mask by the caller.
func favoritesForMask(favorites []Favorite, mask MediaKind) []match.TitleEntry {
	out := make([]match.TitleEntry, 0, len(favorites))
	for _, f := range favorites {
		if f.Kind&mask != 0 {
			out = append(out, f.Entry)
		}
	}
	return out
}

// allowMask returns the media kinds an occurrence's task is allowed to correlate
// against. A task with a recorded kind uses exactly that kind; an unknown task
// (not in the map, e.g. an older database with no persisted map) falls back to
// all kinds so scoping never hides a stuck favorite it can't yet classify.
func allowMask(taskKinds map[string]MediaKind, task string) MediaKind {
	if k, ok := taskKinds[task]; ok {
		return k
	}
	return allKinds
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
// Correlation is scoped by media kind: a series favorite is only matched against
// occurrences from tasks that filter series, and a movie favorite only against
// tasks that filter movies, so a TV favorite never picks up rejections from an
// unrelated movies pipeline (taskKinds maps task name → the kinds it consumes;
// an unknown task correlates against all kinds). A nil/empty taskKinds disables
// scoping — every task reads as unknown, the pre-scoping behavior.
//
// minRuns <= 0 and maxDistance <= 0 fall back to the package defaults.
func Detect(occ []traces.Occurrence, favorites []Favorite, taskKinds map[string]MediaKind, minRuns, maxDistance int) []StuckFavorite {
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
	// kept runs, so memoize the association by (allowed-kind mask, show name):
	// the expensive Probe runs once per distinct show per mask, not once per
	// occurrence. maskFavs memoizes the per-mask favorite sublist Probe scans.
	nameCache := map[string]string{}    // raw title → showName (avoids re-parsing)
	assocCache := map[string]favAssoc{} // "mask\x00showName" → nearest-favorite association
	maskFavs := map[MediaKind][]match.TitleEntry{}
	byFav := map[string]*favAgg{}
	for _, o := range occ {
		mask := allowMask(taskKinds, o.Task)
		if mask == 0 {
			continue // task uses no favorite lists — not correlated with any favorite
		}
		// Only a terminal block is evidence of stuckness. An "undecided" entry
		// never reached an accept/reject — the shape of a discover/search
		// pipeline's output, which emits a result per favorite every run — so it
		// is not a candidate being blocked and must not read as "never accepted".
		final := o.Entry.Final
		if final != "accepted" && final != "consumed" && final != "rejected" && final != "failed" {
			continue
		}
		name, named := nameCache[o.Entry.Title]
		if !named {
			name = showName(o.Entry.Title)
			nameCache[o.Entry.Title] = name
		}
		ckey := strconv.Itoa(int(mask)) + "\x00" + name
		a, cached := assocCache[ckey]
		if !cached {
			sub, ok := maskFavs[mask]
			if !ok {
				sub = favoritesForMask(favorites, mask)
				maskFavs[mask] = sub
			}
			p := match.Probe(name, 0, sub)
			favNorm, dist, ok := nearestFavorite(p, maxDistance)
			a = favAssoc{favNorm: favNorm, dist: dist, ok: ok}
			assocCache[ckey] = a
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
		// A favorite that ever succeeds is healthy, not stuck: an accept, a silent
		// consume, or a "already downloaded / already seen" rejection (the dedup
		// and seen filters — proof it was grabbed before) all clear it.
		if final == "accepted" || final == "consumed" || indicatesAlreadyHave(o.Entry.Reason) {
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

// nearMissRatio bounds a near-miss (distance > 0) relative to title length: a
// candidate counts as "nearly" a favorite only when its edit distance is at most
// this fraction of the shorter normalized title. An absolute ceiling alone
// (maxDistance) links unrelated titles — "fbi" ↔ "vigil", "fallout" ↔ "furious"
// are all within 6 edits yet share no words. A quarter-length bound keeps real
// normalization near-misses (a stray character on a long title) while rejecting
// short, coincidental collisions; a genuine separator gap is caught separately
// by the punctuation-only signal regardless of length.
const nearMissRatio = 0.25

// nearestFavorite picks the favorite a probe result associates with: the matched
// favorite (distance 0) when the title matched, otherwise the nearest candidate
// if it is a genuine near-miss. ok is false when nothing is close enough.
func nearestFavorite(p match.ProbeResult, maxDistance int) (favNorm string, dist int, ok bool) {
	if len(p.Candidates) == 0 {
		return "", 0, false
	}
	best := p.Candidates[0] // matches sort first, then nearest by distance
	if p.Matched {
		return best.Norm, 0, true
	}
	if isNearMiss(p.InputNorm, best, maxDistance) {
		return best.Norm, best.Distance, true
	}
	return "", 0, false
}

// isNearMiss reports whether a non-matching candidate is close enough to the
// input to be treated as an intended-but-failed match rather than unrelated feed
// noise. It must be within the absolute ceiling and either differ only by
// punctuation (a normalization gap) or by at most a quarter of the shorter
// title's length.
func isNearMiss(inputNorm string, c match.CandidateResult, maxDistance int) bool {
	if c.Distance <= 0 || c.Distance > maxDistance {
		return false
	}
	if c.PunctuationOnly {
		return true
	}
	minLen := len([]rune(inputNorm))
	if n := len([]rune(c.Norm)); n < minLen {
		minLen = n
	}
	return float64(c.Distance) <= nearMissRatio*float64(minLen)
}

// indicatesAlreadyHave reports whether a rejection reason means the favorite was
// already acquired — the dedup and seen filters reject repeats with these
// phrases, which is proof the pipeline grabbed it before, not evidence it is
// stuck.
func indicatesAlreadyHave(reason string) bool {
	return strings.Contains(reason, "already downloaded") || strings.Contains(reason, "already seen")
}
