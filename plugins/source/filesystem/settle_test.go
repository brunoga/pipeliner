package filesystem

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

// clock is a settable time source, so a settling window can be crossed
// without sleeping through it.
type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

type harness struct {
	dir   string
	plug  *filesystemPlugin
	clock *clock
	task  string
}

// newHarness builds a plugin over a temp directory with a real in-memory
// store, and installs a settable clock.
func newHarness(t *testing.T, cfg map[string]any) *harness {
	t.Helper()
	dir := t.TempDir()
	if cfg == nil {
		cfg = map[string]any{}
	}
	if _, ok := cfg["path"]; !ok {
		cfg["path"] = dir
	}
	db, err := store.OpenSQLiteNoMigrate(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	p, err := newFilesystemPlugin(cfg, db)
	if err != nil {
		t.Fatalf("build plugin: %v", err)
	}
	c := &clock{t: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	orig := Now
	t.Cleanup(func() { Now = orig })
	Now = c.now
	return &harness{dir: dir, plug: p.(*filesystemPlugin), clock: c, task: "watch"}
}

// scanNames runs one Generate and returns the emitted filenames, sorted.
func (h *harness) scanNames(t *testing.T) []string {
	t.Helper()
	got, err := h.plug.Generate(context.Background(),
		&plugin.TaskContext{Name: h.task})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	out := make([]string, 0, len(got))
	for _, e := range got {
		out = append(out, filepath.Base(e.GetString(entry.FieldFileLocation)))
	}
	sort.Strings(out)
	return out
}

// write creates a file (and any parent directories) with size bytes, and an
// explicit modification time. The timestamp is set deliberately: these tests
// must show that settling does not depend on it.
func (h *harness) write(t *testing.T, rel string, size int, mod time.Time) {
	t.Helper()
	full := filepath.Join(h.dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, make([]byte, size), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	if err := os.Chtimes(full, mod, mod); err != nil {
		t.Fatalf("chtimes %s: %v", rel, err)
	}
}

func eq(t *testing.T, got, want []string, msg string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v, want %v", msg, got, want)
		return
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("%s: got %v, want %v", msg, got, want)
			return
		}
	}
}

// Nothing is emitted on first sight. One observation cannot tell a finished
// delivery from a paused one, so the window has to start somewhere.
func TestNothingSettlesOnFirstSight(t *testing.T) {
	h := newHarness(t, map[string]any{"stable_for": "2m"})
	h.write(t, "a.iso", 10, h.clock.now())

	eq(t, h.scanNames(t), nil, "first scan")
}

// Held unchanged across the window, the item is emitted.
func TestUnchangedItemSettlesAfterTheWindow(t *testing.T) {
	h := newHarness(t, map[string]any{"stable_for": "2m"})
	h.write(t, "a.iso", 10, h.clock.now())

	eq(t, h.scanNames(t), nil, "first scan")
	h.clock.advance(time.Minute)
	eq(t, h.scanNames(t), nil, "before the window elapses")
	h.clock.advance(time.Minute)
	eq(t, h.scanNames(t), []string{"a.iso"}, "after the window")
}

// A size change restarts the window: this is the growing-file case.
func TestChangedItemRestartsTheWindow(t *testing.T) {
	h := newHarness(t, map[string]any{"stable_for": "2m"})
	mod := h.clock.now()
	h.write(t, "a.iso", 10, mod)

	eq(t, h.scanNames(t), nil, "first scan")
	h.clock.advance(3 * time.Minute)
	h.write(t, "a.iso", 20, mod) // still growing; timestamp pinned
	eq(t, h.scanNames(t), nil, "the file grew, so the window restarts")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"a.iso"}, "settled once it stopped growing")
}

// A modification-time change with no size change also restarts the window,
// which catches a file rewritten in place at the same length.
func TestTimestampChangeRestartsTheWindow(t *testing.T) {
	h := newHarness(t, map[string]any{"stable_for": "2m"})
	h.write(t, "a.iso", 10, h.clock.now())

	eq(t, h.scanNames(t), nil, "first scan")
	h.clock.advance(3 * time.Minute)
	h.write(t, "a.iso", 10, h.clock.now())
	eq(t, h.scanNames(t), nil, "rewritten in place, so the window restarts")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"a.iso"}, "settled")
}

// The settling unit is the item, not the file: a disc delivered as a BDMV tree
// is withheld while any part of it is still arriving, even though the file the
// mask selects has not changed since the first scan. This is the case a
// timestamp test on the matched file alone gets wrong — index.bdmv is small
// and written early, so it looks finished long before the streams do.
func TestTreeSettlesAsAWholeNotPerMatchedFile(t *testing.T) {
	h := newHarness(t, map[string]any{
		"recursive": true, "mask": "index.bdmv", "stable_for": "2m",
	})
	old := h.clock.now().Add(-72 * time.Hour)
	h.write(t, "Movie (2012)/BDMV/index.bdmv", 100, old)
	h.write(t, "Movie (2012)/BDMV/STREAM/00001.m2ts", 1000, old)

	eq(t, h.scanNames(t), nil, "first scan")
	h.clock.advance(3 * time.Minute)
	// The rest of the disc is still arriving. index.bdmv is untouched.
	h.write(t, "Movie (2012)/BDMV/STREAM/00002.m2ts", 2000, old)
	eq(t, h.scanNames(t), nil, "a new stream appeared, so the disc is not settled")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"index.bdmv"},
		"settled once the tree stopped growing, emitting only the masked file")
}

// Every file in that test carried a timestamp three days old, which is what
// `rsync -a` produces: it preserves the source's modification times, so a file
// that arrived seconds ago can look ancient. Settling must not be fooled by
// it. Asserted on its own because it is the case that ruled out a simpler
// age-based test.
func TestStaleTimestampsDoNotImplySettled(t *testing.T) {
	h := newHarness(t, map[string]any{"recursive": true, "stable_for": "2m"})
	ancient := h.clock.now().Add(-30 * 24 * time.Hour)
	h.write(t, "Movie/part1.m2ts", 1000, ancient)

	// Well past the window on the very first scan, if age were the test.
	eq(t, h.scanNames(t), nil, "an ancient timestamp is not evidence of arrival")
	h.clock.advance(3 * time.Minute)
	h.write(t, "Movie/part2.m2ts", 1000, ancient)
	eq(t, h.scanNames(t), nil, "still arriving, however old the timestamps claim to be")
}

// Items settle on their own clocks, so one delivery in progress does not hold
// back another that has finished.
func TestItemsSettleIndependently(t *testing.T) {
	h := newHarness(t, map[string]any{"recursive": true, "stable_for": "2m"})
	mod := h.clock.now()
	h.write(t, "done/a.iso", 10, mod)

	eq(t, h.scanNames(t), nil, "first scan")
	h.clock.advance(3 * time.Minute)
	h.write(t, "arriving/b.iso", 10, mod)
	eq(t, h.scanNames(t), []string{"a.iso"}, "the finished item goes, the new one waits")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"a.iso", "b.iso"}, "both settled")
}

// The mask decides what is emitted, never what counts as activity.
func TestMaskDoesNotAffectSettling(t *testing.T) {
	h := newHarness(t, map[string]any{
		"recursive": true, "mask": "*.iso", "stable_for": "2m",
	})
	mod := h.clock.now()
	h.write(t, "Movie/disc.iso", 10, mod)

	eq(t, h.scanNames(t), nil, "first scan")
	h.clock.advance(3 * time.Minute)
	// A file the mask excludes, arriving in the same item, still means the
	// item changed.
	h.write(t, "Movie/notes.txt", 5, mod)
	eq(t, h.scanNames(t), nil, "an unmasked file still counts as the item changing")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"disc.iso"}, "only the masked file is emitted")
}

// Snapshots are dropped as items leave, so the bucket does not accumulate a
// record for every delivery ever processed.
func TestSnapshotsArePrunedWhenItemsGoAway(t *testing.T) {
	h := newHarness(t, map[string]any{"stable_for": "2m"})
	h.write(t, "a.iso", 10, h.clock.now())
	h.scanNames(t)

	bucket := h.plug.db.Bucket(SettleBucketPrefix + h.task)
	keys, err := bucket.Keys()
	if err != nil || len(keys) != 1 {
		t.Fatalf("after first scan: keys=%v err=%v, want one snapshot", keys, err)
	}

	if err := os.Remove(filepath.Join(h.dir, "a.iso")); err != nil {
		t.Fatalf("remove: %v", err)
	}
	h.clock.advance(3 * time.Minute)
	h.scanNames(t)

	keys, err = bucket.Keys()
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("snapshots left after the item went away: %v", keys)
	}
}

// Two pipelines watching one directory keep independent observations: neither
// may declare an item settled on the other's behalf, or the second pipeline
// would process a delivery it had never seen before.
func TestSettlingIsScopedPerTask(t *testing.T) {
	h := newHarness(t, map[string]any{"stable_for": "2m"})
	h.write(t, "a.iso", 10, h.clock.now())

	eq(t, h.scanNames(t), nil, "first task, first scan")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"a.iso"}, "first task settled it")

	h.task = "other"
	eq(t, h.scanNames(t), nil, "the second task has not observed it yet")
	h.clock.advance(3 * time.Minute)
	eq(t, h.scanNames(t), []string{"a.iso"}, "and settles it on its own clock")
}

// Snapshot keys carry the watched root, so one task may watch two directories
// holding same-named items without them being confused for each other.
func TestSnapshotKeysAreScopedPerRoot(t *testing.T) {
	a := newSettleStore(newMemBucket(), "/watch/one")
	b := newSettleStore(newMemBucket(), "/watch/two")
	if a.key("Movie") == b.key("Movie") {
		t.Error("two roots produced the same key for the same item name")
	}
}

// Without stable_for the plugin behaves exactly as it always has, so existing
// configs are untouched — including needing no database.
func TestWithoutStableForNoStateIsNeeded(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.iso"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	p, err := newFilesystemPlugin(map[string]any{"path": dir}, nil)
	if err != nil {
		t.Fatalf("build plugin without a store: %v", err)
	}
	got, err := p.(*filesystemPlugin).Generate(context.Background(), &plugin.TaskContext{})
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(got) != 1 {
		t.Errorf("emitted %d entries, want the file straight away", len(got))
	}
}

// stable_for needs somewhere to record what it saw; saying so at build time
// beats a scan that silently never emits anything.
func TestStableForRequiresAStore(t *testing.T) {
	_, err := newFilesystemPlugin(map[string]any{"path": t.TempDir(), "stable_for": "2m"}, nil)
	if err == nil {
		t.Fatal("stable_for without a store should be refused")
	}
}

func TestStableForRejectsBadDurations(t *testing.T) {
	db, err := store.OpenSQLiteNoMigrate(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer func() { _ = db.Close() }()
	for _, bad := range []string{"soon", "2", "-5m"} {
		if _, err := newFilesystemPlugin(
			map[string]any{"path": t.TempDir(), "stable_for": bad}, db); err == nil {
			t.Errorf("stable_for %q should be rejected", bad)
		}
	}
	if errs := validate(map[string]any{"path": "/x", "stable_for": "nope"}); len(errs) == 0 {
		t.Error("validate should reject a malformed stable_for")
	}
	if errs := validate(map[string]any{"path": "/x", "stable_for": "2m"}); len(errs) != 0 {
		t.Errorf("validate rejected a good stable_for: %v", errs)
	}
}

// --- fingerprint and grouping ---

func TestFingerprintDistinguishesContents(t *testing.T) {
	at := time.Unix(1700000000, 0)
	base := item{name: "m", files: []scanned{
		{rel: "m/a", size: 10, modTime: at},
		{rel: "m/b", size: 20, modTime: at},
	}}
	same := item{name: "m", files: []scanned{
		// Order must not matter: WalkDir order is not a promise.
		{rel: "m/b", size: 20, modTime: at},
		{rel: "m/a", size: 10, modTime: at},
	}}
	if fingerprint(base) != fingerprint(same) {
		t.Error("file order changed the fingerprint")
	}
	for name, mutate := range map[string]func(*item){
		"a file grew":        func(i *item) { i.files[0].size++ },
		"a file was touched": func(i *item) { i.files[0].modTime = at.Add(time.Second) },
		"a file was renamed": func(i *item) { i.files[0].rel = "m/z" },
		"a file appeared":    func(i *item) { i.files = append(i.files, scanned{rel: "m/c", size: 1, modTime: at}) },
		"a file went away":   func(i *item) { i.files = i.files[:1] },
	} {
		changed := item{name: "m", files: append([]scanned(nil), base.files...)}
		mutate(&changed)
		if fingerprint(changed) == fingerprint(base) {
			t.Errorf("%s: fingerprint did not change", name)
		}
	}
}

func TestGroupItemsUsesTheTopLevelComponent(t *testing.T) {
	files := []scanned{
		{rel: filepath.Join("Movie", "BDMV", "index.bdmv")},
		{rel: filepath.Join("Movie", "BDMV", "STREAM", "1.m2ts")},
		{rel: filepath.Join("Other", "x.iso")},
		{rel: "loose.iso"}, // a file at the top level is its own item
	}
	got := groupItems(files)
	if len(got) != 3 {
		t.Fatalf("got %d items, want 3: %+v", len(got), got)
	}
	want := map[string]int{"Movie": 2, "Other": 1, "loose.iso": 1}
	for _, it := range got {
		if want[it.name] != len(it.files) {
			t.Errorf("item %q has %d files, want %d", it.name, len(it.files), want[it.name])
		}
	}
}

// memBucket is a store.Bucket over a map, for the few assertions that are
// about key construction rather than persistence.
type memBucket struct{ m map[string][]byte }

func newMemBucket() *memBucket { return &memBucket{m: map[string][]byte{}} }

func (b *memBucket) Put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.m[key] = raw
	return nil
}

func (b *memBucket) Get(key string, dest any) (bool, error) {
	raw, ok := b.m[key]
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dest)
}

func (b *memBucket) Delete(key string) error { delete(b.m, key); return nil }

func (b *memBucket) Keys() ([]string, error) {
	out := make([]string, 0, len(b.m))
	for k := range b.m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (b *memBucket) All() (map[string][]byte, error) { return b.m, nil }
