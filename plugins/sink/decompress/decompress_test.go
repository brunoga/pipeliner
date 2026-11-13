package decompress

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/binary"
	"hash/crc32"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// --- a RAR 4 writer, for fixtures ---
//
// Nothing on a build machine can write RAR (the format's compressor is
// proprietary), so the tests build stored (uncompressed) RAR 4 archives
// themselves. Stored is still RAR: the same marker, block headers, CRCs and
// volume flags a scene release uses, and the same reader extracts it.

func rarBlock(htype byte, flags uint16, body []byte) []byte {
	h := []byte{htype, 0, 0, 0, 0}
	binary.LittleEndian.PutUint16(h[1:], flags)
	binary.LittleEndian.PutUint16(h[3:], uint16(7+len(body)))
	h = append(h, body...)
	crc := make([]byte, 2)
	binary.LittleEndian.PutUint16(crc, uint16(crc32.ChecksumIEEE(h)))
	return append(crc, h...)
}

// rarVolumes returns one RAR volume per chunk of content, all holding the
// one file name; one chunk is a plain single-volume archive.
func rarVolumes(name string, chunks [][]byte, newNaming bool) [][]byte {
	var full []byte
	for _, c := range chunks {
		full = append(full, c...)
	}
	sum := crc32.ChecksumIEEE(full)
	multi := len(chunks) > 1
	var vols [][]byte
	for i, c := range chunks {
		var v bytes.Buffer
		v.Write([]byte{0x52, 0x61, 0x72, 0x21, 0x1a, 0x07, 0x00})
		var arcFlags uint16
		if multi {
			arcFlags |= 0x0001
			if newNaming {
				arcFlags |= 0x0010
			}
		}
		v.Write(rarBlock(0x73, arcFlags, make([]byte, 6)))

		fileFlags := uint16(0x8000)
		if i > 0 {
			fileFlags |= 0x0001
		}
		if i < len(chunks)-1 {
			fileFlags |= 0x0002
		}
		body := make([]byte, 25)
		binary.LittleEndian.PutUint32(body[0:], uint32(len(c)))
		binary.LittleEndian.PutUint32(body[4:], uint32(len(full)))
		body[8] = 3 // Unix
		binary.LittleEndian.PutUint32(body[9:], sum)
		body[17] = 20   // unpack version
		body[18] = 0x30 // stored
		binary.LittleEndian.PutUint16(body[19:], uint16(len(name)))
		binary.LittleEndian.PutUint32(body[21:], 0o644)
		body = append(body, name...)
		v.Write(rarBlock(0x74, fileFlags, body))
		v.Write(c)

		var endFlags uint16
		if i < len(chunks)-1 {
			endFlags = 0x0001
		}
		v.Write(rarBlock(0x7b, endFlags, nil))
		vols = append(vols, v.Bytes())
	}
	return vols
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func zipOf(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.WriteString(w, data)
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func newDecompress(t *testing.T, cfg map[string]any) *decompressPlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	return p.(*decompressPlugin)
}

// torrentEntry is an entry as torrent_session emits it: the torrent's name
// and the directory its data lives in.
func torrentEntry(name, dir string) *entry.Entry {
	e := entry.New(name, "torrent://abc")
	e.Set(entry.FieldTorrentDownloadDir, dir)
	return e
}

// TestSeasonPackFromStagingLandsInTheSeriesFolder is the case the plugin is
// for: a finished torrent in staging, one RAR set per episode in its own
// folder (old-style .r00 volumes for one, part1/part2 for the other), a
// sample beside them. Each episode lands in the matching series folder; the
// sample, the release's folder names and the archives do not.
func TestSeasonPackFromStagingLandsInTheSeriesFolder(t *testing.T) {
	tmp := t.TempDir()
	staging, series := filepath.Join(tmp, "staging"), filepath.Join(tmp, "series")
	dir := filepath.Join(staging, "Agatha All Along", "Season 01")
	pack := filepath.Join(dir, "Agatha.All.Along.S01.1080p-GRP")

	e1 := rarVolumes("Agatha.All.Along.S01E01.mkv", [][]byte{[]byte("episode one, "), []byte("part two")}, false)
	write(t, filepath.Join(pack, "E01", "e01.rar"), e1[0])
	write(t, filepath.Join(pack, "E01", "e01.r00"), e1[1])
	e2 := rarVolumes("Agatha.All.Along.S01E02.mkv", [][]byte{[]byte("episode "), []byte("two")}, true)
	write(t, filepath.Join(pack, "E02", "e02.part1.rar"), e2[0])
	write(t, filepath.Join(pack, "E02", "e02.part2.rar"), e2[1])
	write(t, filepath.Join(pack, "Sample", "sample.rar"), rarVolumes("sample.mkv", [][]byte{[]byte("clip")}, false)[0])

	p := newDecompress(t, map[string]any{"to": series, "mirror_from": staging})
	e := torrentEntry("Agatha.All.Along.S01.1080p-GRP", dir)
	e.Accept()
	if err := p.Consume(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatal(err)
	}
	if e.IsFailed() {
		t.Fatalf("entry failed: %s", e.FailReason)
	}
	out := filepath.Join(series, "Agatha All Along", "Season 01")
	if got := read(t, filepath.Join(out, "Agatha.All.Along.S01E01.mkv")); got != "episode one, part two" {
		t.Errorf("E01 = %q", got)
	}
	if got := read(t, filepath.Join(out, "Agatha.All.Along.S01E02.mkv")); got != "episode two" {
		t.Errorf("E02 = %q", got)
	}
	entries, _ := os.ReadDir(out)
	var names []string
	for _, d := range entries {
		names = append(names, d.Name())
	}
	if len(names) != 2 {
		t.Errorf("series folder holds %v, want the two episodes only", names)
	}
	if _, err := os.Stat(filepath.Join(pack, "E01", "e01.r00")); err != nil {
		t.Error("archives must stay in staging unless delete_archive is set")
	}
}

// TestAnExistingFileIsNeverOverwritten, and a second run over the same
// torrent is harmless.
func TestAnExistingFileIsNeverOverwritten(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "in", "a.rar"), rarVolumes("ep.mkv", [][]byte{[]byte("new")}, false)[0])
	write(t, filepath.Join(tmp, "out", "ep.mkv"), []byte("old"))

	p := newDecompress(t, map[string]any{"to": filepath.Join(tmp, "out")})
	e := entry.New("a", "file://a")
	e.Set(entry.FieldFileLocation, filepath.Join(tmp, "in", "a.rar"))
	if err := p.Consume(context.Background(), tc(), []*entry.Entry{e}); err != nil || e.IsFailed() {
		t.Fatalf("err=%v failed=%v %s", err, e.IsFailed(), e.FailReason)
	}
	if got := read(t, filepath.Join(tmp, "out", "ep.mkv")); got != "old" {
		t.Errorf("existing file overwritten: %q", got)
	}
	if _, err := os.Stat(filepath.Join(tmp, "out", "ep.mkv.partial")); err == nil {
		t.Error("a .partial file was left behind")
	}
}

// TestAZipCannotWriteOutsideTheDestination: an archive's names are not
// trusted.
func TestAZipCannotWriteOutsideTheDestination(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "in", "evil.zip"), zipOf(t, map[string]string{"../../escaped.txt": "x"}))
	p := newDecompress(t, map[string]any{"to": filepath.Join(tmp, "out")})
	e := entry.New("evil", "file://evil")
	e.Set(entry.FieldFileLocation, filepath.Join(tmp, "in", "evil.zip"))
	_ = p.Consume(context.Background(), tc(), []*entry.Entry{e})
	if !e.IsFailed() || !strings.Contains(e.FailReason, "escapes") {
		t.Errorf("want a failure naming the escape, got failed=%v %q", e.IsFailed(), e.FailReason)
	}
	if _, err := os.Stat(filepath.Join(tmp, "escaped.txt")); err == nil {
		t.Error("a file was written outside the destination")
	}
}

func TestZipExtracts(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "in", "s.zip"), zipOf(t, map[string]string{"Show/S01E01.mkv": "one"}))
	p := newDecompress(t, map[string]any{"to": filepath.Join(tmp, "out"), "keep_dirs": false})
	e := entry.New("s", "file://s")
	e.Set(entry.FieldFileLocation, filepath.Join(tmp, "in", "s.zip"))
	if err := p.Consume(context.Background(), tc(), []*entry.Entry{e}); err != nil || e.IsFailed() {
		t.Fatalf("err=%v %s", err, e.FailReason)
	}
	if got := read(t, filepath.Join(tmp, "out", "S01E01.mkv")); got != "one" {
		t.Errorf("got %q", got)
	}
}

// TestOutsideMirrorFromIsRefused: a torrent whose directory is not below
// mirror_from has no destination to map to, and guessing one could write
// anywhere.
func TestOutsideMirrorFromIsRefused(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "elsewhere", "t", "a.rar"), rarVolumes("x.mkv", [][]byte{[]byte("x")}, false)[0])
	p := newDecompress(t, map[string]any{"to": filepath.Join(tmp, "series"), "mirror_from": filepath.Join(tmp, "staging")})
	e := torrentEntry("t", filepath.Join(tmp, "elsewhere"))
	_ = p.Consume(context.Background(), tc(), []*entry.Entry{e})
	if !e.IsFailed() || !strings.Contains(e.FailReason, "not under mirror_from") {
		t.Errorf("got failed=%v %q", e.IsFailed(), e.FailReason)
	}
}

func TestDeleteArchiveRemovesTheWholeSet(t *testing.T) {
	tmp := t.TempDir()
	vols := rarVolumes("ep.mkv", [][]byte{[]byte("a"), []byte("b"), []byte("c")}, false)
	for i, name := range []string{"x.rar", "x.r00", "x.r01"} {
		write(t, filepath.Join(tmp, "in", name), vols[i])
	}
	write(t, filepath.Join(tmp, "in", "x.nfo"), []byte("keep"))
	p := newDecompress(t, map[string]any{"to": filepath.Join(tmp, "out"), "delete_archive": true})
	e := entry.New("x", "file://x")
	e.Set(entry.FieldFileLocation, filepath.Join(tmp, "in", "x.rar"))
	if err := p.Consume(context.Background(), tc(), []*entry.Entry{e}); err != nil || e.IsFailed() {
		t.Fatalf("err=%v %s", err, e.FailReason)
	}
	if got := read(t, filepath.Join(tmp, "out", "ep.mkv")); got != "abc" {
		t.Errorf("got %q", got)
	}
	left, _ := os.ReadDir(filepath.Join(tmp, "in"))
	if len(left) != 1 || left[0].Name() != "x.nfo" {
		t.Errorf("left behind: %v", left)
	}
}

func TestDryRunExtractsNothing(t *testing.T) {
	tmp := t.TempDir()
	write(t, filepath.Join(tmp, "in", "a.rar"), rarVolumes("ep.mkv", [][]byte{[]byte("x")}, false)[0])
	p := newDecompress(t, map[string]any{"to": filepath.Join(tmp, "out")})
	e := entry.New("a", "file://a")
	e.Set(entry.FieldFileLocation, filepath.Join(tmp, "in", "a.rar"))
	ctx := tc()
	ctx.DryRun = true
	_ = p.Consume(context.Background(), ctx, []*entry.Entry{e})
	if _, err := os.Stat(filepath.Join(tmp, "out")); err == nil {
		t.Error("a dry run extracted")
	}
}

func TestKindOf(t *testing.T) {
	for name, want := range map[string]string{
		"a.rar": "rar", "A.RAR": "rar", "a.part1.rar": "rar", "a.part01.rar": "rar", "a.part001.rar": "rar",
		"a.part2.rar": "", "a.part10.rar": "", "a.r00": "", "a.r01": "",
		"a.zip": "zip", "a.7z": "7z", "a.7z.001": "7z", "a.7z.002": "",
		"a.mkv": "", "a.nfo": "",
	} {
		if got := kindOf(name); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestSourceOf(t *testing.T) {
	e := entry.New("Show.S01-GRP", "torrent://x")
	e.Set(entry.FieldTorrentDownloadDir, "/staging/Show/Season 01")
	if root, base := sourceOf(e); root != "/staging/Show/Season 01/Show.S01-GRP" || base != "/staging/Show/Season 01" {
		t.Errorf("torrent: root %q base %q", root, base)
	}
	f := entry.New("a", "file://a")
	f.Set(entry.FieldFileLocation, "/dl/a.rar")
	f.Set(entry.FieldTorrentDownloadDir, "/ignored")
	if root, base := sourceOf(f); root != "/dl/a.rar" || base != "/dl" {
		t.Errorf("file_location wins: root %q base %q", root, base)
	}
	if root, _ := sourceOf(entry.New("u", "/dl/b.zip")); root != "/dl/b.zip" {
		t.Errorf("URL fallback: %q", root)
	}
	if root, _ := sourceOf(entry.New("u", "http://x/page")); root != "" {
		t.Errorf("nothing to extract should be empty, got %q", root)
	}
}

// TestSystemToolsSkipExistingFiles: whichever extractor runs, an existing
// file is kept.
func TestSystemToolsSkipExistingFiles(t *testing.T) {
	for tool, flag := range map[string]string{"unrar": "-o-", "7z": "-aos", "unar": "-skip"} {
		if args := buildArgs(tool, "/a", "/dest", true); !slices.Contains(args, flag) {
			t.Errorf("%s args %v lack %s", tool, args, flag)
		}
	}
	if args := buildArgs("unrar", "/a", "/dest", false); args[0] != "e" {
		t.Errorf("unrar flatten should use e: %v", args)
	}
	if args := buildArgs("unar", "/a", "/dest", false); !slices.Contains(args, "-no-directory") {
		t.Errorf("unar flatten: %v", args)
	}
}

func TestToolSelection(t *testing.T) {
	if _, err := resolveTool("winzip"); err == nil {
		t.Error("an unknown tool must be refused")
	}
	if path, err := resolveTool("builtin"); err != nil || path != "" {
		t.Errorf("builtin needs nothing: %q %v", path, err)
	}
	p := newDecompress(t, map[string]any{"to": "/x"})
	if p.tool != "builtin" || !p.keepDirs {
		t.Errorf("defaults: tool %q keepDirs %v", p.tool, p.keepDirs)
	}
	if _, err := newPlugin(map[string]any{}, nil); err == nil {
		t.Error("'to' is required")
	}
}

// TestExtractedFilesTakeAfterTheDestination: a season folder made by an
// extraction gets the library root's permissions (setgid and group write
// included), so the download client can still move later episodes into it.
// Ownership follows too, which only root can change and is not tested here.
func TestExtractedFilesTakeAfterTheDestination(t *testing.T) {
	tmp := t.TempDir()
	series := filepath.Join(tmp, "series")
	if err := os.Mkdir(series, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(series, 0o775|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	staging := filepath.Join(tmp, "staging")
	dir := filepath.Join(staging, "Show", "Season 01")
	write(t, filepath.Join(dir, "Show.S01", "s.rar"), rarVolumes("Show.S01E01.mkv", [][]byte{[]byte("x")}, false)[0])

	p := newDecompress(t, map[string]any{"to": series, "mirror_from": staging})
	e := torrentEntry("Show.S01", dir)
	if err := p.Consume(context.Background(), tc(), []*entry.Entry{e}); err != nil || e.IsFailed() {
		t.Fatalf("err=%v %s", err, e.FailReason)
	}
	for path, want := range map[string]os.FileMode{
		filepath.Join(series, "Show"):                                 0o775 | os.ModeSetgid | os.ModeDir,
		filepath.Join(series, "Show", "Season 01"):                    0o775 | os.ModeSetgid | os.ModeDir,
		filepath.Join(series, "Show", "Season 01", "Show.S01E01.mkv"): 0o664,
	} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode() & (os.ModePerm | os.ModeSetgid | os.ModeDir); got != want {
			t.Errorf("%s: mode %v, want %v", path, got, want)
		}
	}
}
