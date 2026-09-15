package tracker

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// hexHash is a valid 40-char hex info hash used in tests.
const hexHash = "aabbccddeeff00112233445566778899aabbccdd"

// buildScrapeResponse constructs a minimal bencoded HTTP scrape response for
// the given 20-byte info hash key and seeder count.
func buildScrapeResponse(ihBin string, seeders int) string {
	key := fmt.Sprintf("%d:%s", len(ihBin), ihBin)
	stats := fmt.Sprintf("d8:completei%de10:downloadedi0e10:incompletei0ee", seeders)
	files := fmt.Sprintf("d%se", key+stats)
	return fmt.Sprintf("d5:files%se", files)
}

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

func TestScrapeHTTPSuccess(t *testing.T) {
	// Build a response for our test hash.
	var ih [20]byte
	for i := range ih {
		ih[i] = byte(i)
	}
	body := buildScrapeResponse(string(ih[:]), 42)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify the request hits the scrape endpoint.
		if !containsStr(r.URL.Path, "scrape") {
			t.Errorf("expected scrape path, got %q", r.URL.Path)
		}
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	announceURL := srv.URL + "/announce"
	seeds, err := scrapeHTTP(context.Background(), ih, announceURL)
	if err != nil {
		t.Fatalf("scrapeHTTP: %v", err)
	}
	if seeds != 42 {
		t.Errorf("seeds: got %d, want 42", seeds)
	}
}

func TestScrapeHTTPFallbackToFirstEntry(t *testing.T) {
	// Use a different info hash in the response key — plugin should still read it.
	body := buildScrapeResponse("xxxxxxxxxxxxxxxxxxxx", 7)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	var ih [20]byte
	seeds, err := scrapeHTTP(context.Background(), ih, srv.URL+"/announce")
	if err != nil {
		t.Fatalf("scrapeHTTP: %v", err)
	}
	if seeds != 7 {
		t.Errorf("seeds: got %d, want 7 (fallback to first entry)", seeds)
	}
}

func TestScrapeHTTPBadStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer srv.Close()

	var ih [20]byte
	_, err := scrapeHTTP(context.Background(), ih, srv.URL+"/announce")
	if err == nil {
		t.Error("expected error for HTTP 403")
	}
}

// --- Scrape (top-level) ---

func TestScrapeEmptyAnnounces(t *testing.T) {
	_, err := Scrape(context.Background(), hexHash, nil)
	if err == nil {
		t.Error("expected error for empty announce list")
	}
}

func TestScrapeInvalidHash(t *testing.T) {
	_, err := Scrape(context.Background(), "not-a-hash", []string{"http://t.example.com/announce"})
	if err == nil {
		t.Error("expected error for invalid info hash")
	}
}

func TestScrapeSkipsUnsupportedScheme(t *testing.T) {
	// ws:// is not supported; should fail with "all scrapes failed".
	_, err := Scrape(context.Background(), hexHash, []string{"ws://tracker.example.com/announce"})
	if err == nil {
		t.Error("expected error for unsupported scheme")
	}
}

func TestScrapeUsesFirstSuccess(t *testing.T) {
	var ih [20]byte
	body := buildScrapeResponse(string(ih[:]), 99)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, body)
	}))
	defer srv.Close()

	seeds, err := Scrape(context.Background(), "0000000000000000000000000000000000000000",
		[]string{srv.URL + "/announce"})
	if err != nil {
		t.Fatalf("Scrape: %v", err)
	}
	if seeds != 99 {
		t.Errorf("seeds: got %d, want 99", seeds)
	}
}

// --- UDP scrape (local mock) ---

func TestScrapeUDPSuccess(t *testing.T) {
	// Start a UDP server that mimics a tracker.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen UDP: %v", err)
	}
	defer pc.Close()

	go func() {
		buf := make([]byte, 512)
		// Handle connect request.
		n, addr, err := pc.ReadFrom(buf)
		if err != nil || n < 16 {
			return
		}
		txID := binary.BigEndian.Uint32(buf[12:16])
		// Send connect response.
		connResp := make([]byte, 16)
		binary.BigEndian.PutUint32(connResp[0:], 0) // action: connect
		binary.BigEndian.PutUint32(connResp[4:], txID)
		binary.BigEndian.PutUint64(connResp[8:], 12345) // connection ID
		pc.WriteTo(connResp, addr)

		// Handle scrape request.
		n, addr, err = pc.ReadFrom(buf)
		if err != nil || n < 36 {
			return
		}
		txID = binary.BigEndian.Uint32(buf[12:16])
		// Send scrape response with 25 seeders.
		scrapeResp := make([]byte, 20)
		binary.BigEndian.PutUint32(scrapeResp[0:], 2) // action: scrape
		binary.BigEndian.PutUint32(scrapeResp[4:], txID)
		binary.BigEndian.PutUint32(scrapeResp[8:], 25)  // seeders
		binary.BigEndian.PutUint32(scrapeResp[12:], 10) // completed
		binary.BigEndian.PutUint32(scrapeResp[16:], 3)  // leechers
		pc.WriteTo(scrapeResp, addr)
	}()

	addr := pc.LocalAddr().String()
	var ih [20]byte
	seeds, err := scrapeUDP(context.Background(), ih, "udp://"+addr)
	if err != nil {
		t.Fatalf("scrapeUDP: %v", err)
	}
	if seeds != 25 {
		t.Errorf("seeds: got %d, want 25", seeds)
	}
}

func containsStr(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(s) > 0 && containsStr(s[1:], sub) || s[:len(sub)] == sub)
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
	// 150 hashes → 3 requests of ≤70.
	var requests []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hs := r.URL.Query()["info_hash"]
		requests = append(requests, len(hs))
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
	if len(requests) != 3 || requests[0] != 70 || requests[1] != 70 || requests[2] != 10 {
		t.Errorf("chunking: %v", requests)
	}
	if len(m) != 150 {
		t.Errorf("results: got %d, want 150", len(m))
	}
	if m[hashes[42]] != 5 {
		t.Errorf("hash 42: %d", m[hashes[42]])
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
					binary.BigEndian.PutUint32(resp[8+12*i:], uint32(10+i)) // seeders
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
