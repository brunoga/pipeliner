package age

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func makeCtx() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.Default()}
}

// TestParseDuration covers valid and invalid duration strings.
func TestParseDuration(t *testing.T) {
	cases := []struct {
		input   string
		want    time.Duration
		wantErr bool
	}{
		{"7d", 168 * time.Hour, false},
		{"2w", 336 * time.Hour, false},
		{"24h", 24 * time.Hour, false},
		{"1d", 24 * time.Hour, false},
		{"1w", 168 * time.Hour, false},
		{"0d", 0, true},
		{"abc", 0, true},
		{"-1d", 0, true},
		{"0h", 0, true},
	}
	for _, tc := range cases {
		got, err := parseDuration(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("parseDuration(%q): expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseDuration(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("parseDuration(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

// TestEntryTime_TimeField verifies that a time.Time field value is used directly.
func TestEntryTime_TimeField(t *testing.T) {
	e := entry.New("test", "http://example.com")
	want := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	e.Set("published_date", want)
	got, ok := entryTime(e, "published_date")
	if !ok {
		t.Fatal("expected ok=true")
	}
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestEntryTime_StringField verifies RFC3339 string parsing.
func TestEntryTime_StringField(t *testing.T) {
	e := entry.New("test", "http://example.com")
	e.Set("published_date", "2024-01-15T12:00:00Z")
	got, ok := entryTime(e, "published_date")
	if !ok {
		t.Fatal("expected ok=true")
	}
	want := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// TestEntryTime_StringField_MultiFormat tests each uncommon date format.
func TestEntryTime_StringField_MultiFormat(t *testing.T) {
	cases := []struct {
		layout string
		input  string
	}{
		{time.RFC822, "15 Jan 24 12:00 UTC"},
		{time.RFC822Z, "15 Jan 24 12:00 +0000"},
		{time.RFC1123, "Mon, 15 Jan 2024 12:00:00 UTC"},
		{time.RFC1123Z, "Mon, 15 Jan 2024 12:00:00 +0000"},
		{"Mon, 02 Jan 2006 15:04:05 -0700", "Mon, 15 Jan 2024 12:00:00 +0000"},
		{"Mon, 02 Jan 2006 15:04:05 MST", "Mon, 15 Jan 2024 12:00:00 UTC"},
		{"2006-01-02T15:04:05", "2024-01-15T12:00:00"},
		{"2006-01-02", "2024-01-15"},
		{"02 Jan 2006", "15 Jan 2024"},
		{"January 2, 2006", "January 15, 2024"},
		{"Jan 2, 2006", "Jan 15, 2024"},
	}
	for _, tc := range cases {
		e := entry.New("test", "http://example.com")
		e.Set("published_date", tc.input)
		_, ok := entryTime(e, "published_date")
		if !ok {
			t.Errorf("format %q: entryTime returned ok=false for input %q", tc.layout, tc.input)
		}
	}
}

// TestEntryTime_Missing verifies that an absent field returns false.
func TestEntryTime_Missing(t *testing.T) {
	e := entry.New("test", "http://example.com")
	_, ok := entryTime(e, "published_date")
	if ok {
		t.Error("expected ok=false for missing field")
	}
}

// TestAgeFilter_NewerThan verifies that too-old entries are rejected and fresh
// entries pass.
func TestAgeFilter_NewerThan(t *testing.T) {
	p, err := newPlugin(map[string]any{"newer_than": "7d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proc := p.(plugin.ProcessorPlugin)
	ctx := context.Background()
	tc := makeCtx()

	// Entry published 30 days ago → should be rejected.
	old := entry.New("old", "http://example.com/old")
	old.Set("published_date", time.Now().Add(-30*24*time.Hour))
	proc.Process(ctx, tc, []*entry.Entry{old}) //nolint:errcheck
	if !old.IsRejected() {
		t.Error("expected 30d-old entry to be rejected with newer_than=7d")
	}

	// Entry published 1 hour ago → should pass.
	fresh := entry.New("fresh", "http://example.com/fresh")
	fresh.Set("published_date", time.Now().Add(-1*time.Hour))
	proc.Process(ctx, tc, []*entry.Entry{fresh}) //nolint:errcheck
	if fresh.IsRejected() {
		t.Errorf("expected fresh entry to pass: %s", fresh.RejectReason)
	}
}

// TestAgeFilter_OlderThan verifies that too-new entries are rejected and old
// entries pass.
func TestAgeFilter_OlderThan(t *testing.T) {
	p, err := newPlugin(map[string]any{"older_than": "30d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proc := p.(plugin.ProcessorPlugin)
	ctx := context.Background()
	tc := makeCtx()

	// Entry published 1 hour ago → too new, should be rejected.
	fresh := entry.New("fresh", "http://example.com/fresh")
	fresh.Set("published_date", time.Now().Add(-1*time.Hour))
	proc.Process(ctx, tc, []*entry.Entry{fresh}) //nolint:errcheck
	if !fresh.IsRejected() {
		t.Error("expected 1h-old entry to be rejected with older_than=30d")
	}

	// Entry published 60 days ago → passes.
	old := entry.New("old", "http://example.com/old")
	old.Set("published_date", time.Now().Add(-60*24*time.Hour))
	proc.Process(ctx, tc, []*entry.Entry{old}) //nolint:errcheck
	if old.IsRejected() {
		t.Errorf("expected 60d-old entry to pass: %s", old.RejectReason)
	}
}

// TestAgeFilter_OnMissing_Pass verifies that a missing field passes the entry
// through with the default on_missing=pass.
func TestAgeFilter_OnMissing_Pass(t *testing.T) {
	p, err := newPlugin(map[string]any{"newer_than": "7d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proc := p.(plugin.ProcessorPlugin)
	e := entry.New("nodate", "http://example.com/nodate")
	proc.Process(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck
	if e.IsRejected() {
		t.Errorf("expected missing-field entry to pass: %s", e.RejectReason)
	}
}

// TestAgeFilter_OnMissing_Reject verifies that on_missing=reject rejects entries
// with no date field.
func TestAgeFilter_OnMissing_Reject(t *testing.T) {
	p, err := newPlugin(map[string]any{"newer_than": "7d", "on_missing": "reject"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proc := p.(plugin.ProcessorPlugin)
	e := entry.New("nodate", "http://example.com/nodate")
	proc.Process(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck
	if !e.IsRejected() {
		t.Error("expected missing-field entry to be rejected with on_missing=reject")
	}
}

// TestAgeFilter_SkipsRejected verifies that already-rejected entries are not touched.
func TestAgeFilter_SkipsRejected(t *testing.T) {
	p, err := newPlugin(map[string]any{"newer_than": "7d"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	proc := p.(plugin.ProcessorPlugin)
	e := entry.New("pre-rejected", "http://example.com/r")
	e.Reject("pre-existing reason")
	proc.Process(context.Background(), makeCtx(), []*entry.Entry{e}) //nolint:errcheck
	if e.RejectReason != "pre-existing reason" {
		t.Errorf("reject reason was mutated: %s", e.RejectReason)
	}
}
