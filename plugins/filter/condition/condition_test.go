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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v", e.State)
	}
}

func TestAcceptConditionFalse(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{gt .score 7.0}}`})
	e := makeEntry("Movie", map[string]any{"score": 5.0})
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if e.IsAccepted() || e.IsRejected() {
		t.Errorf("expected undecided when accept condition is false, got %v", e.State)
	}
}

func TestRejectConditionTrue(t *testing.T) {
	p := open(t, map[string]any{"reject": `{{lt .score 5.0}}`})
	e := makeEntry("Movie", map[string]any{"score": 3.0})
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsRejected() {
		t.Errorf("reject should win over accept, got %v", e.State)
	}
}

func TestTitleFieldAccessible(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{eq .Title "Good Movie"}}`})
	e := makeEntry("Good Movie", nil)
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("expected accepted by title match, got %v", e.State)
	}
}

func TestStringFieldEquality(t *testing.T) {
	p := open(t, map[string]any{"accept": `{{eq .genre "Action"}}`})
	e := makeEntry("Movie", map[string]any{"genre": "Action"})
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
	if !e.IsAccepted() {
		t.Errorf("expected accepted, got %v: %s", e.State, e.RejectReason)
	}
}

func TestTmdbIntegration(t *testing.T) {
	// Simulate fields set by metainfo_tmdb.
	p := open(t, map[string]any{"accept": `{{gt .tmdb_vote_average 7.5}}`})
	e := makeEntry("Inception.2010.1080p", map[string]any{"tmdb_vote_average": 8.8})
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
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
	if d.PluginPhase != plugin.PhaseFilter {
		t.Errorf("phase: got %v", d.PluginPhase)
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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
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
	p.Filter(context.Background(), makeCtx(), e) //nolint:errcheck
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
