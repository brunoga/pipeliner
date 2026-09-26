package config

import (
	"fmt"
	"time"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"
)

// ScratchStore opens the throwaway database CheckTemplates needs to build
// plugins with. It is in-memory, so it takes no file lock and can run while
// the daemon holds the real database; migrations are skipped because the
// base schema is all a freshly built plugin can want, and running them would
// log four misleading "applied migration" lines per call.
func ScratchStore() (*store.SQLiteStore, error) {
	return store.OpenSQLiteNoMigrate(":memory:")
}

// CheckTemplates catches the config errors Validate structurally cannot.
//
// Validate only runs each plugin's descriptor Validate hook; it never calls a
// factory. Templates are parsed in the factory and executed only when the
// plugin does its work, so an unterminated action reaches the daemon and a
// scoping or type error reaches the recipient's mailbox. This instantiates
// every plugin (surfacing parse errors) and then asks those implementing
// plugin.TemplateChecker to render against synthetic entries (surfacing
// execution errors).
//
// Each node is rendered twice, because the two cases fail differently:
//
//   - with every Reachable field present, which exercises the branches a
//     template takes when metadata lookups succeeded;
//   - with only the Certain fields present, which is the minimum the DAG
//     guarantees. A template that reads a MayProduce field without a {{with}}
//     guard fails here, and that is a real defect — it is exactly the entry
//     that arrives when an enrichment misses.
//
// The two kinds of failure are returned separately because they do not
// deserve the same treatment. A buildErr means a template did not compile,
// which is unambiguous and would fail the daemon's own reload. A renderErr
// means it compiled but blew up on an entry we synthesised, which is almost
// always a real defect but rests on the DAG's field model being complete —
// so a caller serving an interactive editor can report those without
// blocking a save.
//
// db should come from ScratchStore. Plugins holding resources are shut down
// before returning.
func CheckTemplates(c *Config, db *store.SQLiteStore) (buildErrs, renderErrs []error) {
	var built []plugin.ShutdownPlugin
	defer func() {
		for _, sd := range built {
			sd.Shutdown()
		}
	}()
	for _, name := range orderedNames(c) {
		g := c.Graphs[name]
		fieldSets := dag.ComputeNodeFields(g, plugin.Lookup)
		for _, n := range g.Nodes() {
			d, ok := plugin.Lookup(n.PluginName)
			if !ok {
				continue // Validate already reported the unknown plugin
			}
			cfg := n.Config
			if cfg == nil {
				cfg = map[string]any{}
			}
			impl, err := d.Factory(cfg, db)
			if err != nil {
				buildErrs = append(buildErrs, fmt.Errorf("pipeline %q node %q (plugin %q): %w",
					name, n.ID, n.PluginName, err))
				continue
			}
			if sd, ok := impl.(plugin.ShutdownPlugin); ok {
				built = append(built, sd)
			}
			tc, ok := impl.(plugin.TemplateChecker)
			if !ok {
				continue
			}
			fs := fieldSets[n.ID]
			for _, sc := range []struct {
				label  string
				fields []string
			}{
				{"all available fields", fs.Reachable},
				{"only guaranteed fields", fs.Certain},
			} {
				entries := []*entry.Entry{syntheticEntry(sc.fields)}
				if err := tc.CheckTemplates(entries); err != nil {
					renderErrs = append(renderErrs, fmt.Errorf("pipeline %q node %q (plugin %q), %s: %w",
						name, n.ID, n.PluginName, sc.label, err))
				}
			}
		}
	}
	return buildErrs, renderErrs
}

// syntheticEntry builds one accepted entry carrying the named fields, each
// holding a value of the type entry.FieldMeta declares for it. Getting the
// types right is the point: a template calling filesize on a field the
// plugin stores as a string has to fail here.
func syntheticEntry(fields []string) *entry.Entry {
	e := entry.New("https://example.invalid/release.torrent",
		"Example.Release.2024.1080p.BluRay.x264-GROUP")
	for _, f := range fields {
		e.Fields[f] = syntheticValue(f)
	}
	e.Accept("example: accepted by the template check")
	return e
}

// syntheticValue returns a plausible value for a field. Fields absent from
// the known-field registry fall back to a string, which is what an ad-hoc
// field set by a config expression looks like.
func syntheticValue(field string) any {
	if field == entry.FieldQuality {
		return quality.Quality{
			Resolution: quality.Resolutionp1080,
			Source:     quality.SourceBluRay,
		}
	}
	meta, ok := entry.LookupField(field)
	if !ok {
		return "example"
	}
	switch meta.Type {
	case entry.FieldTypeInt:
		return 3
	case entry.FieldTypeInt64:
		return int64(2_400_000_000)
	case entry.FieldTypeFloat:
		return 7.5
	case entry.FieldTypeBool:
		return true
	case entry.FieldTypeStringList:
		return []string{"one", "two"}
	case entry.FieldTypeTime:
		return time.Now().Add(-24 * time.Hour)
	default:
		if len(meta.KnownValues) > 0 {
			return meta.KnownValues[0]
		}
		return "example"
	}
}
