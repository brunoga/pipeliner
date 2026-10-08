package mediaserver

import (
	"context"
	"strings"
	"testing"
)

type idStub struct{ items []Item }

func (s *idStub) ListItems(context.Context) ([]Item, error) { return s.items, nil }
func (s *idStub) Refresh(context.Context) error             { return nil }

func lower(s string) string { return strings.ToLower(s) }

func buildIdx(t *testing.T, items []Item) *OwnedEpisodes {
	t.Helper()
	o, err := BuildOwnedEpisodes(context.Background(), &idStub{items: items}, Sections{}, lower)
	if err != nil {
		t.Fatalf("BuildOwnedEpisodes: %v", err)
	}
	return o
}

// TestResolveFindsAShowTheNameMisses is the case this indexing exists for.
// The library calls the show "Brothers (2026)" because its agent disambiguated
// a remake; TheTVDB calls it "Brothers". The names normalize apart, so a name
// lookup finds nothing and the show silently contributes no gaps — while the
// id both sides publish matches exactly.
func TestResolveFindsAShowTheNameMisses(t *testing.T) {
	o := buildIdx(t, []Item{
		{Type: "episode", Show: "Brothers (2026)", Season: 1, Episode: 1, ShowTVDBID: "448114"},
		{Type: "episode", Show: "Brothers (2026)", Season: 1, Episode: 2, ShowTVDBID: "448114"},
	})

	if _, ok := o.FirstSeasonWithAny("brothers", false); ok {
		t.Fatal("precondition: the bare name should not match the library key")
	}

	key := o.Resolve("448114", "brothers")
	season, ok := o.FirstSeasonWithAny(key, false)
	if !ok || season != 1 {
		t.Errorf("FirstSeasonWithAny(%q) = %d, %v; want 1, true", key, season, ok)
	}
	if !o.Has(key, 1, 2) {
		t.Error("S01E02 should be owned under the resolved key")
	}
	if o.Has(key, 1, 3) {
		t.Error("S01E03 is not owned and must not resolve as owned")
	}
}

func TestResolveFallsBackToTheName(t *testing.T) {
	o := buildIdx(t, []Item{
		{Type: "episode", Show: "Severance", Season: 1, Episode: 1, ShowTVDBID: "371980"},
		{Type: "episode", Show: "Andor", Season: 1, Episode: 1}, // server published no id
	})
	for _, tc := range []struct {
		name, id, want string
	}{
		{"id matches", "371980", "severance"},
		{"no id given", "", "severance"},
		{"id is unknown to the library", "999999", "severance"},
		{"show carries no id at all", "", "andor"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookup := tc.want
			if got := o.Resolve(tc.id, lookup); got != tc.want {
				t.Errorf("Resolve(%q, %q) = %q, want %q", tc.id, lookup, got, tc.want)
			}
		})
	}
	// An id nobody published must not invent a match.
	if got := o.Resolve("999999", "nothing here"); got != "nothing here" {
		t.Errorf("unknown id should fall through to the name, got %q", got)
	}
}

// TestResolveIsStableAcrossListingOrder: two library shows claiming one id
// would otherwise make the mapping depend on the order the server listed them.
func TestResolveIsStableAcrossListingOrder(t *testing.T) {
	a := []Item{
		{Type: "episode", Show: "First", Season: 1, Episode: 1, ShowTVDBID: "1"},
		{Type: "episode", Show: "Second", Season: 1, Episode: 1, ShowTVDBID: "1"},
	}
	b := []Item{a[0]}
	if got, want := buildIdx(t, a).Resolve("1", "x"), buildIdx(t, b).Resolve("1", "x"); got != want {
		t.Errorf("first writer should win: %q vs %q", got, want)
	}
}

func TestTVDBIDRoundTrips(t *testing.T) {
	o := buildIdx(t, []Item{
		{Type: "episode", Show: "Severance", Season: 1, Episode: 1, ShowTVDBID: "371980"},
		{Type: "episode", Show: "Andor", Season: 1, Episode: 1},
	})
	if got := o.TVDBID("severance"); got != "371980" {
		t.Errorf("TVDBID = %q, want 371980", got)
	}
	if got := o.TVDBID("andor"); got != "" {
		t.Errorf("a show with no published id should report none, got %q", got)
	}
}

func TestResolveOnNilIndex(t *testing.T) {
	var o *OwnedEpisodes
	if got := o.Resolve("123", "name"); got != "name" {
		t.Errorf("nil index should echo the name, got %q", got)
	}
	if got := o.TVDBID("name"); got != "" {
		t.Errorf("nil index should report no id, got %q", got)
	}
}
