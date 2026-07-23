package webhook

import (
	"context"
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
