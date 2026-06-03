package quality

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/quality"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func entryAt(title string, q quality.Quality) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	e.SetQuality(q)
	return e
}

func runFilter(t *testing.T, cfg map[string]any, entries ...*entry.Entry) {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.(*qualityPlugin).Process(context.Background(), tc(), entries); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRequiresSpec(t *testing.T) {
	if errs := validate(map[string]any{}); len(errs) == 0 {
		t.Fatal("want error when spec is missing")
	}
	if errs := validate(map[string]any{"spec": "bogus"}); len(errs) == 0 {
		t.Fatal("want error for malformed spec")
	}
	if errs := validate(map[string]any{"spec": "720p+"}); len(errs) != 0 {
		t.Errorf("valid spec should validate, got %v", errs)
	}
}

func TestValidateUnknownKey(t *testing.T) {
	if errs := validate(map[string]any{"spec": "720p+", "wat": 1}); len(errs) == 0 {
		t.Fatal("want unknown-key error")
	}
}

func TestRejectsBelowFloor(t *testing.T) {
	low := entryAt("low", quality.Parse("Movie 480p WEB-DL"))
	hi := entryAt("hi", quality.Parse("Movie 1080p WEB-DL"))
	runFilter(t, map[string]any{"spec": "720p+"}, low, hi)
	if !low.IsRejected() {
		t.Error("480p should be rejected against 720p+")
	}
	if hi.IsRejected() {
		t.Error("1080p should pass 720p+")
	}
}

func TestRejectsOutsideRange(t *testing.T) {
	in := entryAt("in", quality.Parse("Movie 1080p WEB-DL"))
	too := entryAt("too", quality.Parse("Movie 2160p WEB-DL"))
	runFilter(t, map[string]any{"spec": "720p-1080p"}, in, too)
	if in.IsRejected() {
		t.Error("1080p should pass 720p-1080p")
	}
	if !too.IsRejected() {
		t.Error("2160p should fail 720p-1080p")
	}
}

func TestMissingQualityDefaultPasses(t *testing.T) {
	bare := entry.New("bare", "http://example.com/bare")
	runFilter(t, map[string]any{"spec": "720p+"}, bare)
	if bare.IsRejected() {
		t.Error("missing _quality should pass by default")
	}
}

func TestMissingQualityRejectMode(t *testing.T) {
	bare := entry.New("bare", "http://example.com/bare")
	runFilter(t, map[string]any{"spec": "720p+", "on_missing": "reject"}, bare)
	if !bare.IsRejected() {
		t.Error("missing _quality should reject when on_missing=reject")
	}
}

func TestAlreadyRejectedEntryIgnored(t *testing.T) {
	e := entryAt("rej", quality.Parse("Movie 1080p"))
	e.Reject("upstream")
	runFilter(t, map[string]any{"spec": "720p+"}, e)
	if e.RejectReason != "upstream" {
		t.Errorf("reject reason changed: %q", e.RejectReason)
	}
}
