package probe

import (
	"bufio"
	"bytes"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
)

// One torrent client per listen port, shared by every probe node in the
// process.
//
// A fixed port is only useful if exactly one socket holds it, and a config
// routinely has several probe nodes -- 3d-mvc has one per lane -- each of which
// used to build its own client. Two of them on one fixed port cannot both
// bind. A reload makes it worse: the new config's nodes are built while the
// old ones may still be finishing a run, so a per-node client would find the
// port taken by its own predecessor. Sharing by port, with a reference count
// that Shutdown releases, gives every node the same client and closes it when
// the last one lets go.
var (
	clientsMu sync.Mutex
	clients   = map[int]*sharedClient{}
)

type sharedClient struct {
	port int
	cl   *torrent.Client
	dir  string
	refs int

	mu sync.Mutex
	// held counts probes using each torrent. Two pipelines can probe the same
	// release at once, AddTorrent then returns the same *Torrent to both, and
	// the first to finish must not drop it from under the other.
	held map[metainfo.Hash]int
	// swarms collects what each torrent's peers did. The callbacks that feed
	// it are client-wide, so it is keyed by info-hash.
	swarms map[metainfo.Hash]*swarmLog
}

// swarmLog is what the client's callbacks saw for one torrent. Only counts:
// the callbacks run with the client's lock held and must not call back into
// the torrent.
type swarmLog struct {
	handshakes int
	unchoked   map[*torrent.PeerConn]struct{}
}

// newTorrentClient is a variable so tests can build a client without binding
// the port the test asked for.
var newTorrentClient = torrent.NewClient

func acquireClient(port int) (*sharedClient, error) {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	if sc, ok := clients[port]; ok {
		sc.refs++
		return sc, nil
	}
	dir, err := os.MkdirTemp("", "pipeliner-probe-")
	if err != nil {
		return nil, fmt.Errorf("%s: scratch directory: %w", pluginName, err)
	}
	sc := &sharedClient{
		port:   port,
		dir:    dir,
		refs:   1,
		held:   map[metainfo.Hash]int{},
		swarms: map[metainfo.Hash]*swarmLog{},
	}
	// DHT is off: these are private torrents, whose peers come from the
	// tracker in the metainfo, and a private torrent ignores DHT and PEX
	// anyway. Seeding and upload are off because this fetches two pieces and
	// leaves.
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.NoDHT = true
	cfg.NoUpload = true
	cfg.Seed = false
	// The port is announced to the tracker, so with a fixed one that is
	// published on the host a peer that cannot be dialled -- one behind NAT,
	// which is most home seeders -- can still reach us. With 0 the client
	// takes a random port that nothing outside the container can reach, and
	// only peers we can dial ourselves ever connect.
	cfg.ListenPort = port
	cfg.Callbacks.CompletedHandshake = func(_ *torrent.PeerConn, ih metainfo.Hash) {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if s := sc.swarms[ih]; s != nil {
			s.handshakes++
		}
	}
	cfg.Callbacks.ReadMessage = func(pc *torrent.PeerConn, m *pp.Message) {
		if m.Type != pp.Unchoke {
			return
		}
		t := pc.Torrent()
		if t == nil {
			return
		}
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if s := sc.swarms[t.InfoHash()]; s != nil {
			s.unchoked[pc] = struct{}{}
		}
	}
	cl, err := newTorrentClient(cfg)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, fmt.Errorf("%s: torrent client on port %d: %w", pluginName, port, err)
	}
	sc.cl = cl
	clients[port] = sc
	return sc, nil
}

func (sc *sharedClient) release() {
	clientsMu.Lock()
	defer clientsMu.Unlock()
	sc.refs--
	if sc.refs > 0 {
		return
	}
	delete(clients, sc.port)
	sc.cl.Close()
	_ = os.RemoveAll(sc.dir)
}

// add registers the torrent and starts recording its swarm.
func (sc *sharedClient) add(mi *metainfo.MetaInfo) (*torrent.Torrent, error) {
	t, err := sc.cl.AddTorrent(mi)
	if err != nil {
		return nil, err
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	ih := t.InfoHash()
	if sc.held[ih] == 0 {
		sc.swarms[ih] = &swarmLog{unchoked: map[*torrent.PeerConn]struct{}{}}
	}
	sc.held[ih]++
	return t, nil
}

// drop releases the torrent, and drops it from the client when no other probe
// is using it. It reports whether it did, so the caller knows whether the
// sample on disk is still someone else's.
func (sc *sharedClient) drop(t *torrent.Torrent) bool {
	sc.mu.Lock()
	ih := t.InfoHash()
	sc.held[ih]--
	last := sc.held[ih] <= 0
	if last {
		delete(sc.held, ih)
		delete(sc.swarms, ih)
	}
	sc.mu.Unlock()
	if last {
		t.Drop()
	}
	return last
}

// SwarmReport says what the swarm did for one probe, so a failure can be told
// apart: a tracker that refused us, a tracker that knew no peers, peers that
// would not answer, and peers that answered but never sent a byte are four
// different problems that all used to read "no peer served any of the sample".
type SwarmReport struct {
	// Tracker is each announce URL's last result, by host: "N peers", the
	// tracker's error, or "never" when no announce completed. The host only,
	// because a private tracker's announce URL carries the user's passkey.
	Tracker string
	// Known is how many peer addresses the client has for the torrent;
	// Dialling, how many connections are still being attempted.
	Known, Dialling int
	// Connected is how many peers completed the handshake over the whole
	// probe; Active, how many are still connected at the end.
	Connected, Active int
	// Unchoked is how many peers agreed to send us data. A peer that
	// connects but never unchokes serves nothing, and this client uploads
	// nothing to earn it.
	Unchoked int
	// Useful is the bytes of piece data received that we asked for.
	Useful int64
	// ListenPort is the port the client announced.
	ListenPort int
}

func (sc *sharedClient) report(t *torrent.Torrent, mi *metainfo.MetaInfo) SwarmReport {
	st := t.Stats()
	r := SwarmReport{
		Known:      st.TotalPeers,
		Dialling:   st.HalfOpenPeers,
		Active:     st.ActivePeers,
		Useful:     st.BytesReadUsefulData.Int64(),
		ListenPort: sc.cl.LocalPort(),
	}
	sc.mu.Lock()
	if s := sc.swarms[t.InfoHash()]; s != nil {
		r.Connected = s.handshakes
		r.Unchoked = len(s.unchoked)
	}
	sc.mu.Unlock()

	var status bytes.Buffer
	sc.cl.WriteStatus(&status)
	r.Tracker = trackerSummary(status.String(), t.InfoHash().HexString(), announceURLs(mi))
	return r
}

func announceURLs(mi *metainfo.MetaInfo) []string {
	var urls []string
	seen := map[string]bool{}
	for _, tier := range mi.UpvertedAnnounceList() {
		for _, u := range tier {
			if u != "" && !seen[u] {
				seen[u] = true
				urls = append(urls, u)
			}
		}
	}
	return urls
}

// trackerSummary pulls each tracker's last announce result for one torrent
// out of the client's status text.
//
// The library exposes no API for an HTTP tracker's announce result -- its
// status callbacks fire for websocket trackers only -- but its status dump
// prints one line per tracker, `"<url>"  next ann: <d>, last ann: <result>`,
// in the torrent's section. That text is not a stable interface, which is why
// this only ever feeds a log line: a format change makes it say "unknown",
// never makes a probe fail.
func trackerSummary(status, infoHashHex string, urls []string) string {
	section := torrentSection(status, infoHashHex)
	var parts []string
	for _, raw := range urls {
		host := raw
		if u, err := url.Parse(raw); err == nil && u.Host != "" {
			host = u.Host
		}
		result := "unknown"
		quoted := fmt.Sprintf("%q", raw)
		for _, line := range section {
			if !strings.Contains(line, quoted) {
				continue
			}
			if i := strings.Index(line, "last ann: "); i >= 0 {
				result = strings.TrimSpace(line[i+len("last ann: "):])
			}
			break
		}
		parts = append(parts, host+": "+redact(result))
	}
	if len(parts) == 0 {
		return "none in metainfo"
	}
	return strings.Join(parts, "; ")
}

// torrentSection returns the status lines of the torrent with this info-hash,
// which runs from its "Infohash:" line to the blank line that ends it.
func torrentSection(status, infoHashHex string) []string {
	var lines []string
	in := false
	sc := bufio.NewScanner(strings.NewReader(status))
	sc.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !in {
			if strings.HasPrefix(line, "Infohash: ") && strings.EqualFold(strings.TrimPrefix(line, "Infohash: "), infoHashHex) {
				in = true
			}
			continue
		}
		if line == "" {
			break
		}
		lines = append(lines, line)
	}
	return lines
}

// redact keeps a tracker error from repeating an announce URL, and with it
// the passkey, into the log. An HTTP client error quotes the URL it failed on,
// with the announce query appended, so this cuts every URL in the text back to
// its scheme and host rather than matching the metainfo's URL exactly.
func redact(s string) string {
	return urlInText.ReplaceAllString(s, "$1://$2/…")
}

var urlInText = regexp.MustCompile(`(https?|udp|wss?)://([^/\s"'?]+)[^\s"']*`)

// logArgs renders the report as log attributes. A nil report -- the probe
// never got as far as the swarm -- adds nothing.
func (r *SwarmReport) logArgs() []any {
	if r == nil {
		return nil
	}
	return []any{
		"tracker", r.Tracker,
		"peers_known", r.Known,
		"peers_dialling", r.Dialling,
		"peers_connected", r.Connected,
		"peers_active", r.Active,
		"peers_unchoked", r.Unchoked,
		"useful_bytes", r.Useful,
		"listen_port", r.ListenPort,
	}
}
