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
//	db             - path to the SQLite database (required)
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
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
	})
}

type listMatchPlugin struct {
	dbPath        string
	listName      string
	removeOnMatch bool
}

func newPlugin(cfg map[string]any) (plugin.Plugin, error) {
	dbPath, _ := cfg["db"].(string)
	if dbPath == "" {
		return nil, fmt.Errorf("list_match: 'db' is required")
	}
	listName, _ := cfg["list"].(string)
	if listName == "" {
		return nil, fmt.Errorf("list_match: 'list' is required")
	}
	remove, _ := cfg["remove_on_match"].(bool)
	return &listMatchPlugin{dbPath: dbPath, listName: listName, removeOnMatch: remove}, nil
}

func (p *listMatchPlugin) Name() string        { return "list_match" }
func (p *listMatchPlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *listMatchPlugin) Filter(_ context.Context, tc *plugin.TaskContext, e *entry.Entry) error {
	s, err := store.OpenSQLite(p.dbPath)
	if err != nil {
		return fmt.Errorf("list_match: open db: %w", err)
	}
	defer s.Close()

	list := entrylist.Open(s, p.listName)
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
	} else {
		e.Reject("list_match: not in list")
	}
	return nil
}
