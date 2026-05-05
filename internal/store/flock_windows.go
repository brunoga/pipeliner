//go:build windows

package store

import "os"

// acquireFileLock is a no-op on Windows. SQLite's own WAL-mode locking
// prevents concurrent access; the in-process lockFiles map handles same-
// process deduplication.
func acquireFileLock(_ *os.File) error { return nil }
