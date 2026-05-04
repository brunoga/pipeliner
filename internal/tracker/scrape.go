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
