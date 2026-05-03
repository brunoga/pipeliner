package require

import (
	"context"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makePlugin(t *testing.T, cfg map[string]any) *requirePlugin {
	t.Helper()
	p, err := newPlugin(cfg)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*requirePlugin)
}

func filter(t *testing.T, p *requirePlugin, e *entry.Entry) {
	t.Helper()
	if err := p.Filter(context.Background(), &plugin.TaskContext{}, e); err != nil {
		t.Fatalf("Filter: %v", err)
	}
}

func TestRequirePresent(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": []any{"tvdb_id"}})
	e := entry.New("title", "url")
	e.Set("tvdb_id", 123)
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("entry with required field should not be rejected")
	}
}

func TestRequireMissing(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": []any{"tvdb_id"}})
	e := entry.New("title", "url")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry missing required field should be rejected")
	}
	if e.RejectReason != "missing required field: tvdb_id" {
		t.Errorf("unexpected reason: %q", e.RejectReason)
	}
}

func TestRequireEmptyString(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": "tvdb_network"})
	e := entry.New("title", "url")
	e.Set("tvdb_network", "")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("empty string field should be rejected")
	}
}

func TestRequireZeroInt(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": "count"})
	e := entry.New("title", "url")
	e.Set("count", 0)
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("zero int field should be rejected")
	}
}

func TestRequireZeroTime(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": "air_date"})
	e := entry.New("title", "url")
	e.Set("air_date", time.Time{})
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("zero time field should be rejected")
	}
}

func TestRequireMultipleFieldsFirstMissing(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": []any{"tvdb_id", "tvdb_network"}})
	e := entry.New("title", "url")
	e.Set("tvdb_network", "HBO")
	filter(t, p, e)
	if !e.IsRejected() {
		t.Error("entry missing first field should be rejected")
	}
}

func TestRequireMultipleFieldsAllPresent(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": []any{"tvdb_id", "tvdb_network"}})
	e := entry.New("title", "url")
	e.Set("tvdb_id", 123)
	e.Set("tvdb_network", "HBO")
	filter(t, p, e)
	if e.IsRejected() {
		t.Error("entry with all required fields should not be rejected")
	}
}

func TestRequireEmptyFieldsConfig(t *testing.T) {
	_, err := newPlugin(map[string]any{"fields": []any{}})
	if err == nil {
		t.Error("expected error for empty fields list")
	}
}

func TestRequireMissingFieldsKey(t *testing.T) {
	_, err := newPlugin(map[string]any{})
	if err == nil {
		t.Error("expected error when fields key is absent")
	}
}
