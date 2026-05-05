// Package store defines the persistence interfaces used throughout the pipeline
// and provides a SQLite-backed default implementation.
package store

// Store is a pluggable persistence backend. Callers interact only with this
// interface; the concrete implementation is chosen at startup.
type Store interface {
	// Bucket returns a named key-value namespace within the store.
	// Multiple calls with the same name return views of the same namespace.
	Bucket(name string) Bucket
	// Close flushes pending writes and releases any resources held by the store.
	Close() error
}

// Bucket is a named key-value namespace. Values are JSON-encoded internally;
// any JSON-serialisable Go value can be stored.
type Bucket interface {
	// Put encodes value as JSON and stores it under key, replacing any
	// existing value.
	Put(key string, value any) error
	// Get decodes the value stored under key into dest.
	// Returns (false, nil) if the key does not exist.
	Get(key string, dest any) (bool, error)
	// Delete removes key. No-op if the key does not exist.
	Delete(key string) error
	// Keys returns all keys in the bucket (order is unspecified).
	Keys() ([]string, error)
	// All returns all key→raw-JSON pairs in the bucket in one query.
	All() (map[string][]byte, error)
}
