package delete

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func exists(path string) bool { _, err := os.Lstat(path); return err == nil }

func run(t *testing.T, cfg map[string]any, dry bool, paths ...string) []*entry.Entry {
	t.Helper()
	pl, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	var es []*entry.Entry
	for _, p := range paths {
		e := entry.New(filepath.Base(p), "file://"+p)
		e.Set(entry.FieldFileLocation, p)
		e.Accept()
		es = append(es, e)
	}
	ctx := tc()
	ctx.DryRun = dry
	if err := pl.(plugin.SinkPlugin).Consume(context.Background(), ctx, es); err != nil {
		t.Fatal(err)
	}
	return es
}

// TestDeletesAndPrunesUpToTheRoot: the season and show folders a deletion
// empties go, the root stays even when emptied -- which is what rmdir -p
// would have got wrong for a folder like 3d-mvc-inbox.
func TestDeletesAndPrunesUpToTheRoot(t *testing.T) {
	tmp := t.TempDir()
	inbox := filepath.Join(tmp, "3d-mvc-inbox")
	series := filepath.Join(tmp, "series")
	a := filepath.Join(inbox, "Film (2012)", "film.mkv")
	b := filepath.Join(series, "Show", "Season 01", "e1.mkv")
	keep := filepath.Join(series, "Show", "Season 02", "e1.mkv")
	for _, f := range []string{a, b, keep} {
		touch(t, f)
	}
	es := run(t, map[string]any{"roots": []any{inbox, series}}, false, a, b)
	for _, e := range es {
		if e.IsFailed() {
			t.Fatalf("%s: %s", e.Title, e.FailReason)
		}
	}
	if exists(a) || exists(b) || exists(filepath.Join(inbox, "Film (2012)")) || exists(filepath.Join(series, "Show", "Season 01")) {
		t.Error("files and the folders they emptied should be gone")
	}
	if !exists(inbox) {
		t.Error("an emptied root must stay")
	}
	if !exists(keep) || !exists(filepath.Join(series, "Show")) {
		t.Error("a folder that still holds something must stay")
	}
}

// TestNothingOutsideTheRoots: a path outside every root, a root itself, a
// path that climbs out with "..", a directory and a relative path all fail
// and delete nothing.
func TestNothingOutsideTheRoots(t *testing.T) {
	tmp := t.TempDir()
	root := filepath.Join(tmp, "media")
	outside := filepath.Join(tmp, "elsewhere", "f.mkv")
	dir := filepath.Join(root, "Show")
	touch(t, outside)
	touch(t, filepath.Join(dir, "e.mkv"))
	es := run(t, map[string]any{"roots": []any{root}}, false,
		outside, root, filepath.Join(root, "..", "elsewhere", "f.mkv"), dir, "media/Show/e.mkv")
	for _, e := range es {
		if !e.IsFailed() {
			t.Errorf("%s should have failed", e.GetString(entry.FieldFileLocation))
		}
	}
	if !exists(outside) || !exists(dir) {
		t.Error("nothing outside the roots, and no directory, may be deleted")
	}
}

func TestDryRunDeletesNothing(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "m", "f.mkv")
	touch(t, f)
	run(t, map[string]any{"roots": []any{filepath.Join(tmp, "m")}}, true, f)
	if !exists(f) {
		t.Error("a dry run deleted a file")
	}
}

func TestSymlinkIsRemovedNotFollowed(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "elsewhere", "real.mkv")
	link := filepath.Join(tmp, "m", "link.mkv")
	touch(t, target)
	if err := os.MkdirAll(filepath.Dir(link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	run(t, map[string]any{"roots": []any{filepath.Join(tmp, "m")}}, false, link)
	if exists(link) || !exists(target) {
		t.Error("the link should go and its target stay")
	}
}

func TestValidate(t *testing.T) {
	for _, cfg := range []map[string]any{
		{}, {"roots": []any{}}, {"roots": []any{"relative"}}, {"roots": []any{"/"}}, {"roots": []any{"/m"}, "x": 1},
	} {
		if errs := validate(cfg); len(errs) == 0 {
			t.Errorf("%v should be refused", cfg)
		}
	}
	if errs := validate(map[string]any{"roots": []any{"/data/media/series"}, "prune_empty": false}); len(errs) != 0 {
		t.Errorf("valid config refused: %v", errs)
	}
}
