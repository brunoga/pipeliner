package swap_state

import (
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

func newP(t *testing.T, cfg map[string]any) plugin.ProcessorPlugin {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(plugin.ProcessorPlugin)
}

// accepted ↔ rejected: both populations must flip in a single pass.
func TestSwap_AcceptedRejected_BothDirections(t *testing.T) {
	p := newP(t, map[string]any{"swap": []any{"accepted", "rejected"}})

	winner := entry.New("winner", "u1")
	winner.Accept("upstream: best copy")
	loser := entry.New("loser", "u2")
	loser.Reject("upstream: dedup loser")
	bystander := entry.New("bystander", "u3") // Undecided, untouched

	out, err := p.Process(context.Background(), tc(), []*entry.Entry{winner, loser, bystander})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if len(out) != 3 {
		t.Fatalf("output length: got %d, want 3 (swap_state never drops entries)", len(out))
	}

	if !winner.IsRejected() {
		t.Errorf("winner: state = %s, want rejected (was accepted)", winner.State)
	}
	if !loser.IsAccepted() {
		t.Errorf("loser: state = %s, want accepted (was rejected)", loser.State)
	}
	if !bystander.IsUndecided() {
		t.Errorf("bystander: state = %s, want undecided (untouched)", bystander.State)
	}
	if bystander.LastStateChange != nil {
		t.Errorf("bystander: LastStateChange should be nil (no swap occurred), got %+v", bystander.LastStateChange)
	}
}

// AcceptReason / RejectReason / FailReason survive across the swap as audit
// history. LastStateChange records the transition itself.
func TestSwap_PreservesPriorReason_AndRecordsChange(t *testing.T) {
	p := newP(t, map[string]any{"swap": []any{"accepted", "failed"}})

	rescued := entry.New("rescued", "u1")
	rescued.Fail("deluge: connection refused")

	if _, err := p.Process(context.Background(), tc(), []*entry.Entry{rescued}); err != nil {
		t.Fatalf("Process: %v", err)
	}

	if !rescued.IsAccepted() {
		t.Fatalf("state: got %s, want accepted", rescued.State)
	}
	if rescued.FailReason != "deluge: connection refused" {
		t.Errorf("FailReason: got %q, want preserved across swap", rescued.FailReason)
	}
	if rescued.LastStateChange == nil {
		t.Fatal("LastStateChange must be set after a swap")
	}
	sc := rescued.LastStateChange
	if sc.From != entry.Failed || sc.To != entry.Accepted {
		t.Errorf("From/To: got %s→%s, want failed→accepted", sc.From, sc.To)
	}
	if sc.Plugin != "swap_state" {
		t.Errorf("Plugin: got %q, want swap_state", sc.Plugin)
	}
	if !strings.Contains(sc.Reason, "accepted") || !strings.Contains(sc.Reason, "failed") {
		t.Errorf("Reason should name both states, got %q", sc.Reason)
	}
	if sc.At.IsZero() {
		t.Error("StateChange.At must be set")
	}
}

// Consumed is orthogonal to State — it must survive the swap unchanged.
func TestSwap_PreservesConsumedFlag(t *testing.T) {
	p := newP(t, map[string]any{"swap": []any{"accepted", "rejected"}})

	e := entry.New("t", "u")
	e.Accept()
	e.Consume()
	if !e.IsConsumed() {
		t.Fatal("setup: entry should be consumed")
	}

	if _, err := p.Process(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !e.IsRejected() {
		t.Errorf("state: got %s, want rejected", e.State)
	}
	if !e.IsConsumed() {
		t.Error("Consumed flag must be preserved across swap")
	}
}

// Process must walk rejected and failed entries — that's the whole point of
// the plugin. Most processors skip them; swap_state is the deliberate
// exception.
func TestSwap_ActsOnRejectedAndFailed(t *testing.T) {
	cases := []struct {
		swap   []any
		set    func(*entry.Entry)
		check  func(*testing.T, *entry.Entry)
		desc   string
	}{
		{[]any{"rejected", "undecided"},
			func(e *entry.Entry) { e.Reject("x") },
			func(t *testing.T, e *entry.Entry) {
				if !e.IsUndecided() {
					t.Errorf("got %s, want undecided", e.State)
				}
			}, "rejected → undecided"},
		{[]any{"failed", "undecided"},
			func(e *entry.Entry) { e.Fail("x") },
			func(t *testing.T, e *entry.Entry) {
				if !e.IsUndecided() {
					t.Errorf("got %s, want undecided", e.State)
				}
			}, "failed → undecided"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			p := newP(t, map[string]any{"swap": c.swap})
			e := entry.New("t", "u")
			c.set(e)
			if _, err := p.Process(context.Background(), tc(), []*entry.Entry{e}); err != nil {
				t.Fatalf("Process: %v", err)
			}
			c.check(t, e)
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
		want string // empty = expect no error; substring otherwise
	}{
		{"ok", map[string]any{"swap": []any{"accepted", "rejected"}}, ""},
		{"missing", map[string]any{}, `"swap" is required`},
		{"one_element", map[string]any{"swap": []any{"accepted"}}, "exactly two"},
		{"three_elements", map[string]any{"swap": []any{"accepted", "rejected", "failed"}}, "exactly two"},
		{"unknown_state", map[string]any{"swap": []any{"accepted", "lost"}}, `unknown state "lost"`},
		{"same_state", map[string]any{"swap": []any{"accepted", "accepted"}}, "must be distinct"},
		{"unknown_key", map[string]any{"swap": []any{"accepted", "rejected"}, "bogus": 1}, "bogus"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			errs := validate(c.cfg)
			if c.want == "" {
				if len(errs) != 0 {
					t.Errorf("want no errors, got %v", errs)
				}
				return
			}
			var found bool
			for _, e := range errs {
				if e != nil && strings.Contains(e.Error(), c.want) {
					found = true
				}
			}
			if !found {
				t.Errorf("want error containing %q, got %v", c.want, errs)
			}
		})
	}
}

// Sanity: Clone() must deep-copy LastStateChange so a clone mutating its
// fields cannot affect the original.
func TestEntryClone_DeepCopiesLastStateChange(t *testing.T) {
	p := newP(t, map[string]any{"swap": []any{"accepted", "rejected"}})

	e := entry.New("t", "u")
	e.Accept()
	if _, err := p.Process(context.Background(), tc(), []*entry.Entry{e}); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if e.LastStateChange == nil {
		t.Fatal("setup: LastStateChange must be set")
	}

	clone := e.Clone()
	if clone.LastStateChange == e.LastStateChange {
		t.Fatal("Clone shared LastStateChange pointer — must be a fresh allocation")
	}
	// Mutate clone, ensure original is unaffected.
	clone.LastStateChange.Reason = "tampered"
	if e.LastStateChange.Reason == "tampered" {
		t.Error("mutation of clone leaked into original")
	}
}

func TestRegistration(t *testing.T) {
	d, ok := plugin.Lookup("swap_state")
	if !ok {
		t.Fatal("swap_state not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("role: got %v, want processor", d.Role)
	}
}
