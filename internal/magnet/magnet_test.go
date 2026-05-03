package magnet

import (
	"encoding/base32"
	"encoding/hex"
	"strings"
	"testing"
)

const hexHash = "aabbccddeeff00112233445566778899aabbccdd"

// hexToBase32 converts a hex info hash to its base32 representation.
func hexToBase32(h string) string {
	b, _ := hex.DecodeString(h)
	return base32.StdEncoding.EncodeToString(b)
}

func TestParseHexHash(t *testing.T) {
	m, err := Parse("magnet:?xt=urn:btih:" + hexHash)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.InfoHash != hexHash {
		t.Errorf("InfoHash: got %q, want %q", m.InfoHash, hexHash)
	}
}

func TestParseBase32Hash(t *testing.T) {
	b32 := hexToBase32(hexHash)
	m, err := Parse("magnet:?xt=urn:btih:" + b32)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.InfoHash != hexHash {
		t.Errorf("InfoHash: got %q, want %q", m.InfoHash, hexHash)
	}
}

func TestParseMultipleTrackers(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash +
		"&tr=http://tracker1.example.com/announce" +
		"&tr=udp://tracker2.example.com:6969"
	m, err := Parse(uri)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Trackers) != 2 {
		t.Errorf("want 2 trackers, got %d: %v", len(m.Trackers), m.Trackers)
	}
}

func TestParseDuplicateTrackersDeduped(t *testing.T) {
	tr := "http://tracker.example.com/announce"
	uri := "magnet:?xt=urn:btih:" + hexHash + "&tr=" + tr + "&tr=" + tr
	m, err := Parse(uri)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Trackers) != 1 {
		t.Errorf("want 1 tracker after dedup, got %d", len(m.Trackers))
	}
}

func TestParseDisplayName(t *testing.T) {
	uri := "magnet:?xt=urn:btih:" + hexHash + "&dn=My+Show+S01E01"
	m, err := Parse(uri)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if !strings.Contains(m.DisplayName, "My Show") {
		t.Errorf("DisplayName: got %q", m.DisplayName)
	}
}

func TestParseNoTrackers(t *testing.T) {
	m, err := Parse("magnet:?xt=urn:btih:" + hexHash)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(m.Trackers) != 0 {
		t.Errorf("want 0 trackers, got %d", len(m.Trackers))
	}
}

func TestParseErrorNotMagnet(t *testing.T) {
	_, err := Parse("http://example.com/foo")
	if err == nil {
		t.Error("expected error for non-magnet URI")
	}
}

func TestParseErrorMissingXt(t *testing.T) {
	_, err := Parse("magnet:?dn=foo")
	if err == nil {
		t.Error("expected error for missing xt parameter")
	}
}

func TestParseErrorInvalidHash(t *testing.T) {
	_, err := Parse("magnet:?xt=urn:btih:NOTAHASH")
	if err == nil {
		t.Error("expected error for invalid info hash")
	}
}

func TestParseHashNormalisedToLower(t *testing.T) {
	m, err := Parse("magnet:?xt=urn:btih:" + strings.ToUpper(hexHash))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if m.InfoHash != hexHash {
		t.Errorf("hash not normalised to lowercase; got %q", m.InfoHash)
	}
}
