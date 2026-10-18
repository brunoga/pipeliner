package probe

import (
	"context"
	"os"
	"slices"
	"testing"
	"time"

	mvcprobe "github.com/brunoga/bdtools/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
)

// fakeSwarm stands in for the network: each info-hash answers with a fixed
// result or error, and every probe is counted.
type fakeSwarm struct {
	results map[string]*mvcprobe.Result
	errs    map[string]error
	probed  []string
}

func (f *fakeSwarm) probe(_ context.Context, _ *probePlugin, _ *plugin.TaskContext, e *entry.Entry) (*mvcprobe.Result, int, int64, *SwarmReport, error) {
	key := verdictKey(e)
	f.probed = append(f.probed, key)
	if err := f.errs[key]; err != nil {
		return nil, 0, 0, nil, err
	}
	if res := f.results[key]; res != nil {
		return res, 2, 1 << 20, nil, nil
	}
	return nil, 0, 0, nil, torrentpeek.ErrNoPeers
}

// clock is a settable time source.
type clock struct{ t time.Time }

func (c *clock) now() time.Time               { return c.t }
func (c *clock) advance(d time.Duration)      { c.t = c.t.Add(d) }
func newClock() *clock                        { return &clock{t: time.Now()} }
func (c *clock) at(d time.Duration) time.Time { return c.t.Add(d) }

type triggers struct{ fired []string }

func (tr *triggers) fire(name string) { tr.fired = append(tr.fired, name) }

func testWorker(t *testing.T, db *store.SQLiteStore, sw *fakeSwarm, c *clock, tr *triggers) *Worker {
	t.Helper()
	w := NewWorker(db, discardLogger(), tr.fire)
	w.probe = sw.probe
	w.now = c.now
	w.gap = 0
	return w
}

func background(t *testing.T, db *store.SQLiteStore, extra map[string]any) *probePlugin {
	t.Helper()
	cfg := map[string]any{"background": true}
	for k, v := range extra {
		cfg[k] = v
	}
	return probeWith(t, db, cfg)
}

func runAs(t *testing.T, p *probePlugin, name string, dry bool, entries ...*entry.Entry) []*entry.Entry {
	t.Helper()
	tc := &plugin.TaskContext{Name: name, Logger: discardLogger(), DryRun: dry}
	out, err := p.Process(context.Background(), tc, entries)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return out
}

func queued(t *testing.T, db *store.SQLiteStore, hash string) (job, bool) {
	t.Helper()
	var j job
	found, err := db.Bucket(QueueBucketName).Get(hash, &j)
	if err != nil {
		t.Fatal(err)
	}
	return j, found
}

// TestABackgroundNodeQueuesAndDoesNotProbe is the point of the mode: the run
// replays what is known, queues what is not, and never touches a swarm.
func TestABackgroundNodeQueuesAndDoesNotProbe(t *testing.T) {
	db := verdictStore(t)
	known := hashEntry("Known Disc 2019 3D BluRay", "aaa")
	if err := probeWith(t, db, nil).saveVerdict(known, newVerdict(mvcDisc(), 2, 1, nil)); err != nil {
		t.Fatal(err)
	}

	p := background(t, db, nil)
	fresh := hashEntry("Fresh Disc 2020 3D BluRay", "bbb")
	out := runAs(t, p, "3d", false, hashEntry("Known Disc 2019 3D BluRay", "aaa"), fresh)

	if p.sc != nil {
		t.Error("a background node must not start a torrent client")
	}
	if len(out) != 2 || out[0].IsRejected() || out[1].IsRejected() {
		t.Fatalf("every entry passes through undecided, got %v", out)
	}
	if !out[0].GetBool(entry.FieldProbeOK) {
		t.Error("the known release should carry its replayed verdict")
	}
	if _, ok := out[1].Get(entry.FieldProbeOK); ok {
		t.Error("a queued release must carry no probe_ok until it is probed")
	}
	if _, found := queued(t, db, "aaa"); found {
		t.Error("a release with a verdict must not be queued")
	}
	j, found := queued(t, db, "bbb")
	if !found {
		t.Fatal("the unknown release should be queued")
	}
	if !slices.Equal(j.Pipelines, []string{"3d"}) || j.URL != fresh.URL {
		t.Errorf("job = %+v", j)
	}
	if j.Settings.Retries != defaultRetries || j.Settings.RetryAfter != defaultRetryAfter {
		t.Errorf("job settings = %+v, want the defaults", j.Settings)
	}
}

// TestRequeueingKeepsTheJobsHistory: a nightly run seeing a release again is
// not news about its swarm, so the retry state survives; what changes is who
// wants it.
func TestRequeueingKeepsTheJobsHistory(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, nil), "a", true, hashEntry("Disc 2020 3D BluRay", "ccc"))

	b := db.Bucket(QueueBucketName)
	j, _ := queued(t, db, "ccc")
	j.Attempts = 1
	j.NotBefore = time.Now().Add(time.Hour)
	if err := b.Put("ccc", &j); err != nil {
		t.Fatal(err)
	}

	runAs(t, background(t, db, map[string]any{"trigger": true}), "b", false, hashEntry("Disc 2020 3D BluRay", "ccc"))
	got, _ := queued(t, db, "ccc")
	if got.Attempts != 1 || !got.NotBefore.Equal(j.NotBefore) {
		t.Errorf("retry state lost: attempts=%d not_before=%v", got.Attempts, got.NotBefore)
	}
	if !slices.Equal(got.Pipelines, []string{"a", "b"}) {
		t.Errorf("pipelines = %v, want both", got.Pipelines)
	}
	if got.DryRun {
		t.Error("a real run asking makes the job real")
	}
	if !got.Trigger {
		t.Error("a node with trigger=True asking makes the job trigger")
	}
}

// TestEachFilmsBestReleaseIsProbedFirst: a backlog must deliver films, not a
// second alternative for a film whose best release is still waiting.
func TestEachFilmsBestReleaseIsProbedFirst(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, nil), "3d", false,
		hashEntry("Avatar 2009 3D HSBS 1080p", "a2"),
		hashEntry("Avatar 2009 BD3D 1080p BluRay", "a1"),
		hashEntry("Hugo 2011 BD3D 1080p BluRay", "h1"),
		hashEntry("Hugo 2011 3D HSBS 1080p", "h2"),
	)
	sw := &fakeSwarm{}
	c := newClock()
	w := testWorker(t, db, sw, c, &triggers{})
	// Give up at once, so each probe removes its job.
	for _, h := range []string{"a1", "a2", "h1", "h2"} {
		j, _ := queued(t, db, h)
		j.Settings.Retries = 0
		_ = db.Bucket(QueueBucketName).Put(h, &j)
	}
	w.drain(context.Background())

	first := sw.probed[:2]
	slices.Sort(first)
	if !slices.Equal(first, []string{"a1", "h1"}) {
		t.Errorf("probe order = %v, want both films' best releases first", sw.probed)
	}
	if len(sw.probed) != 4 {
		t.Errorf("probed %v, want all four", sw.probed)
	}
}

// TestAReadableReleaseIsRecordedAndTriggers runs the whole loop: queued by a
// run, read by the worker, replayed by the next run.
func TestAReadableReleaseIsRecordedAndTriggers(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, map[string]any{"trigger": true}), "3d-mvc", false, hashEntry("Disc 2020 3D BluRay", "ddd"))

	sw := &fakeSwarm{results: map[string]*mvcprobe.Result{"ddd": mvcDisc()}}
	tr := &triggers{}
	w := testWorker(t, db, sw, newClock(), tr)
	w.drain(context.Background())

	if _, found := queued(t, db, "ddd"); found {
		t.Error("a probed release must leave the queue")
	}
	if !slices.Equal(tr.fired, []string{"3d-mvc"}) {
		t.Errorf("triggered %v, want the asking pipeline", tr.fired)
	}
	out := runAs(t, background(t, db, nil), "3d-mvc", false, hashEntry("Disc 2020 3D BluRay", "ddd"))
	if !out[0].GetBool(entry.FieldProbeOK) || out[0].GetString(entry.FieldProbe3DLayout) != "mvc" {
		t.Errorf("the next run should replay the worker's verdict, got %v", out[0].Fields)
	}
}

// TestTriggersAreRateLimited: several readable releases in a row must not
// re-run a pipeline -- and re-search its indexers -- once each.
func TestTriggersAreRateLimited(t *testing.T) {
	db := verdictStore(t)
	sw := &fakeSwarm{results: map[string]*mvcprobe.Result{"e1": mvcDisc(), "e2": mvcDisc()}}
	tr := &triggers{}
	c := newClock()
	w := testWorker(t, db, sw, c, tr)

	runAs(t, background(t, db, map[string]any{"trigger": true}), "p", false, hashEntry("One 2020 3D BluRay", "e1"))
	w.drain(context.Background())
	runAs(t, background(t, db, map[string]any{"trigger": true}), "p", false, hashEntry("Two 2021 3D BluRay", "e2"))
	c.advance(10 * time.Minute)
	w.drain(context.Background())
	if len(tr.fired) != 1 {
		t.Fatalf("fired %v, want one run inside the cooldown", tr.fired)
	}
	c.advance(retriggerGap)
	w.drain(context.Background())
	if len(tr.fired) != 2 {
		t.Errorf("fired %v, want the owed run once the cooldown passed", tr.fired)
	}
	w.drain(context.Background())
	if len(tr.fired) != 2 {
		t.Errorf("fired %v, a paid run must not fire again", tr.fired)
	}
}

// TestNoTriggerWithoutTheOption: the trigger is opt-in.
func TestNoTriggerWithoutTheOption(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, nil), "p", false, hashEntry("Disc 2020 3D BluRay", "fff"))
	tr := &triggers{}
	testWorker(t, db, &fakeSwarm{results: map[string]*mvcprobe.Result{"fff": mvcDisc()}}, newClock(), tr).drain(context.Background())
	if len(tr.fired) != 0 {
		t.Errorf("fired %v without trigger=True", tr.fired)
	}
}

// TestAnUnservedReleaseIsRetriedThenRecorded: nobody serving now is a fact
// about the swarm at this moment, so it is retried later, and recorded only
// once the retries are spent.
func TestAnUnservedReleaseIsRetriedThenRecorded(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, map[string]any{"retries": 2, "retry_after": "3h"}), "p", false, hashEntry("Dead 2020 3D BluRay", "ggg"))
	sw := &fakeSwarm{}
	c := newClock()
	w := testWorker(t, db, sw, c, &triggers{})

	w.drain(context.Background())
	j, found := queued(t, db, "ggg")
	if !found || j.Attempts != 1 || !j.NotBefore.Equal(c.at(3*time.Hour)) {
		t.Fatalf("after one failure: found=%v job=%+v", found, j)
	}
	if w.hasVerdict(j) {
		t.Error("a failure must not be recorded while retries remain")
	}

	c.advance(time.Hour)
	w.drain(context.Background())
	if len(sw.probed) != 1 {
		t.Fatalf("probed %d times, a retry must wait until it is due", len(sw.probed))
	}

	c.advance(2 * time.Hour)
	w.drain(context.Background())
	j, _ = queued(t, db, "ggg")
	if len(sw.probed) != 2 || j.Attempts != 2 || !j.NotBefore.Equal(c.at(6*time.Hour)) {
		t.Fatalf("second retry: probed=%d job=%+v", len(sw.probed), j)
	}

	c.advance(6 * time.Hour)
	w.drain(context.Background())
	if _, found := queued(t, db, "ggg"); found {
		t.Error("a release out of retries must leave the queue")
	}
	var v verdict
	if found, _ := db.Bucket(VerdictBucketName).Get("ggg", &v); !found || v.ok() || !v.Unreachable {
		t.Errorf("the failure should be recorded as unreachable, got found=%v %+v", found, v)
	}
}

// TestADryRunJobRecordsOnlySuccess mirrors a dry-run probe in a pipeline:
// what it learns is kept when it is a fact about the bytes, and a failure,
// which steers a real run, is not.
func TestADryRunJobRecordsOnlySuccess(t *testing.T) {
	db := verdictStore(t)
	bg := background(t, db, map[string]any{"retries": 0, "trigger": true})
	runAs(t, bg, "p", true, hashEntry("Good 2020 3D BluRay", "h1"), hashEntry("Dead 2020 3D BluRay", "h2"))
	tr := &triggers{}
	testWorker(t, db, &fakeSwarm{results: map[string]*mvcprobe.Result{"h1": mvcDisc()}}, newClock(), tr).drain(context.Background())

	vb := db.Bucket(VerdictBucketName)
	var v verdict
	if found, _ := vb.Get("h1", &v); !found || !v.ok() {
		t.Error("a dry-run job's success should be recorded")
	}
	if found, _ := vb.Get("h2", &v); found {
		t.Error("a dry-run job's failure must not be recorded")
	}
	if len(tr.fired) != 0 {
		t.Errorf("a dry-run job must trigger nothing, fired %v", tr.fired)
	}
}

// TestStaleAndSettledJobsAreDropped: a release the indexers stopped listing,
// and one probed by other means meanwhile, need no probe.
func TestStaleAndSettledJobsAreDropped(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, nil), "p", false, hashEntry("Old 2020 3D BluRay", "old"), hashEntry("Done 2020 3D BluRay", "done"))
	if err := probeWith(t, db, nil).saveVerdict(hashEntry("Done 2020 3D BluRay", "done"), newVerdict(mvcDisc(), 2, 1, nil)); err != nil {
		t.Fatal(err)
	}
	sw := &fakeSwarm{}
	c := newClock()
	c.advance(maxJobAge + time.Minute)
	w := testWorker(t, db, sw, c, &triggers{})
	w.drain(context.Background())
	if len(sw.probed) != 0 {
		t.Errorf("probed %v, want nothing", sw.probed)
	}
	for _, h := range []string{"old", "done"} {
		if _, found := queued(t, db, h); found {
			t.Errorf("%s should have been dropped", h)
		}
	}
}

// TestAStoppingDaemonLeavesTheJobAlone: a probe cut short by shutdown learned
// nothing about the swarm, so it must not spend an attempt.
func TestAStoppingDaemonLeavesTheJobAlone(t *testing.T) {
	db := verdictStore(t)
	runAs(t, background(t, db, nil), "p", false, hashEntry("Disc 2020 3D BluRay", "iii"))
	ctx, cancel := context.WithCancel(context.Background())
	w := testWorker(t, db, &fakeSwarm{}, newClock(), &triggers{})
	w.probe = func(context.Context, *probePlugin, *plugin.TaskContext, *entry.Entry) (*mvcprobe.Result, int, int64, *SwarmReport, error) {
		cancel()
		return nil, 0, 0, nil, context.Canceled
	}
	j, _ := queued(t, db, "iii")
	w.work(ctx, j)
	got, found := queued(t, db, "iii")
	if !found || got.Attempts != 0 || !got.NotBefore.IsZero() {
		t.Errorf("job changed by a cancelled probe: found=%v %+v", found, got)
	}
}

// TestAnUndecodableJobIsDropped keeps one bad row from wedging the queue.
func TestAnUndecodableJobIsDropped(t *testing.T) {
	db := verdictStore(t)
	if err := db.Bucket(QueueBucketName).Put("bad", "not a job"); err != nil {
		t.Fatal(err)
	}
	w := testWorker(t, db, &fakeSwarm{}, newClock(), &triggers{})
	if _, ok := w.next(); ok {
		t.Error("an undecodable job must not be returned")
	}
	if found, _ := db.Bucket(QueueBucketName).Get("bad", new(any)); found {
		t.Error("an undecodable job should be deleted")
	}
}

// TestRunReturnsOnCancel: the daemon's shutdown stops the worker.
func TestRunReturnsOnCancel(t *testing.T) {
	db := verdictStore(t)
	w := testWorker(t, db, &fakeSwarm{}, newClock(), &triggers{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { w.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestBackgroundValidation(t *testing.T) {
	for _, tt := range []struct {
		cfg  map[string]any
		want bool // valid
	}{
		{map[string]any{"background": true}, true},
		{map[string]any{"background": true, "retries": 0, "retry_after": "1h", "trigger": true}, true},
		{map[string]any{"background": true, "cache_only": true}, false},
		{map[string]any{"background": true, "require": true}, false},
		{map[string]any{"retries": 1}, false},
		{map[string]any{"retry_after": "1h"}, false},
		{map[string]any{"trigger": true}, false},
		{map[string]any{"background": true, "retries": -1}, false},
		{map[string]any{"background": true, "retries": 1.5}, false},
		{map[string]any{"background": true, "retry_after": "soon"}, false},
	} {
		errs := validate(tt.cfg)
		if got := len(errs) == 0; got != tt.want {
			t.Errorf("validate(%v) valid=%v, want %v: %v", tt.cfg, got, tt.want, errs)
		}
	}
}

// TestNoStoreMeansNothingQueued: without a store (pipeliner check, unit
// tests) a background node passes everything through untouched.
func TestNoStoreMeansNothingQueued(t *testing.T) {
	p := testPlugin(t, map[string]any{"background": true})
	out := runAs(t, p, "p", false, hashEntry("Disc 2020 3D BluRay", "jjj"))
	if len(out) != 1 || out[0].IsRejected() {
		t.Errorf("got %v", out)
	}
}

// TestTheWorkerKeepsItsClientBetweenJobs: each job builds its own probe and
// shuts it down. If that closed the client, the next job -- often started
// straight after -- found the port still held by the old client's UDP
// socket, which the library releases asynchronously: live, "bind: address
// already in use" on every job that followed a success.
func TestTheWorkerKeepsItsClientBetweenJobs(t *testing.T) {
	port := freePort(t)
	w := NewWorker(verdictStore(t), discardLogger(), nil)
	if err := w.hold(port); err != nil {
		t.Fatal(err)
	}
	held := w.held[port]

	for range 2 {
		p := jobSettings{ListenPort: port}.prober(nil, w.template)
		sc, err := p.ensureClient()
		if err != nil {
			t.Fatalf("a job could not get the client: %v", err)
		}
		if sc != held {
			t.Fatal("a job built its own client instead of sharing the worker's")
		}
		p.Shutdown()
		if _, err := os.Stat(held.dir); err != nil {
			t.Fatalf("a job's shutdown closed the worker's client: %v", err)
		}
	}

	w.release()
	if _, err := os.Stat(held.dir); !os.IsNotExist(err) {
		t.Errorf("release should close the client, stat err = %v", err)
	}
	if len(w.held) != 0 {
		t.Errorf("held = %v after release", w.held)
	}
}
