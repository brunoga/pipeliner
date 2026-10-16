package probe

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	pp "github.com/anacrolix/torrent/peer_protocol"
	"github.com/anacrolix/torrent/tracker"
	"github.com/anacrolix/torrent/version"
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
	// self is this client's peer ID, set once it exists. See swarmLog.toSelf.
	self torrent.PeerID
}

// swarmLog is what the client's callbacks saw for one torrent. Only counts:
// the callbacks run with the client's lock held and must not call back into
// the torrent.
type swarmLog struct {
	handshakes int
	// toSelf counts handshakes with our own peer ID. A tracker can list the
	// client that is asking, the client then dials its own published
	// address, and the library's handshake callback runs before it notices
	// and drops the connection -- in both directions, so one listing reads
	// as two connected peers. Measured on mrshine: every dead 3dtorrents
	// swarm reported "1 peers, 2 connected, 0 unchoked", and both numbers
	// were us.
	toSelf   int
	unchoked map[*torrent.PeerConn]struct{}

	// tracker is each announce URL's result, in the order they were tried,
	// and announced the URL that answered, for the "stopped" announce when
	// the probe ends. announcing is true until the announce finishes.
	tracker    []string
	announced  string
	announcing bool
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
	// The probe announces for itself (see announce) rather than letting the
	// client do it. The client keeps its announce results private: the only
	// way to read one is its status dump, which races with the announcer's
	// own timer -- the race detector caught it -- and gives a peer count but
	// not the tracker's seeder and leecher figures, which are what tell an
	// empty swarm from an unreachable one.
	cfg.DisableTrackers = true
	// The port is announced to the tracker, so with a fixed one that is
	// published on the host a peer that cannot be dialled -- one behind NAT,
	// which is most home seeders -- can still reach us. With 0 the client
	// takes a random port that nothing outside the container can reach, and
	// only peers we can dial ourselves ever connect.
	cfg.ListenPort = port
	cfg.Callbacks.CompletedHandshake = func(pc *torrent.PeerConn, ih metainfo.Hash) {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		s := sc.swarms[ih]
		if s == nil {
			return
		}
		if pc.PeerID == sc.self {
			s.toSelf++
			return
		}
		s.handshakes++
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
	sc.mu.Lock()
	sc.self = cl.PeerID()
	sc.mu.Unlock()
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

// add registers the torrent, starts recording its swarm and announces it to
// its trackers. The announce runs in the background, bounded by ctx; the
// pieces are requested meanwhile and the peers it returns are dialled when
// they arrive.
func (sc *sharedClient) add(ctx context.Context, mi *metainfo.MetaInfo) (*torrent.Torrent, error) {
	t, err := sc.cl.AddTorrent(mi)
	if err != nil {
		return nil, err
	}
	sc.mu.Lock()
	ih := t.InfoHash()
	first := sc.held[ih] == 0
	if first {
		sc.swarms[ih] = &swarmLog{unchoked: map[*torrent.PeerConn]struct{}{}, announcing: true}
	}
	sc.held[ih]++
	sc.mu.Unlock()
	// A second probe of the same release shares the first one's swarm and
	// its announce.
	if first {
		go sc.announce(ctx, t, mi)
	}
	return t, nil
}

// announce asks each tracker in turn for peers, stopping at the first that
// answers, and hands the peers to the torrent. BEP 12 says to stop at the
// first working tracker; a private torrent has one anyway.
func (sc *sharedClient) announce(ctx context.Context, t *torrent.Torrent, mi *metainfo.MetaInfo) {
	ih := t.InfoHash()
	var results []string
	answered := ""
	defer func() {
		sc.mu.Lock()
		defer sc.mu.Unlock()
		if s := sc.swarms[ih]; s != nil {
			s.tracker, s.announced, s.announcing = results, answered, false
		}
	}()

	left := int64(-1)
	if info, err := mi.UnmarshalInfo(); err == nil {
		left = info.TotalLength()
	}
	for _, u := range announceURLs(mi) {
		res, err := sc.announceTo(ctx, u, ih, left, tracker.Started)
		if err != nil {
			results = append(results, hostOf(u)+": "+redact(err.Error()))
			if ctx.Err() != nil {
				return
			}
			continue
		}
		results = append(results, fmt.Sprintf("%s: %d seeders, %d leechers, %d peers",
			hostOf(u), res.Seeders, res.Leechers, len(res.Peers)))
		peers := make([]torrent.PeerInfo, 0, len(res.Peers))
		for _, p := range res.Peers {
			pi := torrent.PeerInfo{
				Addr:   &net.TCPAddr{IP: p.IP, Port: p.Port},
				Source: torrent.PeerSourceTracker,
			}
			copy(pi.Id[:], p.ID)
			peers = append(peers, pi)
		}
		t.AddPeers(peers)
		answered = u
		return
	}
}

func (sc *sharedClient) announceTo(ctx context.Context, u string, ih metainfo.Hash, left int64, ev tracker.AnnounceEvent) (tracker.AnnounceResponse, error) {
	tc, err := tracker.NewClient(u, tracker.NewClientOpts{})
	if err != nil {
		return tracker.AnnounceResponse{}, err
	}
	defer func() { _ = tc.Close() }()
	ctx, cancel := context.WithTimeout(ctx, announceTimeout)
	defer cancel()
	self := sc.cl.PeerID()
	// LocalPort is a bound port, so it fits; checked rather than assumed.
	port := sc.cl.LocalPort()
	if port < 0 || port > 65535 {
		return tracker.AnnounceResponse{}, fmt.Errorf("listen port %d out of range", port)
	}
	return tc.Announce(ctx, tracker.AnnounceRequest{
		InfoHash: ih,
		PeerId:   self,
		Left:     left,
		Event:    ev,
		// The client derives its key the same way, so a tracker that ties
		// announces together by key sees one client.
		Key: int32(binary.BigEndian.Uint32(self[16:20])), //nolint:gosec // reinterprets the bits, as the client does

		NumWant: -1,
		Port:    uint16(port),
	}, tracker.AnnounceOpt{UserAgent: version.DefaultHttpUserAgent})
}

// announceTimeout bounds one tracker request. The library's own default.
const announceTimeout = tracker.DefaultTrackerAnnounceTimeout

// drop releases the torrent, and drops it from the client when no other probe
// is using it. It reports whether it did, so the caller knows whether the
// sample on disk is still someone else's.
func (sc *sharedClient) drop(t *torrent.Torrent) bool {
	sc.mu.Lock()
	ih := t.InfoHash()
	sc.held[ih]--
	last := sc.held[ih] <= 0
	announced := ""
	if last {
		if s := sc.swarms[ih]; s != nil {
			announced = s.announced
		}
		delete(sc.held, ih)
		delete(sc.swarms, ih)
	}
	sc.mu.Unlock()
	if last {
		t.Drop()
		// Tell the tracker we left, so it stops handing our address to other
		// peers for the rest of its announce interval. Best effort, and off
		// the probe's path: a slow tracker must not hold the next entry.
		if announced != "" {
			go func() {
				_, _ = sc.announceTo(context.Background(), announced, ih, -1, tracker.Stopped)
			}()
		}
	}
	return last
}

// SwarmReport says what the swarm did for one probe, so a failure can be told
// apart: a tracker that refused us, a tracker that knew no peers, peers that
// would not answer, and peers that answered but never sent a byte are four
// different problems that all used to read "no peer served any of the sample".
type SwarmReport struct {
	// Tracker is each announce URL's result, by host: the seeders, leechers
	// and peers it reported, or its error; "still announcing" when the
	// request has not returned. The host only, because a private tracker's
	// announce URL carries the user's passkey.
	Tracker string
	// Known is how many peer addresses the client has for the torrent;
	// Dialling, how many connections are still being attempted.
	Known, Dialling int
	// Connected is how many peers completed the handshake over the whole
	// probe; Active, how many are still connected at the end. Neither counts
	// connections to ourselves.
	Connected, Active int
	// ToSelf is how many handshakes turned out to be with ourselves, which
	// means the tracker listed us. A tracker result of "1 peers" with ToSelf
	// above zero is a swarm with nobody else in it.
	ToSelf int
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
		Tracker:    "none in metainfo",
	}
	sc.mu.Lock()
	defer sc.mu.Unlock()
	if s := sc.swarms[t.InfoHash()]; s != nil {
		r.Connected = s.handshakes
		r.ToSelf = s.toSelf
		r.Unchoked = len(s.unchoked)
		switch {
		case s.announcing:
			r.Tracker = "still announcing"
		case len(s.tracker) > 0:
			r.Tracker = strings.Join(s.tracker, "; ")
		}
	}
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

// hostOf names a tracker by host only: a private tracker's announce URL
// carries the user's passkey.
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return "tracker"
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
		"conns_to_self", r.ToSelf,
		"peers_active", r.Active,
		"peers_unchoked", r.Unchoked,
		"useful_bytes", r.Useful,
		"listen_port", r.ListenPort,
	}
}
