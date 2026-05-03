// Package magnet parses magnet URIs used in BitTorrent.
package magnet

import (
	"encoding/base32"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
)

// Magnet holds the fields extracted from a magnet URI.
type Magnet struct {
	InfoHash    string   // lowercase hex, always 40 chars
	Trackers    []string // announce URLs from tr= params, deduplicated
	DisplayName string   // from dn= param, may be empty
}

// Parse parses a magnet URI and returns the extracted fields.
// The info hash may be hex-encoded (40 chars) or base32-encoded (32 chars).
func Parse(uri string) (*Magnet, error) {
	if !strings.HasPrefix(uri, "magnet:") {
		return nil, fmt.Errorf("magnet: not a magnet URI")
	}

	u, err := url.Parse(uri)
	if err != nil {
		return nil, fmt.Errorf("magnet: parse URI: %w", err)
	}
	q := u.Query()

	xt := q.Get("xt")
	const btihPrefix = "urn:btih:"
	if !strings.HasPrefix(xt, btihPrefix) {
		return nil, fmt.Errorf("magnet: missing or unsupported xt parameter %q", xt)
	}
	rawHash := xt[len(btihPrefix):]
	infoHash, err := normalizeInfoHash(rawHash)
	if err != nil {
		return nil, fmt.Errorf("magnet: %w", err)
	}

	m := &Magnet{
		InfoHash:    infoHash,
		DisplayName: q.Get("dn"),
	}

	seen := map[string]bool{}
	for _, tr := range q["tr"] {
		if tr != "" && !seen[tr] {
			seen[tr] = true
			m.Trackers = append(m.Trackers, tr)
		}
	}
	return m, nil
}

// normalizeInfoHash accepts a hex (40-char) or base32 (32-char) info hash and
// returns the lowercase hex representation.
func normalizeInfoHash(s string) (string, error) {
	upper := strings.ToUpper(s)
	switch len(s) {
	case 40:
		b, err := hex.DecodeString(s)
		if err != nil {
			return "", fmt.Errorf("invalid hex info hash: %w", err)
		}
		return hex.EncodeToString(b), nil
	case 32:
		// Base32, no padding (20 bytes × 8 bits ÷ 5 bits/char = 32 chars exactly).
		b, err := base32.StdEncoding.DecodeString(upper)
		if err != nil {
			return "", fmt.Errorf("invalid base32 info hash: %w", err)
		}
		return hex.EncodeToString(b), nil
	default:
		return "", fmt.Errorf("invalid info hash length %d (want 32 or 40)", len(s))
	}
}
