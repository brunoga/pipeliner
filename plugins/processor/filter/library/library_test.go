package library

import (
	"context"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/mediaserver"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
)

// fakeDirEntry satisfies fs.DirEntry for walk stubs.
type fakeDirEntry struct{ dir bool }

func (f fakeDirEntry) Name() string               { return "" }
func (f fakeDirEntry) IsDir() bool                { return f.dir }
func (f fakeDirEntry) Type() fs.FileMode          { return 0 }
func (f fakeDirEntry) Info() (fs.FileInfo, error) { return nil, nil }

// newTestPlugin builds a plugin whose walk yields the given fake file paths.
func newTestPlugin(t *testing.T, cfg map[string]any, files []string) *libraryPlugin {
	t.Helper()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["paths"]; !ok {
		cfg["paths"] = []string{"/library"}
	}
	pl, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	p := pl.(*libraryPlugin)
	p.walk = func(root string, fn fs.WalkDirFunc) error {
		for _, f := range files {
			if !strings.HasPrefix(f, root) && root != "/library" {
				continue
			}
			if err := fn(f, fakeDirEntry{}, nil); err != nil {
				return err
			}
		}
		return nil
	}
	return p
}

func testTC() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

func mkEntry(title string, fields map[string]any) *entry.Entry {
	e := entry.New(title, "https://example.com/"+strings.ReplaceAll(title, " ", "-"))
	for k, v := range fields {
		e.Fields[k] = v
	}
	return e
}

func process(t *testing.T, p *libraryPlugin, e *entry.Entry) {
	t.Helper()
	if _, err := p.Process(context.Background(), testTC(), []*entry.Entry{e}); err != nil {
		t.Fatalf("Process: %v", err)
	}
}

func TestRejectsEpisodeAlreadyInLibrary(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/Breaking Bad/Breaking.Bad.S01E01.720p.WEB-DL.mkv"})
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("Breaking.Bad.S01E01.720p.WEB-DL"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatalf("equal-quality episode should be rejected, state=%v reason=%q", e.State, e.RejectReason)
	}
	if !strings.Contains(e.RejectReason, "already in library") {
		t.Errorf("reason: %q", e.RejectReason)
	}
}

func TestUpgradePassesWhenBetter(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/Breaking.Bad.S01E01.720p.WEB-DL.mkv"})
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("Breaking.Bad.S01E01.1080p.BluRay"))
	process(t, p, e)
	if e.IsRejected() {
		t.Fatalf("better release should pass as upgrade, reason=%q", e.RejectReason)
	}
}

func TestUpgradeDisabledRejectsBetter(t *testing.T) {
	p := newTestPlugin(t, map[string]any{"upgrade": false},
		[]string{"/library/Breaking.Bad.S01E01.720p.WEB-DL.mkv"})
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("Breaking.Bad.S01E01.1080p.BluRay"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("with upgrade=false a better release must still be rejected")
	}
}

func TestWorseReleaseRejected(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/Breaking.Bad.S01E01.1080p.BluRay.mkv"})
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("Breaking.Bad.S01E01.720p.WEB-DL"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("worse release must be rejected")
	}
}

func TestMissingEpisodePasses(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/Breaking.Bad.S01E01.720p.mkv"})
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E02"})
	process(t, p, e)
	if e.IsRejected() {
		t.Fatalf("missing episode must pass, reason=%q", e.RejectReason)
	}
}

func TestDecoratedReleaseTitleMatches(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/Breaking.Bad.S01E01.720p.WEB-DL.mkv"})
	// Title still carries release decoration (no metainfo cleanup upstream).
	e := mkEntry("Breaking.Bad.S01E01.720p.WEB-DL.x264-GRP",
		map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("720p WEB-DL"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("decorated release title should still match via parse fallback")
	}
}

func TestMovieMatchByTitleYear(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/movies/Dune.Part.Two.2024.1080p.BluRay.mkv"})
	e := mkEntry("Dune Part Two", map[string]any{
		entry.FieldMediaType: "movie", entry.FieldVideoYear: 2024})
	e.SetQuality(quality.Parse("1080p BluRay"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatalf("movie present at equal quality must be rejected, reason=%q", e.RejectReason)
	}
}

func TestMovieDecoratedTitleFallback(t *testing.T) {
	p := newTestPlugin(t, nil, []string{"/library/movies/Dune.Part.Two.2024.1080p.BluRay.mkv"})
	e := mkEntry("Dune.Part.Two.2024.720p.WEBRip.x265-GRP", map[string]any{
		entry.FieldMediaType: "movie"})
	e.SetQuality(quality.Parse("720p WEBRip"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("decorated movie release should match via parse fallback")
	}
}

func TestNonVideoExtensionsIgnored(t *testing.T) {
	p := newTestPlugin(t, nil, []string{
		"/library/Breaking.Bad.S01E01.720p.nfo",
		"/library/Breaking.Bad.S01E01.720p.srt",
	})
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	process(t, p, e)
	if e.IsRejected() {
		t.Fatal("non-video files must not create index entries")
	}
}

func TestIndexCachedWithinTTL(t *testing.T) {
	calls := 0
	p := newTestPlugin(t, map[string]any{"ttl": "1h"}, nil)
	inner := p.walk
	p.walk = func(root string, fn fs.WalkDirFunc) error { calls++; return inner(root, fn) }
	e1 := mkEntry("X", nil)
	e2 := mkEntry("Y", nil)
	process(t, p, e1)
	process(t, p, e2)
	if calls != 1 {
		t.Fatalf("walk should run once within ttl, got %d", calls)
	}
	p.builtAt = time.Now().Add(-2 * time.Hour)
	process(t, p, e1)
	if calls != 2 {
		t.Fatalf("walk should rerun after ttl, got %d", calls)
	}
}

func TestBestQualityWinsInIndex(t *testing.T) {
	p := newTestPlugin(t, nil, []string{
		"/library/Breaking.Bad.S01E01.720p.WEB-DL.mkv",
		"/library/Breaking.Bad.S01E01.1080p.BluRay.mkv",
	})
	// 1080p in library: an incoming 720p entry with upgrade=true must reject
	// against the BEST copy, not the worst.
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("720p WEB-DL"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("index must keep the best quality per item")
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing paths must fail validation")
	}
	if errs := validate(map[string]any{"paths": []any{"/x"}, "backend": "plex"}); len(errs) == 0 {
		t.Error("unsupported backend must fail validation")
	}
	if errs := validate(map[string]any{"paths": []any{"/x"}, "bogus": 1}); len(errs) == 0 {
		t.Error("unknown key must fail validation")
	}
	if errs := validate(map[string]any{"paths": []any{"/x"}, "ttl": "15m"}); len(errs) != 0 {
		t.Errorf("valid config must pass, got %v", errs)
	}
}

func TestWalkErrorSkipsSubtree(t *testing.T) {
	p := newTestPlugin(t, nil, nil)
	p.walk = func(root string, fn fs.WalkDirFunc) error {
		// One unreadable entry, then a good one — indexing must continue.
		_ = fn(filepath.Join(root, "bad"), fakeDirEntry{}, fs.ErrPermission)
		return fn(filepath.Join(root, "Breaking.Bad.S01E01.720p.mkv"), fakeDirEntry{}, nil)
	}
	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("720p"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("indexing must survive per-entry walk errors")
	}
}

// fakeMSClient backs the server-backend index test.
type fakeMSClient struct {
	items []mediaserver.Item
	err   error
	calls int
}

func (f *fakeMSClient) ListItems(context.Context) ([]mediaserver.Item, error) {
	f.calls++
	return f.items, f.err
}
func (f *fakeMSClient) Refresh(context.Context) error { return nil }

func TestServerBackendIndexAndStaleKeep(t *testing.T) {
	f := &fakeMSClient{items: []mediaserver.Item{
		{Type: "episode", Show: "Breaking Bad", Season: 1, Episode: 1, Resolution: "1080p"},
		{Type: "movie", Title: "Dune Part Two", Year: 2024, Resolution: "2160p"},
	}}
	pl, err := newPlugin(map[string]any{"backend": "plex", "url": "http://x", "token": "t"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	p := pl.(*libraryPlugin)
	p.client = f

	e := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e.SetQuality(quality.Parse("720p WEB-DL"))
	process(t, p, e)
	if !e.IsRejected() {
		t.Fatal("server-indexed episode at better quality must reject a worse release")
	}

	// Server goes down after the index exists: keep the previous index
	// instead of treating the library as empty.
	f.err = fs.ErrPermission
	p.builtAt = time.Now().Add(-2 * time.Hour)
	e2 := mkEntry("Breaking Bad", map[string]any{entry.FieldSeriesEpisodeID: "S01E01"})
	e2.SetQuality(quality.Parse("720p WEB-DL"))
	process(t, p, e2)
	if !e2.IsRejected() {
		t.Fatal("stale index must be kept when the server is unreachable")
	}
}

func TestServerBackendValidation(t *testing.T) {
	if errs := validate(map[string]any{"backend": "plex"}); len(errs) == 0 {
		t.Error("plex backend without url/token must fail validation")
	}
	if errs := validate(map[string]any{"backend": "jellyfin", "url": "http://x", "token": "t"}); len(errs) != 0 {
		t.Errorf("valid jellyfin config: %v", errs)
	}
}
