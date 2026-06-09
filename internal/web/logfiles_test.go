package web

import (
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

	got, older, exhausted, err := lf.Tail(3, nil)
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

	got, _, exhausted, err := lf.Tail(10, nil)
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
	page1, older, _, _ := lf.Tail(2, nil)
	if want := []string{"d", "e"}; !equalStrings(texts(page1), want) {
		t.Fatalf("page1 = %v, want %v", texts(page1), want)
	}

	// Second page: 2 older.
	page2, older2, _, _ := lf.Before(older, 2, nil)
	if want := []string{"b", "c"}; !equalStrings(texts(page2), want) {
		t.Fatalf("page2 = %v, want %v", texts(page2), want)
	}

	// Third page: 1 more older = ['a'], exhausted.
	page3, _, exhausted, _ := lf.Before(older2, 2, nil)
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
	got, _, exhausted, err := lf.Tail(20, nil)
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
	page1, older, _, _ := lf.Tail(3, nil)
	if want := []string{"old3", "new1", "new2"}; !equalStrings(texts(page1), want) {
		t.Fatalf("page1 = %v, want %v", texts(page1), want)
	}
	if page1[0].Pos.FileIdx != 1 {
		t.Errorf("old3 should be in archive 1, got %v", page1[0].Pos)
	}
	// Page back: 2 older = [old1, old2].
	page2, _, exhausted, _ := lf.Before(older, 5, nil)
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
	all, _, _, _ := lf.Tail(5, nil)
	posOfB := all[1].Pos
	got, _, atTail, err := lf.After(posOfB, 10, nil)
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

	all, _, _, _ := lf.Tail(5, nil)
	// all = [old1, old2, old3, new1, new2]
	posOfOld1 := all[0].Pos
	got, _, atTail, err := lf.After(posOfOld1, 10, nil)
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

	got, _, exhausted, err := lf.Tail(10, nil)
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

	got, _, exhausted, err := lf.Tail(10, nil)
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

	got, _, exhausted, err := lf.Tail(10, nil)
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
	page1, older, _, _ := lf.Tail(1, nil)
	if len(page1) != 1 || page1[0].Text != long {
		t.Fatalf("page1 = %d lines (texts truncated)", len(page1))
	}
	// Page 2: the two prior short lines.
	page2, _, exhausted, _ := lf.Before(older, 5, nil)
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
