package webhook

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/ingest"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func TestGenerateDrainsQueue(t *testing.T) {
	ingest.Enqueue("wq1", []ingest.Item{
		{Title: "Show S01E01", URL: "https://x/1", Fields: map[string]any{"indexer": "abc"}},
		{Title: "no url item"},
	})
	pl, err := newPlugin(map[string]any{"queue": "wq1"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	tc := &plugin.TaskContext{Name: "t", Logger: slog.Default()}
	out, err := pl.(*webhookPlugin).Generate(context.Background(), tc)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 entries, got %d", len(out))
	}
	if out[0].URL != "https://x/1" || out[0].Fields["indexer"] != "abc" {
		t.Errorf("first: %+v", out[0])
	}
	if out[1].URL == "" {
		t.Error("missing URL must get a synthetic one")
	}
	// Queue drained: next run is empty.
	out2, _ := pl.(*webhookPlugin).Generate(context.Background(), tc)
	if len(out2) != 0 {
		t.Fatalf("second run must be empty, got %d", len(out2))
	}
}

func TestValidate(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Error("missing queue must fail")
	}
	if errs := validate(map[string]any{"queue": "q"}); len(errs) != 0 {
		t.Errorf("valid: %v", errs)
	}
}

// TestDryRunPeeksWithoutConsuming: a dry run must exercise the queued items
// while leaving them for the next real run — dry-run is side-effect free.
func TestDryRunPeeksWithoutConsuming(t *testing.T) {
	q := "dryrun-peek"
	ingest.Enqueue(q, []ingest.Item{{Title: "Heat 1995"}})

	p, err := newPlugin(map[string]any{"queue": q}, nil)
	if err != nil {
		t.Fatal(err)
	}
	src := p.(*webhookPlugin)

	dry := &plugin.TaskContext{Name: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DryRun: true}
	out, err := src.Generate(context.Background(), dry)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Heat 1995" {
		t.Fatalf("dry run should see the queued item: %v", out)
	}
	if ingest.Len(q) != 1 {
		t.Fatalf("dry run consumed the queue: depth %d, want 1", ingest.Len(q))
	}

	real := &plugin.TaskContext{Name: "t", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	out, err = src.Generate(context.Background(), real)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("real run should drain the item: %v", out)
	}
	if ingest.Len(q) != 0 {
		t.Errorf("real run must drain: depth %d, want 0", ingest.Len(q))
	}
}
