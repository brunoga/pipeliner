package mvc

import (
	"bytes"
	"fmt"
	"io"
)

// An MVC elementary stream carries both eyes in one sequence of access units:
// the base view's NALs, then the dependent view's for the same picture. tsMuxeR
// cannot produce that — its documentation is explicit that it "always splits"
// a combined AVC/MVC track into a base .264 and a dependent .mvc — and the
// decoder takes one stream. Interleaving them is the join between the two, and
// the only reason bd3d2sbs needs VapourSynth and a frameserver plugin at all.
//
// Doing it here instead removes VapourSynth, Python and the plugin from the
// toolchain, which is most of what there was to install.
//
// The access-unit rule, and the per-AU ordering, follow mvc-source's
// implementation, whose comments state it is bit-exact against decoding a real
// combined stream. TestInterleaveMatchesACombinedStream holds that here too.

// startCode3 is the Annex-B NAL separator. A 4-byte code is the same with a
// leading zero, which is why the scan looks for the 3-byte form and then checks
// the preceding byte.
var startCode3 = []byte{0, 0, 1}

// nalSpan is one NAL unit's payload: the bytes after its start code.
type nalSpan struct {
	start, end int
	typ        int
	au         int
}

// splitNALs returns the payload spans of every NAL in an Annex-B stream.
func splitNALs(buf []byte) []nalSpan {
	i := bytes.Index(buf, startCode3)
	if i < 0 {
		return nil
	}
	i += len(startCode3)
	var out []nalSpan
	for i < len(buf) {
		next := bytes.Index(buf[i:], startCode3)
		if next < 0 {
			out = append(out, nalSpan{start: i, end: len(buf), typ: int(buf[i] & 0x1f)})
			break
		}
		end := i + next
		// A 4-byte start code's extra zero belongs to the separator, not to the
		// NAL that precedes it.
		if end > i && buf[end-1] == 0 {
			end--
		}
		out = append(out, nalSpan{start: i, end: end, typ: int(buf[i] & 0x1f)})
		i += next + len(startCode3)
	}
	return out
}

// isVCL reports whether a NAL carries coded picture data for its view. The base
// view uses types 1 and 5; a dependent view slice is a type-20 extension.
func isVCL(typ int, base bool) bool {
	if base {
		return typ == 1 || typ == 5
	}
	return typ == 20
}

// startsPicture reports whether a slice NAL begins a coded picture, which is
// first_mb_in_slice == 0 — the top bit of the first RBSP byte. A type-20
// dependent slice carries a three-byte MVC extension header first, so its slice
// header, and that same bit, start four bytes in.
func startsPicture(buf []byte, n nalSpan, base bool) bool {
	if base {
		return (n.typ == 1 || n.typ == 5) && n.start+1 < n.end && buf[n.start+1]&0x80 != 0
	}
	return n.typ == 20 && n.start+4 < n.end && buf[n.start+4]&0x80 != 0
}

// groupAccessUnits tags each NAL with its access unit. A new unit begins at the
// first non-VCL NAL after a picture, or at a picture-start slice directly
// following another — so a picture's leading parameter sets and prefix NALs stay
// with the picture they precede rather than being orphaned into the unit before.
func groupAccessUnits(buf []byte, base bool) (spans []nalSpan, count int) {
	au, seenVCL := 0, false
	for _, n := range splitNALs(buf) {
		vcl := isVCL(n.typ, base)
		if seenVCL && (!vcl || startsPicture(buf, n, base)) {
			au++
			seenVCL = false
		}
		n.au = au
		spans = append(spans, n)
		if vcl {
			seenVCL = true
		}
	}
	if len(spans) == 0 {
		return nil, 0
	}
	return spans, au + 1
}

// Interleave writes the combined MVC stream for a demuxed base and dependent
// view. Per access unit the base view's NALs come first, then the dependent
// view's for the same picture.
//
// It streams: the caller can hand w the decoder's stdin, so a multi-gigabyte
// pair never lands on disk as a third copy.
func Interleave(w io.Writer, base, dependent []byte) error {
	baseNALs, baseAUs := groupAccessUnits(base, true)
	if baseAUs == 0 {
		return fmt.Errorf("base view: no NAL units found (not an Annex-B stream?)")
	}
	depNALs, depAUs := groupAccessUnits(dependent, false)
	if depAUs == 0 {
		return fmt.Errorf("dependent view: no NAL units found (not an Annex-B stream?)")
	}
	// A mismatch is worth saying out loud rather than silently truncating: it
	// means the two files are not the pair they were taken for.
	if baseAUs != depAUs {
		return fmt.Errorf("the two views disagree on length: base has %d access units, dependent has %d",
			baseAUs, depAUs)
	}
	for au := 0; au < baseAUs; au++ {
		if err := writeAU(w, base, baseNALs, au); err != nil {
			return err
		}
		if err := writeAU(w, dependent, depNALs, au); err != nil {
			return err
		}
	}
	return nil
}

// writeAU emits every NAL of one access unit, each behind a 4-byte start code.
// Re-emitting at a fixed width changes the byte count against a stream that
// used 3-byte codes, but not the decode: the separator is not part of the NAL.
func writeAU(w io.Writer, buf []byte, spans []nalSpan, au int) error {
	for _, n := range spans {
		if n.au != au {
			continue
		}
		if _, err := w.Write([]byte{0, 0, 0, 1}); err != nil {
			return err
		}
		if _, err := w.Write(buf[n.start:n.end]); err != nil {
			return err
		}
	}
	return nil
}

// AccessUnitCount reports how many access units a view stream holds, which is
// how the runner reports progress and checks a pair before committing to a
// multi-hour decode.
func AccessUnitCount(buf []byte, base bool) int {
	_, n := groupAccessUnits(buf, base)
	return n
}
