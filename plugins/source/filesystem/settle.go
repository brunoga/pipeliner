package filesystem

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/store"
)

// SettleBucketPrefix namespaces the settle snapshots. One bucket per task, so
// two pipelines watching the same directory keep independent observations and
// neither can declare an item settled on the other's behalf.
const SettleBucketPrefix = "filesystem_settle:"

// scanned is one file the walk visited.
type scanned struct {
	path    string // absolute
	rel     string // relative to the configured root
	size    int64
	modTime time.Time
	matches bool // passes the configured mask
}

// snapshot is what a previous scan observed about one item.
type snapshot struct {
	// Fingerprint identifies the item's contents: every file beneath it, with
	// its size and modification time.
	Fingerprint string `json:"fingerprint"`
	// FirstSeen is when this fingerprint was first observed. It is *not*
	// refreshed while the fingerprint holds, which is the whole point: the
	// window is measured from when the content stopped changing.
	FirstSeen time.Time `json:"first_seen"`
}

// item groups the files that arrive together. The unit is the top-level child
// of the watched directory, because that is the unit things are delivered in:
// a download client, a torrent and an rsync all produce one directory (or one
// file) per release.
type item struct {
	name  string // top-level component, or the filename for a top-level file
	files []scanned
}

// groupItems buckets scanned files by the top-level component of their
// relative path.
func groupItems(files []scanned) []item {
	byName := map[string][]scanned{}
	var order []string
	for _, f := range files {
		name := f.rel
		if i := strings.IndexRune(name, filepath.Separator); i > 0 {
			name = name[:i]
		}
		if _, ok := byName[name]; !ok {
			order = append(order, name)
		}
		byName[name] = append(byName[name], f)
	}
	sort.Strings(order)
	out := make([]item, 0, len(order))
	for _, n := range order {
		out = append(out, item{name: n, files: byName[n]})
	}
	return out
}

// fingerprint digests every file in the item: its path, size and modification
// time.
//
// Size alone would miss a file rewritten in place at the same length;
// modification time alone would miss a writer that restores the original
// timestamp, which is exactly what rsync -t does — and `rsync -a` implies it,
// so a file freshly arrived from a sync can carry a timestamp days old. Taking
// both means a change has to hide from both to go unnoticed.
//
// The mask is deliberately not applied. Whether a disc has finished arriving
// depends on its streams, not on the one small index file a mask might select;
// what the mask decides is which files are *emitted*, not what counts as
// activity.
func fingerprint(it item) string {
	lines := make([]string, 0, len(it.files))
	for _, f := range it.files {
		lines = append(lines, f.rel+"\x00"+strconv.FormatInt(f.size, 10)+
			"\x00"+strconv.FormatInt(f.modTime.UnixNano(), 10))
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	// The file count is implicit in the lines, but writing it makes a
	// fingerprint collision between differently-sized sets impossible rather
	// than merely improbable.
	// hash.Hash never errors, so the write is unchecked deliberately.
	_, _ = fmt.Fprintf(h, "n=%d", len(it.files))
	return hex.EncodeToString(h.Sum(nil))
}

// settleStore records what each item looked like on previous scans.
type settleStore struct {
	bucket store.Bucket
	root   string
}

func newSettleStore(b store.Bucket, root string) *settleStore {
	return &settleStore{bucket: b, root: root}
}

// key scopes a snapshot to the watched root as well as the item, so one task
// may watch two directories that happen to hold same-named items.
func (s *settleStore) key(name string) string { return s.root + "\x00" + name }

// settled reports whether the item has held the same contents for at least
// window, recording the current observation when it has not.
//
// An item is never settled on first sight. That costs one scan interval of
// latency and buys the only guarantee that matters: nothing is handed
// downstream until it has been observed twice, unchanged, across the window.
// A single observation cannot distinguish a finished delivery from one that is
// paused, and for content arriving as a directory tree there is nothing in a
// single observation to inspect — the tree simply has fewer files in it than
// it will have.
func (s *settleStore) settled(it item, window time.Duration, now time.Time) (bool, error) {
	fp := fingerprint(it)
	k := s.key(it.name)

	var prev snapshot
	found, err := s.bucket.Get(k, &prev)
	if err != nil {
		return false, fmt.Errorf("read settle snapshot for %q: %w", it.name, err)
	}
	if found && prev.Fingerprint == fp {
		return !now.Before(prev.FirstSeen.Add(window)), nil
	}
	// New, or changed since the last look: start the window again.
	if err := s.bucket.Put(k, snapshot{Fingerprint: fp, FirstSeen: now}); err != nil {
		return false, fmt.Errorf("record settle snapshot for %q: %w", it.name, err)
	}
	return false, nil
}

// prune drops snapshots for items the scan no longer sees, so the bucket does
// not grow with every delivery that has been processed and moved away.
func (s *settleStore) prune(present []item) error {
	keep := make(map[string]bool, len(present))
	for _, it := range present {
		keep[s.key(it.name)] = true
	}
	keys, err := s.bucket.Keys()
	if err != nil {
		return fmt.Errorf("list settle snapshots: %w", err)
	}
	prefix := s.root + "\x00"
	for _, k := range keys {
		// Only this root's keys: another node in the same task owns the rest.
		if !strings.HasPrefix(k, prefix) || keep[k] {
			continue
		}
		if err := s.bucket.Delete(k); err != nil {
			return fmt.Errorf("delete settle snapshot %q: %w", k, err)
		}
	}
	return nil
}
