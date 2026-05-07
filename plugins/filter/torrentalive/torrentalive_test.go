package torrentalive

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makePlugin(t *testing.T, cfg map[string]any) *torrentAlivePlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*torrentAlivePlugin)
}

func filter(t *testing.T, p *torrentAlivePlugin, e *entry.Entry) {
	t.Helper()
	tc := &plugin.TaskContext{Logger: slog.Default()}
	if err := p.Filter(context.Background(), tc, e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
}

// --- existing seed-count tests ---

func TestAcceptAboveThreshold(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 10})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_seeds", 15)
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("15 seeds should pass min_seeds=10")
	}
}

func TestRejectBelowThreshold(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 10})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_seeds", 8)
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("8 seeds should fail min_seeds=10")
	}
}

func TestExactlyAtThreshold(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 10})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_seeds", 10)
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("exactly 10 seeds should pass min_seeds=10")
	}
}

func TestNoSeedFieldSkipped(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 10})
	e := entry.New("show", "http://x.com/page")
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("entry without seeds should not be rejected")
	}
}

func TestDefaultMinSeeds(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	if p.minSeeds != 1 {
		t.Errorf("default min_seeds should be 1, got %d", p.minSeeds)
	}
}

func TestInvalidMinSeeds(t *testing.T) {
	_, err := newPlugin(map[string]any{"min_seeds": 0}, nil)
	if err == nil {
		t.Error("expected error for min_seeds=0")
	}
}

// --- scrape config tests ---

func TestDefaultScrapeEnabled(t *testing.T) {
	p := makePlugin(t, map[string]any{})
	if !p.scrape {
		t.Error("scrape should be enabled by default")
	}
}

func TestScrapeDisabled(t *testing.T) {
	p := makePlugin(t, map[string]any{"scrape": false})
	if p.scrape {
		t.Error("scrape should be disabled")
	}
}

func TestInvalidScrapeTimeout(t *testing.T) {
	_, err := newPlugin(map[string]any{"scrape_timeout": "not-a-duration"}, nil)
	if err == nil {
		t.Error("expected error for invalid scrape_timeout")
	}
}

// --- scrape integration tests (mock HTTP tracker) ---

func buildHTTPScrapeResponse(ihBin string, seeders int) string {
	key := fmt.Sprintf("%d:%s", len(ihBin), ihBin)
	stats := fmt.Sprintf("d8:completei%de10:downloadedi0e10:incompletei0ee", seeders)
	return fmt.Sprintf("d5:filesd%s%seee", key, stats)
}

func TestScrapeFromHTTPTracker(t *testing.T) {
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}
	infoHash := fmt.Sprintf("%x", ih)
	body := buildHTTPScrapeResponse(string(ih[:]), 8)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "scrape_timeout": "5s"})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_info_hash", infoHash)
	e.Set("torrent_announce_list", []string{srv.URL + "/announce"})

	filter(t, p, e)

	if e.IsRejected() {
		t.Errorf("8 scraped seeds should pass min_seeds=5; reason: %q", e.RejectReason)
	}
	if v := e.GetInt("torrent_seeds"); v != 8 {
		t.Errorf("seeds written back: got %d, want 8", v)
	}
}

func TestScrapeRejectsLowSeedCount(t *testing.T) {
	var ih [20]byte
	infoHash := fmt.Sprintf("%x", ih)
	body := buildHTTPScrapeResponse(string(ih[:]), 2)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body) //nolint:errcheck
	}))
	defer srv.Close()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "scrape_timeout": "5s"})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_info_hash", infoHash)
	e.Set("torrent_announce_list", []string{srv.URL + "/announce"})

	filter(t, p, e)

	if !e.IsRejected() {
		t.Error("2 scraped seeds should fail min_seeds=5")
	}
}

func TestScrapeTrackerUnreachablePassesThrough(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 5, "scrape_timeout": "1s"})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_info_hash", "aabbccddeeff00112233445566778899aabbccdd")
	e.Set("torrent_announce_list", []string{"http://127.0.0.1:1/announce"})

	filter(t, p, e)

	if e.IsRejected() {
		t.Error("unreachable tracker should leave entry undecided, not rejected")
	}
}

func TestScrapeSkippedWhenNoInfoHash(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 5})
	e := entry.New("show", "http://x.com/page")
	e.Set("announce", "http://tracker.example.com/announce")

	filter(t, p, e)
	if e.IsRejected() {
		t.Error("should be left undecided without info hash")
	}
}

func TestScrapeDisabledNoHTTPCall(t *testing.T) {
	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))
	defer srv.Close()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "scrape": false})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_info_hash", "aabbccddeeff00112233445566778899aabbccdd")
	e.Set("torrent_announce_list", []string{srv.URL + "/announce"})

	filter(t, p, e)

	if called {
		t.Error("tracker should not be contacted when scrape is disabled")
	}
}

// --- UDP tracker scrape test ---

func TestScrapeFromUDPTracker(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer pc.Close()

	go func() {
		buf := make([]byte, 512)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n < 16 {
			return
		}
		txID := binary.BigEndian.Uint32(buf[12:16])
		connResp := make([]byte, 16)
		binary.BigEndian.PutUint32(connResp[0:], 0)
		binary.BigEndian.PutUint32(connResp[4:], txID)
		binary.BigEndian.PutUint64(connResp[8:], 99999)
		pc.WriteTo(connResp, addr) //nolint:errcheck

		n, addr, err = pc.ReadFrom(buf)
		if err != nil || n < 36 {
			return
		}
		txID = binary.BigEndian.Uint32(buf[12:16])
		scrapeResp := make([]byte, 20)
		binary.BigEndian.PutUint32(scrapeResp[0:], 2)
		binary.BigEndian.PutUint32(scrapeResp[4:], txID)
		binary.BigEndian.PutUint32(scrapeResp[8:], 12)
		pc.WriteTo(scrapeResp, addr) //nolint:errcheck
	}()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "scrape_timeout": "5s"})
	e := entry.New("show.torrent", "http://x.com/t.torrent")
	e.Set("torrent_info_hash", "aabbccddeeff00112233445566778899aabbccdd")
	e.Set("torrent_announce_list", []string{"udp://" + pc.LocalAddr().String()})

	filter(t, p, e)

	if e.IsRejected() {
		t.Errorf("12 scraped seeds should pass min_seeds=5; reason: %q", e.RejectReason)
	}
}
