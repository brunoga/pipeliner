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
// github.com/brunoga/mvc/probe, and stamps what came back. Nothing is
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
	mvcprobe "github.com/brunoga/mvc/probe"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/internal/torrentpeek"
)

const pluginName = "probe"

const (
	defaultTimeout   = 2 * time.Minute
	defaultMaxPieces = 6
	// defaultParallel is 1 because the bandwidth cost of a SUCCESSFUL probe is
	// the thing worth serialising: several at once is the kind of burst a
	// private tracker notices. Raising it is opt-in.
	defaultParallel = 1
	// defaultNoPeerTimeout gives up on a swarm that has connected nothing and
	// delivered nothing. Measured: a healthy swarm answered in 6.4s, while
	// dead ones burned the full 2m budget each.
	defaultNoPeerTimeout = 30 * time.Second
	// maxTorrentFile bounds the .torrent fetch. Real ones are tens of KB; a
	// disc image's piece hashes push that to a few hundred.
	maxTorrentFile = 8 << 20
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "read a release's own container from a sample of its torrent, before downloading it",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalPerRelease,
		// torrent_info_hash means a metainfo step has resolved this entry to a
		// real torrent, which is what there is to sample.
		Requires:   plugin.RequireAll(entry.FieldTorrentInfoHash),
		MayProduce: producedFields,
		Factory:    newPlugin,
		Validate:   validate,
		Schema: []plugin.FieldSchema{
			{Key: "timeout", Type: plugin.FieldTypeDuration, Default: "2m", Hint: "Budget for one entry's probe, including fetching its pieces"},
			{Key: "max_pieces", Type: plugin.FieldTypeInt, Default: "6", Hint: "Most pieces to fetch for one entry; the probe asks for more only when it names bytes it could not read"},
			{Key: "require", Type: plugin.FieldTypeBool, Hint: "Reject entries whose probe could not run, instead of passing them with probe_ok false"},
			{Key: "parallel", Type: plugin.FieldTypeInt, Default: "1", Hint: "How many entries to probe at once; a successful probe pulls 20-25 MiB, so raising this bursts at the tracker"},
			{Key: "no_peer_timeout", Type: plugin.FieldTypeDuration, Default: "30s", Hint: "Give up early when no peer has connected and no byte has arrived; 0 disables"},
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
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName,
		"timeout", "max_pieces", "require", "parallel", "no_peer_timeout")...)
	return errs
}

type probePlugin struct {
	timeout       time.Duration
	maxPieces     int
	require       bool
	parallel      int
	noPeerTimeout time.Duration

	http *http.Client

	// The probers are fields so the fetch loop — the retry logic, which is
	// the part most likely to be wrong — can be driven directly in tests.
	probeImage    func(r io.ReaderAt, size int64, label string) (*mvcprobe.Result, error)
	probeMatroska func(r io.Reader, label string) (*mvcprobe.Result, error)

	// The torrent client is built on first use: a pipeline that never reaches
	// this node should not open a listening socket or a scratch directory.
	once    sync.Once
	client  *torrent.Client
	dataDir string
	initErr error
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
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
	return &probePlugin{
		timeout:       timeout,
		maxPieces:     maxPieces,
		parallel:      parallel,
		noPeerTimeout: noPeer,
		require:       plugin.OptBool(cfg, "require", false),
		http:          &http.Client{Timeout: 60 * time.Second},
		probeImage:    mvcprobe.Image,
		probeMatroska: mvcprobe.Matroska,
	}, nil
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
	Ends() []int
	Fetch(ctx context.Context, pieces ...int) error
	FetchedPieces() int
}

// ensureClient builds the torrent client once.
//
// DHT is off: these are private torrents, whose peers come from the tracker in
// the metainfo, and a private torrent ignores DHT and PEX anyway. Seeding and
// upload are off because this fetches two pieces and leaves.
func (p *probePlugin) ensureClient() (*torrent.Client, error) {
	p.once.Do(func() {
		dir, err := os.MkdirTemp("", "pipeliner-probe-")
		if err != nil {
			p.initErr = fmt.Errorf("%s: scratch directory: %w", pluginName, err)
			return
		}
		cfg := torrent.NewDefaultClientConfig()
		cfg.DataDir = dir
		cfg.NoDHT = true
		cfg.NoUpload = true
		cfg.Seed = false
		cfg.ListenPort = 0
		cl, err := torrent.NewClient(cfg)
		if err != nil {
			_ = os.RemoveAll(dir)
			p.initErr = fmt.Errorf("%s: torrent client: %w", pluginName, err)
			return
		}
		p.client, p.dataDir = cl, dir
	})
	return p.client, p.initErr
}

// Shutdown closes the client and removes the samples.
func (p *probePlugin) Shutdown(_ context.Context, _ *plugin.TaskContext) error {
	if p.client != nil {
		p.client.Close()
	}
	if p.dataDir != "" {
		return os.RemoveAll(p.dataDir)
	}
	return nil
}

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
		if !e.IsRejected() {
			todo = append(todo, e)
		}
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

			res, pieces, bytes, err := p.probeEntry(ctx, tc, e)
			e.Set(entry.FieldProbePieces, pieces)
			e.Set(entry.FieldProbeBytes, bytes)
			logMu.Lock()
			defer logMu.Unlock()
			if err != nil {
				e.Set(entry.FieldProbeOK, false)
				// A swarm that served nothing is worth saying plainly: it is
				// not just a slow probe, it is a release whose full download
				// would not have gone any better.
				if errors.Is(err, torrentpeek.ErrNoPeers) {
					e.Set(entry.FieldProbeUnreachable, true)
				}
				tc.Logger.Info(pluginName+": could not probe", "entry", e.Title, "err", err)
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
		}(e)
	}
	wg.Wait()
	return entry.PassThrough(entries), nil
}

// probeEntry samples one entry's torrent and probes it. It returns what the
// probe cost even when it failed, so the accounting is never lost.
func (p *probePlugin) probeEntry(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) (*mvcprobe.Result, int, int64, error) {
	cl, err := p.ensureClient()
	if err != nil {
		return nil, 0, 0, err
	}
	ctx, cancel := context.WithTimeout(ctx, p.timeout)
	defer cancel()

	mi, err := p.fetchMetainfo(ctx, e)
	if err != nil {
		return nil, 0, 0, err
	}
	t, err := cl.AddTorrent(mi)
	if err != nil {
		return nil, 0, 0, fmt.Errorf("adding the torrent: %w", err)
	}
	// Dropping the torrent and removing its sample is what keeps this from
	// accumulating sparse files across a long-running daemon.
	defer func() {
		name := ""
		if info := t.Info(); info != nil {
			name = info.BestName()
		}
		t.Drop()
		if name != "" && p.dataDir != "" {
			if err := os.RemoveAll(filepath.Join(p.dataDir, name)); err != nil {
				tc.Logger.Warn(pluginName+": removing the sample", "entry", e.Title, "err", err)
			}
		}
	}()

	select {
	case <-t.GotInfo():
	case <-ctx.Done():
		return nil, 0, 0, fmt.Errorf("waiting for torrent metadata: %w", ctx.Err())
	}

	pk, err := torrentpeek.New(t)
	if err != nil {
		return nil, 0, 0, err
	}
	pk.NoPeerTimeout = p.noPeerTimeout
	defer func() { _ = pk.Close() }()

	res, err := p.readWithRetries(ctx, pk, nameOf(t, e))
	return res, pk.FetchedPieces(), pk.FetchedBytes(), err
}

// readWithRetries fetches the ends of the torrent, probes, and when the prober
// names bytes it could not read, fetches the piece holding them and tries
// again.
//
// The retry is not belt and braces. A disc with hundreds of playlists — Disney
// titles routinely have them — spills its metadata past the last 16 MiB piece,
// so assuming two pieces would fail on real discs while the prober is sitting
// there telling us exactly which piece it wants.
func (p *probePlugin) readWithRetries(ctx context.Context, pk sampler, label string) (*mvcprobe.Result, error) {
	matroska := strings.HasSuffix(strings.ToLower(label), ".mkv")

	if err := pk.Fetch(ctx, pk.Ends()...); err != nil {
		return nil, err
	}
	for {
		var res *mvcprobe.Result
		var err error
		if matroska {
			// A Matroska file states its tracks up front, so the first piece
			// is the whole story and a reader over it suffices.
			res, err = p.probeMatroska(io.NewSectionReader(pk, 0, pk.PieceLength()), label)
		} else {
			res, err = p.probeImage(pk, pk.Size(), label)
		}
		if err == nil {
			return res, nil
		}

		// The prober says which bytes it wanted; our reader says which piece
		// holds them. Either error carries an offset, so prefer the prober's.
		off, ok := missingOffset(err)
		if !ok {
			return nil, err
		}
		if pk.FetchedPieces() >= p.maxPieces {
			return nil, fmt.Errorf("probe still needs byte %d after %d pieces (max_pieces): %w", off, pk.FetchedPieces(), err)
		}
		want := pk.PieceOf(off)
		if err := pk.Fetch(ctx, want); err != nil {
			return nil, err
		}
	}
}

// missingOffset pulls the offset of the first unreadable byte out of either
// the prober's error or our reader's.
func missingOffset(err error) (int64, bool) {
	var md *mvcprobe.MissingDataError
	if errors.As(err, &md) {
		return md.Offset, true
	}
	var mp *torrentpeek.MissingPieceError
	if errors.As(err, &mp) {
		return mp.Offset, true
	}
	return 0, false
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
	default:
		return "unknown"
	}
}

func layoutName(l mvcprobe.Layout) string {
	switch l {
	case mvcprobe.LayoutMVC:
		return "mvc"
	case mvcprobe.LayoutSideBySide:
		return "sbs"
	case mvcprobe.LayoutTopBottom:
		return "tab"
	case mvcprobe.LayoutOther:
		return "other"
	default:
		return "2d"
	}
}
