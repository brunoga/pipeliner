package web

import (
	"bytes"
	"net/http"
	"os"
	"sort"
	"time"
)

// Config history: every successful save snapshots the previous on-disk config
// into the store, so a bad edit can be reviewed and rolled back from the web
// UI instead of hunting for ad-hoc .bak files. Rollback is deliberate: loading
// a version puts it in the editor for review; saving it applies it (and
// snapshots the config it replaces, so rollbacks are themselves undoable).
const (
	configHistoryBucket = "config_history"
	configHistoryKeep   = 20
)

type configSnapshot struct {
	Content string    `json:"content"`
	SavedAt time.Time `json:"saved_at"`
}

// snapshotConfigForHistory stores the current on-disk config before newData
// replaces it. No-op when there is no store, no config file yet, or the
// content is unchanged. Failures only log — history must never block a save.
func (s *Server) snapshotConfigForHistory(newData []byte) {
	if s.db == nil || s.configPath == "" {
		return
	}
	old, err := os.ReadFile(s.configPath)
	if err != nil || len(old) == 0 || bytes.Equal(old, newData) {
		return
	}
	b := s.db.Bucket(configHistoryBucket)
	key := time.Now().UTC().Format(time.RFC3339Nano)
	if err := b.Put(key, configSnapshot{Content: string(old), SavedAt: time.Now().UTC()}); err != nil {
		return
	}
	// Prune to the newest configHistoryKeep versions (keys sort by time).
	keys, err := b.Keys()
	if err != nil || len(keys) <= configHistoryKeep {
		return
	}
	sort.Strings(keys)
	for _, k := range keys[:len(keys)-configHistoryKeep] {
		_ = b.Delete(k)
	}
}

// apiConfigHistory lists stored config versions, newest first.
//
// GET /api/config/history
func (s *Server) apiConfigHistory(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	b := s.db.Bucket(configHistoryBucket)
	keys, err := b.Keys()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	sort.Sort(sort.Reverse(sort.StringSlice(keys)))
	type version struct {
		ID      string    `json:"id"`
		SavedAt time.Time `json:"saved_at"`
		Size    int       `json:"size"`
	}
	out := make([]version, 0, len(keys))
	for _, k := range keys {
		var snap configSnapshot
		if found, _ := b.Get(k, &snap); found {
			out = append(out, version{ID: k, SavedAt: snap.SavedAt, Size: len(snap.Content)})
		}
	}
	writeJSON(w, map[string]any{"versions": out})
}

// apiConfigHistoryGet returns one stored version's content.
//
// GET /api/config/history/{id}
func (s *Server) apiConfigHistoryGet(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var snap configSnapshot
	found, err := s.db.Bucket(configHistoryBucket).Get(r.PathValue("id"), &snap)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if !found {
		http.Error(w, "version not found", http.StatusNotFound)
		return
	}
	writeJSON(w, map[string]any{"content": snap.Content, "saved_at": snap.SavedAt})
}
