package cache

import (
	"encoding/json"
	"sync"
	"testing"
	"time"
)

// --- in-memory cache ---

func TestHitAndMiss(t *testing.T) {
	c := NewPersistent[string](time.Hour, nil)
	if _, ok := c.Get("k"); ok {
		t.Error("empty cache should miss")
	}
	c.Set("k", "v")
	if v, ok := c.Get("k"); !ok || v != "v" {
		t.Errorf("expected hit with v, got %q ok=%v", v, ok)
	}
}

func TestExpiry(t *testing.T) {
	c := NewPersistent[int](5*time.Millisecond, nil)
	c.Set("x", 42)
	if _, ok := c.Get("x"); !ok {
		t.Fatal("should hit before expiry")
	}
	time.Sleep(10 * time.Millisecond)
	if _, ok := c.Get("x"); ok {
		t.Error("should miss after expiry")
	}
}

func TestZeroTTLDisablesCache(t *testing.T) {
	c := NewPersistent[string](0, nil)
	c.Set("k", "v")
	if _, ok := c.Get("k"); ok {
		t.Error("zero TTL should never cache")
	}
}

func TestNilReceiverSafe(t *testing.T) {
	var c *Cache[string]
	c.Set("k", "v")           // must not panic
	_, ok := c.Get("k")
	if ok {
		t.Error("nil cache should always miss")
	}
}

func TestOverwrite(t *testing.T) {
	c := NewPersistent[string](time.Hour, nil)
	c.Set("k", "first")
	c.Set("k", "second")
	if v, _ := c.Get("k"); v != "second" {
		t.Errorf("got %q, want second", v)
	}
}

func TestConcurrentAccess(t *testing.T) {
	c := NewPersistent[int](time.Hour, nil)
	var wg sync.WaitGroup
	for i := range 50 {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			c.Set("k", i)
		}(i)
		go func() {
			defer wg.Done()
			c.Get("k") //nolint:errcheck
		}()
	}
	wg.Wait()
}

// --- persistent cache ---

// memBucket is a simple in-memory Bucket implementation for testing persistence.
type memBucket struct {
	mu   sync.Mutex
	data map[string][]byte
}

func newMemBucket() *memBucket { return &memBucket{data: make(map[string][]byte)} }

func (b *memBucket) Put(key string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.data[key] = raw
	return nil
}

func (b *memBucket) Get(key string, dest any) (bool, error) {
	b.mu.Lock()
	raw, ok := b.data[key]
	b.mu.Unlock()
	if !ok {
		return false, nil
	}
	return true, json.Unmarshal(raw, dest)
}

func TestPersistentHitFromBucket(t *testing.T) {
	bucket := newMemBucket()

	// Populate via one cache instance.
	c1 := NewPersistent[string](time.Hour, bucket)
	c1.Set("key", "persisted")

	// A second instance (simulating a new process run) with the same bucket.
	c2 := NewPersistent[string](time.Hour, bucket)
	v, ok := c2.Get("key")
	if !ok || v != "persisted" {
		t.Errorf("expected persisted value, got %q ok=%v", v, ok)
	}
}

func TestPersistentExpiredBucketEntryMisses(t *testing.T) {
	bucket := newMemBucket()

	c1 := NewPersistent[int](5*time.Millisecond, bucket)
	c1.Set("k", 99)
	time.Sleep(10 * time.Millisecond)

	c2 := NewPersistent[int](5*time.Millisecond, bucket)
	if _, ok := c2.Get("k"); ok {
		t.Error("expired bucket entry should miss")
	}
}

func TestPersistentMemoryPromotedAfterBucketHit(t *testing.T) {
	bucket := newMemBucket()
	c1 := NewPersistent[string](time.Hour, bucket)
	c1.Set("k", "val")

	c2 := NewPersistent[string](time.Hour, bucket)
	c2.Get("k") // promotes to memory

	// Now corrupt the bucket — next get should still hit memory.
	bucket.data["k"] = []byte("invalid json")
	v, ok := c2.Get("k")
	if !ok || v != "val" {
		t.Errorf("should serve from in-memory after promotion: ok=%v val=%q", ok, v)
	}
}
