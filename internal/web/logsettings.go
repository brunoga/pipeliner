package web

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"
)

// logDebugPluginsResp is the JSON shape for both GET and PUT. The plugins slice
// is always present (never null) so the UI can treat it as a stable array.
type logDebugPluginsResp struct {
	Plugins []string `json:"plugins"`
}

// apiGetLogDebugPlugins returns the current set of plugins whose Debug records
// are forwarded regardless of the global log level. The order is alphabetical
// and stable across calls. 501 when the daemon was started without
// SetPluginLogControl wired (e.g. unit tests).
func (s *Server) apiGetLogDebugPlugins(w http.ResponseWriter, _ *http.Request) {
	if s.pluginLogCtl == nil {
		http.Error(w, "plugin log control not available", http.StatusNotImplemented)
		return
	}
	out := s.pluginLogCtl.DebugPlugins()
	if out == nil {
		out = []string{} // never null in JSON
	}
	writeJSON(w, logDebugPluginsResp{Plugins: out})
}

// apiPutLogDebugPlugins replaces the per-plugin DEBUG override set. The body
// is {"plugins": [...]}. Empty list (or omitted field) disables overrides.
// Unknown plugin names are accepted: they simply never match, and the user is
// free to type a name that will exist once a new plugin is registered. We
// dedupe and sort before storing so the GET response is canonical.
func (s *Server) apiPutLogDebugPlugins(w http.ResponseWriter, r *http.Request) {
	if s.pluginLogCtl == nil {
		http.Error(w, "plugin log control not available", http.StatusNotImplemented)
		return
	}
	var req logDebugPluginsResp
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	seen := make(map[string]struct{}, len(req.Plugins))
	deduped := make([]string, 0, len(req.Plugins))
	for _, name := range req.Plugins {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		deduped = append(deduped, name)
	}
	sort.Strings(deduped)
	s.pluginLogCtl.SetDebugPlugins(deduped)
	if deduped == nil {
		deduped = []string{}
	}
	writeJSON(w, logDebugPluginsResp{Plugins: deduped})
}
