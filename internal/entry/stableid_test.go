package entry

import "testing"

// The ladder's rungs have different scope, and the order encodes that: an info
// hash identifies a release everywhere, a source id only within its source.
func TestStableKeysOrderAndScope(t *testing.T) {
	e := New("Some Release", "https://indexer/dl?path=NONCE")
	e.Set(FieldSource, "jackett:3dtorrents")
	e.Set(FieldSourceID, "http://tracker/download.php?id=abc")
	e.Set(FieldTorrentInfoHash, "DEADBEEF")

	keys := e.StableKeys()
	if len(keys) != 2 {
		t.Fatalf("keys = %v, want hash and source rungs", keys)
	}
	if keys[0] != "hash:deadbeef" {
		t.Errorf("strongest key = %q, want the lowercased hash first", keys[0])
	}
	if keys[1] != "src:jackett:3dtorrents|http://tracker/download.php?id=abc" {
		t.Errorf("second key = %q, want the source-scoped id", keys[1])
	}
	if e.StableKey() != "hash:deadbeef" {
		t.Errorf("StableKey = %q, want the strongest", e.StableKey())
	}
	// The URL is deliberately not a key: it is a nonce.
	for _, k := range keys {
		if k == e.URL {
			t.Error("the URL must never be a stable key")
		}
	}
}

// The same id from two different sources must not collide.
func TestStableKeysAreNamespacedBySource(t *testing.T) {
	a := New("A", "https://a/1")
	a.Set(FieldSource, "jackett:alpha")
	a.Set(FieldSourceID, "12345")
	b := New("B", "https://b/1")
	b.Set(FieldSource, "jackett:beta")
	b.Set(FieldSourceID, "12345")

	if a.StableKey() == b.StableKey() {
		t.Errorf("the same id from two sources collided: %q", a.StableKey())
	}
}

// Entries with nothing durable report nothing, which is the caller's cue to
// fall back rather than to invent identity.
func TestStableKeysEmptyWhenNothingDurable(t *testing.T) {
	e := New("Some Release", "https://indexer/dl?path=NONCE")
	if keys := e.StableKeys(); len(keys) != 0 {
		t.Errorf("keys = %v, want none", keys)
	}
	if e.StableKey() != "" {
		t.Errorf("StableKey = %q, want empty", e.StableKey())
	}
}

// A source id with no source still produces a usable, clearly-marked key
// rather than one that could collide with a real source's namespace.
func TestStableKeysWithoutASourceName(t *testing.T) {
	e := New("X", "https://x/1")
	e.Set(FieldSourceID, "abc")
	keys := e.StableKeys()
	if len(keys) != 1 || keys[0] != "src:?|abc" {
		t.Errorf("keys = %v, want a single ?-namespaced key", keys)
	}
}
