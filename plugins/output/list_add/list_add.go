// Package list_add provides an output plugin that stores accepted entries in a
// named persistent list backed by a SQLite database. The list can later be
// read by the list_match filter plugin in the same or a different task, making
// it possible to decouple "what to watch" from "where to search".
//
// Config keys:
//
//	list - name of the list to add entries to (required)
package list_add

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
		PluginName:  "list_add",
		Description: "add accepted entries to a named persistent list",
		PluginPhase: plugin.PhaseOutput,
		Factory:     newPlugin,
		Validate:    validate,
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireString(cfg, "list", "list_add"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "list_add", "list")...)
	return errs
}

type listAddPlugin struct {
	db       *store.SQLiteStore
	listName string
}

func newPlugin(cfg map[string]any, db *store.SQLiteStore) (plugin.Plugin, error) {
	listName, _ := cfg["list"].(string)
	if listName == "" {
		return nil, fmt.Errorf("list_add: 'list' is required")
	}
	return &listAddPlugin{db: db, listName: listName}, nil
}

func (p *listAddPlugin) Name() string        { return "list_add" }
func (p *listAddPlugin) Phase() plugin.Phase { return plugin.PhaseOutput }

func (p *listAddPlugin) Output(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) error {
	list := entrylist.Open(p.db, p.listName)
	for _, e := range entries {
		if err := list.Add(e.Title, e.URL); err != nil {
			tc.Logger.Error("list_add: store entry", "title", e.Title, "err", err)
		}
	}
	return nil
}
