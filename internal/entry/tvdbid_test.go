package entry

import "testing"

// TestTVDBIDReadsEveryShapeTheFieldArrivesIn: TheTVDB-backed plugins set the
// field as a string, Trakt's JSON brings it in as a number, and a cached entry
// round-tripped through the store brings it back as a float64. All three name
// the same show, so all three must compare equal.
func TestTVDBIDReadsEveryShapeTheFieldArrivesIn(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
		want string
	}{
		{"string", "409591", "409591"},
		{"int", 409591, "409591"},
		{"int64", int64(409591), "409591"},
		{"json float", float64(409591), "409591"},
		{"padded string", " 409591 ", "409591"},
		{"leading zero", "0409591", "409591"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New("Tomb Raider", "pipeliner://series/tomb%20raider")
			e.Set("tvdb_id", tc.val)
			if got := TVDBID(e); got != tc.want {
				t.Errorf("TVDBID = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestTVDBIDTreatsAnEmptyIDAsAbsent: an id that identifies nothing must read
// as "", or two shows of unknown identity would match each other.
func TestTVDBIDTreatsAnEmptyIDAsAbsent(t *testing.T) {
	for _, tc := range []struct {
		name string
		val  any
	}{
		{"zero", 0},
		{"zero string", "0"},
		{"negative", -1},
		{"empty string", ""},
		{"blank string", "   "},
		{"wrong type", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := New("Show", "http://x/1")
			e.Set("tvdb_id", tc.val)
			if got := TVDBID(e); got != "" {
				t.Errorf("TVDBID = %q, want empty", got)
			}
		})
	}
	if got := TVDBID(New("Show", "http://x/1")); got != "" {
		t.Errorf("absent field: TVDBID = %q, want empty", got)
	}
}

// TestTVDBIDKeepsANonNumericID: nothing in the wild publishes one, but
// discarding an id we cannot parse would be worse than passing it through —
// two equal strings still name the same show.
func TestTVDBIDKeepsANonNumericID(t *testing.T) {
	e := New("Show", "http://x/1")
	e.Set("tvdb_id", "series-409591")
	if got := TVDBID(e); got != "series-409591" {
		t.Errorf("TVDBID = %q, want series-409591", got)
	}
}
