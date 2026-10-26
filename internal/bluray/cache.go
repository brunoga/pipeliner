package bluray

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ResponseCache defines a pluggable response cache for raw HTML/AJAX responses.
type ResponseCache interface {
	// Get retrieves a cached payload by key.
	Get(key string) ([]byte, bool)
	// Set stores a payload in the cache.
	Set(key string, data []byte)
}

type memoryCacheEntry struct {
	data      []byte
	expiresAt time.Time
}

// MemoryCache is a thread-safe in-memory response cache with optional TTL.
type MemoryCache struct {
	mu      sync.RWMutex
	ttl     time.Duration
	entries map[string]memoryCacheEntry
}

// NewMemoryCache creates a new in-memory cache with the specified TTL (0 means no expiration).
func NewMemoryCache(ttl time.Duration) *MemoryCache {
	return &MemoryCache{
		ttl:     ttl,
		entries: make(map[string]memoryCacheEntry),
	}
}

// Get retrieves a cached payload if present and not expired.
func (m *MemoryCache) Get(key string) ([]byte, bool) {
	m.mu.RLock()
	entry, ok := m.entries[key]
	m.mu.RUnlock()
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && time.Now().After(entry.expiresAt) {
		m.mu.Lock()
		delete(m.entries, key)
		m.mu.Unlock()
		return nil, false
	}
	out := make([]byte, len(entry.data))
	copy(out, entry.data)
	return out, true
}

// Set stores a payload in memory.
func (m *MemoryCache) Set(key string, data []byte) {
	copied := make([]byte, len(data))
	copy(copied, data)
	var exp time.Time
	if m.ttl > 0 {
		exp = time.Now().Add(m.ttl)
	}
	m.mu.Lock()
	m.entries[key] = memoryCacheEntry{
		data:      copied,
		expiresAt: exp,
	}
	m.mu.Unlock()
}

// DiskCache persists cached responses as files in a directory with optional TTL.
type DiskCache struct {
	mu  sync.RWMutex
	dir string
	ttl time.Duration
}

// NewDiskCache creates a disk-backed cache at the given directory.
func NewDiskCache(dir string, ttl time.Duration) (*DiskCache, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &DiskCache{
		dir: dir,
		ttl: ttl,
	}, nil
}

func (d *DiskCache) pathForKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return filepath.Join(d.dir, hex.EncodeToString(sum[:])+".cache")
}

// Get reads a cached response from disk if it exists and is within TTL.
func (d *DiskCache) Get(key string) ([]byte, bool) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	p := d.pathForKey(key)
	info, err := os.Stat(p)
	if err != nil {
		return nil, false
	}
	if d.ttl > 0 && time.Since(info.ModTime()) > d.ttl {
		return nil, false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return data, true
}

// Set writes a cached response to disk.
func (d *DiskCache) Set(key string, data []byte) {
	if len(data) == 0 {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()

	p := d.pathForKey(key)
	_ = os.WriteFile(p, data, 0o600)
}
