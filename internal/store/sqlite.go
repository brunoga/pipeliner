package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	_ "modernc.org/sqlite" // registers "sqlite" driver with database/sql
)

// process-level lock registry — prevents the same process from double-locking
// and prevents a second pipeliner process from opening the same DB.
var (
	lockMu    sync.Mutex
	lockFiles = map[string]*lockEntry{}
)

type lockEntry struct {
	file  *os.File
	count int
}

func acquireDBLock(path string) (func(), error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve path %q: %w", path, err)
	}
	lockMu.Lock()
	defer lockMu.Unlock()
	if e, ok := lockFiles[abs]; ok {
		e.count++
		return func() { releaseDBLock(abs) }, nil
	}
	lf, err := os.OpenFile(abs+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("store: open lock file: %w", err)
	}
	if err := syscall.Flock(int(lf.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lf.Close()
		return nil, fmt.Errorf("store: %q is already in use by another pipeliner process", path)
	}
	lockFiles[abs] = &lockEntry{file: lf, count: 1}
	return func() { releaseDBLock(abs) }, nil
}

func releaseDBLock(abs string) {
	lockMu.Lock()
	defer lockMu.Unlock()
	e := lockFiles[abs]
	if e == nil {
		return
	}
	e.count--
	if e.count == 0 {
		e.file.Close()
		delete(lockFiles, abs)
	}
}

const schema = `
CREATE TABLE IF NOT EXISTS store (
	bucket TEXT NOT NULL,
	key    TEXT NOT NULL,
	value  TEXT NOT NULL,
	PRIMARY KEY (bucket, key)
);
CREATE INDEX IF NOT EXISTS store_bucket ON store (bucket);
`

// SQLiteStore is a Store backed by a local SQLite database file.
// A single table with (bucket, key, value) stores all namespaces.
// Use ":memory:" as path for an in-memory database (useful in tests).
type SQLiteStore struct {
	db          *sql.DB
	releaseLock func() // nil for :memory: databases
}

// OpenSQLite opens (or creates) a SQLite-backed Store at the given file path.
func OpenSQLite(path string) (*SQLiteStore, error) {
	var release func()
	if path != ":memory:" {
		var err error
		release, err = acquireDBLock(path)
		if err != nil {
			return nil, err
		}
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		if release != nil {
			release()
		}
		return nil, fmt.Errorf("store: open sqlite %q: %w", path, err)
	}

	// SQLite performs best with a single writer; WAL mode allows concurrent readers.
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("store: pragma: %w", err)
		}
	}

	// SQLite is not safe with multiple concurrent connections to the same file
	// in the default threading model, and in-memory databases are per-connection.
	// A single open connection is the correct setting; database/sql serialises
	// concurrent callers and WAL mode allows overlapping readers at the engine level.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		if release != nil {
			release()
		}
		return nil, fmt.Errorf("store: create schema: %w", err)
	}

	return &SQLiteStore{db: db, releaseLock: release}, nil
}

// Bucket returns a SQLite-backed Bucket for the given name.
func (s *SQLiteStore) Bucket(name string) Bucket {
	return &sqliteBucket{db: s.db, name: name}
}

// DB returns the underlying *sql.DB for callers that need direct SQL access
// (e.g. packages that manage their own schema in a separate table).
func (s *SQLiteStore) DB() *sql.DB {
	return s.db
}

// Close closes the underlying database connection and releases the file lock.
func (s *SQLiteStore) Close() error {
	err := s.db.Close()
	if s.releaseLock != nil {
		s.releaseLock()
	}
	return err
}

// sqliteBucket is a Bucket backed by a partition of the SQLite store table.
type sqliteBucket struct {
	db   *sql.DB
	name string
}

// Put JSON-encodes value and upserts it under key.
func (b *sqliteBucket) Put(key string, value any) error {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("store: put %q/%q: marshal: %w", b.name, key, err)
	}
	_, err = b.db.Exec(
		`INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)
		 ON CONFLICT(bucket, key) DO UPDATE SET value = excluded.value`,
		b.name, key, string(encoded),
	)
	if err != nil {
		return fmt.Errorf("store: put %q/%q: %w", b.name, key, err)
	}
	return nil
}

// Get decodes the value stored under key into dest.
func (b *sqliteBucket) Get(key string, dest any) (bool, error) {
	var raw string
	err := b.db.QueryRow(
		`SELECT value FROM store WHERE bucket = ? AND key = ?`,
		b.name, key,
	).Scan(&raw)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: get %q/%q: %w", b.name, key, err)
	}
	if err := json.Unmarshal([]byte(raw), dest); err != nil {
		return true, fmt.Errorf("store: get %q/%q: unmarshal: %w", b.name, key, err)
	}
	return true, nil
}

// Delete removes the key from the bucket. No-op if absent.
func (b *sqliteBucket) Delete(key string) error {
	_, err := b.db.Exec(
		`DELETE FROM store WHERE bucket = ? AND key = ?`,
		b.name, key,
	)
	if err != nil {
		return fmt.Errorf("store: delete %q/%q: %w", b.name, key, err)
	}
	return nil
}

// Keys returns all keys in this bucket.
func (b *sqliteBucket) Keys() ([]string, error) {
	rows, err := b.db.Query(
		`SELECT key FROM store WHERE bucket = ? ORDER BY key`,
		b.name,
	)
	if err != nil {
		return nil, fmt.Errorf("store: keys %q: %w", b.name, err)
	}
	defer rows.Close()

	var keys []string
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			return nil, fmt.Errorf("store: keys %q: scan: %w", b.name, err)
		}
		keys = append(keys, k)
	}
	return keys, rows.Err()
}
