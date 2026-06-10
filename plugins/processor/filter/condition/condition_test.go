package condition

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext { return &plugin.TaskContext{Name: "test"} }

func makeEntry(title string, fields map[string]any) *entry.Entry {
	e := entry.New(title, "http://x.com/a")
	for k, v := range fields {
		e.Set(k, v)
	}
	return e
}

func open(t *testing.T, cfg map[string]any) *conditionPlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*conditionPlugin)
}

func TestAcceptConditionTrue(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{gt .score 7.0}}`})
	e := makeEntry("Movie", map[string]any{"score": 8.5})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v", e.State)
	}
}

func TestAcceptConditionFalse(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{gt .score 7.0}}`})
	e := makeEntry("Movie", map[string]any{"score": 5.0})
	p.filter(context.Background(), makeCtx(), e)
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("expected undecided when accept condition is false, got %v", e.State)
	}
}

func TestRejectConditionTrue(t *testing.T) {
	p := open(t, map[string]any{"reject": `{{lt .score 5.0}}`})
	e := makeEntry("Movie", map[string]any{"score": 3.0})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Errorf("expected rejected, got %v", e.State)
	}
}

func TestRejectWinsOverAccept(t *testing.T) {
	p := open(t, map[string]any{
		"accept": `{{gt .score 0.0}}`,
		"reject": `{{lt .score 5.0}}`,
	})
	e := makeEntry("Movie", map[string]any{"score": 3.0})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Errorf("reject should win over accept, got %v", e.State)
	}
}

func TestTitleFieldAccessible(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{eq .Title "Good Movie"}}`})
	e := makeEntry("Good Movie", nil)
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted by title match, got %v", e.State)
	}
}

func TestStringFieldEquality(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{eq .genre "Action"}}`})
	e := makeEntry("Movie", map[string]any{"genre": "Action"})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v: %s", e.State, e.RejectReason)
	}
}

func TestTmdbIntegration(t *testing.T) {
	// Simulate fields set by metainfo_tmdb.
	p := open(t, map[string]any{"accept": `{{gt .tmdb_vote_average 7.5}}`})
	e := makeEntry("Inception.2010.1080p", map[string]any{"tmdb_vote_average": 8.8})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("expected accepted for high vote average, got %v", e.State)
	}
}

func TestMissingConditionError(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Fatal("expected error when neither accept nor reject is set")
	}
}

func TestInvalidAcceptTemplate(t *testing.T) {
	_, err := newPlugin(map[string]any{"accept": `{{invalid`}, nil)
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

func TestInvalidRejectTemplate(t *testing.T) {
	_, err := newPlugin(map[string]any{"reject": `{{invalid`}, nil)
	if err == nil {
		t.Fatal("expected error for invalid template")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("condition")
	if !ok {
		t.Fatal("condition not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("phase: got %v", d.Role)
	}
}

// --- Multi-rule format ---

func TestRulesFirstRejectFires(t *testing.T) {
	p := open(t, map[string]any{
		"rules": []any{
			map[string]any{"reject": `{{lt .score 5.0}}`},
			map[string]any{"accept": `{{gt .score 3.0}}`},
		},
	})
	e := makeEntry("Movie", map[string]any{"score": 4.0})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsRejected() {
		t.Errorf("first rule should reject; got %v", e.State)
	}
}

func TestRulesSecondAcceptFires(t *testing.T) {
	p := open(t, map[string]any{
		"rules": []any{
			map[string]any{"reject": `{{lt .score 3.0}}`},
			map[string]any{"accept": `{{gt .score 5.0}}`},
		},
	})
	e := makeEntry("Movie", map[string]any{"score": 8.0})
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsAccepted() {
		t.Errorf("second rule should accept; got %v", e.State)
	}
}

func TestRulesNoMatchLeavesUndecided(t *testing.T) {
	p := open(t, map[string]any{
		"rules": []any{
			map[string]any{"reject": `{{lt .score 3.0}}`},
			map[string]any{"accept": `{{gt .score 9.0}}`},
		},
	})
	e := makeEntry("Movie", map[string]any{"score": 5.0})
	p.filter(context.Background(), makeCtx(), e)
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("no rule matched — should be undecided; got %v", e.State)
	}
}

func TestEmptyRulesError(t *testing.T) {
	_, err := newPlugin(map[string]any{"rules": []any{}}, nil)
	if err == nil {
		t.Fatal("expected error for empty rules list")
	}
}

func TestRulesInvalidItemError(t *testing.T) {
	_, err := newPlugin(map[string]any{"rules": []any{"not-a-map"}}, nil)
	if err == nil {
		t.Fatal("expected error for non-map rule item")
	}
}

func TestStateIdentifier(t *testing.T) {
	// `state == "failed"` should fire only on Failed entries. The plugin
	// auto-widens its InputStates to all four when any rule mentions state,
	// so the executor stops hiding non-Accepted/Undecided entries from it.
	p := open(t, map[string]any{"reject": `state == "failed"`})
	if !p.referencesSt {
		t.Fatal("plugin should detect state reference in reject expression")
	}
	if got := p.EffectiveInputStates(); got != entry.StatesAll {
		t.Errorf("EffectiveInputStates() = %v; want StatesAll", got)
	}

	// A Failed entry hitting the reject rule must NOT transition to
	// Rejected — that would overwrite the original failure reason. Reject
	// on a Failed entry is a no-op (no state change, no reason change).
	failed := makeEntry("Movie", nil)
	failed.Fail("original failure")
	p.filter(context.Background(), makeCtx(), failed)
	if !failed.IsFailed() {
		t.Errorf("Failed entry must stay Failed after reject rule fires, got %v", failed.State)
	}
	if failed.FailReason != "original failure" {
		t.Errorf("FailReason mutated to %q; expected original to be preserved", failed.FailReason)
	}

	// Accepted entry not matching state=="failed" stays Undecided
	// (no rule fired).
	acc := makeEntry("Movie", nil)
	acc.Accept()
	p.filter(context.Background(), makeCtx(), acc)
	if !acc.IsAccepted() {
		t.Errorf("Accepted entry should stay Accepted when no rule matches, got %v", acc.State)
	}
}

func TestStateIdentifierAcceptDoesNotUnfail(t *testing.T) {
	// Pathological case: user writes `accept: state == "failed"`. Without
	// the guard, Accept() would un-Fail the entry and corrupt the run
	// summary. The plugin must treat accept-on-Failed as a no-op so the
	// terminal state is preserved.
	p := open(t, map[string]any{"accept": `state == "failed"`})
	e := makeEntry("Movie", nil)
	e.Fail("disk full")
	p.filter(context.Background(), makeCtx(), e)
	if !e.IsFailed() {
		t.Errorf("Failed entry must stay Failed even when accept rule matches, got %v", e.State)
	}
	if e.FailReason != "disk full" {
		t.Errorf("FailReason mutated to %q; expected original to be preserved", e.FailReason)
	}
}

func TestNoStateRefKeepsDefaultInputStates(t *testing.T) {
	// Existing configs that don't mention state must continue to use the
	// default StatesAcceptedUndecided pre-filter — auto-widening must not
	// leak into expressions written for the legacy behavior.
	p := open(t, map[string]any{"accept": `tmdb_vote_average > 7`})
	if p.referencesSt {
		t.Fatal("plugin should NOT detect state reference in a field-only expression")
	}
	if got := p.EffectiveInputStates(); got != entry.StatesAcceptedUndecided {
		t.Errorf("EffectiveInputStates() = %v; want StatesAcceptedUndecided", got)
	}
}
