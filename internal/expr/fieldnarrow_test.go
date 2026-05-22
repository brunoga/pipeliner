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

func TestNarrowedCertainAbsenceCheck(t *testing.T) {
	// field == "" means the field is absent — it must NOT be promoted to certain.
	e, err := Compile(`field == ""`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got := e.NarrowedCertain()
	for _, f := range got {
		if f == "field" {
			t.Error(`NarrowedCertain("field == \"\"") must not promote "field" (absence check, field is absent)`)
		}
	}

	// field == 0 (numeric absence) must also not promote.
	e2, err := Compile(`count == 0`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got2 := e2.NarrowedCertain()
	for _, f := range got2 {
		if f == "count" {
			t.Error(`NarrowedCertain("count == 0") must not promote "count" (absence check)`)
		}
	}

	// field == "something" should still promote (non-empty value).
	e3, err := Compile(`status == "active"`)
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	got3 := e3.NarrowedCertain()
	found := false
	for _, f := range got3 {
		if f == "status" {
			found = true
		}
	}
	if !found {
		t.Error(`NarrowedCertain("status == \"active\"") should promote "status"`)
	}
}

func TestAbsenceRemovedFields(t *testing.T) {
	cases := []struct {
		expr     string
		wantAny  []string // at least one of these must appear
		wantNone []string // none of these must appear
	}{
		// Single absence op removes the field.
		{`field == ""`, []string{"field"}, nil},
		{`count == 0`, []string{"count"}, nil},
		// AND: all absence-op fields removed (union).
		{`a == "" and b == ""`, []string{"a", "b"}, nil},
		// OR of different fields: intersection is empty — nothing removed.
		{`a == "" or b == ""`, nil, []string{"a", "b"}},
		// OR of the same field: appears in every clause — field removed.
		{`a == "" or a == ""`, []string{"a"}, nil},
		// Presence op: AbsenceRemovedFields returns nothing.
		{`a != ""`, nil, []string{"a"}},
		// NOT: no inference.
		{`not (a == "")`, nil, []string{"a"}},
		// Literal: no fields.
		{`true`, nil, nil},
	}
	for _, tc := range cases {
		e, err := Compile(tc.expr)
		if err != nil {
			t.Errorf("Compile(%q): %v", tc.expr, err)
			continue
		}
		got := e.AbsenceRemovedFields()
		gotSet := make(map[string]bool, len(got))
		for _, f := range got {
			gotSet[f] = true
		}
		for _, f := range tc.wantAny {
			if !gotSet[f] {
				t.Errorf("AbsenceRemovedFields(%q): want %q in result, got %v", tc.expr, f, got)
			}
		}
		for _, f := range tc.wantNone {
			if gotSet[f] {
				t.Errorf("AbsenceRemovedFields(%q): want %q NOT in result, got %v", tc.expr, f, got)
			}
		}
	}
}
