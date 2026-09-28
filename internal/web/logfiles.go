package web

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

// LinePos addresses a single line in the rotating log set with a cursor
// that is stable as long as the file at FileIdx is not rotated away.
//
// FileIdx 0 is the active base file, 1 is "<path>.1", 2 is "<path>.2", etc.
// ByteEnd is the byte offset of the first byte AFTER the line's '\n'.
// Older lines have a higher FileIdx; within a file, older = smaller ByteEnd.
type LinePos struct {
	FileIdx int   `json:"file"`
	ByteEnd int64 `json:"end"`
}

// String serializes a position as "<fileIdx>:<byteEnd>" for use as an
// opaque wire cursor.
func (p LinePos) String() string {
	return strconv.Itoa(p.FileIdx) + ":" + strconv.FormatInt(p.ByteEnd, 10)
}

// MarshalJSON emits a position in the same "<fileIdx>:<byteEnd>" form used
// for cursors.
//
// Positions and cursors are the same thing as far as a client is concerned:
// it takes a line's position and feeds it straight back as the cursor for the
// next page, or compares it against one. Letting the struct marshal as an
// object while every cursor field marshalled as a string made those two uses
// silently incompatible — a position stringified to "[object Object]" as a
// cursor, and position comparison in the UI failed to parse and answered
// "false" for everything, which duplicated live lines the tail had already
// rendered. One wire format removes the whole class.
func (p LinePos) MarshalJSON() ([]byte, error) {
	return json.Marshal(p.String())
}

// UnmarshalJSON accepts the string form written by MarshalJSON.
func (p *LinePos) UnmarshalJSON(b []byte) error {
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	v, err := ParseLinePos(s)
	if err != nil {
		return err
	}
	*p = v
	return nil
}

// ParseLinePos decodes a serialized position. Empty input parses to the
// zero value, which callers can treat as "live tail" or "file start"
// depending on context.
func ParseLinePos(s string) (LinePos, error) {
	if s == "" {
		return LinePos{}, nil
	}
	head, tail, ok := strings.Cut(s, ":")
	if !ok {
		return LinePos{}, fmt.Errorf("logpos: missing colon")
	}
	fi, err := strconv.Atoi(head)
	if err != nil || fi < 0 {
		return LinePos{}, fmt.Errorf("logpos: bad file index")
	}
	be, err := strconv.ParseInt(tail, 10, 64)
	if err != nil || be < 0 {
		return LinePos{}, fmt.Errorf("logpos: bad byte offset")
	}
	return LinePos{FileIdx: fi, ByteEnd: be}, nil
}

// LineWithPos is a single matched line plus its stable cursor.
type LineWithPos struct {
	Pos  LinePos `json:"pos"`
	Text string  `json:"text"`
}

// Filter is an AND of every condition it holds; the zero value matches all.
//
// Structured conditions narrow to a day, a pipeline or a node and are matched
// against the shape of a log line rather than as loose text, which is what
// lets them be combined with a free-text search over the remainder. Matching
// task and node by whole value matters: as a substring, "task=movies" also
// selects task=movies-3d and task=movies-ondemand.
type Filter struct {
	Date string   // line's leading YYYY-MM-DD, "" for any day
	Task string   // whole value of task=, "" for any pipeline
	Node string   // whole value of node=, "" for any node
	Subs []string // case-insensitive substrings, all of which must appear
}

// filterKeys maps a query prefix to where its value is stored. Adding a key
// here is all a new structured condition needs.
var filterKeys = map[string]func(*Filter, string){
	"date": func(f *Filter, v string) { f.Date = v },
	"task": func(f *Filter, v string) { f.Task = v },
	"node": func(f *Filter, v string) { f.Node = v },
}

// ParseFilter reads a query into a Filter. A term of the form key:value sets
// the matching structured condition; every other term is a case-insensitive
// substring that must appear in the line.
//
// An unknown key is deliberately left as a substring so queries that happen
// to contain a colon — a URL, a run_id:, a bare timestamp like "14:05" —
// keep behaving as they always did.
func ParseFilter(q string) Filter {
	var f Filter
	for _, term := range strings.Fields(strings.TrimSpace(q)) {
		if key, val, ok := strings.Cut(term, ":"); ok && val != "" {
			if set, known := filterKeys[strings.ToLower(key)]; known {
				set(&f, val)
				continue
			}
		}
		f.Subs = append(f.Subs, strings.ToLower(term))
	}
	return f
}

// empty reports whether the filter constrains nothing.
func (f Filter) empty() bool {
	return f.Date == "" && f.Task == "" && f.Node == "" && len(f.Subs) == 0
}

func (f Filter) match(line string) bool {
	if f.empty() {
		return true
	}
	// The timestamp leads every line, so a day is a prefix test.
	if f.Date != "" && !strings.HasPrefix(line, f.Date) {
		return false
	}
	if f.Task != "" && !hasFieldValue(line, "task=", f.Task) {
		return false
	}
	if f.Node != "" && !hasFieldValue(line, "node=", f.Node) {
		return false
	}
	if len(f.Subs) == 0 {
		return true
	}
	lo := strings.ToLower(line)
	for _, t := range f.Subs {
		if !strings.Contains(lo, t) {
			return false
		}
	}
	return true
}

// hasFieldValue reports whether line carries key immediately followed by val
// as a complete value — terminated by a space or the end of the line. That
// boundary is the whole point: without it task=movies would also match
// task=movies-3d. Comparison is case-insensitive.
func hasFieldValue(line, key, val string) bool {
	lo, k, v := strings.ToLower(line), strings.ToLower(key), strings.ToLower(val)
	for i := 0; ; {
		j := strings.Index(lo[i:], k)
		if j < 0 {
			return false
		}
		j += i
		rest := lo[j+len(k):]
		if strings.HasPrefix(rest, v) {
			if after := rest[len(v):]; after == "" || after[0] == ' ' {
				return true
			}
		}
		i = j + len(k)
	}
}

// LogFiles is a read-only view over a base log file at Path plus its
// .1..MaxArchives archives. The zero value is unusable; instantiate via
// the Server's logFilePath/logFileMaxArchives. Methods are safe to call
// concurrently with the writer appending to the base file (a read may
// race with a write and miss a line that was just appended; the next
// call will pick it up).
type LogFiles struct {
	Path        string
	MaxArchives int
}

func (lf *LogFiles) pathFor(i int) string {
	if i == 0 {
		return lf.Path
	}
	return fmt.Sprintf("%s.%d", lf.Path, i)
}

func (lf *LogFiles) fileSize(idx int) (size int64, exists bool, err error) {
	info, err := os.Stat(lf.pathFor(idx))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	return info.Size(), true, nil
}

// Tail returns the newest n matching lines across the rotating set,
// oldest-first. olderCursor points strictly before the oldest of those
// (suitable to feed Before for further paging). exhausted=true when the
// scan reached the start of the oldest archive without filling.
func (lf *LogFiles) Tail(n int, f Filter) (lines []LineWithPos, olderCursor LinePos, exhausted bool, err error) {
	if n <= 0 {
		return nil, LinePos{}, false, nil
	}
	sz, _, err := lf.fileSize(0)
	if err != nil {
		return nil, LinePos{}, false, err
	}
	// Synthesize a "newer than anything in file 0" cursor so Before walks
	// the whole tail.
	return lf.Before(LinePos{FileIdx: 0, ByteEnd: sz + 1}, n, f)
}

// Before returns up to n matching lines whose Pos < cur, oldest-first.
// olderCursor points strictly before the oldest emitted line, ready for
// the next paging call. exhausted=true when the scan reached the start
// of the oldest archive.
func (lf *LogFiles) Before(cur LinePos, n int, f Filter) ([]LineWithPos, LinePos, bool, error) {
	if n <= 0 {
		return nil, cur, false, nil
	}
	idx := cur.FileIdx
	endByte := cur.ByteEnd
	// rev accumulates newest-first while scanning; we flip to oldest-first
	// before returning.
	rev := make([]LineWithPos, 0, n)

	for idx <= lf.MaxArchives && len(rev) < n {
		sz, exists, err := lf.fileSize(idx)
		if err != nil {
			return nil, LinePos{}, false, err
		}
		if !exists {
			idx++
			endByte = 0
			continue
		}
		// First step into a new archive: scan from EOF.
		if endByte <= 0 || endByte > sz+1 {
			endByte = sz + 1
		}
		got, nextEnd, atStart, err := scanFileBackward(lf.pathFor(idx), idx, endByte, n-len(rev), f)
		if err != nil {
			return nil, LinePos{}, false, err
		}
		rev = append(rev, got...)
		if atStart {
			idx++
			endByte = 0
			continue
		}
		endByte = nextEnd
	}

	atEnd := idx > lf.MaxArchives
	exhausted := atEnd && len(rev) < n

	// Flip rev (newest-first) to chronological order.
	lines := make([]LineWithPos, len(rev))
	for i, l := range rev {
		lines[len(rev)-1-i] = l
	}
	var older LinePos
	switch {
	case len(lines) > 0:
		older = lines[0].Pos
	case atEnd:
		older = LinePos{}
	default:
		older = cur
	}
	return lines, older, exhausted, nil
}

// After returns up to n matching lines whose Pos > cur, oldest-first.
// newerCursor is the position of the newest emitted line. atTail=true
// when the scan reached the EOF of the base file (FileIdx 0).
func (lf *LogFiles) After(cur LinePos, n int, f Filter) ([]LineWithPos, LinePos, bool, error) {
	if n <= 0 {
		return nil, cur, false, nil
	}
	idx := cur.FileIdx
	startByte := cur.ByteEnd

	// Skip over any non-existent archive slots between idx and the
	// oldest existing one. (Pruning may have left a gap, in which case
	// we start at the next-newer archive that does exist.)
	for idx > 0 {
		_, exists, err := lf.fileSize(idx)
		if err != nil {
			return nil, LinePos{}, false, err
		}
		if exists {
			break
		}
		idx--
		startByte = 0
	}

	out := make([]LineWithPos, 0, n)
	atTail := false
	for idx >= 0 && len(out) < n {
		sz, exists, err := lf.fileSize(idx)
		if err != nil {
			return nil, LinePos{}, false, err
		}
		if !exists {
			// Only reachable for idx == 0 when the daemon hasn't written
			// any bytes yet.
			if idx == 0 {
				atTail = true
			}
			break
		}
		if startByte > sz {
			startByte = sz
		}
		got, nextStart, eof, err := scanFileForward(lf.pathFor(idx), idx, startByte, n-len(out), f)
		if err != nil {
			return nil, LinePos{}, false, err
		}
		out = append(out, got...)
		if eof {
			if idx == 0 {
				atTail = true
				break
			}
			idx--
			startByte = 0
			continue
		}
		startByte = nextStart
	}

	var newer LinePos
	if len(out) > 0 {
		newer = out[len(out)-1].Pos
	} else {
		newer = cur
	}
	return out, newer, atTail, nil
}

// scanFileBackward reads the named file backward starting just before
// endByte, returning up to want matching lines in newest-first order.
// nextEnd is the ByteEnd of the next-older line; atStart=true when the
// scan reached the start of the file. The scanner buffers chunks as
// needed so a single call can satisfy a large `want` even when matches
// are sparse.
func scanFileBackward(path string, fileIdx int, endByte int64, want int, f Filter) ([]LineWithPos, int64, bool, error) {
	if want <= 0 {
		return nil, endByte, endByte <= 0, nil
	}
	if endByte <= 0 {
		return nil, 0, true, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close() //nolint:errcheck

	const chunkSize = 8192
	// buf holds the contiguous tail of the file we've inspected so far,
	// covering byte offsets [bufStart, bufStart+len(buf)). nls is the
	// ascending list of '\n' offsets relative to buf. top is the index in
	// nls of the next-newest unconsumed entry.
	var buf []byte
	var nls []int
	bufStart := endByte
	pos := endByte
	top := -1
	out := make([]LineWithPos, 0, want)
	// emittedFirstLine guards against re-emitting the file-start line via
	// the "buf is one un-terminated line" fallback after we've already
	// emitted bytes [0, nls[0]) through the regular path.
	emittedFirstLine := false

	readChunk := func() (bool, error) {
		if pos == 0 {
			return false, nil
		}
		readSize := min(int64(chunkSize), pos)
		chunk := make([]byte, readSize)
		n, rerr := file.ReadAt(chunk, pos-readSize)
		if rerr != nil && rerr != io.EOF {
			return false, rerr
		}
		chunk = chunk[:n]
		pos -= readSize
		shift := len(chunk)
		for k := range nls {
			nls[k] += shift
		}
		var newNl []int
		for k, b := range chunk {
			if b == '\n' {
				newNl = append(newNl, k)
			}
		}
		buf = append(chunk, buf...)
		bufStart = pos
		nls = append(newNl, nls...)
		// Shift top so we keep pointing at the same logical entry rather
		// than re-walking already-skipped past-cursor newlines.
		top += len(newNl)
		if top < 0 {
			top = len(nls) - 1
		}
		return true, nil
	}

	atStart := false
outer:
	for len(out) < want {
		// Skip newlines whose terminator is at or past endByte.
		for top >= 0 && bufStart+int64(nls[top])+1 >= endByte {
			top--
		}
		if top < 0 {
			if pos > 0 {
				ok, rerr := readChunk()
				if rerr != nil {
					return nil, 0, false, rerr
				}
				if !ok {
					break outer
				}
				continue
			}
			// File start. If buf has bytes but no newlines were ever found,
			// the whole buf is a single unterminated line.
			if !emittedFirstLine && len(buf) > 0 && len(nls) == 0 {
				text := stripCR(string(buf))
				end := bufStart + int64(len(buf))
				if end < endByte && text != "" && f.match(text) {
					out = append(out, LineWithPos{
						Pos:  LinePos{FileIdx: fileIdx, ByteEnd: end},
						Text: text,
					})
				}
			}
			atStart = true
			break outer
		}

		cur := nls[top]
		byteEnd := bufStart + int64(cur) + 1
		switch {
		case top > 0:
			start := nls[top-1] + 1
			text := stripCR(string(buf[start:cur]))
			if text != "" && f.match(text) {
				out = append(out, LineWithPos{
					Pos:  LinePos{FileIdx: fileIdx, ByteEnd: byteEnd},
					Text: text,
				})
			}
			top--
		case pos == 0:
			// First line of the file: bytes [0, cur).
			text := stripCR(string(buf[0:cur]))
			if text != "" && f.match(text) {
				out = append(out, LineWithPos{
					Pos:  LinePos{FileIdx: fileIdx, ByteEnd: byteEnd},
					Text: text,
				})
			}
			emittedFirstLine = true
			top--
		default:
			// Head straddle — need older bytes to find this line's start.
			ok, rerr := readChunk()
			if rerr != nil {
				return nil, 0, false, rerr
			}
			if !ok {
				break outer
			}
		}
	}

	var nextEnd int64
	switch {
	case len(out) > 0:
		oldest := out[len(out)-1]
		nextEnd = max(oldest.Pos.ByteEnd-int64(len(oldest.Text))-1, 0)
	case atStart:
		nextEnd = 0
	default:
		// We bailed without matches and without reaching file start; expose
		// the boundary of what we scanned so the next call makes progress.
		nextEnd = bufStart
	}
	return out, nextEnd, atStart || nextEnd == 0, nil
}

func stripCR(s string) string {
	if strings.HasSuffix(s, "\r") {
		return s[:len(s)-1]
	}
	return s
}

// scanFileForward reads the named file starting at startByte and returns
// up to want matching lines in chronological order. nextStart is the
// ByteEnd of the last line returned. eof=true when the scan consumed the
// file to its current end.
func scanFileForward(path string, fileIdx int, startByte int64, want int, f Filter) ([]LineWithPos, int64, bool, error) {
	if want <= 0 {
		return nil, startByte, false, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, false, err
	}
	defer file.Close() //nolint:errcheck

	if _, err := file.Seek(startByte, io.SeekStart); err != nil {
		return nil, 0, false, err
	}
	br := bufio.NewReader(file)
	out := make([]LineWithPos, 0, want)
	pos := startByte
	for len(out) < want {
		line, rerr := br.ReadBytes('\n')
		consumed := int64(len(line))
		hadTerm := consumed > 0 && line[len(line)-1] == '\n'
		text := string(line)
		if hadTerm {
			text = text[:len(text)-1]
		}
		text = stripCR(text)
		if hadTerm {
			newPos := pos + consumed
			if text != "" && f.match(text) {
				out = append(out, LineWithPos{
					Pos:  LinePos{FileIdx: fileIdx, ByteEnd: newPos},
					Text: text,
				})
			}
			pos = newPos
		}
		if rerr == io.EOF {
			return out, pos, true, nil
		}
		if rerr != nil {
			return out, pos, false, rerr
		}
	}
	return out, pos, false, nil
}

// DateRange returns the day of the oldest retained line and of the newest,
// as YYYY-MM-DD. It reads one line from each end rather than scanning, so it
// stays cheap no matter how much history is retained. Empty strings mean
// there is nothing to report.
func (lf *LogFiles) DateRange() (oldest, newest string, err error) {
	if newestLines, _, _, e := lf.Tail(1, Filter{}); e != nil {
		return "", "", e
	} else if len(newestLines) > 0 {
		newest = lineDate(newestLines[0].Text)
	}
	// Walk from the highest archive index down to the first file that exists;
	// its first line is the oldest line retained.
	for idx := lf.MaxArchives; idx >= 0; idx-- {
		sz, exists, e := lf.fileSize(idx)
		if e != nil {
			return "", "", e
		}
		if !exists || sz == 0 {
			continue
		}
		first, _, _, e := scanFileForward(lf.pathFor(idx), idx, 0, 1, Filter{})
		if e != nil {
			return "", "", e
		}
		if len(first) > 0 {
			oldest = lineDate(first[0].Text)
			break
		}
	}
	return oldest, newest, nil
}

// lineDate extracts the leading YYYY-MM-DD from a log line, or "" when the
// line does not start with one (a panic trace, a wrapped continuation).
func lineDate(line string) string {
	const want = len("2026-01-02")
	if len(line) < want {
		return ""
	}
	d := line[:want]
	for i, r := range d {
		if i == 4 || i == 7 {
			if r != '-' {
				return ""
			}
			continue
		}
		if r < '0' || r > '9' {
			return ""
		}
	}
	return d
}
