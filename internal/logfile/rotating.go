// Package logfile provides a size-based rotating io.Writer used as a slog
// sink. When the active file would exceed MaxBytes after the next write it
// is rotated: pipeliner.log → pipeliner.log.1, .1 → .2, ..., with anything
// past .MaxArchives dropped. Rotation happens on whole-write boundaries —
// slog handlers emit one record per Write call, so no record is ever split
// across files.
package logfile

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sync"
)

// Writer is a size-based rotating io.WriteCloser. Safe for concurrent use.
//
// MaxBytes ≤ 0 disables rotation (the file grows unbounded — useful in
// tests). MaxArchives = 0 rotates by truncating the base file (no history
// kept). The file is created lazily on the first Write so callers can
// construct a Writer without touching disk.
type Writer struct {
	Path        string
	MaxBytes    int64
	MaxArchives int

	mu sync.Mutex
	f  *os.File
	n  int64 // current file size in bytes
}

// Write appends p to the active file, rotating first if this write would
// push the file past MaxBytes. Returns the number of bytes successfully
// written (always 0 on rotation failure — callers should treat this as a
// hard error, not "wrote partial").
func (w *Writer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.f == nil {
		if err := w.openLocked(); err != nil {
			return 0, err
		}
	}
	if w.MaxBytes > 0 && w.n > 0 && w.n+int64(len(p)) > w.MaxBytes {
		if err := w.rotateLocked(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.n += int64(n)
	return n, err
}

// Close flushes and releases the active file. Subsequent Writes will
// transparently reopen.
func (w *Writer) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f = nil
	w.n = 0
	return err
}

func (w *Writer) openLocked() error {
	f, err := os.OpenFile(w.Path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}
	w.f = f
	w.n = info.Size()
	return nil
}

// rotateLocked closes the active file, shifts every archive down one slot
// (dropping anything past MaxArchives), and opens a fresh base file.
// Errors propagate so a misconfigured filesystem surfaces immediately
// rather than silently losing log records.
func (w *Writer) rotateLocked() error {
	if err := w.f.Close(); err != nil {
		return err
	}
	w.f = nil
	w.n = 0

	// Drop anything past MaxArchives. We delete unconditionally rather
	// than test-then-delete so the operation stays idempotent.
	if w.MaxArchives > 0 {
		if err := removeIfExists(fmt.Sprintf("%s.%d", w.Path, w.MaxArchives)); err != nil {
			return err
		}
	}
	// Shift .N-1 → .N, ..., .1 → .2.
	for i := w.MaxArchives - 1; i >= 1; i-- {
		src := fmt.Sprintf("%s.%d", w.Path, i)
		dst := fmt.Sprintf("%s.%d", w.Path, i+1)
		if err := renameIfExists(src, dst); err != nil {
			return err
		}
	}
	// Move the base file out of the way. When archives are disabled we
	// just delete it.
	if w.MaxArchives > 0 {
		if err := renameIfExists(w.Path, w.Path+".1"); err != nil {
			return err
		}
	} else {
		if err := removeIfExists(w.Path); err != nil {
			return err
		}
	}
	return w.openLocked()
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

func renameIfExists(src, dst string) error {
	if err := os.Rename(src, dst); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

var _ io.WriteCloser = (*Writer)(nil)
