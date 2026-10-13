package probe

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	mvcprobe "github.com/brunoga/mvc/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
	"github.com/brunoga/pipeliner/quality"
)

func verdictStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func probeWith(t *testing.T, db *store.SQLiteStore, cfg map[string]any) *probePlugin {
	t.Helper()
	pl, err := newPlugin(cfg, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return pl.(*probePlugin)
}

func hashEntry(title, hash string) *entry.Entry {
	e := entry.New(title, "https://indexer.invalid/"+hash+".torrent")
	e.Set(entry.FieldTorrentInfoHash, hash)
	return e
}

func mvcDisc() *mvcprobe.Result {
	return &mvcprobe.Result{
		Kind:     mvcprobe.KindDisc,
		Layout:   mvcprobe.LayoutMVC,
		Is3D:     true,
		Duration: 2 * time.Hour,
		Video:    []mvcprobe.VideoTrack{{Codec: "MVC", Width: 1920, Height: 1080}},
	}
}

func run(t *testing.T, p *probePlugin, entries ...*entry.Entry) []*entry.Entry {
	t.Helper()
	out, err := p.Process(context.Background(), &plugin.TaskContext{Logger: discardLogger()}, entries)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return out
}

// TestASuccessfulVerdictIsReplayedWithoutProbing is the saving that makes the
// cache worth having at all: the container a probe read is a property of the
// bytes, and the info-hash pins the bytes.
func TestASuccessfulVerdictIsReplayedWithoutProbing(t *testing.T) {
	db := verdictStore(t)
	p := probeWith(t, db, map[string]any{})

	e := hashEntry("Some Disc 2019 3D BluRay", "abc123")
	if err := p.saveVerdict(e, newVerdict(mvcDisc(), 2, 17629184, nil)); err != nil {
		t.Fatal(err)
	}

	// A fresh plugin, so nothing is carried in memory between the two runs.
	p2 := probeWith(t, db, map[string]any{})
	got := hashEntry("Some Disc 2019 3D BluRay", "abc123")
	run(t, p2, got)

	if p2.client != nil {
		t.Error("a replayed verdict must not build a torrent client")
	}
	if v := got.Fields[entry.FieldProbeOK]; v != true {
		t.Errorf("probe_ok = %v, want true", v)
	}
	if v := got.GetString(entry.FieldProbe3DLayout); v != "mvc" {
		t.Errorf("probe_3d_layout = %q, want %q", v, "mvc")
	}
	if v := got.Fields[entry.FieldProbeBytes]; v != int64(17629184) {
		t.Errorf("probe_bytes = %v, want the bytes the original probe pulled", v)
	}
}

// TestReplayAndAFreshProbeAgree: the two paths must not drift, which is why
// the whole result is stored and replayed through apply rather than the
// fields it produced.
func TestReplayAndAFreshProbeAgree(t *testing.T) {
	db := verdictStore(t)
	p := probeWith(t, db, map[string]any{})
	res := mvcDisc()

	live := hashEntry("Some Disc 2019 3D BluRay", "abc123")
	live.Set(entry.FieldTorrentFileSize, int64(45_000_000_000))
	live.Set(entry.FieldProbePieces, 2)
	live.Set(entry.FieldProbeBytes, int64(17629184))
	apply(live, res)

	if err := p.saveVerdict(live, newVerdict(res, 2, 17629184, nil)); err != nil {
		t.Fatal(err)
	}
	replayed := hashEntry("Some Disc 2019 3D BluRay", "abc123")
	replayed.Set(entry.FieldTorrentFileSize, int64(45_000_000_000))
	run(t, probeWith(t, db, map[string]any{}), replayed)

	for _, f := range producedFields {
		if live.Fields[f] != replayed.Fields[f] {
			t.Errorf("%s: live = %#v, replayed = %#v", f, live.Fields[f], replayed.Fields[f])
		}
	}
}

// TestReplayRecomputesTheEntryDependentFields: two of apply's fields are
// derived from the ENTRY, not the container — the upscale claim comes from the
// release name and the bitrate from the torrent's size. The same torrent
// cross-posted under a different name must get those recomputed, which is only
// true because the stored record is the result rather than the fields.
func TestReplayRecomputesTheEntryDependentFields(t *testing.T) {
	db := verdictStore(t)
	p := probeWith(t, db, map[string]any{})

	// A 1920-wide container probed once under an honest 1080p name.
	honest := hashEntry("Film 2019 1080p BluRay x264", "deadbeef")
	honest.SetQuality(quality.Parse(honest.Title))
	res := &mvcprobe.Result{
		Kind:   mvcprobe.KindMatroska,
		Layout: mvcprobe.Layout2D,
		Video:  []mvcprobe.VideoTrack{{Codec: "HEVC", Width: 1920, Height: 1080}},
	}
	apply(honest, res)
	if err := p.saveVerdict(honest, newVerdict(res, 2, 1000, nil)); err != nil {
		t.Fatal(err)
	}

	// The same bytes, re-posted as 2160p. The frame has not changed, so this
	// name is a lie and the replay has to say so.
	liar := hashEntry("Film 2019 2160p BluRay x265", "deadbeef")
	liar.SetQuality(quality.Parse(liar.Title))
	run(t, probeWith(t, db, map[string]any{}), liar)

	if v, ok := honest.Fields[entry.FieldProbeUpscale]; !ok || v != false {
		t.Errorf("the honest name should probe as not-upscaled, got %#v (present=%v)", v, ok)
	}
	if v, ok := liar.Fields[entry.FieldProbeUpscale]; !ok || v != true {
		t.Errorf("probe_upscale = %#v (present=%v), want true: 1920 cannot back a 2160p claim", v, ok)
	}
}

// TestASuccessfulVerdictNeverExpires / TestAFailedVerdictExpires are the
// lifetime asymmetry. A container that was read cannot change; a swarm that
// served nothing can recover.
func TestASuccessfulVerdictNeverExpires(t *testing.T) {
	v := &verdict{Format: verdictFormat, Result: mvcDisc(), ProbedAt: time.Now().Add(-5 * 365 * 24 * time.Hour)}
	if v.expired(168*time.Hour, time.Now()) {
		t.Error("a container the probe read must be remembered for good")
	}
}

func TestAFailedVerdictExpires(t *testing.T) {
	old := &verdict{Format: verdictFormat, Err: "no peers", ProbedAt: time.Now().Add(-8 * 24 * time.Hour)}
	if !old.expired(168*time.Hour, time.Now()) {
		t.Error("a week-old failure must be retried")
	}
	fresh := &verdict{Format: verdictFormat, Err: "no peers", ProbedAt: time.Now().Add(-1 * time.Hour)}
	if fresh.expired(168*time.Hour, time.Now()) {
		t.Error("an hour-old failure must not be retried")
	}
	if !fresh.expired(0, time.Now()) {
		t.Error("cache_ttl=0 must disable the failure cache entirely")
	}
}

// TestAnUnknownStoredFormatIsAMiss guards the one way a cached verdict could
// be worse than no verdict: an mvc/probe release renumbering Layout would make
// an old record decode to a confident wrong answer.
func TestAnUnknownStoredFormatIsAMiss(t *testing.T) {
	db := verdictStore(t)
	raw := map[string]any{"format": verdictFormat + 1, "probed_at": time.Now(), "err": "x"}
	if err := db.Bucket(VerdictBucketName).Put("abc123", raw); err != nil {
		t.Fatal(err)
	}
	p := probeWith(t, db, map[string]any{})
	if _, ok := p.loadVerdict(hashEntry("x", "abc123")); ok {
		t.Error("a record stamped with another format must read as a miss")
	}
}

// TestCacheOnlyTellsKnownBadFromNotLookedAt is the property the whole
// arrangement rests on. A gate above dedup refuses on `probe_ok == false`,
// and an absent field compares false, so a release nobody has probed must
// come out of a cache_only node with no probe_* fields at all.
func TestCacheOnlyTellsKnownBadFromNotLookedAt(t *testing.T) {
	db := verdictStore(t)
	seed := probeWith(t, db, map[string]any{})
	known := hashEntry("Dead Swarm 2021 3D", "known")
	if err := seed.saveVerdict(known, newVerdict(nil, 0, 0, errors.New("context deadline exceeded"))); err != nil {
		t.Fatal(err)
	}

	gate := probeWith(t, db, map[string]any{"cache_only": true})
	bad := hashEntry("Dead Swarm 2021 3D", "known")
	fresh := hashEntry("Never Looked At 2022 3D", "unseen")
	run(t, gate, bad, fresh)

	if gate.client != nil {
		t.Error("cache_only must never build a torrent client")
	}
	if v, ok := bad.Fields[entry.FieldProbeOK]; !ok || v != false {
		t.Errorf("a known failure must replay probe_ok=false, got %#v (present=%v)", v, ok)
	}
	if _, ok := fresh.Fields[entry.FieldProbeOK]; ok {
		t.Error("an unprobed release must carry no probe_ok, or a gate cannot tell it from a known failure")
	}
	if bad.IsRejected() {
		t.Error("without require=true the replay must not reject; the lane's condition decides")
	}
}

// TestCacheOnlyRecordsNothing: the gate reads, the probe below writes. A
// cache_only node that wrote would stamp a verdict it never measured.
func TestCacheOnlyRecordsNothing(t *testing.T) {
	db := verdictStore(t)
	gate := probeWith(t, db, map[string]any{"cache_only": true})
	run(t, gate, hashEntry("Never Looked At 2022 3D", "unseen"))

	keys, err := db.Bucket(VerdictBucketName).Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("cache_only wrote %d verdicts: %v", len(keys), keys)
	}
}

// TestAReplayedFailureRejectsOnlyWithRequire mirrors the live probe's own rule,
// so a cached run and a fresh one refuse the same entries.
func TestAReplayedFailureRejectsOnlyWithRequire(t *testing.T) {
	db := verdictStore(t)
	seed := probeWith(t, db, map[string]any{})
	e := hashEntry("Dead Swarm 2021 3D", "known")
	if err := seed.saveVerdict(e, newVerdict(nil, 0, 0, torrentpeek.ErrNoPeers)); err != nil {
		t.Fatal(err)
	}

	lax := hashEntry("Dead Swarm 2021 3D", "known")
	run(t, probeWith(t, db, map[string]any{}), lax)
	if lax.IsRejected() {
		t.Error("require unset: a replayed failure must pass through")
	}
	if v := lax.Fields[entry.FieldProbeUnreachable]; v != true {
		t.Errorf("probe_unreachable = %#v, want true for a recorded ErrNoPeers", v)
	}

	strict := hashEntry("Dead Swarm 2021 3D", "known")
	run(t, probeWith(t, db, map[string]any{"require": true}), strict)
	if !strict.IsRejected() {
		t.Error("require=true: a replayed failure must reject, as a live one does")
	}
}

// TestAnEntryWithNoInfoHashIsNotCached: the key is the content. Without one
// there is nothing to key on, and keying on the URL would make two indexers'
// copies of one torrent two verdicts.
func TestAnEntryWithNoInfoHashIsNotCached(t *testing.T) {
	db := verdictStore(t)
	p := probeWith(t, db, map[string]any{})
	e := entry.New("No Hash", "https://indexer.invalid/x.torrent")
	if err := p.saveVerdict(e, newVerdict(mvcDisc(), 2, 10, nil)); err != nil {
		t.Fatal(err)
	}
	keys, err := db.Bucket(VerdictBucketName).Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("stored %d verdicts for an entry with no info-hash: %v", len(keys), keys)
	}
}

// TestTheKeyIsTheInfoHashCaseFolded: indexers disagree on the case of a hex
// hash, and two cases of one hash are one torrent.
func TestTheKeyIsTheInfoHashCaseFolded(t *testing.T) {
	db := verdictStore(t)
	p := probeWith(t, db, map[string]any{})
	if err := p.saveVerdict(hashEntry("x", "ABC123DEF"), newVerdict(mvcDisc(), 1, 1, nil)); err != nil {
		t.Fatal(err)
	}
	if _, ok := p.loadVerdict(hashEntry("x", "abc123def")); !ok {
		t.Error("a verdict stored under an upper-case hash must be found under the lower-case one")
	}
	keys, _ := db.Bucket(VerdictBucketName).Keys()
	if len(keys) != 1 || keys[0] != "abc123def" {
		t.Errorf("keys = %v, want one case-folded key", keys)
	}
}

// TestTheStoredResultSurvivesJSON: Layout and Kind are integers on the wire,
// and their meanings are pinned by TestLayoutAndKindNames. If this stopped
// round-tripping, every cached verdict would replay as a different layout.
func TestTheStoredResultSurvivesJSON(t *testing.T) {
	in := mvcDisc()
	in.BaseViewRight = true
	in.Audio = []mvcprobe.AudioTrack{{Codec: "TRUE-HD", Language: "eng", Channels: 8}}
	raw, err := json.Marshal(&verdict{Format: verdictFormat, Result: in})
	if err != nil {
		t.Fatal(err)
	}
	var out verdict
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.Result == nil {
		t.Fatal("result did not survive the round trip")
	}
	if out.Result.Layout != in.Layout || out.Result.Kind != in.Kind {
		t.Errorf("kind/layout = %v/%v, want %v/%v", out.Result.Kind, out.Result.Layout, in.Kind, in.Layout)
	}
	if out.Result.Duration != in.Duration {
		t.Errorf("duration = %v, want %v", out.Result.Duration, in.Duration)
	}
	if !out.Result.BaseViewRight {
		t.Error("base_view_right did not survive")
	}
	if len(out.Result.Audio) != 1 || out.Result.Audio[0].Language != "eng" {
		t.Errorf("audio did not survive: %#v", out.Result.Audio)
	}
}

// TestNoStoreMeansNoCache: `pipeliner check` and the unit tests build plugins
// without a database, and that has to be a plugin with no cache rather than a
// panic.
func TestNoStoreMeansNoCache(t *testing.T) {
	p := probeWith(t, nil, map[string]any{"cache_only": true})
	e := hashEntry("Anything", "abc")
	run(t, p, e)
	if _, ok := p.loadVerdict(e); ok {
		t.Error("a plugin with no store must have no verdicts")
	}
	if err := p.saveVerdict(e, newVerdict(mvcDisc(), 1, 1, nil)); err != nil {
		t.Errorf("saveVerdict with no store: %v", err)
	}
}

// TestADryRunDoesNotRecordAFailure is the asymmetry that keeps a dry run from
// changing what a real run does. A recorded failure makes a cache_only gate
// refuse that release for a whole cache_ttl, so recording one from a
// diagnostic run would suppress a release on the next REAL run on the strength
// of a swarm snapshot nobody asked to act on — the trap discover avoids by not
// stamping titles as "just searched" during a dry run.
//
// A success is kept either way: it is a fact about immutable bytes, so it
// cannot go stale, and dropping it would mean re-pulling 20-25 MiB.
func TestADryRunDoesNotRecordAFailure(t *testing.T) {
	for _, tc := range []struct {
		name   string
		dryRun bool
		want   int
	}{
		{"dry run", true, 0},
		{"real run", false, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := verdictStore(t)
			p := probeWith(t, db, map[string]any{"timeout": "5s"})
			t.Cleanup(func() { p.Shutdown(context.Background(), nil) })

			// A host that cannot resolve, so fetchMetainfo fails and the
			// probe records a failure rather than a result.
			e := hashEntry("Dead Swarm 2021 3D", "aabbcc")
			out, err := p.Process(context.Background(),
				&plugin.TaskContext{Logger: discardLogger(), DryRun: tc.dryRun},
				[]*entry.Entry{e})
			if err != nil {
				t.Fatalf("Process: %v", err)
			}
			if len(out) != 1 {
				t.Fatalf("a failed probe must pass the entry on, got %d", len(out))
			}
			if v := e.Fields[entry.FieldProbeOK]; v != false {
				t.Errorf("probe_ok = %#v, want false", v)
			}
			keys, kerr := db.Bucket(VerdictBucketName).Keys()
			if kerr != nil {
				t.Fatal(kerr)
			}
			if len(keys) != tc.want {
				t.Errorf("stored %d verdicts, want %d: %v", len(keys), tc.want, keys)
			}
		})
	}
}
