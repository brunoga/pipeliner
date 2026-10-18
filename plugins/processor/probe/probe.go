// Package probe reports what a release's own container says about itself,
// read from a sample of the torrent before the torrent is downloaded.
//
// A release name is not evidence. "COMPLETE BLURAY FULL-SBS" is a
// side-by-side re-encode rather than the MVC disc it claims; "2160p" is
// sometimes a 6 Mbps upscale; "DDP Atmos" is lossy where "TrueHD" is not. The
// container states the truth, and the part of it that does so is small and
// predictably placed: a Blu-ray image keeps its UDF directory at the front and
// its BDMV metadata at the end, and a Matroska file names its tracks in its
// first kilobytes.
//
// So this plugin fetches the first and last piece of the torrent — 16 to 32
// MiB, under a thousandth of a 46 GB disc — hands them to
// github.com/brunoga/bdtools/probe, and stamps what came back. Nothing is
// downloaded to the torrent client and nothing is kept: the sample lives in a
// temporary directory that is removed when the run ends.
//
// # It reports; it does not decide
//
// Every field is a measurement, including probe_is_3d, which is explicit in
// both directions — false means the container says it is 2D. What to do about
// any of it belongs in a condition rule downstream, so one plugin serves a
// pipeline hunting MVC discs and one hunting 2D films equally.
//
// A probe that cannot run leaves probe_ok false and the entry untouched
// otherwise, because a release should not be lost to an unreachable swarm. Set
// require=true to reject those entries instead.
//
// # What a container does not state
//
// Fields are absent rather than guessed. Video bit depth is never stated by a
// disc's clip info. A disc's audio channel count and bitrate come from the
// stream's opening frames, which lie in the first piece only on some discs —
// so decide on codec and language, which are always present. Blu-ray language
// codes are ISO 639-2, which cannot distinguish Brazilian from European
// Portuguese; both read as "por".
package probe

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	mvcprobe "github.com/brunoga/bdtools/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
	"github.com/brunoga/pipeliner/quality"
)

const pluginName = "probe"

const (
	defaultTimeout   = 2 * time.Minute
	defaultMaxPieces = 6
	// budgetPiece is the piece size max_pieces is counted in. It was measured
	// on disc images, whose pieces are 16 MiB; a torrent cut into smaller
	// pieces gets the same number of bytes, not the same number of pieces,
	// or a 2 MiB read over 256 KiB pieces would exhaust it on its own.
	budgetPiece = 16 << 20
	// defaultParallel is 1 because the bandwidth cost of a SUCCESSFUL probe is
	// the thing worth serialising: several at once is the kind of burst a
	// private tracker notices. Raising it is opt-in.
	defaultParallel = 1
	// defaultNoPeerTimeout gives up on a swarm that has connected nothing and
	// delivered nothing. Measured: a healthy swarm answered in 6.4s, while
	// dead ones burned the full 2m budget each.
	defaultNoPeerTimeout = 30 * time.Second
	// defaultCacheTTL is how long a FAILED probe is remembered. It has to
	// exceed the pipeline's own interval or the cache never helps — a nightly
	// sweep with a 24h ttl re-probes the same dead swarm every night, which
	// is the thing the cache exists to stop. A week means a release whose
	// swarm was dead is tried again once a week instead.
	defaultCacheTTL = 168 * time.Hour
	// defaultRetries and defaultRetryAfter are a background job's second
	// and third chances. Three hours apart, because what they wait for is
	// the swarm's seeders announcing again, which is not minutes away.
	defaultRetries    = 2
	defaultRetryAfter = 3 * time.Hour
	// maxTorrentFile bounds the .torrent fetch. Real ones are tens of KB; a
	// disc image's piece hashes push that to a few hundred.
	maxTorrentFile = 8 << 20
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "read a release's own container from a sample of its torrent, before downloading it",
		Role:        plugin.RoleProcessor,
		// require=true is the only way this plugin rejects anything: without
		// it an unreadable release keeps probe_ok false and is passed on, so
		// the node cannot cost an item its alternatives whatever it sits
		// below. Saying so here is what keeps the ordering check from
		// warning about a refusal that cannot happen.
		Refusal: plugin.RefusalPerRelease,
		RefusalFor: func(cfg map[string]any) plugin.Refusal {
			if plugin.OptBool(cfg, "require", false) {
				return plugin.RefusalPerRelease
			}
			return plugin.RefusalNone
		},
		// torrent_info_hash means a metainfo step has resolved this entry to a
		// real torrent, which is what there is to sample.
		Requires:   plugin.RequireAll(entry.FieldTorrentInfoHash),
		MayProduce: producedFields,
		// Every verdict goes into cache_probe keyed by info-hash, so a node
		// of this plugin above a collapse stamps the same answer a later run
		// would. See VerdictBucketName.
		ReplaysPerReleaseVerdict: true,
		Factory:                  newPlugin,
		Validate:                 validate,
		Schema: []plugin.FieldSchema{
			{Key: "timeout", Type: plugin.FieldTypeDuration, Default: "2m", Hint: "Budget for one entry's probe, including fetching its pieces"},
			{Key: "max_pieces", Type: plugin.FieldTypeInt, Default: "6", Hint: "Most pieces to fetch for one entry, counted in 16 MiB pieces; the probe asks for more only when it names bytes it could not read"},
			{Key: "require", Type: plugin.FieldTypeBool, Hint: "Reject entries whose probe could not run, instead of passing them with probe_ok false"},
			{Key: "parallel", Type: plugin.FieldTypeInt, Default: "1", Hint: "How many entries to probe at once; a successful probe pulls 20-25 MiB, so raising this bursts at the tracker"},
			{Key: "no_peer_timeout", Type: plugin.FieldTypeDuration, Default: "30s", Hint: "Give up early when no peer has connected and no byte has arrived; 0 disables"},
			{Key: "cache_ttl", Type: plugin.FieldTypeDuration, Default: "168h", Hint: "How long a FAILED probe is remembered before the swarm is tried again; a container that was read is remembered for good, because the info-hash pins the bytes"},
			{Key: "listen_port", Type: plugin.FieldTypeInt, Default: "0", Hint: "Port the torrent client listens on and announces. 0 picks a random one, which nothing outside a container can reach; publish a fixed one so peers that cannot be dialled can connect to us"},
			{Key: "cache_only", Type: plugin.FieldTypeBool, Hint: "Replay stored verdicts and never touch the network, so this node can gate on what an earlier run learned. A release nobody has probed is left untouched, with no probe_* fields at all"},
			{Key: "background", Type: plugin.FieldTypeBool, Hint: "Replay stored verdicts and queue every other release for the daemon's background worker instead of probing it in the run. The run never waits on a swarm; a queued release carries no probe_* fields until the worker has read it"},
			{Key: "retries", Type: plugin.FieldTypeInt, Default: "2", Hint: "background: how many more times the worker tries a release nobody served before recording the failure"},
			{Key: "retry_after", Type: plugin.FieldTypeDuration, Default: "3h", Hint: "background: wait before the first retry; the n-th retry waits n times this. Long enough for the swarm's seeders to announce again"},
			{Key: "trigger", Type: plugin.FieldTypeBool, Hint: "background: run this pipeline again, at most once an hour, when the worker reads a release it queued"},
		},
		Caches: []plugin.CacheInfo{
			{Name: VerdictBucketName, Display: "Probe Verdict Cache"},
			{Name: QueueBucketName, Display: "Probe Background Queue"},
		},
	})
}

var producedFields = []string{
	entry.FieldProbeOK,
	entry.FieldProbeKind,
	entry.FieldProbeIs3D,
	entry.FieldProbeUnreachable,
	entry.FieldProbe3DLayout,
	entry.FieldProbeWidth,
	entry.FieldProbeHeight,
	entry.FieldProbeUpscale,
	entry.FieldProbeVideoCodec,
	entry.FieldProbeDurationSec,
	entry.FieldProbeBitrateMbps,
	entry.FieldProbeAudioCodecs,
	entry.FieldProbeAudioLanguages,
	entry.FieldProbeSubtitleLanguages,
	entry.FieldProbeBaseViewRight,
	entry.FieldProbePieces,
	entry.FieldProbeBytes,
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.OptDuration(cfg, "timeout", pluginName); err != nil {
		errs = append(errs, err)
	}
	if v, ok := cfg["max_pieces"]; ok {
		n, isNum := toFloat(v)
		if !isNum || n < 1 {
			errs = append(errs, fmt.Errorf("%s: 'max_pieces' must be at least 1", pluginName))
		}
	}
	if v, ok := cfg["parallel"]; ok {
		n, isNum := toFloat(v)
		if !isNum || n < 1 {
			errs = append(errs, fmt.Errorf("%s: 'parallel' must be at least 1", pluginName))
		}
	}
	if err := plugin.OptDuration(cfg, "no_peer_timeout", pluginName); err != nil {
		errs = append(errs, err)
	}
	if err := plugin.OptDuration(cfg, "cache_ttl", pluginName); err != nil {
		errs = append(errs, err)
	}
	if v, ok := cfg["listen_port"]; ok {
		n, isNum := toFloat(v)
		if !isNum || n < 0 || n > 65535 || n != float64(int(n)) {
			errs = append(errs, fmt.Errorf("%s: 'listen_port' must be a port number, or 0 for a random one", pluginName))
		}
	}
	if v, ok := cfg["retries"]; ok {
		n, isNum := toFloat(v)
		if !isNum || n < 0 || n != float64(int(n)) {
			errs = append(errs, fmt.Errorf("%s: 'retries' must be a whole number, 0 or more", pluginName))
		}
	}
	if err := plugin.OptDuration(cfg, "retry_after", pluginName); err != nil {
		errs = append(errs, err)
	}
	background := plugin.OptBool(cfg, "background", false)
	if background && plugin.OptBool(cfg, "cache_only", false) {
		errs = append(errs, fmt.Errorf("%s: 'background' and 'cache_only' cannot both be set: cache_only never queues anything", pluginName))
	}
	if background && plugin.OptBool(cfg, "require", false) {
		// require rejects a release the probe could not read, and a
		// background node reads nothing in the run, so it would reject every
		// release nobody has probed yet -- all of them, the first night.
		errs = append(errs, fmt.Errorf("%s: 'require' cannot be used with 'background': nothing is probed in the run, so it would reject every release not yet probed; gate on probe_ok in a condition below instead", pluginName))
	}
	if !background {
		for _, k := range []string{"retries", "retry_after", "trigger"} {
			if _, ok := cfg[k]; ok {
				errs = append(errs, fmt.Errorf("%s: '%s' only applies with background=True", pluginName, k))
			}
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"timeout", "max_pieces", "require", "parallel", "no_peer_timeout",
		"cache_ttl", "cache_only", "listen_port",
		"background", "retries", "retry_after", "trigger")...)
	return errs
}

type probePlugin struct {
	timeout       time.Duration
	maxPieces     int
	require       bool
	parallel      int
	noPeerTimeout time.Duration

	// verdicts is nil when there is no store, which disables the cache
	// without changing anything else. cacheOnly then makes the node a no-op,
	// which is the right answer for `pipeliner check`.
	verdicts  store.Bucket
	cacheTTL  time.Duration
	cacheOnly bool

	// background queues what has no verdict for the Worker instead of
	// probing it here. queue is nil without a store, like verdicts.
	background bool
	queue      store.Bucket
	retries    int
	retryAfter time.Duration
	trigger    bool

	http *http.Client

	// The probers are fields so the fetch loop — the retry logic, which is
	// the part most likely to be wrong — can be driven directly in tests.
	probeImage    func(r io.ReaderAt, size int64, label string) (*mvcprobe.Result, error)
	probeMatroska func(r io.Reader, label string) (*mvcprobe.Result, error)
	probeMP4      func(r io.ReaderAt, size int64, label string) (*mvcprobe.Result, error)

	listenPort int

	// The torrent client is acquired on first use: a pipeline that never
	// reaches this node should not open a listening socket or a scratch
	// directory. It is shared with every other probe node on the same port;
	// see acquireClient.
	clientMu sync.Mutex
	sc       *sharedClient
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	timeout := defaultTimeout
	if v, _ := cfg["timeout"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid timeout %q: %w", pluginName, v, err)
		}
		timeout = d
	}
	maxPieces := defaultMaxPieces
	if v, ok := cfg["max_pieces"]; ok {
		if n, isNum := toFloat(v); isNum && n >= 1 {
			maxPieces = int(n)
		}
	}
	parallel := defaultParallel
	if v, ok := cfg["parallel"]; ok {
		if n, isNum := toFloat(v); isNum && n >= 1 {
			parallel = int(n)
		}
	}
	noPeer := defaultNoPeerTimeout
	if v, _ := cfg["no_peer_timeout"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid no_peer_timeout %q: %w", pluginName, v, err)
		}
		noPeer = d
	}
	cacheTTL := defaultCacheTTL
	if v, _ := cfg["cache_ttl"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid cache_ttl %q: %w", pluginName, v, err)
		}
		cacheTTL = d
	}
	listenPort := 0
	if v, ok := cfg["listen_port"]; ok {
		if n, isNum := toFloat(v); isNum && n >= 0 && n <= 65535 {
			listenPort = int(n)
		}
	}
	retries := defaultRetries
	if v, ok := cfg["retries"]; ok {
		if n, isNum := toFloat(v); isNum && n >= 0 {
			retries = int(n)
		}
	}
	retryAfter := defaultRetryAfter
	if v, _ := cfg["retry_after"].(string); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return nil, fmt.Errorf("%s: invalid retry_after %q: %w", pluginName, v, err)
		}
		retryAfter = d
	}
	p := defaults()
	p.listenPort = listenPort
	p.timeout = timeout
	p.maxPieces = maxPieces
	p.parallel = parallel
	p.noPeerTimeout = noPeer
	p.verdicts = verdictBucket(db)
	p.cacheTTL = cacheTTL
	p.cacheOnly = plugin.OptBool(cfg, "cache_only", false)
	p.require = plugin.OptBool(cfg, "require", false)
	p.background = plugin.OptBool(cfg, "background", false)
	if p.background && db != nil {
		p.queue = db.Bucket(QueueBucketName)
	}
	p.retries = retries
	p.retryAfter = retryAfter
	p.trigger = plugin.OptBool(cfg, "trigger", false)
	return p, nil
}

// defaults is a probe with the default settings and the real readers.
func defaults() *probePlugin {
	return &probePlugin{
		timeout:       defaultTimeout,
		maxPieces:     defaultMaxPieces,
		parallel:      defaultParallel,
		noPeerTimeout: defaultNoPeerTimeout,
		cacheTTL:      defaultCacheTTL,
		retries:       defaultRetries,
		retryAfter:    defaultRetryAfter,
		http:          &http.Client{Timeout: 60 * time.Second},
		probeImage:    mvcprobe.Image,
		probeMatroska: mvcprobe.Matroska,
		probeMP4:      mvcprobe.MP4,
	}
}

func (p *probePlugin) Name() string { return pluginName }

// sampler is the part of torrentpeek.Peeker the fetch loop uses. It exists so
// the loop — which is where the retry logic lives, and the thing most likely
// to be wrong — can be tested without a swarm.
type sampler interface {
	io.ReaderAt
	Size() int64
	PieceLength() int64
	PieceOf(off int64) int
	Fetch(ctx context.Context, pieces ...int) error
	FetchedPieces() int
	HasPiece(i int) bool
}

// ensureClient acquires the shared client for this node's port, once. A
// failure is not remembered: the usual one is the port still being held by a
// client that is closing, and the next entry should try again.
func (p *probePlugin) ensureClient() (*sharedClient, error) {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.sc != nil {
		return p.sc, nil
	}
	sc, err := acquireClient(p.listenPort)
	if err != nil {
		return nil, err
	}
	p.sc = sc
	return sc, nil
}

// Shutdown lets go of the shared client, which closes it and removes the
// samples once no other node holds it.
//
// The signature is plugin.ShutdownPlugin's. It used to take a context and
// return an error, which is a different method: the executor never called
// it, and every config reload leaked a torrent client and its scratch
// directory for the life of the daemon. Harmless on a random port, it would
// hold a fixed one against the next config's client.
func (p *probePlugin) Shutdown() {
	p.clientMu.Lock()
	defer p.clientMu.Unlock()
	if p.sc != nil {
		p.sc.release()
		p.sc = nil
	}
}

var _ plugin.ShutdownPlugin = (*probePlugin)(nil)

// Process probes each entry in turn. Entries are deliberately not probed in
// parallel: each one asks a private tracker for an announce and pulls tens of
// megabytes, and doing that to several swarms at once is the kind of burst a
// tracker notices.
// Process probes each entry that is still in play.
//
// Entries are probed at most `parallel` at a time, 1 by default. The cost
// being bounded is bandwidth: a successful probe pulls 20-25 MiB, and several
// of those at once from one IP is the kind of burst a private tracker
// notices. A FAILED probe costs no bytes at all, so the serialisation buys
// nothing in exactly the case that is slowest — which is why raising this is
// worth it on a feed full of dead swarms, and why the no-peer timeout matters
// more than the concurrency does.
func (p *probePlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	todo := make([]*entry.Entry, 0, len(entries))
	for _, e := range entries {
		if e.IsRejected() {
			continue
		}
		// A stored verdict is replayed and costs nothing. Doing this before
		// the budget below is the whole point: a node placed above dedup with
		// cache_only=true replays what earlier runs learned, so the refusal
		// lands while the alternatives are still there, and the expensive
		// node below the collapse still probes only the few about to be
		// downloaded.
		if v, ok := p.loadVerdict(e); ok {
			p.replay(e, v)
			tc.Logger.Debug(pluginName+": verdict replayed", "entry", e.Title,
				"ok", v.ok(), "probed_at", v.ProbedAt)
			continue
		}
		todo = append(todo, e)
	}
	if p.cacheOnly {
		// Nothing more to do: an unprobed release keeps no probe_* fields,
		// which is how a gate tells "known bad" from "not looked at yet".
		return entry.PassThrough(entries), nil
	}
	if p.background {
		// Queued, not probed: the run moves on, and the release carries no
		// probe_* fields until a later run replays what the worker learned.
		if n := p.enqueue(tc, todo); n > 0 {
			tc.Logger.Info(pluginName+": queued for a background probe", "entries", n)
		}
		return entry.PassThrough(entries), nil
	}
	if len(todo) == 0 {
		return entry.PassThrough(entries), nil
	}

	parallel := p.parallel
	if parallel < 1 {
		parallel = 1
	}
	if parallel > len(todo) {
		parallel = len(todo)
	}

	// A mutex rather than per-entry channels: the only shared state is the
	// logger and each entry's own fields, and an entry is touched by exactly
	// one goroutine.
	var logMu sync.Mutex
	sem := make(chan struct{}, parallel)
	var wg sync.WaitGroup
	for _, e := range todo {
		wg.Add(1)
		go func(e *entry.Entry) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				return
			}
			defer func() { <-sem }()

			res, pieces, bytes, swarm, err := p.probeEntry(ctx, tc, e)
			e.Set(entry.FieldProbePieces, pieces)
			e.Set(entry.FieldProbeBytes, bytes)
			logMu.Lock()
			defer logMu.Unlock()
			// Recorded before the outcome is acted on, and for a failure as
			// well as a success: a release nobody will serve is exactly the
			// one worth not asking about again tomorrow.
			//
			// Except on a dry run, where only a success is kept. The two
			// outcomes differ in whether they can go stale, and that is what
			// decides it. A container that was read is a fact about immutable
			// bytes, so recording it from a dry run can never be wrong and
			// saves re-pulling 20-25 MiB. A failure is a snapshot of a swarm,
			// and recording one would keep a gate refusing that release on
			// the next REAL run for a whole cache_ttl on the strength of a
			// diagnostic -- the same trap discover avoids by not stamping
			// titles as "just searched" during a dry run.
			v := newVerdict(res, pieces, bytes, err)
			if v.ok() || !tc.DryRun {
				if serr := p.saveVerdict(e, v); serr != nil {
					tc.Logger.Warn(pluginName+": could not store verdict", "entry", e.Title, "err", serr)
				}
			} else {
				tc.Logger.Debug(pluginName+": dry run, failure not recorded", "entry", e.Title)
			}
			if err != nil {
				e.Set(entry.FieldProbeOK, false)
				// A swarm that served nothing is worth saying plainly: it is
				// not just a slow probe, it is a release whose full download
				// would not have gone any better.
				if errors.Is(err, torrentpeek.ErrNoPeers) {
					e.Set(entry.FieldProbeUnreachable, true)
				}
				tc.Logger.Info(pluginName+": could not probe",
					append([]any{"entry", e.Title, "err", err}, swarm.logArgs()...)...)
				if p.require {
					e.Reject(fmt.Sprintf("%s: could not read the release's container: %v", pluginName, err))
				}
				return
			}
			apply(e, res)
			tc.Logger.Info(pluginName+": probed", "entry", e.Title,
				"kind", e.GetString(entry.FieldProbeKind),
				"3d", res.Is3D, "layout", layoutName(res.Layout),
				"size", fmt.Sprintf("%dx%d", widthOf(res), heightOf(res)),
				"mbps", e.Fields[entry.FieldProbeBitrateMbps],
				"pieces", pieces, "bytes", bytes)
			tc.Logger.Debug(pluginName+": swarm", append([]any{"entry", e.Title}, swarm.logArgs()...)...)
		}(e)
	}
	wg.Wait()
	return entry.PassThrough(entries), nil
}

// probeEntry samples one entry's torrent and probes it. It returns what the
// probe cost even when it failed, so the accounting is never lost, and what
// the swarm did, which is nil when the probe never reached the swarm.
func (p *probePlugin) probeEntry(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) (*mvcprobe.Result, int, int64, *SwarmReport, error) {
	sc, err := p.ensureClient()
	if err != nil {
		return nil, 0, 0, nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	mi, err := p.fetchMetainfo(ctx, e)
	if err != nil {
		return nil, 0, 0, nil, err
	}
	t, err := sc.add(ctx, mi)
	if err != nil {
		return nil, 0, 0, nil, fmt.Errorf("adding the torrent: %w", err)
	}
	// The report is taken when the probe ends, success or failure, and
	// before the torrent is dropped: afterwards there is nothing to ask.
	var swarm *SwarmReport
	defer func() {
		name := ""
		if info := t.Info(); info != nil {
			name = info.BestName()
		}
		// Dropping the torrent and removing its sample is what keeps this
		// from accumulating sparse files across a long-running daemon.
		if sc.drop(t) && name != "" {
			if err := removeSample(sc.dir, name); err != nil {
				tc.Logger.Warn(pluginName+": removing the sample", "entry", e.Title, "err", err)
			}
		}
	}()
	snapshot := func() *SwarmReport {
		r := sc.report(t, mi)
		return &r
	}

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		swarm = snapshot()
		return nil, 0, 0, swarm, fmt.Errorf("waiting for torrent metadata: %w", ctx.Err())
	}

	pk, err := torrentpeek.New(t)
	if err != nil {
		return nil, 0, 0, nil, err
	}
	pk.NoPeerTimeout = p.noPeerTimeout
	defer func() { _ = pk.Close() }()

	res, err := p.readWithRetries(ctx, pk, targetOf(t, e))
	swarm = snapshot()
	return res, pk.FetchedPieces(), pk.FetchedBytes(), swarm, err
}

// removeSample deletes what a probe left in dir for the torrent called name:
// a directory for a multi-file torrent, a file for a single-file one. The
// client names a file it has not completed name+".part", which is every file
// a probe leaves, so removing only name missed all of them and a daemon
// accumulated one sparse, image-sized file per probed release.
func removeSample(dir, name string) error {
	path := filepath.Join(dir, name)
	return errors.Join(os.RemoveAll(path), os.RemoveAll(path+".part"))
}

// target is the file a probe reads: where it sits in the torrent, how long
// it is, and its name, whose extension picks the reader.
type target struct {
	name      string
	off, size int64
}

// targetOf picks the torrent's largest file. A single-file torrent is that
// file. A release packaged as a folder -- a Vision Pro .mov beside an .nfo
// and a sample -- used to be read as if the whole torrent were one disc
// image, because the reader was chosen by the torrent's name, which a folder
// torrent does not end in an extension; it failed on every such release.
func targetOf(t *torrent.Torrent, e *entry.Entry) target {
	info := t.Info()
	if info == nil {
		return target{name: e.Title, size: t.Length()}
	}
	return targetIn(info, nameOf(t, e))
}

// targetIn is targetOf over the metainfo alone. name is the single file's.
func targetIn(info *metainfo.Info, name string) target {
	if len(info.Files) == 0 {
		return target{name: name, size: info.Length}
	}
	var best target
	var off int64
	for _, f := range info.UpvertedFiles() {
		if f.Length > best.size {
			best = target{name: f.DisplayPath(info), off: off, size: f.Length}
		}
		off += f.Length
	}
	return best
}

// reader names the prober a file's extension calls for.
func readerFor(name string) string {
	switch strings.ToLower(filepath.Ext(name)) {
	case ".mkv", ".mk3d", ".webm":
		return "matroska"
	case ".mp4", ".mov", ".m4v":
		return "mp4"
	}
	return "image"
}

// readWithRetries fetches the ends of the target file, probes, and when the
// prober names bytes it could not read, fetches the piece holding them and
// tries again.
//
// The retry is not belt and braces. A disc with hundreds of playlists — Disney
// titles routinely have them — spills its metadata past the last 16 MiB piece,
// so assuming two pieces would fail on real discs while the prober is sitting
// there telling us exactly which piece it wants.
//
// An MP4's movie box is in its first or its last piece, so the same two
// pieces serve it; when a muxer padded it past the last one, the retry finds
// it the same way.
func (p *probePlugin) readWithRetries(ctx context.Context, pk sampler, tg target) (*mvcprobe.Result, error) {
	kind := readerFor(tg.name)
	if tg.size <= 0 {
		tg.size = pk.Size() - tg.off
	}
	// The pieces at the file's two ends. For a single-file torrent these are
	// the torrent's own first and last pieces.
	if err := pk.Fetch(ctx, pk.PieceOf(tg.off), pk.PieceOf(tg.off+tg.size-1)); err != nil {
		return nil, err
	}
	file := io.NewSectionReader(pk, tg.off, tg.size)
	for {
		var res *mvcprobe.Result
		var err error
		switch kind {
		case "matroska":
			// A Matroska file states its tracks up front, so the first piece
			// is the whole story and a reader over it suffices.
			res, err = p.probeMatroska(io.NewSectionReader(file, 0, min(pk.PieceLength(), tg.size)), tg.name)
		case "mp4":
			res, err = p.probeMP4(file, tg.size, tg.name)
		default:
			res, err = p.probeImage(file, tg.size, tg.name)
		}
		if err == nil {
			return res, nil
		}

		// The prober says which bytes it wanted; our reader says which
		// pieces hold them. Every absent one is fetched in one round: the
		// read starts wherever the prober started it, often in a piece that
		// is already here, so fetching just the piece holding its first byte
		// fetched nothing, and the same read failed the same way forever.
		first, last, ok := missingRange(err, tg.off)
		if !ok {
			return nil, err
		}
		var want []int
		for i := pk.PieceOf(first); i <= pk.PieceOf(last); i++ {
			if !pk.HasPiece(i) {
				want = append(want, i)
			}
		}
		if len(want) == 0 {
			// Nothing to fetch would change the answer, so stop rather
			// than ask the prober the same question again.
			return nil, fmt.Errorf("probe cannot read bytes %d-%d although every piece holding them is here: %w", first, last, err)
		}
		if budget := p.pieceBudget(pk.PieceLength()); pk.FetchedPieces()+len(want) > budget {
			return nil, fmt.Errorf("probe still needs bytes %d-%d after %d pieces (max_pieces): %w", first, last, pk.FetchedPieces(), err)
		}
		if err := pk.Fetch(ctx, want...); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
	}
}

// pieceBudget is max_pieces in pieces of this torrent's size. It is counted
// in 16 MiB pieces, so a torrent with smaller ones may fetch more of them,
// up to the same number of bytes.
func (p *probePlugin) pieceBudget(pieceLen int64) int {
	if pieceLen <= 0 || pieceLen >= budgetPiece {
		return p.maxPieces
	}
	return int(int64(p.maxPieces) * budgetPiece / pieceLen)
}

// missingRange pulls the torrent offsets of the first and last byte of the
// read that failed out of either the prober's error, which counts from the
// start of the file it read (at base in the torrent), or our reader's, which
// counts from the torrent's. The prober's is preferred, since it knows which
// bytes the format needed rather than which read happened to fail.
func missingRange(err error, base int64) (first, last int64, ok bool) {
	var md *mvcprobe.MissingDataError
	if errors.As(err, &md) {
		return base + md.Offset, base + md.Offset + max(md.Length, 1) - 1, true
	}
	var mp *torrentpeek.MissingPieceError
	if errors.As(err, &mp) {
		return mp.Offset, mp.Offset + max(mp.Length, 1) - 1, true
	}
	return 0, 0, false
}

func (p *probePlugin) fetchMetainfo(ctx context.Context, e *entry.Entry) (*metainfo.MetaInfo, error) {
	// A local path is what the filesystem source sets; otherwise the entry URL
	// serves the .torrent, as it does for an indexer proxy link.
	if loc := e.GetString(entry.FieldFileLocation); loc != "" && strings.HasSuffix(strings.ToLower(loc), ".torrent") {
		mi, err := metainfo.LoadFromFile(loc)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", filepath.Base(loc), err)
		}
		return mi, nil
	}
	if e.URL == "" || strings.HasPrefix(e.URL, "magnet:") {
		return nil, errors.New("no .torrent to read: the entry has no URL a tracker will serve")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, e.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetching the .torrent: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetching the .torrent: HTTP %d", resp.StatusCode)
	}
	mi, err := metainfo.Load(io.LimitReader(resp.Body, maxTorrentFile))
	if err != nil {
		return nil, fmt.Errorf("parsing the .torrent: %w", err)
	}
	return mi, nil
}

func nameOf(t *torrent.Torrent, e *entry.Entry) string {
	if info := t.Info(); info != nil {
		if n := info.BestName(); n != "" {
			return n
		}
	}
	return e.Title
}

// apply stamps the measurements. A field the container did not state is left
// unset rather than written as zero, so a condition can tell "it says 0" from
// "it did not say".
func apply(e *entry.Entry, res *mvcprobe.Result) {
	e.Set(entry.FieldProbeOK, true)
	e.Set(entry.FieldProbeKind, kindName(res.Kind))
	e.Set(entry.FieldProbeIs3D, res.Is3D)
	e.Set(entry.FieldProbe3DLayout, layoutName(res.Layout))
	switch res.Layout {
	case mvcprobe.LayoutMVC:
		raiseFormat3D(e, quality.Format3DBD)
	case mvcprobe.LayoutMVHEVC:
		raiseFormat3D(e, quality.Format3DMVHEVC)
	}
	if res.Kind == mvcprobe.KindDisc {
		e.Set(entry.FieldProbeBaseViewRight, res.BaseViewRight)
	}
	if w := widthOf(res); w > 0 {
		e.Set(entry.FieldProbeWidth, w)
	}
	if h := heightOf(res); h > 0 {
		e.Set(entry.FieldProbeHeight, h)
	}
	if c := videoCodec(res); c != "" {
		e.Set(entry.FieldProbeVideoCodec, c)
	}
	if up, known := upscale(res, e); known {
		e.Set(entry.FieldProbeUpscale, up)
	}
	if res.Duration > 0 {
		secs := int(res.Duration.Seconds())
		e.Set(entry.FieldProbeDurationSec, secs)
		// The measurement the release name cannot fake, and unlike the bitrate
		// filter's estimate it needs no enrichment runtime: the torrent's own
		// size over the container's own duration.
		if size := toInt64(e.Fields[entry.FieldTorrentFileSize]); size > 0 && secs > 0 {
			mbps := float64(size) * 8 / float64(secs) / 1e6
			e.Set(entry.FieldProbeBitrateMbps, float64(int(mbps*10+0.5))/10)
		}
	}
	if codecs := audioCodecs(res); len(codecs) > 0 {
		e.Set(entry.FieldProbeAudioCodecs, codecs)
	}
	if langs := audioLanguages(res); len(langs) > 0 {
		e.Set(entry.FieldProbeAudioLanguages, langs)
	}
	if langs := subtitleLanguages(res); len(langs) > 0 {
		e.Set(entry.FieldProbeSubtitleLanguages, langs)
	}
}

// raiseFormat3D lifts the entry's quality to the 3D format the container
// proved, when the name had claimed less. Without it a disc named only "3D
// BluRay" stays at Unspecified after the probe has read its MVC dependent
// view, and every comparison downstream still ranks it as a guess: a dedup
// over both 3d-mvc lanes would keep a Vision Pro copy of the film over the
// disc it was converted from.
//
// Never lowered, and never applied to a conversion. The probe reads the
// format, not where the picture came from: a 2D-to-3D conversion encoded as
// MVC is still a conversion, and the name is the only thing that says so.
func raiseFormat3D(e *entry.Entry, f quality.Format3D) {
	q, ok := e.Quality()
	if !ok || q.Format3D == quality.Format3DConv {
		return
	}
	up := q
	up.Format3D = f
	if q.Format3D == quality.Format3DNone || up.Better(q) {
		e.SetQuality(up)
	}
}

// upscale compares the coded frame against the resolution the release name
// claims. It reports (false, true) when the frame backs the claim, which is as
// definite an answer as the other direction, and (_, false) when either side
// is unknown — the name said nothing parseable, or the container stated no
// frame size.
//
// The comparison is on WIDTH. Height varies with the aspect ratio while width
// does not: a 2160p scope film is commonly 3840x1600, and a height test would
// read that as an upscale. The thresholds sit well below each nominal width so
// a crop or a DCI frame passes, while the next tier down cannot: 1920 is far
// under the 3400 a 2160p claim needs.
//
// A side-by-side 3D frame is twice as wide as its claim (a "1080p" SBS release
// is 3840 across), which clears the threshold rather than failing it — the
// test is for a frame too small for its claim, and that one is too big.
func upscale(res *mvcprobe.Result, e *entry.Entry) (up, known bool) {
	w := widthOf(res)
	if w <= 0 {
		return false, false
	}
	q, ok := e.Quality()
	if !ok {
		return false, false
	}
	var min int
	switch q.Resolution {
	case quality.Resolutionp2160:
		min = 3400
	case quality.Resolutionp1080:
		min = 1700
	case quality.Resolutionp720:
		min = 1100
	default:
		return false, false
	}
	return w < min, true
}

// widthOf and heightOf report the base view's frame. A 3D disc also lists its
// MVC dependent view, whose size the disc does not state, so the first track
// that states a size is the one to use.
func widthOf(res *mvcprobe.Result) int {
	for _, v := range res.Video {
		if v.Width > 0 {
			return v.Width
		}
	}
	return 0
}

func heightOf(res *mvcprobe.Result) int {
	for _, v := range res.Video {
		if v.Height > 0 {
			return v.Height
		}
	}
	return 0
}

func videoCodec(res *mvcprobe.Result) string {
	if len(res.Video) == 0 {
		return ""
	}
	return res.Video[0].Codec
}

func audioCodecs(res *mvcprobe.Result) []string {
	out := make([]string, 0, len(res.Audio))
	for _, a := range res.Audio {
		if a.Codec != "" {
			out = append(out, a.Codec)
		}
	}
	return out
}

func audioLanguages(res *mvcprobe.Result) []string {
	return uniqueLangs(func(yield func(string)) {
		for _, a := range res.Audio {
			yield(a.Language)
		}
	})
}

func subtitleLanguages(res *mvcprobe.Result) []string {
	return uniqueLangs(func(yield func(string)) {
		for _, s := range res.Subtitle {
			yield(s.Language)
		}
	})
}

// uniqueLangs keeps first-seen order and drops blanks, so a condition testing
// `probe_audio_languages contains "eng"` is not defeated by duplicates or by
// tracks the container left untagged.
func uniqueLangs(each func(yield func(string))) []string {
	seen := map[string]bool{}
	var out []string
	each(func(l string) {
		if l == "" || seen[l] {
			return
		}
		seen[l] = true
		out = append(out, l)
	})
	return out
}

// toFloat and toInt64 accept the numeric shapes a Starlark config and a
// JSON-decoded entry field arrive in.
func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}

func kindName(k mvcprobe.Kind) string {
	switch k {
	case mvcprobe.KindDisc:
		return "disc"
	case mvcprobe.KindMatroska:
		return "matroska"
	case mvcprobe.KindMP4:
		return "mp4"
	default:
		return "unknown"
	}
}

// layoutName is probe_3d_layout's vocabulary. "mv-hevc" matches the name
// parser's video_3d_layout, so a rule can compare the two directly.
//
// A layout this build does not know reads as "other", which is 3D, not as
// "2d". It used to be the reverse, and the prober's layouts are appended to
// as formats are learnt: before this build knew MV-HEVC, a Vision Pro disc
// read through a newer prober would have been recorded as a 2D release.
func layoutName(l mvcprobe.Layout) string {
	switch l {
	case mvcprobe.Layout2D:
		return "2d"
	case mvcprobe.LayoutMVC:
		return "mvc"
	case mvcprobe.LayoutMVHEVC:
		return "mv-hevc"
	case mvcprobe.LayoutSideBySide:
		return "sbs"
	case mvcprobe.LayoutTopBottom:
		return "tab"
	default:
		return "other"
	}
}
