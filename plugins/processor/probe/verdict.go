package probe

import (
	"errors"
	"fmt"
	"strings"
	"time"

	mvcprobe "github.com/brunoga/mvc/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
)

// VerdictBucketName holds one record per torrent probed, keyed by lowercase
// info-hash. The key is the content, not the release: two indexers posting the
// same torrent share a verdict, and so do two pipelines.
const VerdictBucketName = "cache_probe"

// verdictFormat is the stored record's layout. It guards against a future
// mvc/probe release renumbering Kind or Layout, which would otherwise make an
// old record decode to a confident wrong answer — "not MVC" for a disc that
// is. A record stamped with anything else is treated as a miss and re-probed.
// Bump it when the meaning of a stored field changes.
const verdictFormat = 1

// verdict is what one probe learned about one torrent.
//
// Result is stored whole and replayed through apply, so a replayed entry and a
// freshly probed one cannot drift: there is one function that turns a result
// into fields, and both paths call it. That also keeps the two fields apply
// derives from the ENTRY rather than the container — probe_upscale, which
// compares the frame against the resolution the release name claims, and
// probe_bitrate_mbps, which divides the torrent's size by the container's
// duration — correct for a release whose name differs from the one probed.
type verdict struct {
	Format int `json:"format"`
	// Title records which release the probe was run for. Nothing reads it;
	// it is there so the database tab shows a legible row.
	Title    string    `json:"title,omitempty"`
	ProbedAt time.Time `json:"probed_at"`

	// Result is nil exactly when the probe did not produce one.
	Result *mvcprobe.Result `json:"result,omitempty"`

	// Err is the failure as it was logged, kept for the same reason as Title.
	Err         string `json:"err,omitempty"`
	Unreachable bool   `json:"unreachable,omitempty"`

	Pieces int   `json:"pieces,omitempty"`
	Bytes  int64 `json:"bytes,omitempty"`
}

// ok reports whether this verdict recorded a container the probe could read.
func (v *verdict) ok() bool { return v.Result != nil }

// expired reports whether a stored verdict should be re-probed.
//
// The two outcomes have different lifetimes because they are different kinds
// of fact. A container that was read is a property of the bytes, and the
// info-hash pins the bytes: that answer can never change, so it is kept for
// good. A probe that failed is a property of the swarm at a moment — nobody
// was serving, or nobody served fast enough — and swarms recover, so that one
// expires.
func (v *verdict) expired(ttl time.Duration, now time.Time) bool {
	if v.ok() {
		return false
	}
	if ttl <= 0 {
		return true
	}
	return now.Sub(v.ProbedAt) >= ttl
}

// verdictKey is the bucket key for an entry, or "" when the entry carries no
// info-hash to key on.
func verdictKey(e *entry.Entry) string {
	return strings.ToLower(e.GetString(entry.FieldTorrentInfoHash))
}

// loadVerdict returns the live stored verdict for e, if there is one.
func (p *probePlugin) loadVerdict(e *entry.Entry) (*verdict, bool) {
	if p.verdicts == nil {
		return nil, false
	}
	key := verdictKey(e)
	if key == "" {
		return nil, false
	}
	var v verdict
	found, err := p.verdicts.Get(key, &v)
	if err != nil || !found || v.Format != verdictFormat {
		return nil, false
	}
	if v.expired(p.cacheTTL, time.Now()) {
		return nil, false
	}
	return &v, true
}

// saveVerdict records what a probe learned. A failure to write is not a
// failure to probe: the entry already carries the fields, so the run is
// correct and only the next run pays for it again.
func (p *probePlugin) saveVerdict(e *entry.Entry, v *verdict) error {
	if p.verdicts == nil {
		return nil
	}
	key := verdictKey(e)
	if key == "" {
		return nil
	}
	v.Format = verdictFormat
	v.Title = e.Title
	v.ProbedAt = time.Now()
	return p.verdicts.Put(key, v)
}

// replay stamps a stored verdict onto an entry, exactly as the probe that
// produced it did.
//
// A miss leaves the entry untouched, which is what lets a cache_only node
// gate on a verdict: probe_ok is present-and-false for a release known to be
// unreadable and ABSENT for one nobody has looked at, and an absent field
// compares false (TestMissingFieldComparesFalse), so `probe_ok == false`
// refuses the first and passes the second.
func (p *probePlugin) replay(e *entry.Entry, v *verdict) {
	e.Set(entry.FieldProbePieces, v.Pieces)
	e.Set(entry.FieldProbeBytes, v.Bytes)
	if v.ok() {
		apply(e, v.Result)
		return
	}
	e.Set(entry.FieldProbeOK, false)
	if v.Unreachable {
		e.Set(entry.FieldProbeUnreachable, true)
	}
	if p.require {
		e.Reject(fmt.Sprintf("%s: could not read the release's container: %s", pluginName, v.Err))
	}
}

// newVerdict builds the record for a probe that has just run.
func newVerdict(res *mvcprobe.Result, pieces int, bytes int64, err error) *verdict {
	v := &verdict{Pieces: pieces, Bytes: bytes}
	if err != nil {
		v.Err = err.Error()
		v.Unreachable = errors.Is(err, torrentpeek.ErrNoPeers)
		return v
	}
	v.Result = res
	return v
}

// verdictBucket resolves the bucket, tolerating a nil store so a plugin built
// without one (unit tests, `pipeliner check`) simply has no cache.
func verdictBucket(db *store.SQLiteStore) store.Bucket {
	if db == nil {
		return nil
	}
	return db.Bucket(VerdictBucketName)
}
