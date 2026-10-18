package probe

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	mvcprobe "github.com/brunoga/bdtools/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/quality"
)

// QueueBucketName holds the releases waiting for a background probe, one
// record per torrent keyed by lowercase info-hash, like the verdicts.
//
// The queue lives in the store rather than in memory for two reasons. A
// pipeline run that queues work may be a CLI run that exits long before the
// probe happens, and the daemon that does the probing restarts on upgrades;
// either way the work must still be there.
const QueueBucketName = "probe_queue"

const (
	// workerGap is the least time between the starts of two background
	// probes. The worker already probes one release at a time; this keeps a
	// backlog of quick failures from turning into a burst of announces, which
	// is what a private tracker notices.
	workerGap = 30 * time.Second
	// workerPoll is how often the worker looks at the queue when nothing has
	// woken it, which is what picks up a retry once it falls due.
	workerPoll = time.Minute
	// maxJobAge drops a release nobody has re-queued in two weeks. A nightly
	// pipeline re-queues what it still sees on every run (keeping the job's
	// attempts), so this only removes releases the indexers stopped listing.
	maxJobAge = 14 * 24 * time.Hour
	// retriggerGap is the least time between two runs the worker starts for
	// one pipeline. A backlog can turn up several readable releases in a row,
	// and each run re-searches every indexer the pipeline reads; one run an
	// hour sees them all.
	retriggerGap = time.Hour
)

// job is one release waiting for a background probe.
//
// It carries everything the probe needs, because the run that queued it is
// gone by the time it is probed: where to fetch the .torrent, and the probe
// settings of the node that asked.
type job struct {
	InfoHash     string `json:"info_hash"`
	Title        string `json:"title"`
	URL          string `json:"url,omitempty"`
	FileLocation string `json:"file_location,omitempty"`

	// Pipelines that asked, which are the ones a readable result triggers.
	Pipelines []string `json:"pipelines"`
	// Rank is the release's place among its film's releases in the run that
	// queued it, best first. The worker probes every film's best candidate
	// before any film's second, so a backlog delivers films rather than
	// alternatives.
	Rank int `json:"rank"`
	// DryRun is true while only dry runs have asked. Such a job is still
	// probed -- a diagnostic that queues work and never sees it done would be
	// useless -- but, as for a dry-run probe in a pipeline, only a success is
	// recorded, and it triggers nothing.
	DryRun  bool `json:"dry_run,omitempty"`
	Trigger bool `json:"trigger,omitempty"`

	Settings jobSettings `json:"settings"`

	QueuedAt  time.Time `json:"queued_at"`
	Attempts  int       `json:"attempts,omitempty"`
	NotBefore time.Time `json:"not_before,omitzero"`
	LastErr   string    `json:"last_err,omitempty"`
}

// jobSettings are the asking node's probe settings.
type jobSettings struct {
	ListenPort    int           `json:"listen_port,omitempty"`
	Timeout       time.Duration `json:"timeout"`
	NoPeerTimeout time.Duration `json:"no_peer_timeout"`
	MaxPieces     int           `json:"max_pieces"`
	CacheTTL      time.Duration `json:"cache_ttl"`
	Retries       int           `json:"retries"`
	RetryAfter    time.Duration `json:"retry_after"`
}

// prober builds the probe a job runs with: the asking node's, minus the
// pipeline.
func (s jobSettings) prober(verdicts store.Bucket, template *probePlugin) *probePlugin {
	return &probePlugin{
		timeout:       s.Timeout,
		maxPieces:     s.MaxPieces,
		parallel:      1,
		noPeerTimeout: s.NoPeerTimeout,
		verdicts:      verdicts,
		cacheTTL:      s.CacheTTL,
		listenPort:    s.ListenPort,
		http:          template.http,
		probeImage:    template.probeImage,
		probeMatroska: template.probeMatroska,
		probeMP4:      template.probeMP4,
	}
}

// wakeWorker tells a running worker there is new work, so a release queued by
// a run is looked at in seconds rather than at the next poll. Buffered by one
// and sent without blocking: a wake already pending says the same thing.
var wakeWorker = make(chan struct{}, 1)

func wake() {
	select {
	case wakeWorker <- struct{}{}:
	default:
	}
}

// enqueue queues the entries a background node has no verdict for. A release
// already queued keeps its attempts and retry time -- a nightly run seeing it
// again is not news about its swarm -- and takes the better rank and the
// union of the pipelines that want it.
func (p *probePlugin) enqueue(tc *plugin.TaskContext, entries []*entry.Entry) int {
	if p.queue == nil {
		return 0
	}
	ranks := filmRanks(entries)
	now := time.Now()
	n := 0
	for _, e := range entries {
		key := verdictKey(e)
		if key == "" {
			continue
		}
		var j job
		found, err := p.queue.Get(key, &j)
		if err != nil {
			found = false
		}
		if !found {
			j = job{InfoHash: key, Rank: ranks[e], DryRun: tc.DryRun, QueuedAt: now}
		}
		j.Title = e.Title
		j.URL = e.URL
		j.FileLocation = e.GetString(entry.FieldFileLocation)
		j.Rank = min(j.Rank, ranks[e])
		j.DryRun = j.DryRun && tc.DryRun
		j.Trigger = j.Trigger || p.trigger
		if !slices.Contains(j.Pipelines, tc.Name) {
			j.Pipelines = append(j.Pipelines, tc.Name)
		}
		// Refreshed on every sighting: maxJobAge counts from the last run
		// that still wanted the release, and the settings are the node's
		// current ones.
		j.QueuedAt = now
		j.Settings = p.settings()
		if err := p.queue.Put(key, &j); err != nil {
			tc.Logger.Warn(pluginName+": could not queue a background probe", "entry", e.Title, "err", err)
			continue
		}
		n++
	}
	if n > 0 {
		wake()
	}
	return n
}

// settings is this node's probe configuration, as a job carries it.
func (p *probePlugin) settings() jobSettings {
	return jobSettings{
		ListenPort:    p.listenPort,
		Timeout:       p.timeout,
		NoPeerTimeout: p.noPeerTimeout,
		MaxPieces:     p.maxPieces,
		CacheTTL:      p.cacheTTL,
		Retries:       p.retries,
		RetryAfter:    p.retryAfter,
	}
}

// filmRanks numbers each entry among the releases of its film in this batch,
// best quality first. The film is the title and year parsed from the release
// name, as the movies filter keys it: the title field holds the raw name until
// a parser downstream rewrites it, and a background node sits above them.
func filmRanks(entries []*entry.Entry) map[*entry.Entry]int {
	films := map[string][]*entry.Entry{}
	qualities := make(map[*entry.Entry]quality.Quality, len(entries))
	for _, e := range entries {
		title, year := e.GetString(entry.FieldTitle), entry.ReleaseYear(e)
		q, hasQ := e.Quality()
		if m, ok := movies.Parse(e.Title); ok {
			title, year = m.Title, m.Year
			if !hasQ {
				q = m.Quality
			}
		}
		qualities[e] = q
		k := match.Normalize(title) + "|" + strconv.Itoa(year)
		films[k] = append(films[k], e)
	}
	ranks := make(map[*entry.Entry]int, len(entries))
	for _, group := range films {
		sort.SliceStable(group, func(i, j int) bool {
			return qualities[group[i]].Better(qualities[group[j]])
		})
		for i, e := range group {
			ranks[e] = i
		}
	}
	return ranks
}

// Worker probes queued releases in the background, one at a time, outside
// any pipeline run.
//
// A pipeline run cannot afford patience and a swarm needs it. Measured on
// mrshine: live MVC discs took 2m25s-2m58s to serve two pieces, and a seeder
// that can only dial us learns we exist at its next tracker announce, often
// 30-60 minutes away. A run that waits that long blocks every pipeline behind
// it; a run that does not wait refuses good releases. The worker takes the
// waiting out of the run: a background probe node queues what it has no
// verdict for and moves on, the worker probes it with time to spare and
// retries it hours later when nobody answered, and a pipeline that asked to
// be told is run again as soon as a release it wanted turns out readable.
type Worker struct {
	queue    store.Bucket
	verdicts store.Bucket
	logger   *slog.Logger
	trigger  func(pipeline string)

	// template supplies the parts of a probe a job does not carry: the HTTP
	// client and the container readers, which tests replace.
	template *probePlugin
	// probe runs one probe. It is p.probeEntry; tests replace it, since a
	// real one needs a swarm.
	probe func(ctx context.Context, p *probePlugin, tc *plugin.TaskContext, e *entry.Entry) (*mvcprobe.Result, int, int64, *SwarmReport, error)

	now       func() time.Time
	gap       time.Duration
	lastProbe time.Time

	mu          sync.Mutex
	lastTrigger map[string]time.Time
	owed        map[string]bool

	// held keeps one reference to the torrent client on each port the worker
	// has probed from, for as long as the worker runs. Without it every job
	// released the last reference and closed the client, and the next job,
	// often started straight after, failed to bind the port the old client's
	// UDP socket had not let go of yet: "bind: address already in use".
	held map[int]*sharedClient
}

// NewWorker returns a worker over db. trigger runs a pipeline; it may be nil,
// in which case nothing is triggered.
func NewWorker(db *store.SQLiteStore, logger *slog.Logger, trigger func(pipeline string)) *Worker {
	w := &Worker{
		queue:       db.Bucket(QueueBucketName),
		verdicts:    db.Bucket(VerdictBucketName),
		logger:      logger,
		trigger:     trigger,
		template:    defaults(),
		held:        map[int]*sharedClient{},
		now:         time.Now,
		gap:         workerGap,
		lastTrigger: map[string]time.Time{},
		owed:        map[string]bool{},
	}
	w.probe = func(ctx context.Context, p *probePlugin, tc *plugin.TaskContext, e *entry.Entry) (*mvcprobe.Result, int, int64, *SwarmReport, error) {
		if err := w.hold(p.listenPort); err != nil {
			return nil, 0, 0, nil, err
		}
		return p.probeEntry(ctx, tc, e)
	}
	return w
}

// hold takes the worker's own reference to the client on port, once.
func (w *Worker) hold(port int) error {
	if _, ok := w.held[port]; ok {
		return nil
	}
	sc, err := acquireClient(port)
	if err != nil {
		return err
	}
	w.held[port] = sc
	return nil
}

// release lets go of every client the worker holds.
func (w *Worker) release() {
	for port, sc := range w.held {
		sc.release()
		delete(w.held, port)
	}
}

// Run works the queue until ctx is cancelled. Only the daemon runs it; any
// process can queue.
func (w *Worker) Run(ctx context.Context) {
	tick := time.NewTicker(workerPoll)
	defer tick.Stop()
	defer w.release()
	for {
		w.drain(ctx)
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-wakeWorker:
		}
	}
}

// drain probes every job that is due, best-ranked first, then pays any
// triggers the cooldown was holding back.
func (w *Worker) drain(ctx context.Context) {
	for ctx.Err() == nil {
		j, ok := w.next()
		if !ok {
			break
		}
		if wait := w.gap - w.now().Sub(w.lastProbe); wait > 0 {
			select {
			case <-ctx.Done():
				return
			case <-time.After(wait):
			}
		}
		w.lastProbe = w.now()
		w.work(ctx, j)
	}
	w.payTriggers()
}

// next returns the due job to probe now, dropping jobs that need no probe any
// more on the way.
func (w *Worker) next() (job, bool) {
	all, err := w.queue.All()
	if err != nil {
		w.logger.Warn(pluginName+": reading the background queue", "err", err)
		return job{}, false
	}
	now := w.now()
	var due []job
	for key, raw := range all {
		var j job
		if err := decodeJob(raw, &j); err != nil {
			_ = w.queue.Delete(key)
			continue
		}
		if now.Sub(j.QueuedAt) > maxJobAge {
			_ = w.queue.Delete(key)
			continue
		}
		// Probed meanwhile -- by a pipeline's own probe node, or because two
		// indexers listed the same torrent.
		if w.hasVerdict(j) {
			_ = w.queue.Delete(key)
			continue
		}
		if j.NotBefore.After(now) {
			continue
		}
		due = append(due, j)
	}
	if len(due) == 0 {
		return job{}, false
	}
	sort.Slice(due, func(a, b int) bool {
		if due[a].Rank != due[b].Rank {
			return due[a].Rank < due[b].Rank
		}
		return due[a].QueuedAt.Before(due[b].QueuedAt)
	})
	return due[0], true
}

func (w *Worker) hasVerdict(j job) bool {
	var v verdict
	found, err := w.verdicts.Get(j.InfoHash, &v)
	if err != nil || !found || v.Format != verdictFormat {
		return false
	}
	return !v.expired(j.Settings.CacheTTL, w.now())
}

// work probes one job and records what came of it.
func (w *Worker) work(ctx context.Context, j job) {
	e := entry.New(j.Title, j.URL)
	e.Set(entry.FieldTorrentInfoHash, j.InfoHash)
	if j.FileLocation != "" {
		e.Set(entry.FieldFileLocation, j.FileLocation)
	}
	p := j.Settings.prober(w.verdicts, w.template)
	defer p.Shutdown()
	tc := &plugin.TaskContext{
		Name:   strings.Join(j.Pipelines, ","),
		Logger: w.logger.With("pipeline", strings.Join(j.Pipelines, ",")),
	}

	res, pieces, bytes, swarm, err := w.probe(ctx, p, tc, e)
	if ctx.Err() != nil {
		// The daemon is stopping. Nothing was learned about the swarm, so
		// the job stays exactly as it was.
		return
	}
	v := newVerdict(res, pieces, bytes, err)
	if err == nil {
		if serr := p.saveVerdict(e, v); serr != nil {
			w.logger.Warn(pluginName+": could not store verdict", "entry", j.Title, "err", serr)
		}
		_ = w.queue.Delete(j.InfoHash)
		w.logger.Info(pluginName+": background probe read the release", "entry", j.Title,
			"layout", layoutName(res.Layout), "attempt", j.Attempts+1, "pieces", pieces, "bytes", bytes)
		if j.Trigger && !j.DryRun {
			w.owe(j.Pipelines)
		}
		return
	}

	j.Attempts++
	j.LastErr = err.Error()
	if j.Attempts <= j.Settings.Retries {
		// Tried again later rather than written off: a swarm nobody served
		// now may be served once its seeders next announce, and a seeder we
		// cannot dial only finds us then.
		j.NotBefore = w.now().Add(time.Duration(j.Attempts) * j.Settings.RetryAfter)
		if perr := w.queue.Put(j.InfoHash, &j); perr != nil {
			w.logger.Warn(pluginName+": could not requeue", "entry", j.Title, "err", perr)
		}
		w.logger.Info(pluginName+": background probe failed, will retry", append([]any{
			"entry", j.Title, "err", err, "attempt", j.Attempts, "retry_at", j.NotBefore.Format(time.RFC3339)},
			swarm.logArgs()...)...)
		return
	}
	if !j.DryRun {
		if serr := p.saveVerdict(e, v); serr != nil {
			w.logger.Warn(pluginName+": could not store verdict", "entry", j.Title, "err", serr)
		}
	}
	_ = w.queue.Delete(j.InfoHash)
	w.logger.Info(pluginName+": background probe gave up", append([]any{
		"entry", j.Title, "err", err, "attempts", j.Attempts}, swarm.logArgs()...)...)
}

func decodeJob(raw []byte, j *job) error { return json.Unmarshal(raw, j) }

// owe records that pipelines should run because a release they wanted is now
// known readable.
func (w *Worker) owe(pipelines []string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, name := range pipelines {
		w.owed[name] = true
	}
}

// payTriggers runs each owed pipeline whose last worker-started run is more
// than retriggerGap ago. A pipeline still in its cooldown stays owed and is
// run by a later drain, which the poll guarantees.
func (w *Worker) payTriggers() {
	if w.trigger == nil {
		return
	}
	w.mu.Lock()
	var fire []string
	now := w.now()
	for name := range w.owed {
		if now.Sub(w.lastTrigger[name]) < retriggerGap {
			continue
		}
		delete(w.owed, name)
		w.lastTrigger[name] = now
		fire = append(fire, name)
	}
	w.mu.Unlock()
	sort.Strings(fire)
	for _, name := range fire {
		w.logger.Info(pluginName+": running pipeline for a release the background probe read", "pipeline", name)
		w.trigger(name)
	}
}
