package tracker

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// hexHash is a valid 40-char hex info hash used in tests.
const hexHash = "aabbccddeeff00112233445566778899aabbccdd"

// --- announceToScrapeURL ---

func TestAnnounceToScrapeURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"http://tracker.example.com:6969/announce", "http://tracker.example.com:6969/scrape"},
		{"http://tracker.example.com/announce.php", "http://tracker.example.com/scrape.php"},
		{"https://tracker.example.com/announce", "https://tracker.example.com/scrape"},
	}
	for _, c := range cases {
		got, err := announceToScrapeURL(c.in)
		if err != nil {
			t.Errorf("announceToScrapeURL(%q): %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("announceToScrapeURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestAnnounceToScrapeURLNoAnnounce(t *testing.T) {
	_, err := announceToScrapeURL("http://tracker.example.com/tracker")
	if err == nil {
		t.Error("expected error for URL without 'announce' in path")
	}
}

// --- HTTP scrape ---

// --- Scrape (top-level) ---

// --- UDP scrape (local mock) ---

// The batch path dials separately, so it needs the same guarantee.
func TestDoScrapeUDPBatchHonoursCancellation(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer pc.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err = doScrapeUDPBatch(ctx, [][20]byte{{}}, []string{hexHash}, "udp://"+pc.LocalAddr().String())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("a cancelled context should fail the batch scrape")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap context.Canceled, got %v", err)
	}
	if elapsed > 2*time.Second {
		t.Errorf("took %v, want a prompt return — the cancellation was ignored", elapsed)
	}
}

// --- batch scraping ---

func TestScrapeBatchHTTP(t *testing.T) {
	h1 := bytes.Repeat([]byte{0x01}, 20)
	h2 := bytes.Repeat([]byte{0x02}, 20)
	h3 := bytes.Repeat([]byte{0x03}, 20) // tracker won't answer this one

	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		// All requested hashes must arrive in one request.
		if got := len(r.URL.Query()["info_hash"]); got != 3 {
			t.Errorf("info_hash params: got %d, want 3", got)
		}
		resp := "d5:filesd" +
			fmt.Sprintf("%d:%s", len(h1), h1) + "d8:completei7e10:downloadedi0e10:incompletei1ee" +
			fmt.Sprintf("%d:%s", len(h2), h2) + "d8:completei0e10:downloadedi0e10:incompletei0ee" +
			"ee"
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	hashes := []string{
		fmt.Sprintf("%x", h1), fmt.Sprintf("%x", h2), fmt.Sprintf("%x", h3),
	}
	m, err := ScrapeBatch(context.Background(), hashes, srv.URL+"/announce")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 1 {
		t.Errorf("requests: got %d, want 1 (batched)", requests)
	}
	if m[hashes[0]] != 7 || m[hashes[1]] != 0 {
		t.Errorf("results: %v", m)
	}
	if _, ok := m[hashes[2]]; ok {
		t.Error("unanswered hash must be absent (unknown), not zero")
	}
}

func TestScrapeBatchChunks(t *testing.T) {
	// 150 hashes → several requests, each within the URL and hash caps.
	var requests []int
	var longest int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs := r.URL.Query()["info_hash"]
		requests = append(requests, len(hs))
		longest = max(longest, len("http://"+r.Host+r.RequestURI))
		resp := "d5:filesd"
		for _, h := range hs {
			resp += fmt.Sprintf("%d:%s", len(h), h) + "d8:completei5e10:downloadedi0e10:incompletei0ee"
		}
		resp += "ee"
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	hashes := make([]string, 150)
	for i := range hashes {
		b := bytes.Repeat([]byte{byte(i + 1)}, 20)
		hashes[i] = fmt.Sprintf("%x", b)
	}
	m, err := ScrapeBatch(context.Background(), hashes, srv.URL+"/announce")
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, n := range requests {
		if n > maxHashesPerScrape {
			t.Errorf("a request carried %d hashes, cap is %d", n, maxHashesPerScrape)
		}
		total += n
	}
	if total != 150 || len(requests) < 3 {
		t.Errorf("chunking: %v", requests)
	}
	if longest > maxScrapeURLLen {
		t.Errorf("longest scrape URL is %d bytes, cap is %d", longest, maxScrapeURLLen)
	}
	if len(m) != 150 {
		t.Errorf("results: got %d, want 150", len(m))
	}
	if m[hashes[42]] != 5 {
		t.Errorf("hash 42: %d", m[hashes[42]])
	}
}

// 3dtorrents' tracker resets the connection on a URL over 4107 bytes. 70
// hashes that escape to 60 bytes each make 4.4 KB, so the first chunk of an
// 88-entry run failed and 72 dead torrents came back unknown instead of 0.
func TestScrapeBatchFitsATrackerWithA4KBRequestLine(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len("http://"+r.Host+r.RequestURI) > 4107 {
			hj, _ := w.(http.Hijacker)
			conn, _, _ := hj.Hijack()
			_ = conn.Close()
			return
		}
		resp := "d5:filesd"
		for _, h := range r.URL.Query()["info_hash"] {
			resp += fmt.Sprintf("%d:%s", len(h), h) + "d8:completei0e10:downloadedi81e10:incompletei0ee"
		}
		resp += "ee"
		fmt.Fprint(w, resp)
	}))
	defer srv.Close()

	// Bytes outside the unreserved set escape to %XX, the worst case.
	hashes := make([]string, 88)
	for i := range hashes {
		b := bytes.Repeat([]byte{0x80 + byte(i)}, 20)
		hashes[i] = fmt.Sprintf("%x", b)
	}
	m, err := ScrapeBatch(context.Background(), hashes, srv.URL+"/a1b2c3d4e5f6a7b8c9d0e1f2a3b4c5d6/announce")
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 88 {
		t.Errorf("answered %d of 88", len(m))
	}
	for _, h := range hashes {
		if s, ok := m[h]; !ok || s != 0 {
			t.Fatalf("%s: seeds=%d known=%v, want a known 0", h, s, ok)
		}
	}
}

func TestScrapeBatchUDP(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	go func() {
		buf := make([]byte, 2048)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			pkt := buf[:n]
			switch {
			case n == 16 && binary.BigEndian.Uint32(pkt[8:]) == 0: // connect
				resp := make([]byte, 16)
				binary.BigEndian.PutUint32(resp[0:], 0)
				copy(resp[4:8], pkt[12:16]) // txID
				binary.BigEndian.PutUint64(resp[8:], 0xDEADBEEF)
				pc.WriteTo(resp, addr) //nolint:errcheck
			case n >= 36 && binary.BigEndian.Uint32(pkt[8:]) == 2: // scrape
				count := (n - 16) / 20
				resp := make([]byte, 8+12*count)
				binary.BigEndian.PutUint32(resp[0:], 2)
				copy(resp[4:8], pkt[12:16])
				for i := 0; i < count; i++ {
					binary.BigEndian.PutUint32(resp[8+12*i:], uint32(10+i)) //nolint:gosec // test values are tiny
				}
				pc.WriteTo(resp, addr) //nolint:errcheck
			}
		}
	}()

	hashes := []string{
		fmt.Sprintf("%x", bytes.Repeat([]byte{0xAA}, 20)),
		fmt.Sprintf("%x", bytes.Repeat([]byte{0xBB}, 20)),
	}
	m, err := ScrapeBatch(context.Background(), hashes, "udp://"+pc.LocalAddr().String()+"/announce")
	if err != nil {
		t.Fatal(err)
	}
	if m[hashes[0]] != 10 || m[hashes[1]] != 11 {
		t.Errorf("UDP batch results: %v", m)
	}
}
