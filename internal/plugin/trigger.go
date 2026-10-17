package plugin

import (
	"fmt"
	"slices"
	"strings"
)

// TriggerKind is why a pipeline run is happening.
type TriggerKind string

const (
	// TriggerSchedule is a cron/interval fire from the daemon.
	TriggerSchedule TriggerKind = "schedule"
	// TriggerManual is a person pressing Run, or the CLI running one task.
	TriggerManual TriggerKind = "manual"
	// TriggerWebhook is a push to an ingest queue that named this pipeline.
	TriggerWebhook TriggerKind = "webhook"
	// TriggerStartup is the run some pipelines take once when the daemon
	// starts.
	TriggerStartup TriggerKind = "startup"
	// TriggerProbe is the background probe worker reporting that a release
	// it was asked about turned out readable, so the pipeline that asked can
	// act on it now rather than at its next scheduled run.
	TriggerProbe TriggerKind = "probe"
)

// Trigger is why this run is happening, and for a push, which queue it came
// from. It reaches plugins on the TaskContext.
//
// The cause was known at the top of every run and discarded two calls later,
// which is what made a push run a pipeline's unrelated sources: an ingest POST
// naming a pipeline triggered the whole graph, so the feeds alongside the
// webhook ran too. One measured instance cost a Trakt fetch of 988 titles and
// a 2m09s search sweep for a push of one film -- and the pushed film then
// competed with 44 unrelated candidates for the four slots a downstream limit
// node allowed.
type Trigger struct {
	Kind TriggerKind
	// Queue is the ingest queue that was pushed, for Kind == TriggerWebhook.
	Queue string
}

// RunOnKey is the node-level config key that says which triggers a source
// should run on. It stays in the node's config rather than being stripped out
// of it, so a visual-editor save round-trips it like any other keyword; the
// unknown-key check below is what keeps plugins from rejecting it.
const RunOnKey = "run_on"

// reservedNodeKeys are config keys the executor interprets rather than the
// plugin, so a plugin's own unknown-key check must not object to them.
var reservedNodeKeys = []string{RunOnKey}

// Matches reports whether a node with this run_on list should run.
//
// An empty list means always, so a node says nothing about triggers until it
// chooses to -- adding the mechanism changes no existing pipeline.
//
// A token is a kind ("schedule"), "any", or a kind with a qualifier
// ("webhook:3d-movies"), which matches only a push to that queue.
//
// "schedule" also admits the startup fire. A pipeline configured to run at
// boot is running its schedule early, so a source that says "schedule" would
// be surprised to sit that one out -- and nobody writing run_on is thinking
// about the boot fire. "startup" on its own still means only the boot fire.
// A probe fire is the same thing for the same reason: it is the pipeline's
// scheduled work brought forward because a release it wanted became usable.
func (t Trigger) Matches(runOn []string) bool {
	if len(runOn) == 0 {
		return true
	}
	for _, raw := range runOn {
		kind, qualifier, hasQualifier := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), ":")
		if kind == "any" {
			return true
		}
		if kind != string(t.Kind) && !(t.Kind.schedulelike() && kind == string(TriggerSchedule)) {
			continue
		}
		if !hasQualifier {
			return true
		}
		// A qualifier only means anything for a webhook, where it names the
		// queue. Compared case-insensitively, as queue names are elsewhere.
		if t.Kind == TriggerWebhook && strings.EqualFold(qualifier, t.Queue) {
			return true
		}
	}
	return false
}

// schedulelike reports whether a fire of this kind is the pipeline's scheduled
// work run early, which a source that runs on "schedule" also runs on.
func (k TriggerKind) schedulelike() bool {
	return k == TriggerStartup || k == TriggerProbe
}

// ValidRunOn reports whether every token in a run_on list is one this
// executor understands, returning the first that is not.
func ValidRunOn(runOn []string) (bad string, ok bool) {
	for _, raw := range runOn {
		kind, qualifier, hasQualifier := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), ":")
		switch TriggerKind(kind) {
		case TriggerSchedule, TriggerManual, TriggerStartup, TriggerProbe:
			if hasQualifier {
				return raw, false // only a webhook takes a qualifier
			}
		case TriggerWebhook:
			if hasQualifier && qualifier == "" {
				return raw, false
			}
		default:
			if kind != "any" || hasQualifier {
				return raw, false
			}
		}
	}
	return "", true
}

// RunOnKinds lists the trigger kinds a run_on list admits, for the validator's
// "no source runs on this trigger" check. A list naming "any", or an empty
// one, admits every kind.
func RunOnKinds(runOn []string) []TriggerKind {
	all := []TriggerKind{TriggerSchedule, TriggerManual, TriggerWebhook, TriggerStartup, TriggerProbe}
	if len(runOn) == 0 {
		return all
	}
	var out []TriggerKind
	add := func(k TriggerKind) {
		if slices.Contains(all, k) && !slices.Contains(out, k) {
			out = append(out, k)
		}
	}
	for _, raw := range runOn {
		kind, _, _ := strings.Cut(strings.ToLower(strings.TrimSpace(raw)), ":")
		if kind == "any" {
			return all
		}
		k := TriggerKind(kind)
		add(k)
		if k == TriggerSchedule {
			add(TriggerStartup) // see Matches
			add(TriggerProbe)
		}
	}
	return out
}

// RunOnOf reads the run_on list from a node's config, accepting a single
// string as well as a list because "run_on=\"webhook\"" is the common case.
func RunOnOf(cfg map[string]any) ([]string, error) {
	v, ok := cfg[RunOnKey]
	if !ok {
		return nil, nil
	}
	switch val := v.(type) {
	case string:
		return []string{val}, nil
	case []string:
		return val, nil
	case []any:
		out := make([]string, 0, len(val))
		for _, x := range val {
			s, ok := x.(string)
			if !ok {
				return nil, fmt.Errorf("%s must be a string or a list of strings", RunOnKey)
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("%s must be a string or a list of strings", RunOnKey)
}
