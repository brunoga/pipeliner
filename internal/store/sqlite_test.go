package store

import (
	"fmt"
	"sync"
	"testing"
)

// openMem opens an in-memory SQLite store for testing.
func openMem(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// --- Store interface compliance ---

func TestOpenSQLite(t *testing.T) {
	s := openMem(t)
	var _ Store = s // compile-time interface check
}

func TestOpenSQLiteFile(t *testing.T) {
	path := t.TempDir() + "/test.db"
	s, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("OpenSQLite file: %v", err)
	}
	defer s.Close()

	b := s.Bucket("test")
	if err := b.Put("k", "v"); err != nil {
		t.Fatal(err)
	}

	// Reopen and verify data survives.
	s.Close()
	s2, err := OpenSQLite(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()

	var v string
	found, err := s2.Bucket("test").Get("k", &v)
	if err != nil || !found || v != "v" {
		t.Errorf("data did not survive reopen: found=%v err=%v val=%q", found, err, v)
	}
}

// --- Bucket operations ---

func TestPutGetString(t *testing.T) {
	b := openMem(t).Bucket("b")
	if err := b.Put("key", "hello"); err != nil {
		t.Fatal(err)
	}
	var got string
	found, err := b.Get("key", &got)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected key to exist")
	}
	if got != "hello" {
		t.Errorf("want %q, got %q", "hello", got)
	}
}

func TestPutGetInt(t *testing.T) {
	b := openMem(t).Bucket("b")
	b.Put("n", 42)
	var n int
	found, err := b.Get("n", &n)
	if err != nil || !found || n != 42 {
		t.Errorf("int round-trip: found=%v err=%v val=%d", found, err, n)
	}
}

func TestPutGetStruct(t *testing.T) {
	type rec struct {
		Name string `json:"name"`
		Val  int    `json:"val"`
	}
	b := openMem(t).Bucket("b")
	b.Put("r", rec{Name: "test", Val: 7})

	var got rec
	found, err := b.Get("r", &got)
	if err != nil || !found {
		t.Fatalf("struct round-trip: found=%v err=%v", found, err)
	}
	if got.Name != "test" || got.Val != 7 {
		t.Errorf("unexpected value: %+v", got)
	}
}

func TestGetMissingKey(t *testing.T) {
	b := openMem(t).Bucket("b")
	var v string
	found, err := b.Get("nonexistent", &v)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Error("expected not found for missing key")
	}
}

func TestPutOverwrites(t *testing.T) {
	b := openMem(t).Bucket("b")
	b.Put("k", "first")
	b.Put("k", "second")
	var v string
	b.Get("k", &v)
	if v != "second" {
		t.Errorf("expected overwrite: got %q", v)
	}
}

func TestDelete(t *testing.T) {
	b := openMem(t).Bucket("b")
	b.Put("k", "v")
	if err := b.Delete("k"); err != nil {
		t.Fatal(err)
	}
	var v string
	found, _ := b.Get("k", &v)
	if found {
		t.Error("expected key to be deleted")
	}
}

func TestDeleteMissingKeyIsNoOp(t *testing.T) {
	b := openMem(t).Bucket("b")
	if err := b.Delete("nonexistent"); err != nil {
		t.Errorf("delete missing key should be no-op, got: %v", err)
	}
}

func TestKeys(t *testing.T) {
	b := openMem(t).Bucket("b")
	b.Put("b", 1)
	b.Put("a", 2)
	b.Put("c", 3)

	keys, err := b.Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 3 {
		t.Errorf("want 3 keys, got %d: %v", len(keys), keys)
	}
	// Keys are returned sorted by the SQLite query.
	want := []string{"a", "b", "c"}
	for i, k := range keys {
		if k != want[i] {
			t.Errorf("keys[%d]: want %q, got %q", i, want[i], k)
		}
	}
}

func TestKeysEmptyBucket(t *testing.T) {
	b := openMem(t).Bucket("empty")
	keys, err := b.Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 0 {
		t.Errorf("expected no keys, got %v", keys)
	}
}

func TestBucketsAreIsolated(t *testing.T) {
	s := openMem(t)
	s.Bucket("a").Put("key", "from-a")
	s.Bucket("b").Put("key", "from-b")

	var va, vb string
	s.Bucket("a").Get("key", &va)
	s.Bucket("b").Get("key", &vb)

	if va != "from-a" || vb != "from-b" {
		t.Errorf("bucket isolation broken: a=%q b=%q", va, vb)
	}
}

// --- Concurrent safety ---

func TestConcurrentWrites(t *testing.T) {
	b := openMem(t).Bucket("concurrent")

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			key := fmt.Sprintf("key-%d", i)
			if err := b.Put(key, i); err != nil {
				t.Errorf("concurrent put %q: %v", key, err)
			}
		}(i)
	}
	wg.Wait()

	keys, err := b.Keys()
	if err != nil {
		t.Fatal(err)
	}
	if len(keys) != 50 {
		t.Errorf("expected 50 keys after concurrent writes, got %d", len(keys))
	}
}

// --- Error paths ---

func TestOpenSQLiteInvalidPath(t *testing.T) {
	// A path inside a non-existent directory that can't be created should fail.
	_, err := OpenSQLite("/nonexistent-dir-that-cannot-exist/db.sqlite")
	if err == nil {
		t.Error("expected error for invalid path")
	}
}

func TestPutUnmarshalableValue(t *testing.T) {
	b := openMem(t).Bucket("b")
	// Channels cannot be JSON-marshaled.
	err := b.Put("bad", make(chan int))
	if err == nil {
		t.Error("expected marshal error for channel value")
	}
}

func TestGetUnmarshalIntoWrongType(t *testing.T) {
	b := openMem(t).Bucket("b")
	b.Put("k", "this is a string")
	// Try to unmarshal a JSON string into an int — should error.
	var dest int
	found, err := b.Get("k", &dest)
	if !found {
		t.Fatal("key should exist")
	}
	if err == nil {
		t.Error("expected unmarshal error when type is incompatible")
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	b := openMem(t).Bucket("rw")
	b.Put("shared", 0)

	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			b.Put(fmt.Sprintf("w%d", i), i)
		}(i)
		go func() {
			defer wg.Done()
			var v int
			b.Get("shared", &v)
		}()
	}
	wg.Wait()
}
