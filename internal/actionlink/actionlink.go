// Package actionlink mints and verifies the one-click links pipeliner embeds
// in notifications — "follow this series", and anything else a notification
// should be able to trigger from a mail client.
//
// A mail client can only produce a GET with no custom headers, so neither of
// pipeliner's authenticated paths (a session cookie, or the ingest endpoint's
// bearer header) is reachable from an email. These links instead carry their
// own proof: the payload is signed with HMAC-SHA256, so a link that leaks —
// through a referrer header, a mail provider's logs, a forwarded message —
// can only perform the single action it was minted for. It is not a bearer
// credential and cannot be edited into a different request.
//
// Acting on the link is deliberately a two-step flow (see the web handler):
// mail providers and security scanners routinely prefetch links, so the GET
// only renders a confirmation page and the POST behind it performs the work.
package actionlink

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"sync"
)

// Payload is what a signed link asks pipeliner to do: push one item onto an
// ingest queue and trigger a pipeline to drain it.
type Payload struct {
	Queue    string            `json:"queue"`
	Pipeline string            `json:"pipeline"`
	Title    string            `json:"title"`
	Fields   map[string]string `json:"fields,omitempty"`
	// Label is the human-readable action shown on the confirmation page.
	Label string `json:"label,omitempty"`
}

var (
	mu      sync.RWMutex
	baseURL string
	secret  string
)

// Configure sets the public base URL links point at and the secret they are
// signed with. Both empty (the default) disables link generation: the
// template helper renders nothing rather than emitting a URL that cannot
// work.
func Configure(base, key string) {
	mu.Lock()
	defer mu.Unlock()
	baseURL = strings.TrimRight(base, "/")
	secret = key
}

// Enabled reports whether links can be minted.
func Enabled() bool {
	mu.RLock()
	defer mu.RUnlock()
	return baseURL != "" && secret != ""
}

// URL returns a signed action URL, or "" when link generation is not
// configured.
func URL(p Payload) (string, error) {
	mu.RLock()
	base, key := baseURL, secret
	mu.RUnlock()
	if base == "" || key == "" {
		return "", nil
	}
	if p.Queue == "" || p.Pipeline == "" {
		return "", fmt.Errorf("actionlink: queue and pipeline are required")
	}
	blob, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	// The encoded payload is signed verbatim, so verification never depends
	// on re-serialising the struct identically.
	enc := base64.RawURLEncoding.EncodeToString(blob)
	return fmt.Sprintf("%s/action?p=%s&sig=%s", base, url.QueryEscape(enc), sign(key, enc)), nil
}

// Decode verifies a signature and returns the payload it protects.
func Decode(enc, sig string) (Payload, error) {
	mu.RLock()
	key := secret
	mu.RUnlock()
	if key == "" {
		return Payload{}, fmt.Errorf("actionlink: not configured")
	}
	if !hmac.Equal([]byte(sign(key, enc)), []byte(sig)) {
		return Payload{}, fmt.Errorf("actionlink: bad signature")
	}
	blob, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return Payload{}, fmt.Errorf("actionlink: bad payload: %w", err)
	}
	var p Payload
	if err := json.Unmarshal(blob, &p); err != nil {
		return Payload{}, fmt.Errorf("actionlink: bad payload: %w", err)
	}
	if p.Queue == "" || p.Pipeline == "" {
		return Payload{}, fmt.Errorf("actionlink: payload missing queue or pipeline")
	}
	return p, nil
}

func sign(key, enc string) string {
	m := hmac.New(sha256.New, []byte(key))
	m.Write([]byte(enc)) //nolint:errcheck // hash.Write never returns an error
	return hex.EncodeToString(m.Sum(nil))
}
