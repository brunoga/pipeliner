package store

import "testing"

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

	if err := fs.MarkFailed(url, "stalled for 6h"); err != nil {
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
	if err := fs.MarkFailed(url, "first"); err != nil {
		t.Fatal(err)
	}
	if err := fs.MarkFailed(url, "second"); err != nil {
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
