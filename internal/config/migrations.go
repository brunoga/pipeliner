package config

// Config-load migrations rewrite legacy `process(...)` shapes into the current
// form at parse time. Each Migration runs for every process() call; it may
// inspect/modify the config map, inject upstream nodes, and emit a warning.
//
// To add a new migration, append a Migration to the migrations slice. Keep
// each migration small and testable: it should match a specific (plugin
// name, config key) pattern and do nothing for unrelated calls.

import (
	"fmt"

	"github.com/brunoga/pipeliner/internal/dag"
)

// MigrationContext is the surface a Migration sees when it runs against a
// pending process() call. The migration may:
//   - Mutate Config in place (delete deprecated keys, rename keys, etc.).
//   - Use AddNode to register an intermediate node (typically as an upstream
//     of the original one) and use the returned ID in the returned slice.
//   - Return a warning string that will be surfaced via Config.LoadWarnings.
type MigrationContext struct {
	PluginName string
	Config     map[string]any
	Upstreams  []dag.NodeID
	CallKey    string
	// AddNode registers an intermediate node and returns its NodeID. Use the
	// returned ID as an upstream of the original (or later-injected) node.
	AddNode func(pluginName string, cfg map[string]any, upstreams []dag.NodeID, callKey string) dag.NodeID
}

// Migration is one config-load rewrite rule.
type Migration struct {
	Name  string
	Apply func(mc MigrationContext) (newUpstreams []dag.NodeID, warning string)
}

// migrations is the ordered list of migrations applied to every process() call.
var migrations = []Migration{
	legacyQualityMigration,
}

// legacyQualityMigration rewrites `process("series"|"movies"|"premiere",
// quality="X", ...)` into an explicit upstream `process("quality", spec="X")`
// node followed by the original node with the quality key removed.
var legacyQualityMigration = Migration{
	Name: "legacy-quality-knob",
	Apply: func(mc MigrationContext) ([]dag.NodeID, string) {
		switch mc.PluginName {
		case "series", "movies", "premiere":
		default:
			return mc.Upstreams, ""
		}
		specRaw, ok := mc.Config["quality"]
		if !ok {
			return mc.Upstreams, ""
		}
		spec, _ := specRaw.(string)
		delete(mc.Config, "quality")
		if spec == "" {
			return mc.Upstreams, ""
		}
		qid := mc.AddNode("quality", map[string]any{"spec": spec}, mc.Upstreams, mc.CallKey)
		return []dag.NodeID{qid}, fmt.Sprintf(
			"%q: %q config key is deprecated — auto-inserted an upstream process(%q, spec=%q) node; please update your config",
			mc.PluginName, "quality", "quality", spec)
	},
}

// applyMigrations runs every migration against a pending process() call.
// Returns the (possibly rewritten) upstream slice the original node should use.
func (ctx *execContext) applyMigrations(pluginName string, cfg map[string]any, upstreams []dag.NodeID, callKey string) []dag.NodeID {
	for _, m := range migrations {
		// Bind a per-migration AddNode that tags injected nodes with this
		// migration's Name so the visual editor can mark them as
		// auto-migrated.
		migrationName := m.Name
		addNode := func(pluginName string, cfg map[string]any, upstreams []dag.NodeID, callKey string) dag.NodeID {
			return ctx.addPendingNode(pluginName, cfg, upstreams, callKey, migrationName)
		}
		newUps, warn := m.Apply(MigrationContext{
			PluginName: pluginName,
			Config:     cfg,
			Upstreams:  upstreams,
			CallKey:    callKey,
			AddNode:    addNode,
		})
		upstreams = newUps
		if warn != "" {
			ctx.loadWarnings = append(ctx.loadWarnings, fmt.Errorf("%s", warn))
		}
	}
	return upstreams
}

// addPendingNode assigns the next NodeID for pluginName and appends a record
// to ctx.pendingNodes. autoMigrated tags nodes injected by a migration; pass
// "" for user-authored nodes.
func (ctx *execContext) addPendingNode(pluginName string, cfg map[string]any, upstreams []dag.NodeID, callKey, autoMigrated string) dag.NodeID {
	id := ctx.nextNodeID(pluginName)
	ctx.pendingNodes = append(ctx.pendingNodes, &dagNodeRecord{
		id:              id,
		pluginName:      pluginName,
		config:          cfg,
		upstreams:       upstreams,
		functionCallKey: callKey,
		autoMigrated:    autoMigrated,
	})
	return id
}
