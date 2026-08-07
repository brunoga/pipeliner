package web

import (
	"net/http"

	"github.com/brunoga/pipeliner/internal/notify"
)

// apiNotifiers exposes the config schema of every registered notify backend
// (email, webhook, pushover, …). The visual editor fetches this so that,
// when a notify node selects a backend via `via=`, it can render that
// backend's fields — bound to the node's nested `config={}` dict — instead
// of leaving credentials editable only in the text config.
func (s *Server) apiNotifiers(w http.ResponseWriter, _ *http.Request) {
	type fieldResp struct {
		Key       string   `json:"key"`
		Type      string   `json:"type"`
		Required  bool     `json:"required"`
		Default   any      `json:"default,omitempty"`
		Enum      []string `json:"enum,omitempty"`
		Hint      string   `json:"hint,omitempty"`
		Multiline bool     `json:"multiline,omitempty"`
	}
	type notifierResp struct {
		Name   string      `json:"name"`
		Schema []fieldResp `json:"schema"`
	}

	out := make([]notifierResp, 0)
	for _, n := range notify.All() {
		fields := make([]fieldResp, 0, len(n.Schema))
		for _, f := range n.Schema {
			fields = append(fields, fieldResp{
				Key:       f.Key,
				Type:      string(f.Type),
				Required:  f.Required,
				Default:   f.Default,
				Enum:      f.Enum,
				Hint:      f.Hint,
				Multiline: f.Multiline,
			})
		}
		out = append(out, notifierResp{Name: n.Name, Schema: fields})
	}
	writeJSON(w, out)
}
