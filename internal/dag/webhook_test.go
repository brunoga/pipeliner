package dag

import (
	"strings"
	"testing"
)

func webhookGraph(t *testing.T, queues ...string) *Graph {
	t.Helper()
	g := New()
	for i, q := range queues {
		n := &Node{
			ID:         NodeID("w" + string(rune('1'+i))),
			PluginName: "webhook",
			Config:     map[string]any{"queue": q},
		}
		if err := g.AddNode(n); err != nil {
			t.Fatal(err)
		}
	}
	return g
}

func TestWebhookQueues(t *testing.T) {
	if got := WebhookQueues(nil); got != nil {
		t.Errorf("nil graph gave %v, want nil", got)
	}
	g := webhookGraph(t, "movies", "favorites")
	got := WebhookQueues(g)
	if len(got) != 2 || got[0] != "movies" || got[1] != "favorites" {
		t.Errorf("WebhookQueues = %v, want [movies favorites] in node order", got)
	}
	// A webhook node with no queue contributes nothing rather than an empty
	// string, which would otherwise look like a real queue name downstream.
	bare := New()
	if err := bare.AddNode(&Node{ID: "w", PluginName: "webhook", Config: map[string]any{}}); err != nil {
		t.Fatal(err)
	}
	if got := WebhookQueues(bare); len(got) != 0 {
		t.Errorf("a queue-less webhook gave %v, want none", got)
	}
}

// More than one webhook source is legal — merge and route exist to combine
// sources — but it makes the queue non-derivable, so the validator says so at
// config load rather than leaving it to a 400 on the first push.
func TestWebhookWarningsOnlyWhenAmbiguous(t *testing.T) {
	if w := webhookWarnings(webhookGraph(t)); len(w) != 0 {
		t.Errorf("no webhook source should not warn, got %v", w)
	}
	if w := webhookWarnings(webhookGraph(t, "movies")); len(w) != 0 {
		t.Errorf("a single webhook source should not warn, got %v", w)
	}

	w := webhookWarnings(webhookGraph(t, "urgent", "bulk"))
	if len(w) != 1 {
		t.Fatalf("two webhook sources should warn once, got %v", w)
	}
	for _, want := range []string{"urgent", "bulk"} {
		if !strings.Contains(w[0].Error(), want) {
			t.Errorf("warning %q should name queue %q", w[0], want)
		}
	}
}
