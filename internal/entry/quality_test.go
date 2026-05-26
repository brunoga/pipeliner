package entry

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/quality"
)

func TestQualityRoundTrip(t *testing.T) {
	e := New("Title", "http://x.com/a")
	want := quality.Quality{
		Resolution: 5,
		Source:     5,
		Codec:      3,
	}
	e.SetQuality(want)

	got, ok := e.Quality()
	if !ok {
		t.Fatal("Quality() returned ok=false after SetQuality")
	}
	if got != want {
		t.Errorf("Quality() = %+v, want %+v", got, want)
	}
}

func TestQualityUnset(t *testing.T) {
	e := New("Title", "http://x.com/a")
	if _, ok := e.Quality(); ok {
		t.Error("Quality() should return ok=false when no struct was stored")
	}
}

func TestQualityWrongTypeReturnsFalse(t *testing.T) {
	// If something else writes a non-Quality value to the same key,
	// Quality() must report ok=false rather than panicking.
	e := New("Title", "http://x.com/a")
	e.Fields[FieldQuality] = "not a Quality struct"
	if _, ok := e.Quality(); ok {
		t.Error("Quality() should return ok=false when value is the wrong type")
	}
}
