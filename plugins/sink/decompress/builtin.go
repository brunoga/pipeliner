package decompress

import (
	"archive/zip"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nwaples/rardecode/v2"
)

// extractRAR extracts a RAR archive, following its volumes, into dest.
func extractRAR(archive, dest string, keepDirs bool) (written, skipped int, err error) {
	r, err := rardecode.OpenReader(archive)
	if err != nil {
		return 0, 0, err
	}
	defer r.Close()
	for {
		h, err := r.Next()
		if errors.Is(err, io.EOF) {
			return written, skipped, nil
		}
		if err != nil {
			return written, skipped, err
		}
		if h.IsDir {
			continue
		}
		ok, err := writeFile(dest, h.Name, keepDirs, r)
		if err != nil {
			return written, skipped, err
		}
		if ok {
			written++
		} else {
			skipped++
		}
	}
}

// extractZIP extracts a ZIP archive into dest.
func extractZIP(archive, dest string, keepDirs bool) (written, skipped int, err error) {
	z, err := zip.OpenReader(archive)
	if err != nil {
		return 0, 0, err
	}
	defer z.Close()
	for _, f := range z.File {
		if f.FileInfo().IsDir() {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return written, skipped, err
		}
		ok, err := writeFile(dest, f.Name, keepDirs, rc)
		rc.Close()
		if err != nil {
			return written, skipped, err
		}
		if ok {
			written++
		} else {
			skipped++
		}
	}
	return written, skipped, nil
}

// writeFile writes one archived file below dest. It returns false, and writes
// nothing, when the file already exists: a finished episode is never
// overwritten, which also makes running the same extraction twice harmless.
//
// The name comes from the archive and is not trusted. One that is absolute
// or climbs out with ".." is refused, so an archive cannot write outside
// dest. The data goes to a ".partial" file that is renamed into place only
// once complete.
func writeFile(dest, name string, keepDirs bool, r io.Reader) (bool, error) {
	name = path.Clean(strings.ReplaceAll(name, `\`, "/"))
	if path.IsAbs(name) || name == ".." || strings.HasPrefix(name, "../") {
		return false, fmt.Errorf("archive entry %q escapes the destination", name)
	}
	if !keepDirs {
		name = path.Base(name)
	}
	target := filepath.Join(dest, filepath.FromSlash(name))
	if _, err := os.Lstat(target); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return false, err
	}
	tmp := target + ".partial"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return false, err
	}
	if _, err := io.Copy(f, r); err != nil {
		f.Close()
		os.Remove(tmp)
		return false, err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return false, err
	}
	return true, os.Rename(tmp, target)
}
