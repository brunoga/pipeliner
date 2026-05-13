// Package list_match provides a filter plugin that accepts entries whose title
// is present in a named persistent entry list and rejects all others.
//
// The list is written by the list_add output plugin and persisted in a SQLite
// database so it survives across runs. An optional remove_on_match setting
// deletes each matched entry from the list after it is accepted, which is
// useful for one-shot download queues.
//
// Config keys:
//
//	list           - name of the list to match against (required)
//	remove_on_match - remove the entry from the list on match (default: false)
package list_match

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/entrylist"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "list_match",
		Description: "accept entries whose title is in a named persistent list; reject others",
		Role:        plugin.RoleProcessor,
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "list",             Type: plugin.FieldTypeString, Required: true, Hint: "Persistent list name to match against"},
			{Key: "remove_on_match",  Type: plugin.FieldTypeBool,                   Hint: "Remove matched entry from the list (default false)"},
			{Key: "reject_unmatched", Type: plugin.FieldTypeBool,                   Hint: "Reject entries not in the list (default true)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "list", "list_match"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "list_match", "list", "remove_on_match", "reject_unmatched")...)
	return errs
}

type listMatchPlugin struct {
	db              *store.SQLiteStore
	listName        string
	removeOnMatch   bool
	rejectUnmatched bool
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	listName, _ := cfg["list"].(string)
	if listName == "" {
		return nil, fmt.Errorf("list_match: 'list' is required")
	}
	remove, _ := cfg["remove_on_match"].(bool)
	rejectUnmatched := true
	if v, ok := cfg["reject_unmatched"]; ok {
		rejectUnmatched, _ = v.(bool)
	}
	return &listMatchPlugin{db: db, listName: listName, removeOnMatch: remove, rejectUnmatched: rejectUnmatched}, nil
}

func (p *listMatchPlugin) Name() string        { return "list_match" }

func (p *listMatchPlugin) filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	list := entrylist.Open(p.db, p.listName)
	found, err := list.Contains(e.Title)
	if err != nil {
		return fmt.Errorf("list_match: lookup: %w", err)
	}
	if found {
		e.Accept()
		if p.removeOnMatch {
			if err := list.Remove(e.Title); err != nil {
				tc.Logger.Error("list_match: remove entry", "title", e.Title, "err", err)
			}
		}
	} else if p.rejectUnmatched {
		e.Reject("list_match: not in list")
	}
	return nil
}

func (p *listMatchPlugin) Process(ctx context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		if err := p.filter(ctx, tc, e); err != nil {
			tc.Logger.Warn("filter error", "entry", e.Title, "err", err)
		}
	}
	return entry.PassThrough(entries), nil
}
