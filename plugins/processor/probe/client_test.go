package probe

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"

	"github.com/brunoga/pipeliner/internal/plugin"
)

// freePort finds a port that is free for both TCP and UDP, which is what the
// client binds (uTP runs over UDP on the same port).
func freePort(t *testing.T) int {
	t.Helper()
	for range 20 {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Skipf("cannot listen here: %v", err)
		}
		port := l.Addr().(*net.TCPAddr).Port
		_ = l.Close()
		u, err := net.ListenPacket("udp", net.JoinHostPort("", strconv.Itoa(port)))
		if err != nil {
			continue
		}
		_ = u.Close()
		return port
	}
	t.Skip("no port free for both TCP and UDP")
	return 0
}

func portPlugin(t *testing.T, port int) *probePlugin {
	t.Helper()
	pl, err := newPlugin(map[string]any{"listen_port": float64(port)}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return pl.(*probePlugin)
}

// TestNodesOnOnePortShareOneClient is why the client is shared at all: a
// config has a probe node per lane, and two clients cannot both bind one
// fixed port.
func TestNodesOnOnePortShareOneClient(t *testing.T) {
	port := freePort(t)
	a, b := portPlugin(t, port), portPlugin(t, port)

	sa, err := a.ensureClient()
	if err != nil {
		t.Fatalf("first node: %v", err)
	}
	sb, err := b.ensureClient()
	if err != nil {
		t.Fatalf("second node on the same port: %v", err)
	}
	if sa != sb {
		t.Fatal("two nodes on one port built two clients")
	}
	if got := sa.cl.LocalPort(); got != port {
		t.Errorf("client listens on %d, want the configured %d", got, port)
	}

	a.Shutdown()
	if _, err := os.Stat(sa.dir); err != nil {
		t.Fatalf("the first node's shutdown removed a client the second still holds: %v", err)
	}
	if _, err := b.ensureClient(); err != nil {
		t.Fatalf("the second node lost its client: %v", err)
	}

	b.Shutdown()
	if _, err := os.Stat(sa.dir); !os.IsNotExist(err) {
		t.Errorf("the last shutdown should remove the scratch directory, stat err = %v", err)
	}
	// The port is free again: a fresh node can bind it.
	c := portPlugin(t, port)
	if _, err := c.ensureClient(); err != nil {
		t.Errorf("the port was not released: %v", err)
	}
	c.Shutdown()
}

// TestShutdownIsTheExecutorsShutdown: the method used to take a context and
// return an error, which made it a different method from the one the
// executor calls, so it never ran and every reload leaked a client. With a
// fixed port the leak would hold the port against the next config.
func TestShutdownIsTheExecutorsShutdown(t *testing.T) {
	pl, err := newPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	sd, ok := pl.(plugin.ShutdownPlugin)
	if !ok {
		t.Fatal("probe does not implement plugin.ShutdownPlugin, so the executor never shuts it down")
	}
	p := pl.(*probePlugin)
	sc, err := p.ensureClient()
	if err != nil {
		t.Skipf("cannot start a torrent client here: %v", err)
	}
	sd.Shutdown()
	if _, err := os.Stat(sc.dir); !os.IsNotExist(err) {
		t.Errorf("Shutdown left the scratch directory behind, stat err = %v", err)
	}
}

func TestListenPortIsValidated(t *testing.T) {
	for _, tc := range []struct {
		v    any
		want bool // true = valid
	}{
		{float64(0), true},
		{float64(50413), true},
		{float64(65535), true},
		{float64(-1), false},
		{float64(65536), false},
		{float64(1.5), false},
		{"50413", false},
	} {
		errs := validate(map[string]any{"listen_port": tc.v})
		if (len(errs) == 0) != tc.want {
			t.Errorf("listen_port=%#v: errors %v, want valid=%v", tc.v, errs, tc.want)
		}
	}
}

// smallTorrent is metainfo for a torrent nobody seeds, announcing to url.
func smallTorrent(t *testing.T, announce string) *metainfo.MetaInfo {
	t.Helper()
	info := metainfo.Info{Name: "x.bin", PieceLength: 16 << 10, Length: 16 << 10, Pieces: make([]byte, 20)}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	return &metainfo.MetaInfo{InfoBytes: b, Announce: announce}
}

// TestADroppedTorrentStaysForTheOtherProbe: two pipelines probing the same
// release get the same *Torrent back from the client, and the first to finish
// must not drop it from under the second.
func TestADroppedTorrentStaysForTheOtherProbe(t *testing.T) {
	sc, err := acquireClient(0)
	if err != nil {
		t.Skipf("cannot start a torrent client here: %v", err)
	}
	defer sc.release()
	mi := smallTorrent(t, "")
	t1, err := sc.add(mi)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := sc.add(mi)
	if err != nil {
		t.Fatal(err)
	}
	if sc.drop(t1) {
		t.Error("the first drop dropped a torrent another probe holds")
	}
	if _, ok := sc.cl.Torrent(t2.InfoHash()); !ok {
		t.Fatal("the torrent is gone while a probe still holds it")
	}
	if !sc.drop(t2) {
		t.Error("the last drop did not drop the torrent")
	}
	if _, ok := sc.cl.Torrent(t2.InfoHash()); ok {
		t.Error("the torrent is still in the client after the last drop")
	}
}

// TestTrackerResultIsReadFromTheLibrary runs the real client against a fake
// tracker, so the status-text parsing is checked against what the library
// actually prints rather than against a copy of its format. A library upgrade
// that changes that text fails here instead of quietly turning every log line
// into "unknown".
func TestTrackerResultIsReadFromTheLibrary(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want string
	}{
		{"refused", "d14:failure reason20:unregistered torrente", "unregistered torrent"},
		{"one peer", "d8:intervali1800e5:peers6:\x7f\x00\x00\x01\x1a\xe1e", "1 peers"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tc.body))
			}))
			defer srv.Close()
			sc, err := acquireClient(0)
			if err != nil {
				t.Skipf("cannot start a torrent client here: %v", err)
			}
			defer sc.release()

			// The passkey is in the path, as private trackers put it.
			announce := srv.URL + "/s3cr3tpasskey/announce"
			mi := smallTorrent(t, announce)
			tt, err := sc.add(mi)
			if err != nil {
				t.Fatal(err)
			}
			defer sc.drop(tt)

			var r SwarmReport
			deadline := time.Now().Add(10 * time.Second)
			for time.Now().Before(deadline) {
				r = sc.report(tt, mi)
				if strings.Contains(r.Tracker, tc.want) {
					break
				}
				time.Sleep(50 * time.Millisecond)
			}
			if !strings.Contains(r.Tracker, tc.want) {
				t.Fatalf("tracker = %q, want it to contain %q", r.Tracker, tc.want)
			}
			if strings.Contains(r.Tracker, "s3cr3tpasskey") {
				t.Errorf("the passkey reached the report: %q", r.Tracker)
			}
		})
	}
}

func TestTrackerSummaryFromStatusText(t *testing.T) {
	const passkey = "0123456789abcdef"
	ann := "https://tracker.example/" + passkey + "/announce"
	status := strings.Join([]string{
		"# Torrents: 2 (2 incomplete)",
		"",
		"other",
		"Infohash: 1111111111111111111111111111111111111111",
		"Enabled trackers:",
		`    URL  Extra`,
		`    "` + ann + `"  next ann: anytime, last ann: 99 peers`,
		"",
		"ours",
		"Infohash: 2222222222222222222222222222222222222222",
		"Enabled trackers:",
		`    URL  Extra`,
		`    "` + ann + `"  next ann: anytime, last ann: Get "` + ann + `?info_hash=x&port=1": dial tcp: i/o timeout`,
		`    "udp://other.example:80/announce"  next ann: anytime, last ann: never`,
		"",
	}, "\n")
	got := trackerSummary(status, "2222222222222222222222222222222222222222",
		[]string{ann, "udp://other.example:80/announce", "http://missing.example/announce"})
	for _, want := range []string{
		"tracker.example: Get",
		"dial tcp: i/o timeout",
		"other.example:80: never",
		"missing.example: unknown",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("summary %q lacks %q", got, want)
		}
	}
	if strings.Contains(got, "99 peers") {
		t.Errorf("summary took another torrent's line: %q", got)
	}
	if strings.Contains(got, passkey) {
		t.Errorf("summary leaks the passkey: %q", got)
	}
}

// TestReportCountsWhatThePeersDid drives a real seeder, so the callbacks
// feeding the report are checked against real connections.
func TestReportCountsWhatThePeersDid(t *testing.T) {
	dir := t.TempDir()
	payload := make([]byte, 4*(64<<10))
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	path := filepath.Join(dir, "payload.bin")
	if err := os.WriteFile(path, payload, 0o600); err != nil {
		t.Fatal(err)
	}
	info := metainfo.Info{PieceLength: 64 << 10}
	if err := info.BuildFromFilePath(path); err != nil {
		t.Fatal(err)
	}
	b, err := bencode.Marshal(info)
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: b}

	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dir
	cfg.NoDHT = true
	cfg.DisableTrackers = true
	cfg.Seed = true
	cfg.ListenPort = 0
	seeder, err := torrent.NewClient(cfg)
	if err != nil {
		t.Skipf("cannot start a torrent client here: %v", err)
	}
	defer seeder.Close()
	st, err := seeder.AddTorrent(mi)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.VerifyDataContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	if seeder.LocalPort() == 0 {
		t.Skip("the seeder is not listening; the sandbox forbids it")
	}

	sc, err := acquireClient(0)
	if err != nil {
		t.Skipf("cannot start a torrent client here: %v", err)
	}
	defer sc.release()
	tt, err := sc.add(mi)
	if err != nil {
		t.Fatal(err)
	}
	defer sc.drop(tt)
	tt.AddPeers([]torrent.PeerInfo{{Addr: &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: seeder.LocalPort()}}})
	tt.DownloadPieces(0, 1)
	deadline := time.Now().Add(15 * time.Second)
	for tt.PieceBytesMissing(0) != 0 {
		if time.Now().After(deadline) {
			t.Skip("the piece did not arrive")
		}
		time.Sleep(20 * time.Millisecond)
	}

	r := sc.report(tt, mi)
	if r.Connected < 1 {
		t.Errorf("connected = %d, want at least the seeder", r.Connected)
	}
	if r.Unchoked < 1 {
		t.Errorf("unchoked = %d, but the seeder sent a piece, so it unchoked us", r.Unchoked)
	}
	if r.Useful < 64<<10 {
		t.Errorf("useful bytes = %d, want at least the piece (%d)", r.Useful, 64<<10)
	}
	if r.Tracker != "none in metainfo" {
		t.Errorf("tracker = %q for a torrent with no announce URL", r.Tracker)
	}
}
