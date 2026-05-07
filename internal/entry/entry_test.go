package entry

import (
	"testing"
	"time"
)

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

func TestClone(t *testing.T) {
	e := New("original", "http://orig.com")
	e.Accept()
	e.Set("key", "value")

	c := e.Clone()

	// clone has same values
	if c.Title != e.Title || c.URL != e.URL || c.State != e.State {
		t.Errorf("clone fields differ from original")
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
