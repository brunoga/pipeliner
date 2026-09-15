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
	if err := p.filter(context.Background(), tc, e); err != nil {
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
		fmt.Fprint(w, body)
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
		fmt.Fprint(w, body)
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
		pc.WriteTo(connResp, addr)

		n, addr, err = pc.ReadFrom(buf)
		if err != nil || n < 36 {
			return
		}
		txID = binary.BigEndian.Uint32(buf[12:16])
		scrapeResp := make([]byte, 20)
		binary.BigEndian.PutUint32(scrapeResp[0:], 2)
		binary.BigEndian.PutUint32(scrapeResp[4:], txID)
		binary.BigEndian.PutUint32(scrapeResp[8:], 12)
		pc.WriteTo(scrapeResp, addr)
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

// --- verify tests ---

// TestVerifyScrapesDespiteFeedCount is the phantom-seed case: the indexer
// reports a healthy count but the tracker knows better. With verify=true the
// scrape result must win over the feed count.
func TestVerifyScrapesDespiteFeedCount(t *testing.T) {
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i + 1)
	}
	infoHash := fmt.Sprintf("%x", ih)
	body := buildHTTPScrapeResponse(string(ih[:]), 1) // truth: 1 seed

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "verify": true, "scrape_timeout": "5s"})
	e := entry.New("rare.3d.movie", "http://x.com/t.torrent")
	e.Set("torrent_seeds", 10) // indexer claims 10
	e.Set("torrent_info_hash", infoHash)
	e.Set("torrent_announce_list", []string{srv.URL + "/announce"})

	filter(t, p, e)

	if !e.IsRejected() {
		t.Error("verify should trust the scrape (1 seed) over the feed count (10) and reject")
	}
	if v := e.GetInt("torrent_seeds"); v != 1 {
		t.Errorf("scraped count should be written back: got %d, want 1", v)
	}
}

// Without verify, the same phantom feed count passes unchallenged (the
// pre-existing fast path) — pinned here as the contrast case.
func TestNoVerifyTrustsFeedCount(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 5})
	e := entry.New("rare.3d.movie", "http://x.com/t.torrent")
	e.Set("torrent_seeds", 10)
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("without verify the feed count should be trusted")
	}
}

// TestVerifyFallsBackToFeedWhenNoHash: verify wants to scrape but the entry has
// no resolvable info hash — degrade gracefully to the feed count rather than
// leaving the entry ungated.
func TestVerifyFallsBackToFeedWhenNoHash(t *testing.T) {
	p := makePlugin(t, map[string]any{"min_seeds": 5, "verify": true})
	e := entry.New("bare", "http://x.com/t.torrent") // no hash, not a magnet
	e.Set("torrent_seeds", 10)
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("verify without a scrapeable hash should fall back to the feed count (10 ≥ 5)")
	}

	low := entry.New("bare2", "http://x.com/t2.torrent")
	low.Set("torrent_seeds", 2)
	filter(t, p, low)
	if !low.IsRejected() {
		t.Error("feed fallback must still enforce min_seeds (2 < 5)")
	}
}

// TestVerifyFallsBackToFeedOnScrapeFailure: tracker unreachable → feed count.
func TestVerifyFallsBackToFeedOnScrapeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadURL := srv.URL
	srv.Close() // now unreachable

	var ih [20]byte
	p := makePlugin(t, map[string]any{"min_seeds": 5, "verify": true, "scrape_timeout": "500ms"})
	e := entry.New("rare", "http://x.com/t.torrent")
	e.Set("torrent_seeds", 10)
	e.Set("torrent_info_hash", fmt.Sprintf("%x", ih))
	e.Set("torrent_announce_list", []string{deadURL + "/announce"})

	filter(t, p, e)
	if e.IsRejected() {
		t.Error("scrape failure with verify should fall back to the feed count")
	}
}

func TestVerifyRequiresScrape(t *testing.T) {
	if _, err := newPlugin(map[string]any{"verify": true, "scrape": false}, nil); err == nil {
		t.Error("newPlugin should reject verify=true with scrape=false")
	}
	if errs := validate(map[string]any{"verify": true, "scrape": false}); len(errs) == 0 {
		t.Error("validate should flag verify=true with scrape=false")
	}
	if errs := validate(map[string]any{"verify": true}); len(errs) != 0 {
		t.Errorf("verify=true alone should validate cleanly, got %v", errs)
	}
}

// --- batch scrape tests ---

// TestBatchScrapeOneRequestForManyEntries proves Process batches: many
// entries sharing a tracker produce ONE scrape request, not one per entry.
func TestBatchScrapeOneRequestForManyEntries(t *testing.T) {
	const n = 25
	hashes := make([]string, n)
	var body string
	{
		files := ""
		for i := 0; i < n; i++ {
			var ih [20]byte
			for j := range ih {
				ih[j] = byte(i + 1)
			}
			hashes[i] = fmt.Sprintf("%x", ih)
			seeders := i // entry i has i seeds
			files += fmt.Sprintf("%d:%s", len(ih), string(ih[:])) +
				fmt.Sprintf("d8:completei%de10:downloadedi0e10:incompletei0ee", seeders)
		}
		body = "d5:filesd" + files + "ee"
	}

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests++
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "scrape_timeout": "5s"})
	tc := &plugin.TaskContext{Logger: slog.Default()}
	entries := make([]*entry.Entry, n)
	for i := 0; i < n; i++ {
		e := entry.New(fmt.Sprintf("t%d", i), fmt.Sprintf("http://x/%d.torrent", i))
		e.Set("torrent_info_hash", hashes[i])
		e.Set("torrent_announce_list", []string{srv.URL + "/announce"})
		entries[i] = e
	}
	if _, err := p.Process(context.Background(), tc, entries); err != nil {
		t.Fatal(err)
	}

	if requests != 1 {
		t.Errorf("scrape requests: got %d, want 1 (batched)", requests)
	}
	// Entries 0-4 have <5 seeds → rejected; 5+ accepted (left undecided).
	for i, e := range entries {
		if i < 5 && !e.IsRejected() {
			t.Errorf("entry %d (%d seeds) should be rejected", i, i)
		}
		if i >= 5 && e.IsRejected() {
			t.Errorf("entry %d (%d seeds) should pass", i, i)
		}
	}
}

// TestBatchScrapeUnansweredFallsBack: hashes the tracker doesn't answer fall
// back to the feed count (verify mode) or stay undecided.
func TestBatchScrapeUnansweredFallsBack(t *testing.T) {
	var answered [20]byte
	for j := range answered {
		answered[j] = 0x0A
	}
	body := "d5:filesd" + fmt.Sprintf("%d:%s", len(answered), string(answered[:])) +
		"d8:completei9e10:downloadedi0e10:incompletei0ee" + "ee"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	p := makePlugin(t, map[string]any{"min_seeds": 5, "verify": true, "scrape_timeout": "5s"})
	tc := &plugin.TaskContext{Logger: slog.Default()}

	e1 := entry.New("answered", "http://x/a.torrent")
	e1.Set("torrent_info_hash", fmt.Sprintf("%x", answered))
	e1.Set("torrent_announce_list", []string{srv.URL + "/announce"})
	e1.Set("torrent_seeds", 1) // feed says 1, scrape says 9 → passes

	var unanswered [20]byte
	unanswered[0] = 0x0B
	e2 := entry.New("unanswered", "http://x/b.torrent")
	e2.Set("torrent_info_hash", fmt.Sprintf("%x", unanswered))
	e2.Set("torrent_announce_list", []string{srv.URL + "/announce"})
	e2.Set("torrent_seeds", 2) // feed fallback: 2 < 5 → rejected

	if _, err := p.Process(context.Background(), tc, []*entry.Entry{e1, e2}); err != nil {
		t.Fatal(err)
	}
	if e1.IsRejected() {
		t.Errorf("answered entry should pass on scraped count 9: %s", e1.RejectReason)
	}
	if !e2.IsRejected() {
		t.Error("unanswered entry should fall back to the feed count (2 < 5) and be rejected")
	}
}
