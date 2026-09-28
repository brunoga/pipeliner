package web

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeLogFile writes the given lines (one per element) joined with '\n'
// terminators, mirroring how the rotating writer stores records.
func writeLogFile(t *testing.T, path string, lines []string) {
	t.Helper()
	var buf []byte
	for _, l := range lines {
		buf = append(buf, l...)
		buf = append(buf, '\n')
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// texts strips a slice of LineWithPos down to its text content for easy
// comparison.
func texts(lwps []LineWithPos) []string {
	out := make([]string, len(lwps))
	for i, l := range lwps {
		out[i] = l.Text
	}
	return out
}

func TestLogFiles_TailReturnsNewestNLinesOldestFirst(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base, []string{"a", "b", "c", "d", "e"})
	lf := &LogFiles{Path: base, MaxArchives: 0}

	got, older, exhausted, err := lf.Tail(3, Filter{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := []string{"c", "d", "e"}; !equalStrings(texts(got), want) {
		t.Errorf("texts = %v, want %v", texts(got), want)
	}
	if exhausted {
		t.Error("want exhausted=false (older lines exist)")
	}
	// olderCursor equals the oldest emitted line's position; the next
	// Before(olderCursor) scan returns lines strictly older than it.
	if older != got[0].Pos {
		t.Errorf("olderCursor = %v, want oldest emitted = %v", older, got[0].Pos)
	}
}

func TestLogFiles_TailExhaustedWhenAllLinesFit(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base, []string{"a", "b", "c"})
	lf := &LogFiles{Path: base, MaxArchives: 0}

	got, _, exhausted, err := lf.Tail(10, Filter{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if want := []string{"a", "b", "c"}; !equalStrings(texts(got), want) {
		t.Errorf("texts = %v, want %v", texts(got), want)
	}
	if !exhausted {
		t.Error("want exhausted=true")
	}
}

func TestLogFiles_BeforePagesOlder(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base, []string{"a", "b", "c", "d", "e"})
	lf := &LogFiles{Path: base, MaxArchives: 0}

	// First page: newest 2.
	page1, older, _, _ := lf.Tail(2, Filter{})
	if want := []string{"d", "e"}; !equalStrings(texts(page1), want) {
		t.Fatalf("page1 = %v, want %v", texts(page1), want)
	}

	// Second page: 2 older.
	page2, older2, _, _ := lf.Before(older, 2, Filter{})
	if want := []string{"b", "c"}; !equalStrings(texts(page2), want) {
		t.Fatalf("page2 = %v, want %v", texts(page2), want)
	}

	// Third page: 1 more older = ['a'], exhausted.
	page3, _, exhausted, _ := lf.Before(older2, 2, Filter{})
	if want := []string{"a"}; !equalStrings(texts(page3), want) {
		t.Fatalf("page3 = %v, want %v", texts(page3), want)
	}
	if !exhausted {
		t.Error("want exhausted=true at file start")
	}
}

func TestLogFiles_BeforeAcrossArchives(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	// Rotation order: .2 oldest, .1 middle, base newest.
	writeLogFile(t, base+".2", []string{"oldest-a", "oldest-b"})
	writeLogFile(t, base+".1", []string{"mid-c", "mid-d"})
	writeLogFile(t, base, []string{"new-e", "new-f"})
	lf := &LogFiles{Path: base, MaxArchives: 5}

	// One big page should cross all three files in chronological order.
	got, _, exhausted, err := lf.Tail(20, Filter{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	want := []string{"oldest-a", "oldest-b", "mid-c", "mid-d", "new-e", "new-f"}
	if !equalStrings(texts(got), want) {
		t.Errorf("texts = %v, want %v", texts(got), want)
	}
	if !exhausted {
		t.Error("want exhausted=true")
	}
	// Positions move from higher fileIdx to lower as we walk newer.
	for i := 1; i < len(got); i++ {
		prev := got[i-1].Pos
		cur := got[i].Pos
		// older(prev, cur): prev has higher FileIdx OR same fileIdx and smaller ByteEnd.
		if prev.FileIdx < cur.FileIdx {
			t.Errorf("pos[%d]=%v not older than pos[%d]=%v (file index order wrong)", i-1, prev, i, cur)
		}
		if prev.FileIdx == cur.FileIdx && prev.ByteEnd >= cur.ByteEnd {
			t.Errorf("pos[%d]=%v not older than pos[%d]=%v (byte order wrong)", i-1, prev, i, cur)
		}
	}
}

func TestLogFiles_BeforePagesOlderAcrossArchiveBoundary(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base+".1", []string{"old1", "old2", "old3"})
	writeLogFile(t, base, []string{"new1", "new2"})
	lf := &LogFiles{Path: base, MaxArchives: 5}

	// Newest 3 lines = [old3, new1, new2]. Tail returns oldest-first.
	page1, older, _, _ := lf.Tail(3, Filter{})
	if want := []string{"old3", "new1", "new2"}; !equalStrings(texts(page1), want) {
		t.Fatalf("page1 = %v, want %v", texts(page1), want)
	}
	if page1[0].Pos.FileIdx != 1 {
		t.Errorf("old3 should be in archive 1, got %v", page1[0].Pos)
	}
	// Page back: 2 older = [old1, old2].
	page2, _, exhausted, _ := lf.Before(older, 5, Filter{})
	if want := []string{"old1", "old2"}; !equalStrings(texts(page2), want) {
		t.Fatalf("page2 = %v, want %v", texts(page2), want)
	}
	if !exhausted {
		t.Error("want exhausted=true")
	}
}

func TestLogFiles_FilterAndMatchAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base+".1", []string{
		"INFO  pipeline=tv started",
		"DEBUG  random chatter",
		"INFO  pipeline=tv done",
	})
	writeLogFile(t, base, []string{
		"INFO  pipeline=movies started",
		"WARN  generic warning",
		"INFO  pipeline=tv reload",
	})
	lf := &LogFiles{Path: base, MaxArchives: 5}

	// All lines containing both "info" and "tv". Case-insensitive, AND.
	got, _, exhausted, err := lf.Tail(10, ParseFilter("INFO tv"))
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	want := []string{
		"INFO  pipeline=tv started",
		"INFO  pipeline=tv done",
		"INFO  pipeline=tv reload",
	}
	if !equalStrings(texts(got), want) {
		t.Errorf("texts = %v, want %v", texts(got), want)
	}
	if !exhausted {
		t.Error("want exhausted=true")
	}
}

func TestLogFiles_FilterPaginatesCorrectlyWithSparseMatches(t *testing.T) {
	// Build a file where only every 5th line matches. We want to verify
	// Before/Tail keep scanning past non-matches to fill the page rather
	// than returning early.
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	lines := make([]string, 200)
	for i := range lines {
		if i%5 == 0 {
			lines[i] = fmt.Sprintf("MATCH line %03d", i)
		} else {
			lines[i] = fmt.Sprintf("skip line %03d", i)
		}
	}
	writeLogFile(t, base, lines)
	lf := &LogFiles{Path: base, MaxArchives: 0}

	got, _, _, err := lf.Tail(40, ParseFilter("MATCH"))
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 40 {
		t.Fatalf("len = %d, want 40 (sparse-match scan must continue past non-matches)", len(got))
	}
	// First match returned should be 200 - 40*5 = 0: i = 0, 5, 10, ... = first 40 from the *tail*.
	// There are 40 matches (i=0,5,...,195). Tail of 40 hits all of them.
	if got[0].Text != "MATCH line 000" {
		t.Errorf("oldest = %q, want %q", got[0].Text, "MATCH line 000")
	}
	if got[len(got)-1].Text != "MATCH line 195" {
		t.Errorf("newest = %q, want %q", got[len(got)-1].Text, "MATCH line 195")
	}
}

func TestLogFiles_AfterReturnsNewerLines(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base, []string{"a", "b", "c", "d", "e"})
	lf := &LogFiles{Path: base, MaxArchives: 0}

	// Tail first 2 = [d, e]. olderCursor points before d. Use the
	// FIRST line's start position as the After cursor to get [b, c, d, e].
	// Use Tail to get a known position: get all 5 lines, then ask
	// After(pos of 'b').
	all, _, _, _ := lf.Tail(5, Filter{})
	posOfB := all[1].Pos
	got, _, atTail, err := lf.After(posOfB, 10, Filter{})
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	if want := []string{"c", "d", "e"}; !equalStrings(texts(got), want) {
		t.Errorf("texts = %v, want %v", texts(got), want)
	}
	if !atTail {
		t.Error("want atTail=true after consuming base file to EOF")
	}
}

func TestLogFiles_AfterBridgesAcrossArchives(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	writeLogFile(t, base+".1", []string{"old1", "old2", "old3"})
	writeLogFile(t, base, []string{"new1", "new2"})
	lf := &LogFiles{Path: base, MaxArchives: 5}

	all, _, _, _ := lf.Tail(5, Filter{})
	// all = [old1, old2, old3, new1, new2]
	posOfOld1 := all[0].Pos
	got, _, atTail, err := lf.After(posOfOld1, 10, Filter{})
	if err != nil {
		t.Fatalf("After: %v", err)
	}
	want := []string{"old2", "old3", "new1", "new2"}
	if !equalStrings(texts(got), want) {
		t.Errorf("texts = %v, want %v", texts(got), want)
	}
	if !atTail {
		t.Error("want atTail=true")
	}
}

func TestLogFiles_LinePosRoundtrip(t *testing.T) {
	p := LinePos{FileIdx: 2, ByteEnd: 12345}
	s := p.String()
	if s != "2:12345" {
		t.Errorf("string = %q, want \"2:12345\"", s)
	}
	got, err := ParseLinePos(s)
	if err != nil {
		t.Fatalf("ParseLinePos: %v", err)
	}
	if got != p {
		t.Errorf("got %v, want %v", got, p)
	}
}

func TestLogFiles_ParseLinePosEmpty(t *testing.T) {
	got, err := ParseLinePos("")
	if err != nil {
		t.Fatalf("ParseLinePos(\"\"): %v", err)
	}
	if got != (LinePos{}) {
		t.Errorf("got %v, want zero", got)
	}
}

func TestLogFiles_ParseLinePosInvalid(t *testing.T) {
	for _, in := range []string{"nope", "-1:0", "0:-1", "0:abc", ":"} {
		if _, err := ParseLinePos(in); err == nil {
			t.Errorf("ParseLinePos(%q) should have errored", in)
		}
	}
}

func TestLogFiles_TailHandlesEmptyFile(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	if err := os.WriteFile(base, nil, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	lf := &LogFiles{Path: base, MaxArchives: 0}

	got, _, exhausted, err := lf.Tail(10, Filter{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
	if !exhausted {
		t.Error("want exhausted=true for empty file")
	}
}

func TestLogFiles_TailHandlesMissingFile(t *testing.T) {
	dir := t.TempDir()
	lf := &LogFiles{Path: filepath.Join(dir, "pipeliner.log"), MaxArchives: 0}

	got, _, exhausted, err := lf.Tail(10, Filter{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len = %d, want 0", len(got))
	}
	if !exhausted {
		t.Error("want exhausted=true for missing file")
	}
}

// TestLogFiles_BackwardScanHandlesLongLine exercises the head-straddle
// re-read path: a single line longer than the 8 KiB chunk size must still
// emit as a single line at the correct position. Without straddle
// handling the scanner would either drop it or splice it incorrectly.
func TestLogFiles_BackwardScanHandlesLongLine(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	long := strings.Repeat("x", 20_000) // ~2.5 chunks
	writeLogFile(t, base, []string{"head", long, "tail"})
	lf := &LogFiles{Path: base, MaxArchives: 0}

	got, _, exhausted, err := lf.Tail(10, Filter{})
	if err != nil {
		t.Fatalf("Tail: %v", err)
	}
	want := []string{"head", long, "tail"}
	if !equalStrings(texts(got), want) {
		// Avoid printing the 20k-byte line; compare lengths instead.
		t.Errorf("texts mismatch (lens got=%v want=%v)",
			mapLen(texts(got)), mapLen(want))
	}
	if !exhausted {
		t.Error("want exhausted=true (file fully consumed)")
	}
	// Positions must be strictly increasing in chronological order so a
	// downstream paging call lands inside the correct window.
	for i := 1; i < len(got); i++ {
		if !(got[i-1].Pos.ByteEnd < got[i].Pos.ByteEnd) {
			t.Errorf("Pos[%d]=%d not before Pos[%d]=%d", i-1, got[i-1].Pos.ByteEnd, i, got[i].Pos.ByteEnd)
		}
	}
}

// TestLogFiles_BackwardScanPagesLongLine verifies that a single paging
// call returning the long line (when it's the newest one) still emits a
// usable older_cursor that resumes correctly.
func TestLogFiles_BackwardScanPagesLongLine(t *testing.T) {
	dir := t.TempDir()
	base := filepath.Join(dir, "pipeliner.log")
	long := strings.Repeat("y", 15_000)
	writeLogFile(t, base, []string{"old1", "old2", long})
	lf := &LogFiles{Path: base, MaxArchives: 0}

	// Page 1: just the long line.
	page1, older, _, _ := lf.Tail(1, Filter{})
	if len(page1) != 1 || page1[0].Text != long {
		t.Fatalf("page1 = %d lines (texts truncated)", len(page1))
	}
	// Page 2: the two prior short lines.
	page2, _, exhausted, _ := lf.Before(older, 5, Filter{})
	if want := []string{"old1", "old2"}; !equalStrings(texts(page2), want) {
		t.Errorf("page2 = %v, want %v", texts(page2), want)
	}
	if !exhausted {
		t.Error("want exhausted=true")
	}
}

func mapLen(s []string) []int {
	out := make([]int, len(s))
	for i, x := range s {
		out[i] = len(x)
	}
	return out
}

// --- structured log filters ---

const (
	lineMovies   = `2026-09-25 10:10:01.123 INFO  entry accepted task=movies run_id=abc node=movies_5 plugin=movies title="Dune"`
	lineMovies3D = `2026-09-25 10:20:02.456 INFO  entry rejected task=movies-3d run_id=def node=movies_24 plugin=movies title="Avatar"`
	lineOtherDay = `2026-09-26 03:10:00.789 INFO  entry accepted task=movies run_id=ghi node=dedup_6 plugin=dedup title="Heat"`
	lineTrailing = `2026-09-25 11:00:00.000 INFO  pipeline done task=movies`
)

// The collision this exists to prevent: as a plain substring "task=movies"
// also selects task=movies-3d and task=movies-ondemand.
func TestFilterTaskMatchesWholeValueOnly(t *testing.T) {
	f := ParseFilter("task:movies")
	if !f.match(lineMovies) {
		t.Error("task:movies should match task=movies")
	}
	if f.match(lineMovies3D) {
		t.Error("task:movies must NOT match task=movies-3d")
	}
	// A value at end-of-line has no trailing space to terminate it.
	if !f.match(lineTrailing) {
		t.Error("task:movies should match a task= value at end of line")
	}
	// And the reverse direction: the longer name must not be found by itself.
	if ParseFilter("task:movies-3d").match(lineMovies) {
		t.Error("task:movies-3d must not match task=movies")
	}
}

func TestFilterNodeMatchesWholeValueOnly(t *testing.T) {
	if !ParseFilter("node:movies_5").match(lineMovies) {
		t.Error("node:movies_5 should match")
	}
	// movies_5 is a prefix of movies_53; neither may match the other.
	longer := strings.Replace(lineMovies, "node=movies_5 ", "node=movies_53 ", 1)
	if ParseFilter("node:movies_5").match(longer) {
		t.Error("node:movies_5 must NOT match node=movies_53")
	}
}

func TestFilterDateIsALeadingPrefix(t *testing.T) {
	f := ParseFilter("date:2026-09-25")
	if !f.match(lineMovies) {
		t.Error("date:2026-09-25 should match a line from that day")
	}
	if f.match(lineOtherDay) {
		t.Error("date:2026-09-25 must not match 2026-09-26")
	}
	// A date appearing later in the line is not the line's day.
	if f.match(`2026-09-26 00:00:00.000 INFO  note about 2026-09-25 elsewhere`) {
		t.Error("date must be anchored to the line's own timestamp")
	}
}

// The three conditions plus free text have to compose — that is the point of
// the feature, so it is asserted directly rather than inferred.
func TestFilterCombinesAllConditions(t *testing.T) {
	f := ParseFilter(`date:2026-09-25 task:movies node:movies_5 accepted dune`)
	if !f.match(lineMovies) {
		t.Fatalf("all conditions satisfied but no match: %+v", f)
	}
	for name, line := range map[string]string{
		"wrong day":    lineOtherDay,
		"wrong task":   lineMovies3D,
		"wrong node":   strings.Replace(lineMovies, "node=movies_5 ", "node=dedup_6 ", 1),
		"missing text": strings.Replace(lineMovies, `title="Dune"`, `title="Heat"`, 1),
	} {
		if f.match(line) {
			t.Errorf("%s: should not match", name)
		}
	}
}

// Free text stays case-insensitive and order-independent, as before.
func TestFilterFreeTextUnchanged(t *testing.T) {
	if !ParseFilter("DUNE accepted").match(lineMovies) {
		t.Error("free text should be case-insensitive and order-independent")
	}
	if ParseFilter("dune missing").match(lineMovies) {
		t.Error("every substring must be present")
	}
}

// Backwards compatibility: a colon in a query is only special for a known
// key, so old queries containing timestamps, URLs or run_id: keep working.
func TestFilterUnknownKeysStaySubstrings(t *testing.T) {
	for _, q := range []string{"10:10", "run_id:abc", "https://example.com"} {
		f := ParseFilter(q)
		if f.Date != "" || f.Task != "" || f.Node != "" {
			t.Errorf("%q was parsed as a structured condition: %+v", q, f)
		}
		if len(f.Subs) != 1 {
			t.Errorf("%q should be one substring, got %+v", q, f.Subs)
		}
	}
	if !ParseFilter("10:10").match(lineMovies) {
		t.Error("a bare timestamp should still match as text")
	}
	// Log lines spell fields with "=", so a "run_id:abc" query is text that
	// simply is not present — the point is that it is not silently reinterpreted
	// as a structured condition that would have matched everything.
	if ParseFilter("run_id:abc").match(lineMovies) {
		t.Error("run_id:abc should be plain text, and this line has run_id=abc")
	}
	if !ParseFilter("run_id=abc").match(lineMovies) {
		t.Error("run_id=abc should match as text")
	}
}

func TestFilterEmptyMatchesEverything(t *testing.T) {
	for _, q := range []string{"", "   ", "\t"} {
		f := ParseFilter(q)
		if !f.empty() {
			t.Errorf("%q should be empty, got %+v", q, f)
		}
		if !f.match(lineMovies) {
			t.Errorf("%q should match every line", q)
		}
	}
	// A key with no value is not a condition; it is text.
	if f := ParseFilter("task:"); f.Task != "" {
		t.Errorf("task: with no value set a condition: %+v", f)
	}
}

// A position and a cursor are the same thing to a client: it feeds a line's
// position back as the cursor for the next page. They must therefore share a
// wire format, or the round-trip silently breaks.
func TestLinePosMarshalsAsACursorString(t *testing.T) {
	b, err := json.Marshal(LinePos{FileIdx: 2, ByteEnd: 8305176})
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"2:8305176"` {
		t.Errorf("got %s, want \"2:8305176\"", b)
	}
}

func TestLinePosRoundTripsThroughJSON(t *testing.T) {
	want := LinePos{FileIdx: 1, ByteEnd: 42}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	var got LinePos
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The position a response carries must be usable verbatim as the cursor on
// the next request — that is the contract the UI relies on.
func TestAPositionFromAResponseParsesAsACursor(t *testing.T) {
	b, err := json.Marshal(LineWithPos{Pos: LinePos{FileIdx: 0, ByteEnd: 1234}, Text: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	var decoded struct {
		Pos  string `json:"pos"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("pos must decode as a string: %v", err)
	}
	got, err := ParseLinePos(decoded.Pos)
	if err != nil {
		t.Fatalf("ParseLinePos(%q): %v", decoded.Pos, err)
	}
	if want := (LinePos{FileIdx: 0, ByteEnd: 1234}); got != want {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLinePosRejectsAMalformedString(t *testing.T) {
	var p LinePos
	if err := json.Unmarshal([]byte(`"not-a-position"`), &p); err == nil {
		t.Error("a string with no colon must not unmarshal")
	}
}
