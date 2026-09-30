package series

import (
	"slices"
	"testing"
	"time"
)

func TestNewShow(t *testing.T) {
	cases := []struct {
		name string
		year int
		want Show
	}{
		{"Brothers", 2026, Show{"brothers", 2026}},
		{"Brothers 2026", 0, Show{"brothers", 2026}},
		{"Brothers (2026)", 2026, Show{"brothers", 2026}},
		{"brothers 2026", 2025, Show{"brothers", 2025}},
		{"Ted Lasso", 0, Show{"ted lasso", 0}},
		// A trailing year that contradicts the known one is part of the title.
		{"Class of 1984", 2018, Show{"class of 1984", 2018}},
		// A name that is only a year is not stripped to nothing.
		{"1923", 2022, Show{"1923", 2022}},
	}
	for _, tc := range cases {
		if got := NewShow(tc.name, tc.year); got != tc.want {
			t.Errorf("NewShow(%q, %d) = %+v, want %+v", tc.name, tc.year, got, tc.want)
		}
	}
}

func TestShowMatches(t *testing.T) {
	cases := []struct {
		a, b Show
		want bool
	}{
		{Show{"brothers", 2026}, Show{"brothers", 0}, true},
		{Show{"brothers", 2026}, Show{"brothers", 2025}, true},
		{Show{"doctor who", 2005}, Show{"doctor who", 1963}, false},
		{Show{"brothers", 2026}, Show{"band of brothers", 2026}, false},
		{Show{"", 0}, Show{"", 0}, false},
	}
	for _, tc := range cases {
		if got := tc.a.Matches(tc.b); got != tc.want {
			t.Errorf("%+v.Matches(%+v) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func markEp(t *testing.T, tr *Tracker, show, epID string, at time.Time) {
	t.Helper()
	if err := tr.Mark(Record{SeriesName: show, EpisodeID: epID, DownloadedAt: at}); err != nil {
		t.Fatal(err)
	}
}

func TestResolveNewShowComposesNameAndYear(t *testing.T) {
	tr := openTracker(t)
	if got := tr.Resolve("Brothers", 2026); got.Key != "brothers 2026" || !slices.Equal(got.Keys, []string{"brothers 2026"}) {
		t.Errorf("with year: got %+v", got)
	}
	if got := tr.Resolve("Last Seen", 0); got.Key != "last seen" {
		t.Errorf("without year: got %+v", got)
	}
}

func TestResolveKeepsExistingKey(t *testing.T) {
	tr := openTracker(t)
	// Shows tracked before name+year keys keep their key: no migration.
	markEp(t, tr, "ted lasso", "S04E07", time.Now())
	got := tr.Resolve("Ted Lasso", 2020)
	if got.Key != "ted lasso" || !slices.Equal(got.Keys, []string{"ted lasso"}) {
		t.Errorf("got %+v, want the existing key", got)
	}
}

// The real splits found in production: one show tracked under both spellings.
func TestResolveFindsEverySpelling(t *testing.T) {
	tr := openTracker(t)
	now := time.Now()
	for _, show := range []string{"last seen", "the hawk", "off campus", "i will find you", "brothers"} {
		markEp(t, tr, show, "S01E01", now)
		markEp(t, tr, show+" 2026", "S01E01", now)
	}
	markEp(t, tr, "brothers 2026", "S01E02", now)
	markEp(t, tr, "band of brothers", "S01E01", now)

	for _, tc := range []struct {
		name string
		year int
	}{
		{"Brothers", 0}, {"Brothers", 2026}, {"Brothers 2026", 0}, {"brothers (2026)", 2026},
	} {
		got := tr.Resolve(tc.name, tc.year)
		if got.Key != "brothers 2026" || !slices.Equal(got.Keys, []string{"brothers 2026", "brothers"}) {
			t.Errorf("Resolve(%q, %d) = %+v", tc.name, tc.year, got)
		}
		if _, ok := tr.GetAny(got.Keys, "S01E02"); !ok {
			t.Errorf("Resolve(%q, %d): S01E02 not found across spellings", tc.name, tc.year)
		}
	}
	for _, show := range []string{"Last Seen", "The Hawk", "Off Campus", "I Will Find You"} {
		if got := tr.Resolve(show, 0); len(got.Keys) != 2 {
			t.Errorf("Resolve(%q) = %+v, want both spellings", show, got)
		}
	}
}

func TestResolveKeepsContradictingYearsApart(t *testing.T) {
	tr := openTracker(t)
	markEp(t, tr, "doctor who 1963", "S01E01", time.Now())
	got := tr.Resolve("Doctor Who", 2005)
	if got.Key != "doctor who 2005" || len(got.Keys) != 1 {
		t.Errorf("got %+v, want the 1963 show kept apart", got)
	}
}

func TestGetAnyPrefersLatest(t *testing.T) {
	tr := openTracker(t)
	old := time.Now().Add(-time.Hour)
	markEp(t, tr, "brothers 2026", "S01E01", old)
	markEp(t, tr, "brothers", "S01E01", time.Now())
	rec, ok := tr.GetAny([]string{"brothers 2026", "brothers"}, "S01E01")
	if !ok || rec.SeriesName != "brothers" {
		t.Errorf("got %+v, want the most recent record", rec)
	}
}

func TestHighestEpisodeAcrossKeys(t *testing.T) {
	tr := openTracker(t)
	now := time.Now()
	markEp(t, tr, "brothers 2026", "S01E02", now)
	markEp(t, tr, "brothers", "S02E01", now)
	markEp(t, tr, "brothers extra", "S09E01", now)
	rec, ok := tr.HighestEpisode("brothers 2026", "brothers")
	if !ok || rec.EpisodeID != "S02E01" {
		t.Errorf("got %+v, want S02E01", rec)
	}
	if rec, ok := tr.Latest("brothers 2026", "brothers"); !ok || rec.SeriesName == "brothers extra" {
		t.Errorf("Latest leaked another show: %+v", rec)
	}
}
