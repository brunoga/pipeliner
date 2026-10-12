package dag_test

import (
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func nodeWithRunOn(id dag.NodeID, pluginName string, runOn any, upstreams ...dag.NodeID) *dag.Node {
	return &dag.Node{
		ID: id, PluginName: pluginName, Upstreams: upstreams,
		Config: map[string]any{plugin.RunOnKey: runOn},
	}
}

func hasErr(errs []error, substr string) bool {
	for _, e := range errs {
		if strings.Contains(e.Error(), substr) {
			return true
		}
	}
	return false
}

// TestRunOnValidOnASource is the shape the live movies-3d-discover pipeline
// wants: a feed that sits out a push, beside a webhook that is the push.
func TestRunOnValidOnASource(t *testing.T) {
	g := makeGraph(t,
		nodeWithRunOn("feed", "rssp", []any{"schedule", "manual"}),
		nodeWithRunOn("hook", "webhookp", "webhook"),
		node("proc", "seenp", "feed", "hook"),
		node("out", "txp", "proc"),
	)
	reg := makeRegistry(sourceDescFor("rssp"), sourceDescFor("webhookp"),
		processorDescFor("seenp"), sinkDescFor("txp"))

	errs, warns := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Errorf("run_on on sources should validate: %v", errs)
	}
	for _, w := range warns {
		if strings.Contains(w.Error(), "run_on") {
			t.Errorf("no trigger is unserved here, got warning: %v", w)
		}
	}
}

// TestRunOnRejectedOnAProcessor: a gate that silently stops applying
// depending on why the run happened would be a worse trap than the one
// run_on exists to fix.
func TestRunOnRejectedOnAProcessor(t *testing.T) {
	g := makeGraph(t,
		node("src", "rssp"),
		nodeWithRunOn("proc", "seenp", "webhook", "src"),
		node("out", "txp", "proc"),
	)
	reg := makeRegistry(sourceDescFor("rssp"), processorDescFor("seenp"), sinkDescFor("txp"))

	errs, _ := dag.Validate(g, reg)
	if !hasErr(errs, "source nodes only") {
		t.Errorf("run_on on a processor should be an error, got %v", errs)
	}
}

func TestRunOnRejectsAnUnknownTrigger(t *testing.T) {
	g := makeGraph(t,
		nodeWithRunOn("src", "rssp", "cron"),
		node("out", "txp", "src"),
	)
	reg := makeRegistry(sourceDescFor("rssp"), sinkDescFor("txp"))

	errs, _ := dag.Validate(g, reg)
	if !hasErr(errs, "is not a trigger") {
		t.Errorf("an unknown token should be an error, got %v", errs)
	}
}

func TestRunOnRejectsANonList(t *testing.T) {
	g := makeGraph(t,
		nodeWithRunOn("src", "rssp", 7),
		node("out", "txp", "src"),
	)
	reg := makeRegistry(sourceDescFor("rssp"), sinkDescFor("txp"))

	errs, _ := dag.Validate(g, reg)
	if !hasErr(errs, "must be a string or a list of strings") {
		t.Errorf("a number should be an error, got %v", errs)
	}
}

// TestRunOnWarnsWhenATriggerIsUnserved is the shape a typo takes: the
// pipeline loads, runs, and silently produces nothing for that trigger.
func TestRunOnWarnsWhenATriggerIsUnserved(t *testing.T) {
	g := makeGraph(t,
		nodeWithRunOn("feed", "rssp", "schedule"),
		node("out", "txp", "feed"),
	)
	reg := makeRegistry(sourceDescFor("rssp"), sinkDescFor("txp"))

	errs, warns := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Fatalf("unexpected errors: %v", errs)
	}
	for _, want := range []string{"webhook", "manual"} {
		found := false
		for _, w := range warns {
			if strings.Contains(w.Error(), "no source runs on a "+want) {
				found = true
			}
		}
		if !found {
			t.Errorf("want a warning that no source serves %s, got %v", want, warns)
		}
	}
}

// TestNoRunOnAnywhereIsSilent: the mechanism must be invisible until a config
// asks for it.
func TestNoRunOnAnywhereIsSilent(t *testing.T) {
	g := makeGraph(t,
		node("src", "rssp"),
		node("out", "txp", "src"),
	)
	reg := makeRegistry(sourceDescFor("rssp"), sinkDescFor("txp"))

	errs, warns := dag.Validate(g, reg)
	if len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
	for _, w := range warns {
		if strings.Contains(w.Error(), "run_on") {
			t.Errorf("a graph with no run_on should say nothing about it: %v", w)
		}
	}
}
