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

// --- precedence and the other namespaces ---

// TestProviderIDPrecedence: an id pipeliner resolved itself, or took from a
// list provider's metadata, beats the one an indexer published alongside the
// release — that last one is a claim about a file, not an established identity.
func TestProviderIDPrecedence(t *testing.T) {
	e := New("Breaking.Bad.S01E01", "http://x/1")
	e.Set("jackett_tvdb_id", "999999")
	if got := TVDBID(e); got != "999999" {
		t.Errorf("indexer id alone: TVDBID = %q, want 999999", got)
	}
	e.Set("trakt_tvdb_id", 81189)
	if got := TVDBID(e); got != "81189" {
		t.Errorf("trakt id present: TVDBID = %q, want 81189", got)
	}
	e.Set("tvdb_id", "81190")
	if got := TVDBID(e); got != "81190" {
		t.Errorf("own id present: TVDBID = %q, want 81190", got)
	}
}

// TestProviderIDSkipsAnEmptyField: tvdb_favorites declares tvdb_id in Produces
// and shipped it blank for a release (see 1.57.1) — a declared-but-empty field
// must not hide an id a lower-precedence field does carry.
func TestProviderIDSkipsAnEmptyField(t *testing.T) {
	e := New("Breaking.Bad.S01E01", "http://x/1")
	e.Set("tvdb_id", "")
	e.Set("jackett_tvdb_id", "81189")
	if got := TVDBID(e); got != "81189" {
		t.Errorf("TVDBID = %q, want the indexer's 81189", got)
	}
}

func TestTMDBID(t *testing.T) {
	e := New("Dune.2021.2160p", "http://x/1")
	if got := TMDBID(e); got != "" {
		t.Errorf("no id: TMDBID = %q, want empty", got)
	}
	e.Set("jackett_tmdb_id", "438631")
	if got := TMDBID(e); got != "438631" {
		t.Errorf("TMDBID = %q, want 438631", got)
	}
	e.Set("tmdb_id", 841)
	if got := TMDBID(e); got != "841" {
		t.Errorf("TMDBID = %q, want 841 (our own id wins)", got)
	}
}

func TestIMDBID(t *testing.T) {
	e := New("Inception.2010.1080p", "http://x/1")
	e.Set("jackett_imdb_id", "TT1375666")
	// Case-folded: an id is an id whichever way the indexer shouts it.
	if got := IMDBID(e); got != "tt1375666" {
		t.Errorf("IMDBID = %q, want tt1375666", got)
	}
	e.Set(FieldVideoImdbID, "tt1375667")
	if got := IMDBID(e); got != "tt1375667" {
		t.Errorf("IMDBID = %q, want tt1375667", got)
	}
}

// TestIMDBIDPlaceholderReadsAsAbsent: "tt0000000" is IMDb's own "unknown",
// and two unknowns are not the same film.
func TestIMDBIDPlaceholderReadsAsAbsent(t *testing.T) {
	for _, v := range []string{"tt0000000", "tt0", "", "   "} {
		e := New("x", "http://x/1")
		e.Set(FieldVideoImdbID, v)
		if got := IMDBID(e); got != "" {
			t.Errorf("IMDBID(%q) = %q, want empty", v, got)
		}
	}
}

// TestIDsOnANilEntry: the helpers are called from key functions that run over
// slices built elsewhere; a nil must not panic.
func TestIDsOnANilEntry(t *testing.T) {
	if TVDBID(nil) != "" || TMDBID(nil) != "" || IMDBID(nil) != "" {
		t.Error("a nil entry must publish no ids")
	}
}

// TestNumericIDRejectsAFraction: a float that is not a whole number is not an
// id — reading it would invent one.
func TestNumericIDRejectsAFraction(t *testing.T) {
	e := New("x", "http://x/1")
	e.Set("tmdb_id", 841.5)
	if got := TMDBID(e); got != "" {
		t.Errorf("TMDBID = %q, want empty for a fractional value", got)
	}
}
