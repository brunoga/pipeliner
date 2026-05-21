package expr

import (
	"sort"
	"testing"
)

func TestFieldRefs(t *testing.T) {
	cases := []struct {
		expr string
		want []string
	}{
		{"torrent_seeds >= 10", []string{"torrent_seeds"}},
		{"title == \"foo\"", []string{"title"}},
		{"enriched == true", []string{"enriched"}},
		{"torrent_seeds >= 10 and enriched == true", []string{"torrent_seeds", "enriched"}},
		{"a or b or c", []string{"a", "b", "c"}},
		{"a == b", []string{"a", "b"}},
		{"not enriched", []string{"enriched"}},
		{"true", nil},
		{"\"fixed\"", nil},
	}
	for _, tc := range cases {
		e, err := Compile(tc.expr)
		if err != nil {
			t.Errorf("Compile(%q): %v", tc.expr, err)
			continue
		}
		got := e.FieldRefs()
		sort.Strings(got)
		sort.Strings(tc.want)
		if len(got) != len(tc.want) {
			t.Errorf("FieldRefs(%q): got %v, want %v", tc.expr, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("FieldRefs(%q)[%d]: got %q, want %q", tc.expr, i, got[i], tc.want[i])
			}
		}
	}
}

func TestNarrowedCertain(t *testing.T) {
	cases := []struct {
		expr     string
		wantAny  []string // at least one of these must appear
		wantNone []string // none of these must appear
	}{
		// Simple comparison promotes the field.
		{"torrent_seeds >= 10", []string{"torrent_seeds"}, nil},
		{"title == \"foo\"", []string{"title"}, nil},
		{"enriched == true", []string{"enriched"}, nil},

		// AND: both sides promoted.
		{"torrent_seeds >= 10 and enriched == true", []string{"torrent_seeds", "enriched"}, nil},

		// OR: only fields in both sides promoted.
		{"torrent_seeds > 0 or torrent_seeds < 100", []string{"torrent_seeds"}, nil},
		{"torrent_seeds > 0 or enriched == true", nil, []string{"torrent_seeds", "enriched"}},

		// NOT: no promotion.
		{"not enriched", nil, []string{"enriched"}},

		// Literal: no fields.
		{"true", nil, nil},
	}
	for _, tc := range cases {
		e, err := Compile(tc.expr)
		if err != nil {
			t.Errorf("Compile(%q): %v", tc.expr, err)
			continue
		}
		got := e.NarrowedCertain()
		gotSet := make(map[string]bool, len(got))
		for _, f := range got {
			gotSet[f] = true
		}
		for _, f := range tc.wantAny {
			if !gotSet[f] {
				t.Errorf("NarrowedCertain(%q): want %q in result, got %v", tc.expr, f, got)
			}
		}
		for _, f := range tc.wantNone {
			if gotSet[f] {
				t.Errorf("NarrowedCertain(%q): want %q NOT in result, got %v", tc.expr, f, got)
			}
		}
	}
}
