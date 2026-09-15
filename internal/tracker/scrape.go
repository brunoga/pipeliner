// Package tracker implements BitTorrent tracker scraping.
//
// Both the HTTP scrape convention (BEP 48) and the UDP tracker protocol
// (BEP 15) are supported. Scrape returns seeder counts without announcing
// a download.
package tracker

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/bencode"
)

// Scrape returns the seeder count for the given info hash by querying each
// announce URL in turn, stopping at the first successful response.
// Returns (0, nil) on success with zero seeds. Returns (0, err) when every
// tracker fails — callers should treat this as "unknown" and not reject.
func Scrape(ctx context.Context, infoHashHex string, announceURLs []string) (int, error) {
	b, err := hex.DecodeString(infoHashHex)
	if err != nil || len(b) != 20 {
		return 0, fmt.Errorf("tracker: invalid info hash %q", infoHashHex)
	}
	var ih [20]byte
	copy(ih[:], b)

	var lastErr error
	for _, u := range announceURLs {
		seeds, err := scrapeOne(ctx, ih, u)
		if err == nil {
			return seeds, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return 0, fmt.Errorf("tracker: all scrapes failed (last: %w)", lastErr)
	}
	return 0, fmt.Errorf("tracker: no announce URLs provided")
}

func scrapeOne(ctx context.Context, ih [20]byte, announceURL string) (int, error) {
	lower := strings.ToLower(announceURL)
	switch {
	case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
		return scrapeHTTP(ctx, ih, announceURL)
	case strings.HasPrefix(lower, "udp://"):
		return scrapeUDP(ctx, ih, announceURL)
	default:
		return 0, fmt.Errorf("unsupported scheme in %q", announceURL)
	}
}

// --- HTTP scrape (BEP 48) ---

func scrapeHTTP(ctx context.Context, ih [20]byte, announceURL string) (int, error) {
	scrapeURL, err := announceToScrapeURL(announceURL)
	if err != nil {
		return 0, err
	}

	// info_hash is the raw 20-byte binary, percent-encoded.
	reqURL := scrapeURL + "?info_hash=" + url.QueryEscape(string(ih[:]))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return 0, fmt.Errorf("tracker HTTP: build request: %w", err)
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, fmt.Errorf("tracker HTTP: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("tracker HTTP: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if err != nil {
		return 0, fmt.Errorf("tracker HTTP: read body: %w", err)
	}
	return parseHTTPScrape(data, ih)
}

// announceToScrapeURL derives the scrape URL from an announce URL by replacing
// the first occurrence of "announce" in the path with "scrape".
func announceToScrapeURL(announceURL string) (string, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return "", fmt.Errorf("tracker: parse announce URL: %w", err)
	}
	if !strings.Contains(u.Path, "announce") {
		return "", fmt.Errorf("tracker: cannot derive scrape URL from %q (no 'announce' in path)", announceURL)
	}
	u.Path = strings.Replace(u.Path, "announce", "scrape", 1)
	u.RawQuery = ""
	return u.String(), nil
}

func parseHTTPScrape(data []byte, ih [20]byte) (int, error) {
	v, err := bencode.Decode(data)
	if err != nil {
		return 0, fmt.Errorf("tracker HTTP: decode response: %w", err)
	}
	root, ok := v.(map[string]any)
	if !ok {
		return 0, fmt.Errorf("tracker HTTP: response root is not a dict")
	}
	files, ok := root["files"].(map[string]any)
	if !ok {
		return 0, fmt.Errorf("tracker HTTP: missing 'files' dict in response")
	}

	// Try exact match on the 20-byte binary key.
	stats, ok := files[string(ih[:])].(map[string]any)
	if !ok {
		// Fall back to the first entry (some trackers return a single result
		// regardless of requested hash).
		for _, val := range files {
			if m, ok := val.(map[string]any); ok {
				stats = m
				break
			}
		}
	}
	if stats == nil {
		return 0, fmt.Errorf("tracker HTTP: no stats for requested hash")
	}
	complete, _ := stats["complete"].(int64)
	return int(complete), nil
}

// --- UDP tracker protocol (BEP 15) ---

const udpMagic uint64 = 0x41727101980

func scrapeUDP(ctx context.Context, ih [20]byte, announceURL string) (int, error) {
	type result struct {
		seeds int
		err   error
	}
	ch := make(chan result, 1)
	go func() {
		s, err := doScrapeUDP(ctx, ih, announceURL)
		ch <- result{s, err}
	}()
	select {
	case r := <-ch:
		return r.seeds, r.err
	case <-ctx.Done():
		return 0, ctx.Err()
	}
}

func doScrapeUDP(ctx context.Context, ih [20]byte, announceURL string) (int, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return 0, fmt.Errorf("tracker UDP: parse URL: %w", err)
	}
	host := u.Host
	if u.Port() == "" {
		host += ":80"
	}

	timeout := 10 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem > 0 && rem < timeout {
			timeout = rem
		}
	}

	conn, err := net.DialTimeout("udp", host, timeout)
	if err != nil {
		return 0, fmt.Errorf("tracker UDP: dial %s: %w", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck

	txID := rand.Uint32() //nolint:gosec // transaction IDs are correlation tokens, not security primitives

	// 1. Connect request
	connReq := make([]byte, 16)
	binary.BigEndian.PutUint64(connReq[0:], udpMagic)
	binary.BigEndian.PutUint32(connReq[8:], 0) // action: connect
	binary.BigEndian.PutUint32(connReq[12:], txID)
	if _, err := conn.Write(connReq); err != nil {
		return 0, fmt.Errorf("tracker UDP: send connect: %w", err)
	}

	connResp := make([]byte, 16)
	if _, err := io.ReadFull(conn, connResp); err != nil {
		return 0, fmt.Errorf("tracker UDP: recv connect: %w", err)
	}
	if binary.BigEndian.Uint32(connResp[0:]) != 0 {
		return 0, fmt.Errorf("tracker UDP: unexpected action in connect response")
	}
	if binary.BigEndian.Uint32(connResp[4:]) != txID {
		return 0, fmt.Errorf("tracker UDP: transaction ID mismatch in connect response")
	}
	connID := binary.BigEndian.Uint64(connResp[8:])

	// 2. Scrape request
	scrapeReq := make([]byte, 36)
	binary.BigEndian.PutUint64(scrapeReq[0:], connID)
	binary.BigEndian.PutUint32(scrapeReq[8:], 2) // action: scrape
	binary.BigEndian.PutUint32(scrapeReq[12:], txID)
	copy(scrapeReq[16:], ih[:])
	if _, err := conn.Write(scrapeReq); err != nil {
		return 0, fmt.Errorf("tracker UDP: send scrape: %w", err)
	}

	scrapeResp := make([]byte, 20)
	if _, err := io.ReadFull(conn, scrapeResp); err != nil {
		return 0, fmt.Errorf("tracker UDP: recv scrape: %w", err)
	}
	if binary.BigEndian.Uint32(scrapeResp[0:]) != 2 {
		return 0, fmt.Errorf("tracker UDP: unexpected action in scrape response")
	}
	if binary.BigEndian.Uint32(scrapeResp[4:]) != txID {
		return 0, fmt.Errorf("tracker UDP: transaction ID mismatch in scrape response")
	}
	seeders := binary.BigEndian.Uint32(scrapeResp[8:])
	return int(seeders), nil
}

// --- batch scraping ---
//
// Both protocols natively support querying many info hashes in one request:
// the UDP scrape packet carries up to ~70 hashes (BEP 15), and the HTTP
// scrape convention accepts repeated info_hash parameters. Batch scraping is
// what makes seed verification viable on discover-scale pipelines — 2000
// entries from one tracker become ~30 requests instead of 2000.

// maxHashesPerScrape caps hashes per request: the UDP response is 8+12·N
// bytes and must fit a typical MTU, and huge HTTP query strings risk server
// URL-length limits.
const maxHashesPerScrape = 70

// ScrapeBatch returns seeder counts for the given info hashes from a single
// tracker, chunking into requests of at most maxHashesPerScrape hashes. The
// result maps lowercase hex hash → seeder count and contains an entry for
// every hash the tracker answered; missing hashes are unknown, not zero.
// An error is returned only when every chunk failed.
func ScrapeBatch(ctx context.Context, infoHashesHex []string, announceURL string) (map[string]int, error) {
	hashes := make([][20]byte, 0, len(infoHashesHex))
	hexByIdx := make([]string, 0, len(infoHashesHex))
	seen := make(map[string]bool, len(infoHashesHex))
	for _, h := range infoHashesHex {
		lh := strings.ToLower(h)
		if seen[lh] {
			continue
		}
		b, err := hex.DecodeString(lh)
		if err != nil || len(b) != 20 {
			continue // skip malformed hashes rather than failing the batch
		}
		seen[lh] = true
		var ih [20]byte
		copy(ih[:], b)
		hashes = append(hashes, ih)
		hexByIdx = append(hexByIdx, lh)
	}
	if len(hashes) == 0 {
		return nil, fmt.Errorf("tracker: no valid info hashes")
	}

	out := make(map[string]int, len(hashes))
	var lastErr error
	okChunks := 0
	for start := 0; start < len(hashes); start += maxHashesPerScrape {
		end := min(start+maxHashesPerScrape, len(hashes))
		chunk := hashes[start:end]
		chunkHex := hexByIdx[start:end]

		var res map[string]int
		var err error
		lower := strings.ToLower(announceURL)
		switch {
		case strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://"):
			res, err = scrapeHTTPBatch(ctx, chunk, chunkHex, announceURL)
		case strings.HasPrefix(lower, "udp://"):
			res, err = scrapeUDPBatch(ctx, chunk, chunkHex, announceURL)
		default:
			return nil, fmt.Errorf("unsupported scheme in %q", announceURL)
		}
		if err != nil {
			lastErr = err
			continue
		}
		okChunks++
		for k, v := range res {
			out[k] = v
		}
	}
	if okChunks == 0 {
		return nil, fmt.Errorf("tracker: batch scrape failed: %w", lastErr)
	}
	return out, nil
}

func scrapeHTTPBatch(ctx context.Context, chunk [][20]byte, chunkHex []string, announceURL string) (map[string]int, error) {
	scrapeURL, err := announceToScrapeURL(announceURL)
	if err != nil {
		return nil, err
	}
	var sb strings.Builder
	sb.WriteString(scrapeURL)
	for i, ih := range chunk {
		if i == 0 {
			sb.WriteString("?info_hash=")
		} else {
			sb.WriteString("&info_hash=")
		}
		sb.WriteString(url.QueryEscape(string(ih[:])))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sb.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("tracker HTTP: build request: %w", err)
	}
	req.Header.Set("User-Agent", "pipeliner/1.0")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tracker HTTP: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tracker HTTP: status %d", resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return nil, fmt.Errorf("tracker HTTP: read body: %w", err)
	}

	v, err := bencode.Decode(data)
	if err != nil {
		return nil, fmt.Errorf("tracker HTTP: decode response: %w", err)
	}
	root, ok := v.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tracker HTTP: response root is not a dict")
	}
	files, ok := root["files"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("tracker HTTP: missing 'files' dict in response")
	}
	out := make(map[string]int, len(chunk))
	for i, ih := range chunk {
		if stats, ok := files[string(ih[:])].(map[string]any); ok {
			complete, _ := stats["complete"].(int64)
			out[chunkHex[i]] = int(complete)
		}
	}
	// Single-hash quirk preserved from parseHTTPScrape: some trackers return
	// one result under a different key regardless of the requested hash.
	if len(out) == 0 && len(chunk) == 1 {
		for _, val := range files {
			if m, ok := val.(map[string]any); ok {
				complete, _ := m["complete"].(int64)
				out[chunkHex[0]] = int(complete)
				break
			}
		}
	}
	return out, nil
}

func scrapeUDPBatch(ctx context.Context, chunk [][20]byte, chunkHex []string, announceURL string) (map[string]int, error) {
	type result struct {
		m   map[string]int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		m, err := doScrapeUDPBatch(ctx, chunk, chunkHex, announceURL)
		ch <- result{m, err}
	}()
	select {
	case r := <-ch:
		return r.m, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func doScrapeUDPBatch(ctx context.Context, chunk [][20]byte, chunkHex []string, announceURL string) (map[string]int, error) {
	u, err := url.Parse(announceURL)
	if err != nil {
		return nil, fmt.Errorf("tracker UDP: parse URL: %w", err)
	}
	host := u.Host
	if u.Port() == "" {
		host += ":80"
	}

	timeout := 10 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if rem := time.Until(dl); rem > 0 && rem < timeout {
			timeout = rem
		}
	}
	conn, err := net.DialTimeout("udp", host, timeout)
	if err != nil {
		return nil, fmt.Errorf("tracker UDP: dial %s: %w", host, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(timeout)) //nolint:errcheck

	txID := rand.Uint32() //nolint:gosec // correlation token, not a security primitive

	connReq := make([]byte, 16)
	binary.BigEndian.PutUint64(connReq[0:], udpMagic)
	binary.BigEndian.PutUint32(connReq[8:], 0)
	binary.BigEndian.PutUint32(connReq[12:], txID)
	if _, err := conn.Write(connReq); err != nil {
		return nil, fmt.Errorf("tracker UDP: send connect: %w", err)
	}
	connResp := make([]byte, 16)
	if _, err := io.ReadFull(conn, connResp); err != nil {
		return nil, fmt.Errorf("tracker UDP: recv connect: %w", err)
	}
	if binary.BigEndian.Uint32(connResp[0:]) != 0 || binary.BigEndian.Uint32(connResp[4:]) != txID {
		return nil, fmt.Errorf("tracker UDP: bad connect response")
	}
	connID := binary.BigEndian.Uint64(connResp[8:])

	scrapeReq := make([]byte, 16+20*len(chunk))
	binary.BigEndian.PutUint64(scrapeReq[0:], connID)
	binary.BigEndian.PutUint32(scrapeReq[8:], 2)
	binary.BigEndian.PutUint32(scrapeReq[12:], txID)
	for i, ih := range chunk {
		copy(scrapeReq[16+20*i:], ih[:])
	}
	if _, err := conn.Write(scrapeReq); err != nil {
		return nil, fmt.Errorf("tracker UDP: send scrape: %w", err)
	}

	// Response: 8-byte header + 12 bytes (seeders/completed/leechers) per hash
	// in request order.
	scrapeResp := make([]byte, 8+12*len(chunk))
	n, err := io.ReadAtLeast(conn, scrapeResp, 8)
	if err != nil {
		return nil, fmt.Errorf("tracker UDP: recv scrape: %w", err)
	}
	if binary.BigEndian.Uint32(scrapeResp[0:]) != 2 || binary.BigEndian.Uint32(scrapeResp[4:]) != txID {
		return nil, fmt.Errorf("tracker UDP: bad scrape response")
	}
	out := make(map[string]int, len(chunk))
	for i := range chunk {
		off := 8 + 12*i
		if off+12 > n {
			break // tracker answered fewer hashes than asked — rest unknown
		}
		out[chunkHex[i]] = int(binary.BigEndian.Uint32(scrapeResp[off:]))
	}
	return out, nil
}
