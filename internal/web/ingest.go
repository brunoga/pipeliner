package web

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/brunoga/pipeliner/internal/ingest"
)

// maxIngestBody caps a single push payload.
const maxIngestBody = 1 << 20 // 1 MiB

// SetIngestToken enables the push-ingest endpoint. When empty (the default)
// the endpoint answers 404, indistinguishable from not existing.
func (s *Server) SetIngestToken(token string) { s.ingestToken = token }

// apiIngest handles POST /api/ingest/{queue}: bearer-token-authenticated
// machine pushes (autobrr, IRC bridges, …). The body is one item or an array
// of items: {"title": "...", "url": "...", "fields": {...}}. Items without a
// title are rejected. With ?pipeline=<name>, the named pipeline is triggered
// after the items are queued so the push takes effect immediately.
//
// This route is mounted on the unauthenticated mux: browsers never call it,
// and machines can't do session login. The token is the whole auth story —
// treat it like a password.
func (s *Server) apiIngest(w http.ResponseWriter, r *http.Request) {
	if s.ingestToken == "" {
		http.NotFound(w, r)
		return
	}
	auth := r.Header.Get("Authorization")
	tok, ok := strings.CutPrefix(auth, "Bearer ")
	if !ok || subtle.ConstantTimeCompare([]byte(tok), []byte(s.ingestToken)) != 1 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	queue := r.PathValue("queue")
	if queue == "" {
		http.Error(w, "missing queue name", http.StatusBadRequest)
		return
	}

	body := http.MaxBytesReader(w, r.Body, maxIngestBody)
	dec := json.NewDecoder(body)
	var items []ingest.Item
	// Accept a single object or an array.
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		http.Error(w, "invalid json: "+err.Error(), http.StatusBadRequest)
		return
	}
	if len(raw) > 0 && raw[0] == '[' {
		if err := json.Unmarshal(raw, &items); err != nil {
			http.Error(w, "invalid json array: "+err.Error(), http.StatusBadRequest)
			return
		}
	} else {
		var one ingest.Item
		if err := json.Unmarshal(raw, &one); err != nil {
			http.Error(w, "invalid json object: "+err.Error(), http.StatusBadRequest)
			return
		}
		items = []ingest.Item{one}
	}

	valid := items[:0]
	rejected := 0
	for _, it := range items {
		if it.Title == "" {
			rejected++
			continue
		}
		valid = append(valid, it)
	}
	accepted, dropped := ingest.Enqueue(queue, valid)

	pipeline := r.URL.Query().Get("pipeline")
	triggered := false
	if pipeline != "" && accepted > 0 && s.daemon != nil {
		s.daemon.Trigger(pipeline, false)
		triggered = true
	}

	writeJSON(w, map[string]any{
		"queued":    accepted,
		"dropped":   dropped,
		"rejected":  rejected,
		"triggered": triggered,
	})
}
