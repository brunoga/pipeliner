package mvc

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// These drive the real decoder against real MVC elementary streams. Asserting
// on generated argv proves the shape of a command; only running it proves the
// pipeline decodes, and a mistake here surfaces hours into a conversion.
//
// They skip unless pointed at the pieces, because neither the decoder nor the
// fixtures can be committed here:
//
//	MVC_TEST_FIXTURES  mvc-source's tests/fixtures (base/dependent/combined)
//	MVC_TEST_EDGE264   path to edge264_test, if not on PATH
func edge264Bin(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("MVC_TEST_EDGE264"); p != "" {
		return p
	}
	for _, n := range []string{"edge264", "edge264_test"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("edge264 not found; set MVC_TEST_EDGE264")
	return ""
}

func fixtureDir(t *testing.T) string {
	t.Helper()
	d := os.Getenv("MVC_TEST_FIXTURES")
	if d == "" {
		t.Skip("set MVC_TEST_FIXTURES to mvc-source's tests/fixtures")
	}
	return d
}

func readFixture(t *testing.T, dir, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Skipf("fixture %s missing: %v", name, err)
	}
	return b
}

// y4mHeader returns the first line of a Y4M stream, which carries the frame size.
func y4mHeader(b []byte) string {
	if i := bytes.IndexByte(b, '\n'); i > 0 {
		return string(b[:i])
	}
	return ""
}

// decodePair runs the real pipeline's first two stages: interleave the demuxed
// views in process, hand them to the decoder on stdin, take Y4M back.
func decodePair(t *testing.T, bin string, base, dep []byte) []byte {
	t.Helper()
	var in bytes.Buffer
	if err := Interleave(&in, base, dep); err != nil {
		t.Fatalf("interleave: %v", err)
	}
	cmd := exec.Command(bin, "-y", "-k", "-O", "-")
	cmd.Stdin = &in
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", bin, err, errb.String())
	}
	return out.Bytes()
}

// The decoder must accept the interleaved stream on stdin — the whole pipeline
// depends on it, since tsMuxeR always splits the views and a third copy of a
// multi-gigabyte pair must never hit the disk.
func TestDecoderTakesTheInterleavedPairOnStdin(t *testing.T) {
	bin := edge264Bin(t)
	dir := fixtureDir(t)
	got := decodePair(t, bin, readFixture(t, dir, "mvc_base.264"), readFixture(t, dir, "mvc_dependent.mvc"))
	if len(got) == 0 {
		t.Fatal("decoded to nothing")
	}
	h := y4mHeader(got)
	if !strings.HasPrefix(h, "YUV4MPEG2") {
		t.Fatalf("not a Y4M stream: %q", h)
	}
	// -O stacks the eyes, so the frame is double the single-view width.
	if !strings.Contains(h, "W1280 H480") {
		t.Errorf("header = %q, want a 1280x480 side-by-side frame", h)
	}
}

// Streaming the pair through stdin must give exactly what decoding an
// equivalent combined stream gives. This is the check that the interleaving is
// right rather than merely plausible.
func TestStdinPairMatchesACombinedStream(t *testing.T) {
	bin := edge264Bin(t)
	dir := fixtureDir(t)
	viaPair := decodePair(t, bin, readFixture(t, dir, "mvc_base.264"), readFixture(t, dir, "mvc_dependent.mvc"))
	viaCombined, err := exec.Command(bin, "-y", "-k", "-O", filepath.Join(dir, "mvc_combined.264")).Output()
	if err != nil {
		t.Fatalf("decoding the combined stream: %v", err)
	}
	if !bytes.Equal(viaPair, viaCombined) {
		t.Errorf("pair decode differs from combined decode: %d vs %d bytes", len(viaPair), len(viaCombined))
	}
}

// The two halves must differ. If the dependent view silently failed to decode,
// the dimensions would still be right and the output would be a 3D file with no
// depth — the one failure nothing else here would notice.
func TestDecodedEyesDiffer(t *testing.T) {
	bin := edge264Bin(t)
	dir := fixtureDir(t)
	y4m := decodePair(t, bin, readFixture(t, dir, "mvc_base.264"), readFixture(t, dir, "mvc_dependent.mvc"))

	// Compare the luma rows of the first frame, half against half.
	h := y4mHeader(y4m)
	if !strings.Contains(h, "W1280 H480") {
		t.Skipf("unexpected geometry %q", h)
	}
	const w, ht = 1280, 480
	i := bytes.IndexByte(y4m, '\n') + 1
	// A frame begins with a "FRAME\n" header.
	j := bytes.Index(y4m[i:], []byte("FRAME"))
	if j < 0 {
		t.Fatal("no frame in the stream")
	}
	i += j
	k := bytes.IndexByte(y4m[i:], '\n')
	if k < 0 {
		t.Fatal("truncated frame header")
	}
	luma := y4m[i+k+1:]
	if len(luma) < w*ht {
		t.Fatalf("short frame: %d bytes", len(luma))
	}
	var diff int64
	for row := 0; row < ht; row++ {
		off := row * w
		for x := 0; x < w/2; x++ {
			d := int(luma[off+x]) - int(luma[off+w/2+x])
			if d < 0 {
				d = -d
			}
			diff += int64(d)
		}
	}
	mean := float64(diff) / float64(w/2*ht)
	if mean < 1 {
		t.Errorf("mean |left-right| = %.3f; the eyes are identical, so the dependent view did not decode", mean)
	}
}

// The decoder's own single-view mode is the control: without the dependent view
// the frame is one eye wide, which is what makes the stacked width meaningful.
func TestSingleViewIsHalfTheStackedWidth(t *testing.T) {
	bin := edge264Bin(t)
	dir := fixtureDir(t)
	out, err := exec.Command(bin, "-y", "-k", "-o", filepath.Join(dir, "mvc_combined.264")).Output()
	if err != nil {
		t.Fatalf("decoding base view only: %v", err)
	}
	if h := y4mHeader(out); !strings.Contains(h, "W640 H480") {
		t.Errorf("base-view header = %q, want 640x480", h)
	}
}
