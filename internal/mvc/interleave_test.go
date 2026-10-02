package mvc

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// annexB builds a stream from (type, payload) pairs, each behind a 4-byte start
// code. payload[0] is the byte after the NAL header, so its top bit is the
// first_mb_in_slice flag a picture-start check reads.
func annexB(nals ...[]byte) []byte {
	var b bytes.Buffer
	for _, n := range nals {
		b.Write([]byte{0, 0, 0, 1})
		b.Write(n)
	}
	return b.Bytes()
}

func nal(typ int, rest ...byte) []byte {
	return append([]byte{byte(typ)}, rest...)
}

func TestSplitNALsHandlesBothStartCodeWidths(t *testing.T) {
	// 3-byte code, then a 4-byte one.
	buf := []byte{0, 0, 1, 0x67, 0xAA, 0, 0, 0, 1, 0x68, 0xBB}
	got := splitNALs(buf)
	if len(got) != 2 {
		t.Fatalf("got %d NALs, want 2: %+v", got, got)
	}
	if got[0].typ != 7 || got[1].typ != 8 {
		t.Errorf("types = %d,%d want 7,8", got[0].typ, got[1].typ)
	}
	// The 4-byte code's extra zero must not be counted into the first NAL.
	if !bytes.Equal(buf[got[0].start:got[0].end], []byte{0x67, 0xAA}) {
		t.Errorf("first NAL = % x, want 67 aa", buf[got[0].start:got[0].end])
	}
}

func TestSplitNALsOnGarbage(t *testing.T) {
	if got := splitNALs([]byte{1, 2, 3, 4}); got != nil {
		t.Errorf("a stream with no start code should yield nothing, got %+v", got)
	}
	if got := splitNALs(nil); got != nil {
		t.Errorf("empty input should yield nothing, got %+v", got)
	}
}

// A picture's leading parameter sets belong to the picture they precede, not to
// the one before. Getting this wrong shifts every SPS/PPS one access unit
// earlier and the dependent view stops referencing the right sets.
func TestAccessUnitBoundaryKeepsLeadingNALsWithTheirPicture(t *testing.T) {
	// SPS PPS IDR | SPS PPS slice
	buf := annexB(
		nal(7, 0x10), nal(8, 0x20), nal(5, 0x80),
		nal(7, 0x10), nal(8, 0x20), nal(1, 0x80),
	)
	spans, n := groupAccessUnits(buf, true)
	if n != 2 {
		t.Fatalf("got %d access units, want 2", n)
	}
	wantAU := []int{0, 0, 0, 1, 1, 1}
	for i, s := range spans {
		if s.au != wantAU[i] {
			t.Errorf("NAL %d (type %d) in AU %d, want %d", i, s.typ, s.au, wantAU[i])
		}
	}
}

// Back-to-back pictures with no leading NAL between them still split.
func TestBackToBackPicturesSplit(t *testing.T) {
	buf := annexB(nal(5, 0x80), nal(1, 0x80), nal(1, 0x80))
	_, n := groupAccessUnits(buf, true)
	if n != 3 {
		t.Errorf("got %d access units, want 3", n)
	}
}

// A slice that continues a picture (first_mb_in_slice != 0) must not open a new
// access unit — a multi-slice picture would otherwise be torn into several.
func TestContinuationSlicesStayInTheSamePicture(t *testing.T) {
	buf := annexB(nal(5, 0x80), nal(5, 0x01), nal(5, 0x01))
	_, n := groupAccessUnits(buf, true)
	if n != 1 {
		t.Errorf("got %d access units, want 1 (one picture in three slices)", n)
	}
}

// The dependent view's slice header sits behind a three-byte MVC extension, so
// the picture-start bit is four bytes in, not one.
func TestDependentViewPictureStartIsOffsetByTheMVCHeader(t *testing.T) {
	// type 20, three extension bytes, then the slice header byte.
	buf := annexB(
		append(nal(20, 0x01, 0x02, 0x03), 0x80),
		append(nal(20, 0x01, 0x02, 0x03), 0x80),
	)
	_, n := groupAccessUnits(buf, false)
	if n != 2 {
		t.Errorf("got %d access units, want 2", n)
	}
	// Read as a base stream the same bytes are not slices at all.
	if _, bn := groupAccessUnits(buf, true); bn != 1 {
		t.Errorf("as a base stream these are non-VCL, so one AU; got %d", bn)
	}
}

func TestInterleaveOrdersBaseThenDependentPerAccessUnit(t *testing.T) {
	base := annexB(nal(7, 0x10), nal(5, 0x80), nal(1, 0x80))
	dep := annexB(
		append(nal(20, 1, 2, 3), 0x80),
		append(nal(20, 1, 2, 3), 0x80),
	)
	// base: AU0 = SPS+IDR, AU1 = slice. dependent: AU0, AU1.
	var out bytes.Buffer
	if err := Interleave(&out, base, dep); err != nil {
		t.Fatal(err)
	}
	types := make([]int, 0, 5)
	for _, n := range splitNALs(out.Bytes()) {
		types = append(types, n.typ)
	}
	want := []int{7, 5, 20, 1, 20}
	if len(types) != len(want) {
		t.Fatalf("got types %v, want %v", types, want)
	}
	for i := range want {
		if types[i] != want[i] {
			t.Errorf("NAL %d = type %d, want %d (order: %v)", i, types[i], want[i], types)
		}
	}
}

// A length mismatch means the two files are not the pair they were taken for.
// Saying so beats silently truncating to the shorter one, which would produce a
// film that loses its second eye partway through.
func TestInterleaveRejectsAMismatchedPair(t *testing.T) {
	base := annexB(nal(5, 0x80), nal(1, 0x80), nal(1, 0x80))
	dep := annexB(append(nal(20, 1, 2, 3), 0x80))
	err := Interleave(&bytes.Buffer{}, base, dep)
	if err == nil {
		t.Fatal("a mismatched pair must be refused")
	}
	for _, want := range []string{"3", "1", "access units"} {
		if !bytes.Contains([]byte(err.Error()), []byte(want)) {
			t.Errorf("error should mention %q, got %q", want, err)
		}
	}
}

func TestInterleaveRejectsNonAnnexBInput(t *testing.T) {
	good := annexB(nal(5, 0x80))
	if err := Interleave(&bytes.Buffer{}, []byte{1, 2, 3}, good); err == nil {
		t.Error("garbage base view must be refused")
	}
	if err := Interleave(&bytes.Buffer{}, good, []byte{1, 2, 3}); err == nil {
		t.Error("garbage dependent view must be refused")
	}
}

// The proof: interleaving a real demuxed pair must decode to exactly what the
// equivalent combined stream decodes to. Skips unless pointed at the fixtures
// and a built edge264, since neither can be committed here.
//
//	MVC_TEST_FIXTURES  mvc-source's tests/fixtures
//	MVC_TEST_EDGE264   path to edge264_test (or on PATH as edge264/edge264_test)
func TestInterleaveMatchesACombinedStream(t *testing.T) {
	dir := os.Getenv("MVC_TEST_FIXTURES")
	if dir == "" {
		t.Skip("set MVC_TEST_FIXTURES to mvc-source's tests/fixtures")
	}
	dec := os.Getenv("MVC_TEST_EDGE264")
	if dec == "" {
		for _, n := range []string{"edge264_test", "edge264"} {
			if p, err := exec.LookPath(n); err == nil {
				dec = p
				break
			}
		}
	}
	if dec == "" {
		t.Skip("edge264 not found; set MVC_TEST_EDGE264")
	}
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Skipf("fixture %s missing: %v", name, err)
		}
		return b
	}
	base, dep := read("mvc_base.264"), read("mvc_dependent.mvc")

	mine := filepath.Join(t.TempDir(), "interleaved.264")
	f, err := os.Create(mine)
	if err != nil {
		t.Fatal(err)
	}
	if err := Interleave(f, base, dep); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	decode := func(path string) []byte {
		out, err := exec.Command(dec, "-y", "-k", "-O", path).Output()
		if err != nil {
			t.Fatalf("%s on %s: %v", dec, path, err)
		}
		return out
	}
	got := decode(mine)
	want := decode(filepath.Join(dir, "mvc_combined.264"))
	if len(got) == 0 {
		t.Fatal("interleaved stream decoded to nothing")
	}
	if !bytes.Equal(got, want) {
		t.Errorf("interleaved decode differs from the combined stream's: %d vs %d bytes",
			len(got), len(want))
	}
}
