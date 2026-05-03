// Package seen provides a deduplication filter and learn plugin backed by SeenStore.
package seen

import (
	"context"
	"crypto/sha256"
	"fmt"
	"sort"
	"strings"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "seen",
		Description: "reject already-processed entries; marks accepted entries as seen in learn phase",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
	})
}

type seenPlugin struct {
	fields []string // which entry fields to include in the fingerprint
	local  bool     // if true, scope the seen store to the task name
	db     *store.SQLiteStore
}

func newPlugin(cfg map[string]any) (plugin.Plugin, error) {
	dbPath, _ := cfg["db"].(string)
	if dbPath == "" {
		dbPath = "pipeliner.db"
	}

	fields := toStringSlice(cfg["fields"])
	if len(fields) == 0 {
		fields = []string{"url"}
	}

	local, _ := cfg["local"].(bool)

	db, err := store.OpenSQLite(dbPath)
	if err != nil {
		return nil, fmt.Errorf("seen: open store: %w", err)
	}

	return &seenPlugin{
		fields: fields,
		local:  local,
		db:     db,
	}, nil
}

func (p *seenPlugin) Name() string        { return "seen" }
func (p *seenPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *seenPlugin) Filter(ctx context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	ss := p.seenStore(tc)
	fp := fingerprint(e, p.fields)
	if ss.IsSeen(fp) {
		e.Reject("already seen")
	}
	return nil
}

func (p *seenPlugin) Learn(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	ss := p.seenStore(tc)
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		fp := fingerprint(e, p.fields)
		if err := ss.Mark(fp, store.SeenRecord{
			Title:  e.Title,
			URL:    e.URL,
			Task:   tc.Name,
			Fields: p.fields,
		}); err != nil {
			return fmt.Errorf("seen: mark %q: %w", fp, err)
		}
	}
	return nil
}

// seenStore returns a SeenStore scoped to the task (if local) or global.
func (p *seenPlugin) seenStore(tc *plugin.TaskContext) *store.SeenStore {
	bucket := "seen"
	if p.local {
		bucket = "seen:" + tc.Name
	}
	return store.NewSeenStore(p.db.Bucket(bucket))
}

// fingerprint computes a SHA-256 hex digest over the specified entry fields.
// Fields are sorted before hashing so order doesn't matter.
func fingerprint(e *entry.Entry, fields []string) string {
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		var val string
		switch f {
		case "title":
			val = e.Title
		case "url":
			val = e.URL
		default:
			val = e.GetString(f)
		}
		parts = append(parts, f+"="+val)
	}
	sort.Strings(parts)
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return fmt.Sprintf("%x", sum)
}

func toStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}
