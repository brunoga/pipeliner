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
	if len(favorites) == 0 {
		return nil
	}

	byFav := map[string]*favAgg{}
	for _, o := range occ {
		p := match.Probe(showName(o.Entry.Title), 0, favorites)
		favNorm, dist, ok := nearestFavorite(p, maxDistance)
		if !ok {
			continue
		}
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
