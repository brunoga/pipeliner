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
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*requirePlugin)
}

func filter(t *testing.T, p *requirePlugin, e *entry.Entry) {
	t.Helper()
	if _, err := p.Process(context.Background(), &plugin.TaskContext{}, []*entry.Entry{e}); err != nil {
		t.Fatalf("Process: %v", err)
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

// TestRequireURLChecksEntryStructField is the regression test for the
// user-reported bug: a config writer added "url" to require's fields
// expecting it to gate on Entry.URL, but require was inspecting only
// the Fields metadata bag — which nothing populates with "url" — so
// every entry was rejected. With Entry.Get's struct-field fallback,
// "url" now reads e.URL.
func TestRequireURLChecksEntryStructField(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": []any{"url"}})

	// Non-empty struct URL passes.
	good := entry.New("good", "http://example.com/1.torrent")
	filter(t, p, good)
	if good.IsRejected() {
		t.Errorf("entry with non-empty URL should pass, got rejected: %q", good.RejectReason)
	}

	// Empty struct URL is rejected with the expected reason.
	bad := entry.New("bad", "")
	filter(t, p, bad)
	if !bad.IsRejected() {
		t.Error("entry with empty URL should be rejected")
	}
	if bad.RejectReason != "missing required field: url" {
		t.Errorf("reject reason: got %q", bad.RejectReason)
	}
}

// TestRequireTitleChecksFieldsThenStruct confirms the Fields-wins
// rule: a metainfo plugin can rewrite Fields["title"] to the canonical
// name and require should see that; if no plugin set it, the raw
// struct e.Title still qualifies.
func TestRequireTitleChecksFieldsThenStruct(t *testing.T) {
	p := makePlugin(t, map[string]any{"fields": []any{"title"}})

	// Only struct title set — passes via fallback.
	a := entry.New("My.Show.S01E01", "http://x.com/1")
	filter(t, p, a)
	if a.IsRejected() {
		t.Errorf("entry with non-empty struct Title should pass via fallback, got %q", a.RejectReason)
	}

	// Fields override empty: even though struct e.Title was set,
	// Fields["title"] = "" wins and the entry is rejected.
	b := entry.New("My.Show.S01E01", "http://x.com/2")
	b.Set("title", "")
	filter(t, p, b)
	if !b.IsRejected() {
		t.Error("empty Fields[\"title\"] should override struct fallback and reject")
	}
}

func TestRequireEmptyFieldsConfig(t *testing.T) {
	_, err := newPlugin(map[string]any{"fields": []any{}}, nil)
	if err == nil {
		t.Error("expected error for empty fields list")
	}
}

func TestRequireMissingFieldsKey(t *testing.T) {
	_, err := newPlugin(map[string]any{}, nil)
	if err == nil {
		t.Error("expected error when fields key is absent")
	}
}
