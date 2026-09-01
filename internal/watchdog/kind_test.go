package watchdog

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/store"
)

func graphWith(plugins ...string) *dag.Graph {
	g := dag.New()
	var prev dag.NodeID
	for i, name := range plugins {
		id := dag.NodeID(name + "-" + string(rune('0'+i)))
		n := &dag.Node{ID: id, PluginName: name}
		if prev != "" {
			n.Upstreams = []dag.NodeID{prev}
		}
		if err := g.AddNode(n); err != nil {
			panic(err)
		}
		prev = id
	}
	return g
}

func TestKindsForGraph(t *testing.T) {
	cases := []struct {
		name    string
		plugins []string
		want    MediaKind
	}{
		{"series pipeline", []string{"rss", "series", "transmission"}, KindSeries},
		{"movies pipeline", []string{"rss", "movies", "transmission"}, KindMovies},
		{"both", []string{"rss", "series", "movies", "transmission"}, KindSeries | KindMovies},
		{"neither", []string{"rss", "require", "transmission"}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := KindsForGraph(graphWith(c.plugins...)); got != c.want {
				t.Errorf("KindsForGraph = %d, want %d", got, c.want)
			}
		})
	}
	if got := KindsForGraph(nil); got != 0 {
		t.Errorf("nil graph = %d, want 0", got)
	}
}

func TestSaveLoadTaskKinds(t *testing.T) {
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	b := db.Bucket(TaskKindsBucket)

	// Empty bucket → empty map, no error.
	got, err := LoadTaskKinds(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("empty bucket = %v, want empty", got)
	}

	want := map[string]MediaKind{"tv": KindSeries, "movies-3d": KindMovies, "mixed": KindSeries | KindMovies, "notify-only": 0}
	if err := SaveTaskKinds(b, want); err != nil {
		t.Fatal(err)
	}
	got, err = LoadTaskKinds(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(want) {
		t.Fatalf("round-trip len = %d, want %d", len(got), len(want))
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("round-trip[%q] = %d, want %d", k, got[k], v)
		}
	}

	// Rewriting replaces the whole map (no stale leftovers).
	if err := SaveTaskKinds(b, map[string]MediaKind{"tv": KindSeries}); err != nil {
		t.Fatal(err)
	}
	got, _ = LoadTaskKinds(b)
	if len(got) != 1 || got["tv"] != KindSeries {
		t.Errorf("rewrite should replace map, got %v", got)
	}
}
