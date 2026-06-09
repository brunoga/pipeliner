package web

import (
	"bufio"
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

// Filter is an AND-list of case-insensitive substrings. Empty matches all.
type Filter []string

// ParseFilter splits q on whitespace and lowercases each term. Returns nil
// when q has no terms.
func ParseFilter(q string) Filter {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	terms := strings.Fields(strings.ToLower(q))
	if len(terms) == 0 {
		return nil
	}
	return Filter(terms)
}

func (f Filter) match(line string) bool {
	if len(f) == 0 {
		return true
	}
	lo := strings.ToLower(line)
	for _, t := range f {
		if !strings.Contains(lo, t) {
			return false
		}
	}
	return true
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

