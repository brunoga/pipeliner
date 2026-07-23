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
		Role:        plugin.RoleProcessor,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "fields", Type: plugin.FieldTypeList, Default: []string{"url"}, Hint: "Entry fields used to build the seen fingerprint"},
			{Key: "local", Type: plugin.FieldTypeBool, Hint: "Isolate seen store per task instead of sharing globally"},
			{Key: "retry_failed", Type: plugin.FieldTypeBool, Hint: "Reject URLs from the shared failed-grab bucket (written by mark_failed); the exact failed release is never re-grabbed even after its tracker records are forgotten"},
		},
	})
}

func validate(cfg map[string]any) []error {
	return plugin.OptUnknownKeys(cfg, "seen", "fields", "local", "retry_failed")
}

type seenPlugin struct {
	fields []string // which entry fields to include in the fingerprint
	local  bool     // if true, scope the seen store to the task name
	// retryFailed enables the failed-grab check: URLs in the shared
	// seen_failed bucket are rejected with the recorded failure reason.
	// The bucket is keyed by raw release URL (not fingerprint), so the
	// check is independent of the configured fingerprint fields — a seen
	// filter keyed by episode ID still blocks the exact failed release
	// while letting alternative releases of the same episode through
	// (mark_failed forgets the tracker records that would otherwise
	// block them).
	retryFailed bool
	db          *store.SQLiteStore
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	fields := toStringSlice(cfg["fields"])
	if len(fields) == 0 {
		fields = []string{"url"}
	}

	local, _ := cfg["local"].(bool)

	return &seenPlugin{
		fields:      fields,
		local:       local,
		retryFailed: plugin.OptBool(cfg, "retry_failed", false),
		db:          db,
	}, nil
}

func (p *seenPlugin) Name() string { return "seen" }

func (p *seenPlugin) filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	if p.retryFailed {
		fs := store.NewFailedStore(p.db.Bucket(store.FailedBucketName))
		if rec, ok := fs.Get(e.URL); ok {
			reason := rec.Reason
			if reason == "" {
				reason = "previous grab failed"
			}
			e.Reject("seen: previously failed (" + reason + ")")
			return nil
		}
	}
	ss := p.seenStore(tc)
	fp := fingerprint(e, p.fields)
	if ss.IsSeen(fp) {
		e.Reject("already seen")
	}
	return nil
}

func (p *seenPlugin) persist(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	ss := p.seenStore(tc)
	for _, e := range entries {
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

func (p *seenPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("seen filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}

// Commit implements plugin.CommitPlugin. It persists the seen state for all
// entries that were accepted by Process and not subsequently failed by any
// downstream sink. This ensures we only mark entries as seen when the full
// pipeline (including download/output) succeeded.
func (p *seenPlugin) Commit(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	return p.persist(ctx, tc, entries)
}
