package web

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/brunoga/pipeliner/internal/mediaserver"
	imovies "github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/reconcile"
)

// toolsSettingsBucket persists small per-tool settings; the Plex account token
// lives here so the reconcile tool remembers it across runs. The DB already
// holds API keys of equivalent sensitivity (threat model: trusted LAN).
const (
	toolsSettingsBucket = "tools_settings"
	plexTokenKey        = "plex_token"
)

// apiToolsPlexStatus reports whether a Plex token is saved.
//
// GET /api/tools/plex
func (s *Server) apiToolsPlexStatus(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var tok string
	found, _ := s.db.Bucket(toolsSettingsBucket).Get(plexTokenKey, &tok)
	writeJSON(w, map[string]any{"has_token": found && tok != ""})
}

// plexReconcileServer is one discovered server in the reconcile response.
type plexReconcileServer struct {
	Name     string `json:"name"`
	URI      string `json:"uri,omitempty"`
	Sections int    `json:"sections"`
	Err      string `json:"err,omitempty"`
}

// apiToolsPlexReconcile discovers the account's Plex servers, lists their
// movie sections, and returns the movie-tracker records with no matching
// library item — the ones silently blocked from re-download. A token in the
// request body is used (and saved on success); with an empty token the saved
// one is used.
//
// POST /api/tools/plex/reconcile {"token": "..."}
func (s *Server) apiToolsPlexReconcile(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	settings := s.db.Bucket(toolsSettingsBucket)
	token := req.Token
	fromRequest := token != ""
	if token == "" {
		if found, _ := settings.Get(plexTokenKey, &token); !found || token == "" {
			http.Error(w, "no Plex token: provide one in the request", http.StatusBadRequest)
			return
		}
	}

	servers, err := mediaserver.DiscoverPlexServers(r.Context(), token)
	if err != nil {
		http.Error(w, "plex discovery failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	if fromRequest {
		// Discovery succeeded → the token is good; remember it.
		if err := settings.Put(plexTokenKey, token); err != nil {
			slog.Warn("tools: save plex token", "err", err)
		}
	}

	// Gather movie sections from every owned server in parallel. A server that
	// is unreachable is reported, not fatal — but reconciling against a partial
	// library would flag everything on the missing server as "missing", so any
	// owned server that cannot be listed aborts the reconcile.
	type serverResult struct {
		info     plexReconcileServer
		sections []mediaserver.MovieSection
	}
	var (
		mu      sync.Mutex
		results []serverResult
		wg      sync.WaitGroup
	)
	for _, srv := range servers {
		if !srv.Owned {
			continue
		}
		wg.Add(1)
		go func(srv mediaserver.DiscoveredServer) {
			defer wg.Done()
			res := serverResult{info: plexReconcileServer{Name: srv.Name}}
			base, err := srv.Connect(r.Context())
			if err != nil {
				res.info.Err = err.Error()
			} else {
				res.info.URI = base
				secs, err := mediaserver.PlexMovieSections(r.Context(), base, srv.Token)
				if err != nil {
					res.info.Err = err.Error()
				} else {
					res.sections = secs
					res.info.Sections = len(secs)
				}
			}
			mu.Lock()
			results = append(results, res)
			mu.Unlock()
		}(srv)
	}
	wg.Wait()

	var (
		serverInfos []plexReconcileServer
		sections    []mediaserver.MovieSection
		anyErr      string
	)
	for _, res := range results {
		serverInfos = append(serverInfos, res.info)
		sections = append(sections, res.sections...)
		if res.info.Err != "" {
			anyErr = res.info.Err
		}
	}
	if anyErr != "" {
		writeJSON(w, map[string]any{
			"servers": serverInfos,
			"error":   "one or more servers could not be listed — reconcile aborted to avoid false positives: " + anyErr,
		})
		return
	}
	if len(sections) == 0 {
		writeJSON(w, map[string]any{
			"servers": serverInfos,
			"error":   "no movie sections found on any owned server",
		})
		return
	}

	records, err := s.movieTrackerRecords(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	missing := reconcile.Movies(records, sections)
	libCount := 0
	for _, sec := range sections {
		libCount += len(sec.Items)
	}
	writeJSON(w, map[string]any{
		"servers": serverInfos,
		"tracker": len(records),
		"library": libCount,
		"missing": missing,
	})
}

// movieTrackerRecords loads every record from the movies tracker bucket.
func (s *Server) movieTrackerRecords(_ context.Context) ([]reconcile.TrackerRecord, error) {
	b := s.db.Bucket(imovies.TrackerBucketName)
	keys, err := b.Keys()
	if err != nil {
		return nil, err
	}
	out := make([]reconcile.TrackerRecord, 0, len(keys))
	for _, k := range keys {
		var rec imovies.Record
		if found, _ := b.Get(k, &rec); found {
			out = append(out, reconcile.TrackerRecord{Key: k, Record: rec})
		}
	}
	return out, nil
}

// apiToolsPlexForget removes the given movie-tracker keys so the titles can be
// re-downloaded. Keys come from a prior reconcile response.
//
// POST /api/tools/plex/forget {"keys": ["title|year", "title|year|3d", ...]}
func (s *Server) apiToolsPlexForget(w http.ResponseWriter, r *http.Request) {
	if s.db == nil {
		http.Error(w, "database not available", http.StatusNotImplemented)
		return
	}
	var req struct {
		Keys []string `json:"keys"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Keys) == 0 {
		http.Error(w, "missing keys", http.StatusBadRequest)
		return
	}
	b := s.db.Bucket(imovies.TrackerBucketName)
	forgotten := 0
	for _, k := range req.Keys {
		if err := b.Delete(k); err == nil {
			forgotten++
		}
	}
	writeJSON(w, map[string]any{"forgotten": forgotten})
}
