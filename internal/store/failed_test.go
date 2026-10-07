package store

import (
	"strings"
	"testing"
)

func TestFailedStoreLifecycle(t *testing.T) {
	s := openMem(t)
	fs := NewFailedStore(s.Bucket(FailedBucketName))

	url := "https://indexer.example.com/release/123.torrent"

	if fs.IsFailed(url) {
		t.Fatal("url should not be failed before MarkFailed")
	}
	if _, ok := fs.Get(url); ok {
		t.Fatal("Get should miss before MarkFailed")
	}

	if err := fs.MarkFailed("", url, "stalled for 6h"); err != nil {
		t.Fatalf("MarkFailed: %v", err)
	}

	if !fs.IsFailed(url) {
		t.Fatal("url should be failed after MarkFailed")
	}
	rec, ok := fs.Get(url)
	if !ok {
		t.Fatal("Get should hit after MarkFailed")
	}
	if rec.URL != url {
		t.Errorf("rec.URL = %q", rec.URL)
	}
	if rec.Reason != "stalled for 6h" {
		t.Errorf("rec.Reason = %q", rec.Reason)
	}
	if rec.FailedAt.IsZero() {
		t.Error("rec.FailedAt should be set")
	}

	// Different URL stays unaffected.
	if fs.IsFailed("https://indexer.example.com/release/456.torrent") {
		t.Error("unrelated url reported failed")
	}
}

func TestFailedStoreRemarkOverwrites(t *testing.T) {
	s := openMem(t)
	fs := NewFailedStore(s.Bucket(FailedBucketName))

	url := "magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := fs.MarkFailed("", url, "first"); err != nil {
		t.Fatal(err)
	}
	if err := fs.MarkFailed("", url, "second"); err != nil {
		t.Fatal(err)
	}
	rec, ok := fs.Get(url)
	if !ok {
		t.Fatal("Get should hit")
	}
	if rec.Reason != "second" {
		t.Errorf("rec.Reason = %q, want second", rec.Reason)
	}
}

// TestFailedLookupSurvivesRotatingURLs is the production bug: Jackett
// re-encrypts its download links on every search, so the same dead release
// arrives with a different URL each run. A URL-keyed blocklist stopped
// matching and one seedless torrent was re-downloaded nine times — purged by
// the janitor, re-found under a fresh URL, grabbed again. The info hash does
// not rotate, so the blocklist must key on it.
func TestFailedLookupSurvivesRotatingURLs(t *testing.T) {
	s := openMem(t)
	fs := NewFailedStore(s.Bucket(FailedBucketName))
	const hash = "aabbccddeeff00112233445566778899aabbccdd"
	firstURL := "https://jackett/dl/3dtorrents/?path=Q2ZESjhJ...HsHALS"

	if err := fs.MarkFailed(hash, firstURL, "janitor: no seeds"); err != nil {
		t.Fatal(err)
	}

	// Next search: same release, freshly encrypted URL.
	rotatedURL := "https://jackett/dl/3dtorrents/?path=Q2ZESjhJ...HsHCC0"
	if _, ok := fs.Lookup("", rotatedURL); ok {
		t.Fatal("test premise wrong: the rotated URL should not match by URL")
	}
	rec, ok := fs.Lookup(hash, rotatedURL)
	if !ok {
		t.Fatal("a failed release must stay blocked when its URL rotates")
	}
	if rec.Reason != "janitor: no seeds" {
		t.Errorf("reason = %q", rec.Reason)
	}

	// Case-insensitive, since hashes arrive in either case.
	if _, ok := fs.Lookup(strings.ToUpper(hash), rotatedURL); !ok {
		t.Error("hash lookup should be case-insensitive")
	}

	// The exact failed URL still matches, so records written before hashes
	// were stored keep working.
	if _, ok := fs.Lookup("", firstURL); !ok {
		t.Error("the original URL must still match")
	}

	// An unrelated release is unaffected.
	if _, ok := fs.Lookup("ffffffffffffffffffffffffffffffffffffffff", "https://other"); ok {
		t.Error("unrelated releases must not be blocked")
	}
}

// The bug this exists for, end to end. A Jackett release fails under one proxy
// URL and comes back under a different one, with no info hash because the
// indexer supplies none. Before the stable key, nothing matched and the dead
// torrent was grabbed again — nine times, in the case that prompted the
// original fix.
func TestLookupMatchesAcrossARotatingProxyURL(t *testing.T) {
	fs := NewFailedStore(openMem(t).Bucket(FailedBucketName))

	const stable = "src:jackett:3dtorrents|http://www.3dtorrents.org/download.php?id=b263e30"
	// Grabbed and failed under the URL the first search handed out.
	if err := fs.MarkFailed("", "https://jackett/dl?path=FIRST-NONCE", "no seeds", stable); err != nil {
		t.Fatal(err)
	}

	// The next run finds the same release under a fresh URL and still has no
	// hash to offer — only the stable key.
	if _, ok := fs.Lookup("", "https://jackett/dl?path=SECOND-NONCE"); ok {
		t.Fatal("a rotating URL must not match on its own; the fixture is wrong")
	}
	rec, ok := fs.Lookup("", "https://jackett/dl?path=SECOND-NONCE", stable)
	if !ok {
		t.Fatal("the stable key should have matched the failed record")
	}
	if rec.Reason != "no seeds" {
		t.Errorf("reason = %q, want it carried through", rec.Reason)
	}
}

// The hash still outranks everything, and a record written by an older build —
// which has no stable keys — is still found by hash or by its exact URL.
func TestLookupPrefersHashAndKeepsLegacyRecords(t *testing.T) {
	fs := NewFailedStore(openMem(t).Bucket(FailedBucketName))
	if err := fs.MarkFailed("ABCDEF", "https://old/url", "legacy"); err != nil {
		t.Fatal(err)
	}
	if _, ok := fs.Lookup("abcdef", ""); !ok {
		t.Error("hash lookup should match, case-insensitively")
	}
	if _, ok := fs.Lookup("", "https://old/url"); !ok {
		t.Error("a legacy URL-keyed record must still be found")
	}
	if _, ok := fs.Lookup("", "https://other/url", "src:x|y"); ok {
		t.Error("an unrelated key must not match")
	}
}
