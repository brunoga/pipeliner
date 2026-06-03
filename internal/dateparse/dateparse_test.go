package dateparse

import (
	"testing"
	"time"
)

func TestParseRFC3339(t *testing.T) {
	got, ok := Parse("2024-03-15T10:30:00Z")
	if !ok {
		t.Fatal("want parse success")
	}
	want := time.Date(2024, 3, 15, 10, 30, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestParseRSSStyle(t *testing.T) {
	cases := []string{
		"Mon, 02 Jan 2006 15:04:05 -0700",
		"Mon, 02 Jan 2006 15:04:05 MST",
		"02 Jan 2006",
		"January 2, 2006",
		"2006-01-02",
	}
	for _, s := range cases {
		if _, ok := Parse(s); !ok {
			t.Errorf("expected to parse %q", s)
		}
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	cases := []string{"", "not a date", "The Matrix", "12345"}
	for _, s := range cases {
		if _, ok := Parse(s); ok {
			t.Errorf("expected %q to fail to parse", s)
		}
	}
}

func TestFromAnyTimeTime(t *testing.T) {
	want := time.Now().UTC().Truncate(time.Second)
	got, ok := FromAny(want)
	if !ok {
		t.Fatal("want success for time.Time input")
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestFromAnyString(t *testing.T) {
	if _, ok := FromAny("2024-03-15"); !ok {
		t.Error("want success for date string")
	}
}

func TestFromAnyOther(t *testing.T) {
	if _, ok := FromAny(42); ok {
		t.Error("want failure for non-string non-time input")
	}
	if _, ok := FromAny(nil); ok {
		t.Error("want failure for nil")
	}
}
