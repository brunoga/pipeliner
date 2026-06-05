package entry

import (
	"testing"
	"time"
)

func TestStateSet_HasAndNamedConstants(t *testing.T) {
	cases := []struct {
		name string
		set  StateSet
		want map[State]bool
	}{
		{"AcceptedOnly", StatesAcceptedOnly, map[State]bool{
			Accepted: true, Rejected: false, Failed: false, Undecided: false,
		}},
		{"UndecidedOnly", StatesUndecidedOnly, map[State]bool{
			Accepted: false, Rejected: false, Failed: false, Undecided: true,
		}},
		{"AcceptedUndecided", StatesAcceptedUndecided, map[State]bool{
			Accepted: true, Rejected: false, Failed: false, Undecided: true,
		}},
		{"AllButFailed", StatesAllButFailed, map[State]bool{
			Accepted: true, Rejected: true, Failed: false, Undecided: true,
		}},
		{"All", StatesAll, map[State]bool{
			Accepted: true, Rejected: true, Failed: true, Undecided: true,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for s, want := range tc.want {
				if got := tc.set.Has(s); got != want {
					t.Errorf("Has(%s): got %v, want %v", s, got, want)
				}
			}
		})
	}
}

func TestSplitByStates_PartitionsPreservingOrder(t *testing.T) {
	mk := func(state State) *Entry {
		e := New("t", "u")
		e.State = state
		return e
	}
	a, b, c, d, e := mk(Accepted), mk(Rejected), mk(Undecided), mk(Failed), mk(Accepted)
	in := []*Entry{a, b, c, d, e}

	matching, nonMatching := SplitByStates(in, StatesAcceptedUndecided)
	wantMatch := []*Entry{a, c, e}
	wantNon := []*Entry{b, d}
	if len(matching) != len(wantMatch) {
		t.Fatalf("matching length: got %d, want %d", len(matching), len(wantMatch))
	}
	for i, e := range wantMatch {
		if matching[i] != e {
			t.Errorf("matching[%d]: pointer mismatch — got %p, want %p", i, matching[i], e)
		}
	}
	if len(nonMatching) != len(wantNon) {
		t.Fatalf("nonMatching length: got %d, want %d", len(nonMatching), len(wantNon))
	}
	for i, e := range wantNon {
		if nonMatching[i] != e {
			t.Errorf("nonMatching[%d]: pointer mismatch — got %p, want %p", i, nonMatching[i], e)
		}
	}
}

func TestSplitConsumed_PartitionsPreservingOrder(t *testing.T) {
	mk := func(consumed bool) *Entry {
		e := New("t", "u")
		e.Accept()
		if consumed {
			e.Consume()
		}
		return e
	}
	a, b, c, d := mk(false), mk(true), mk(false), mk(true)
	in := []*Entry{a, b, c, d}

	nonConsumed, consumed := SplitConsumed(in)
	if len(nonConsumed) != 2 || nonConsumed[0] != a || nonConsumed[1] != c {
		t.Errorf("nonConsumed: got %v, want [a, c] preserving order", nonConsumed)
	}
	if len(consumed) != 2 || consumed[0] != b || consumed[1] != d {
		t.Errorf("consumed: got %v, want [b, d] preserving order", consumed)
	}
}

func TestSplitConsumed_EmptyAndAllInOneBucket(t *testing.T) {
	if nc, c := SplitConsumed(nil); nc != nil || c != nil {
		t.Errorf("nil input: got nc=%v c=%v, want both nil", nc, c)
	}
	e := New("t", "u")
	e.Accept()
	nc, c := SplitConsumed([]*Entry{e})
	if len(nc) != 1 || nc[0] != e || c != nil {
		t.Errorf("non-consumed only: got nc=%v c=%v", nc, c)
	}
	e.Consume()
	nc, c = SplitConsumed([]*Entry{e})
	if nc != nil || len(c) != 1 || c[0] != e {
		t.Errorf("consumed only: got nc=%v c=%v", nc, c)
	}
}

func TestSplitByStates_EmptyAndAllInOneBucket(t *testing.T) {
	if m, n := SplitByStates(nil, StatesAll); m != nil || n != nil {
		t.Errorf("nil input: got matching=%v nonMatching=%v, want both nil", m, n)
	}
	e := New("t", "u")
	e.State = Accepted
	m, n := SplitByStates([]*Entry{e}, StatesAcceptedOnly)
	if len(m) != 1 || m[0] != e {
		t.Errorf("matching: got %v, want [e]", m)
	}
	if n != nil {
		t.Errorf("nonMatching: got %v, want nil (no nonmatching entries)", n)
	}
	m2, n2 := SplitByStates([]*Entry{e}, StatesUndecidedOnly)
	if m2 != nil {
		t.Errorf("inverted: matching should be nil, got %v", m2)
	}
	if len(n2) != 1 || n2[0] != e {
		t.Errorf("inverted: nonMatching should be [e], got %v", n2)
	}
}

func TestNew(t *testing.T) {
	e := New("title", "http://example.com")
	if e.Title != "title" {
		t.Errorf("want title %q, got %q", "title", e.Title)
	}
	if e.URL != "http://example.com" {
		t.Errorf("want url %q, got %q", "http://example.com", e.URL)
	}
	if e.OriginalURL != e.URL {
		t.Errorf("OriginalURL should equal URL at creation")
	}
	if !e.IsUndecided() {
		t.Errorf("new entry should be undecided")
	}
}

func TestStateTransitions(t *testing.T) {
	t.Run("accept then reject yields rejected", func(t *testing.T) {
		e := New("t", "u")
		e.Accept()
		e.Reject("bad")
		if !e.IsRejected() {
			t.Errorf("expected rejected, got %s", e.State)
		}
	})

	t.Run("reject then accept stays rejected", func(t *testing.T) {
		e := New("t", "u")
		e.Reject("bad")
		e.Accept()
		if !e.IsRejected() {
			t.Errorf("reject should win over accept, got %s", e.State)
		}
	})

	t.Run("accept from undecided", func(t *testing.T) {
		e := New("t", "u")
		e.Accept()
		if !e.IsAccepted() {
			t.Errorf("expected accepted, got %s", e.State)
		}
	})

	t.Run("fail", func(t *testing.T) {
		e := New("t", "u")
		e.Fail("exploded")
		if !e.IsFailed() {
			t.Errorf("expected failed, got %s", e.State)
		}
		if e.FailReason != "exploded" {
			t.Errorf("want fail reason %q, got %q", "exploded", e.FailReason)
		}
	})

	t.Run("reject preserves reason", func(t *testing.T) {
		e := New("t", "u")
		e.Reject("too small")
		if e.RejectReason != "too small" {
			t.Errorf("want reject reason %q, got %q", "too small", e.RejectReason)
		}
	})

	t.Run("fail is no-op when already rejected", func(t *testing.T) {
		e := New("t", "u")
		e.Reject("rejected first")
		e.Fail("later failure")
		if !e.IsRejected() {
			t.Errorf("rejection should win over fail, got %s", e.State)
		}
		if e.FailReason != "" {
			t.Errorf("FailReason should be empty when rejected, got %q", e.FailReason)
		}
	})

	t.Run("accept with reason stores AcceptReason", func(t *testing.T) {
		e := New("t", "u")
		e.Accept("series: Breaking Bad S01E01 matched")
		if !e.IsAccepted() {
			t.Errorf("expected accepted, got %s", e.State)
		}
		if e.AcceptReason != "series: Breaking Bad S01E01 matched" {
			t.Errorf("want AcceptReason %q, got %q", "series: Breaking Bad S01E01 matched", e.AcceptReason)
		}
	})

	t.Run("accept without reason leaves AcceptReason empty", func(t *testing.T) {
		e := New("t", "u")
		e.Accept()
		if e.AcceptReason != "" {
			t.Errorf("want empty AcceptReason, got %q", e.AcceptReason)
		}
	})

	t.Run("accept is no-op when already rejected even with reason", func(t *testing.T) {
		e := New("t", "u")
		e.Reject("bad")
		e.Accept("should not win")
		if !e.IsRejected() {
			t.Errorf("rejection should win, got %s", e.State)
		}
		if e.AcceptReason != "" {
			t.Errorf("AcceptReason should not be set on rejected entry, got %q", e.AcceptReason)
		}
	})
}

func TestFieldsRoundTrip(t *testing.T) {
	e := New("t", "u")

	e.Set("str", "hello")
	if got := e.GetString("str"); got != "hello" {
		t.Errorf("GetString: want %q, got %q", "hello", got)
	}

	e.Set("num", 42)
	if got := e.GetInt("num"); got != 42 {
		t.Errorf("GetInt: want 42, got %d", got)
	}

	e.Set("flag", true)
	if got := e.GetBool("flag"); !got {
		t.Errorf("GetBool: want true, got %v", got)
	}

	now := time.Now()
	e.Set("ts", now)
	if got := e.GetTime("ts"); !got.Equal(now) {
		t.Errorf("GetTime: want %v, got %v", now, got)
	}
}

func TestFieldsZeroOnMissing(t *testing.T) {
	e := New("t", "u")
	if got := e.GetString("missing"); got != "" {
		t.Errorf("GetString missing: want empty, got %q", got)
	}
	if got := e.GetInt("missing"); got != 0 {
		t.Errorf("GetInt missing: want 0, got %d", got)
	}
	if got := e.GetBool("missing"); got {
		t.Errorf("GetBool missing: want false, got true")
	}
	if got := e.GetTime("missing"); !got.IsZero() {
		t.Errorf("GetTime missing: want zero time, got %v", got)
	}
}

func TestGetIntTypeCoercions(t *testing.T) {
	e := New("t", "u")

	e.Set("i64", int64(99))
	if got := e.GetInt("i64"); got != 99 {
		t.Errorf("int64 coercion: want 99, got %d", got)
	}

	e.Set("f64", float64(7))
	if got := e.GetInt("f64"); got != 7 {
		t.Errorf("float64 coercion: want 7, got %d", got)
	}
}

func TestFieldsWrongType(t *testing.T) {
	e := New("t", "u")
	e.Set("num", 123)
	if got := e.GetString("num"); got != "" {
		t.Errorf("GetString wrong type: want empty, got %q", got)
	}
	e.Set("str", "yes")
	if got := e.GetBool("str"); got {
		t.Errorf("GetBool wrong type: want false, got true")
	}
}

// TestGetFallsBackToStructFields covers the lookup convention shared
// with interp.EntryData: Fields wins, but the well-known struct field
// names ("title", "url", "original_url", "task") fall back to the
// matching struct field when Fields has nothing for that key. The
// concrete trigger for adding this: `require(fields=["url"])` was
// rejecting every entry because nothing populates Fields["url"], even
// though e.URL was set — config writers reasonably expected "url" to
// mean the entry URL.
func TestGetFallsBackToStructFields(t *testing.T) {
	e := New("raw title", "http://example.com/x")
	e.Task = "tvshows-discover"
	e.OriginalURL = "http://example.com/original"

	cases := []struct {
		key  string
		want string
	}{
		{"title", "raw title"},
		{"url", "http://example.com/x"},
		{"original_url", "http://example.com/original"},
		{"task", "tvshows-discover"},
	}
	for _, tc := range cases {
		v, ok := e.Get(tc.key)
		if !ok {
			t.Errorf("Get(%q) ok=false; struct fallback should always report true", tc.key)
		}
		if got, _ := v.(string); got != tc.want {
			t.Errorf("Get(%q) = %q, want %q", tc.key, got, tc.want)
		}
		if got := e.GetString(tc.key); got != tc.want {
			t.Errorf("GetString(%q) = %q, want %q (typed helpers must mirror Get)", tc.key, got, tc.want)
		}
	}
}

// TestGetFieldsWinsOverStructFallback confirms that an explicit Fields
// entry shadows the struct fallback. This matters for "title" in
// particular — metainfo plugins overwrite Fields["title"] with the
// canonical name from TVDB/TMDb, and downstream consumers must see
// the enriched value rather than the raw torrent filename in e.Title.
func TestGetFieldsWinsOverStructFallback(t *testing.T) {
	e := New("raw.title.S01E01", "http://example.com/x")
	e.Fields["title"] = "Canonical Title"

	if got := e.GetString("title"); got != "Canonical Title" {
		t.Errorf("GetString(title): got %q, want %q (Fields must win)", got, "Canonical Title")
	}
	// The struct field is unchanged; only the lookup picks Fields first.
	if e.Title != "raw.title.S01E01" {
		t.Errorf("struct Title was mutated: %q", e.Title)
	}
}

// TestGetUnknownKeyReturnsAbsent confirms the fallback only triggers
// for the four well-known struct field names. Anything else still
// returns (nil, false) so callers that distinguish "absent" from
// "zero value" — e.g. route ports, year parsing — keep working.
func TestGetUnknownKeyReturnsAbsent(t *testing.T) {
	e := New("t", "u")
	if v, ok := e.Get("anything_else"); ok || v != nil {
		t.Errorf("Get(unknown) = (%v, %v); want (nil, false)", v, ok)
	}
	// Also verify struct fields that aren't in the fallback list don't
	// leak through: State and AcceptReason are exported but not
	// addressable by name (they're pipeline machinery, not data).
	e.Accept("test")
	if v, ok := e.Get("state"); ok || v != nil {
		t.Errorf("Get(state) should not expose struct State; got (%v, %v)", v, ok)
	}
	if v, ok := e.Get("accept_reason"); ok || v != nil {
		t.Errorf("Get(accept_reason) should not expose struct AcceptReason; got (%v, %v)", v, ok)
	}
}

func TestClone(t *testing.T) {
	e := New("original", "http://orig.com")
	e.Accept("test reason")
	e.Set("key", "value")

	c := e.Clone()

	// clone has same values
	if c.Title != e.Title || c.URL != e.URL || c.State != e.State {
		t.Errorf("clone fields differ from original")
	}
	if c.AcceptReason != e.AcceptReason {
		t.Errorf("clone AcceptReason %q differs from original %q", c.AcceptReason, e.AcceptReason)
	}
	if c.GetString("key") != "value" {
		t.Errorf("clone missing field")
	}

	// mutating clone does not affect original
	c.Reject("changed")
	c.Set("key", "other")

	if !e.IsAccepted() {
		t.Errorf("original state changed after mutating clone")
	}
	if e.GetString("key") != "value" {
		t.Errorf("original field changed after mutating clone")
	}
}

func TestString(t *testing.T) {
	e := New("my title", "http://example.com")
	s := e.String()
	if s == "" {
		t.Error("String() returned empty")
	}
}

func TestSetMovieInfoWritesMovieTitle(t *testing.T) {
	e := New("Dune.2021.1080p", "http://example.com")
	e.SetMovieInfo(MovieInfo{
		VideoInfo: VideoInfo{GenericInfo: GenericInfo{Title: "Dune"}},
	})
	if got := e.GetString(FieldMovieTitle); got != "Dune" {
		t.Errorf("FieldMovieTitle: want %q, got %q", "Dune", got)
	}
	if got := e.GetString(FieldTitle); got != "Dune" {
		t.Errorf("FieldTitle: want %q, got %q", "Dune", got)
	}
}

func TestSetTitleIfEmptyWritesWhenAbsent(t *testing.T) {
	e := New("Superman.2025.2160p", "http://example.com")
	e.SetTitleIfEmpty("Superman")
	if got := e.GetString(FieldTitle); got != "Superman" {
		t.Errorf("FieldTitle: want %q, got %q", "Superman", got)
	}
}

func TestSetTitleIfEmptyPreservesExisting(t *testing.T) {
	e := New("Superman.2025.2160p", "http://example.com")
	e.Fields[FieldTitle] = "Superman"
	e.SetTitleIfEmpty("Superman 2025 2160p UHD BluRay")
	if got := e.GetString(FieldTitle); got != "Superman" {
		t.Errorf("FieldTitle: want %q (preserved), got %q", "Superman", got)
	}
}

func TestSetTitleIfEmptySkipsEmptyArg(t *testing.T) {
	e := New("Whatever", "http://example.com")
	e.SetTitleIfEmpty("")
	if _, ok := e.Fields[FieldTitle]; ok {
		t.Error("FieldTitle should not be set when arg is empty")
	}
}

func TestSetMovieInfoEmptyTitleSkipsMovieTitle(t *testing.T) {
	e := New("Some.Movie.1080p", "http://example.com")
	e.SetMovieInfo(MovieInfo{})
	if _, ok := e.Fields[FieldMovieTitle]; ok {
		t.Error("FieldMovieTitle should not be set when title is empty")
	}
}

func TestSetSeriesInfoWritesEpisodeID(t *testing.T) {
	e := New("Show.S01E01.720p", "http://example.com")
	e.SetSeriesInfo(SeriesInfo{
		VideoInfo: VideoInfo{GenericInfo: GenericInfo{Title: "Show"}},
		EpisodeID: "S01E01",
		Season:    1,
		Episode:   1,
	})
	if got := e.GetString(FieldSeriesEpisodeID); got != "S01E01" {
		t.Errorf("FieldSeriesEpisodeID: want %q, got %q", "S01E01", got)
	}
	if got := e.GetString(FieldTitle); got != "Show" {
		t.Errorf("FieldTitle: want %q, got %q", "Show", got)
	}
}

func TestLookupField(t *testing.T) {
	// A field that's definitely registered.
	if _, ok := LookupField(FieldTitle); !ok {
		t.Errorf("LookupField(%q): want found", FieldTitle)
	}
	// A name that isn't registered.
	if meta, ok := LookupField("definitely_not_a_real_field"); ok {
		t.Errorf("LookupField(unknown): want not found, got %+v", meta)
	}
}

func TestLookupFieldDeprecated(t *testing.T) {
	// Temporarily register a deprecated field. Tests that mutate KnownFields
	// must not run in parallel; LookupField itself is safe to call from many
	// goroutines because the slice is never resliced concurrently.
	orig := KnownFields
	KnownFields = append(append([]FieldMeta{}, orig...), FieldMeta{
		Name:       "_test_deprecated",
		Type:       FieldTypeString,
		Deprecated: true,
		ReplacedBy: "title",
	})
	t.Cleanup(func() { KnownFields = orig })

	meta, ok := LookupField("_test_deprecated")
	if !ok {
		t.Fatal("LookupField: deprecated field not found")
	}
	if !meta.Deprecated {
		t.Errorf("Deprecated: want true, got false")
	}
	if meta.ReplacedBy != "title" {
		t.Errorf("ReplacedBy: want %q, got %q", "title", meta.ReplacedBy)
	}
}
