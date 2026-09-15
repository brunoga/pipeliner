package web

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// apiDBBackup streams a consistent snapshot of the SQLite store as a download.
// The snapshot is taken with VACUUM INTO (via store.Backup), which is safe
// while the daemon is running and produces a compact single-file copy with no
// WAL sidecars — everything the trackers, caches, and logs live in.
//
// GET /api/db/backup
func (s *Server) apiDBBackup(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	tmpDir, err := os.MkdirTemp("", "pipeliner-backup-*")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer os.RemoveAll(tmpDir)

	dest := filepath.Join(tmpDir, "backup.db")
	if err := s.db.Backup(dest); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	f, err := os.Open(dest)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	name := fmt.Sprintf("pipeliner-%s.db", time.Now().Format("2006-01-02-150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Content-Length", strconv.FormatInt(st.Size(), 10))
	io.Copy(w, f) //nolint:errcheck // client may abort mid-download
}
