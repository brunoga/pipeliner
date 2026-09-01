package watchdog

import (
	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/store"
)

// MediaKind is a bitmask of the media a favorite belongs to or a pipeline
// consumes. A favorite carries exactly one kind (the list cache it came from);
// a pipeline may consume both if its graph filters both shows and movies.
type MediaKind int

const (
	// KindSeries marks TV/series favorites and pipelines with a series filter.
	KindSeries MediaKind = 1 << iota
	// KindMovies marks movie favorites and pipelines with a movies filter.
	KindMovies
)

// allKinds is the permissive mask used when a task's kind is unknown, so a
// missing classification never hides a genuinely stuck favorite.
const allKinds = KindSeries | KindMovies

// Favorite is a favorite title tagged with the media kind of the list it was
// resolved from. The kind scopes correlation: a series favorite is only matched
// against occurrences from pipelines that actually filter series, so a TV
// favorite never picks up rejections from an unrelated movies pipeline.
type Favorite struct {
	Entry match.TitleEntry
	Kind  MediaKind
}

// MakeFavorites tags a resolved title list with a media kind. Callers union the
// result of one call per list cache (series, movies).
func MakeFavorites(list []match.TitleEntry, kind MediaKind) []Favorite {
	out := make([]Favorite, len(list))
	for i, e := range list {
		out[i] = Favorite{Entry: e, Kind: kind}
	}
	return out
}

// KindsForGraph reports which media a pipeline consumes, derived from the plugin
// nodes it contains: a series node means it filters against series lists, a
// movies node against movie lists. A pipeline with neither returns 0 — it uses
// no favorite lists, so its occurrences should not be correlated with any
// favorite.
func KindsForGraph(g *dag.Graph) MediaKind {
	if g == nil {
		return 0
	}
	var k MediaKind
	for _, n := range g.Nodes() {
		switch n.PluginName {
		case "series":
			k |= KindSeries
		case "movies":
			k |= KindMovies
		}
	}
	return k
}

// TaskKindsBucket holds the persisted task-name → MediaKind map, written when
// tasks are (re)built and read by the watchdog handler and the stuck_favorites
// plugin so both scope correlation identically without re-parsing the config.
const TaskKindsBucket = "watchdog_task_kinds"

// taskKindsKey is the single key the whole map is stored under, so rewriting it
// atomically drops entries for removed tasks (no stale leftovers).
const taskKindsKey = "map"

// SaveTaskKinds persists the task→kind map, replacing any previous map. Tasks
// that consume no favorite lists (kind 0) are still recorded so their
// occurrences are correctly excluded from correlation.
func SaveTaskKinds(b store.Bucket, kinds map[string]MediaKind) error {
	return b.Put(taskKindsKey, kinds)
}

// LoadTaskKinds reads the persisted task→kind map. A missing map yields an empty
// map (not an error): every task then reads as unknown, and Detect falls back to
// unscoped correlation — the pre-scoping behavior.
func LoadTaskKinds(b store.Bucket) (map[string]MediaKind, error) {
	var kinds map[string]MediaKind
	if _, err := b.Get(taskKindsKey, &kinds); err != nil {
		return nil, err
	}
	if kinds == nil {
		kinds = map[string]MediaKind{}
	}
	return kinds, nil
}
