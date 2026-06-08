package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// AtomicWriteFile writes data to path via a sibling temp file plus rename so
// the destination is either left untouched or replaced in a single step. A
// crash, kill, or disk-full mid-write can no longer truncate the existing
// file to zero bytes. The temp file lives in the same directory as path so
// rename is atomic on POSIX (rename across filesystems would not be).
func AtomicWriteFile(path string, data []byte, perm os.FileMode) (retErr error) {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp.*")
	if err != nil {
		return fmt.Errorf("atomic write %q: create temp: %w", path, err)
	}
	tmpName := tmp.Name()
	// On any failure after this point, the temp file must be removed so we
	// don't litter the directory with .tmp.* artifacts on repeated failures.
	defer func() {
		if retErr != nil {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("atomic write %q: write temp: %w", path, err)
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return fmt.Errorf("atomic write %q: chmod temp: %w", path, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("atomic write %q: sync temp: %w", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("atomic write %q: close temp: %w", path, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("atomic write %q: rename: %w", path, err)
	}
	return nil
}
