// Package cache provides a generic thread-safe TTL cache with optional
// persistent storage.
//
// Without a backing store the cache is in-memory only and does not survive
// process restarts. With a Bucket (e.g. from internal/store) the cache is
// warm across runs: on a cache miss the bucket is checked before making an
// API call, and successful lookups are written back to the bucket so future
// runs skip the API entirely until the TTL expires.
//
// Call Preload() at startup to bulk-load all non-expired entries from the
// bucket into memory in a single query, eliminating per-key SQLite reads
// during normal operation.
package cache

import (
	"encoding/json"
	"sync"
	"time"
)

// Bucket is the persistence interface required by the cache.
// store.Bucket from internal/store satisfies this interface.
type Bucket interface {
	Put(key string, value any) error
	Get(key string, dest any) (bool, error)
}

// Cache is a generic thread-safe TTL cache keyed by string.
// A zero TTL disables caching entirely (all Gets miss, all Sets are no-ops).
type Cache[V any] struct {
	mu      sync.RWMutex
	entries map[string]cacheEntry[V]
	ttl     time.Duration
	bucket  Bucket // nil = in-memory only
}

type cacheEntry[V any] struct {
	value     V
	expiresAt time.Time
}

// storedEntry is the on-disk representation stored in the Bucket.
type storedEntry struct {
	Value     json.RawMessage `json:"v"`
	ExpiresAt time.Time       `json:"e"`
}

// NewPersistent returns a Cache backed by bucket for cross-run persistence.
// Hits are served from the in-memory map; misses check the bucket before
// returning empty. Writes go to both the map and the bucket.
func NewPersistent[V any](ttl time.Duration, bucket Bucket) *Cache[V] {
	return &Cache[V]{
		entries: make(map[string]cacheEntry[V]),
		ttl:     ttl,
		bucket:  bucket,
	}
}

// Get returns the cached value for key if present and not expired.
// It checks the in-memory map first; on a miss it falls back to the bucket
// (if configured), promotes a valid bucket entry to memory, and returns it.
// A nil receiver is treated as a disabled cache.
func (c *Cache[V]) Get(key string) (V, bool) {
	if c == nil || c.ttl == 0 {
		var zero V
		return zero, false
	}

	// Fast path: in-memory hit.
	c.mu.RLock()
	if e, ok := c.entries[key]; ok && time.Now().Before(e.expiresAt) {
		v := e.value
		c.mu.RUnlock()
		return v, true
	}
	c.mu.RUnlock()

	// Slow path: check backing store.
	if c.bucket == nil {
		var zero V
		return zero, false
	}

	var stored storedEntry
	found, err := c.bucket.Get(key, &stored)
	if err != nil || !found || time.Now().After(stored.ExpiresAt) {
		var zero V
		return zero, false
	}

	var value V
	if err := json.Unmarshal(stored.Value, &value); err != nil {
		var zero V
		return zero, false
	}

	// Promote to in-memory.
	c.mu.Lock()
	c.entries[key] = cacheEntry[V]{value: value, expiresAt: stored.ExpiresAt}
	c.mu.Unlock()

	return value, true
}

// bulkBucket is an optional extension of Bucket that supports loading all
// entries in one query, used by Preload.
type bulkBucket interface {
	All() (map[string][]byte, error)
}

// Preload bulk-loads all non-expired entries from the backing bucket into the
// in-memory map. Call once at startup to eliminate per-key SQLite reads during
// normal operation. No-op if the bucket does not implement bulkBucket.
func (c *Cache[V]) Preload() {
	if c == nil || c.ttl == 0 || c.bucket == nil {
		return
	}
	bb, ok := c.bucket.(bulkBucket)
	if !ok {
		return
	}
	all, err := bb.All()
	if err != nil || len(all) == 0 {
		return
	}
	now := time.Now()
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, raw := range all {
		var stored storedEntry
		if err := json.Unmarshal(raw, &stored); err != nil {
			continue
		}
		if now.After(stored.ExpiresAt) {
			continue
		}
		var value V
		if err := json.Unmarshal(stored.Value, &value); err != nil {
			continue
		}
		c.entries[key] = cacheEntry[V]{value: value, expiresAt: stored.ExpiresAt}
	}
}

// Set stores value under key with the configured TTL.
// A nil receiver or zero TTL is a no-op.
func (c *Cache[V]) Set(key string, value V) {
	if c == nil || c.ttl == 0 {
		return
	}

	expiresAt := time.Now().Add(c.ttl)

	c.mu.Lock()
	c.entries[key] = cacheEntry[V]{value: value, expiresAt: expiresAt}
	c.mu.Unlock()

	if c.bucket != nil {
		raw, err := json.Marshal(value)
		if err == nil {
			c.bucket.Put(key, storedEntry{Value: raw, ExpiresAt: expiresAt}) //nolint:errcheck
		}
	}
}
