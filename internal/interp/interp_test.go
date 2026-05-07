package interp

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func TestToGoTemplate(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"{title}", "{{.title}}"},
		{"{title:02d}", `{{printf "%02d" .title}}`},
		{"/media/{series_name}/Season {series_season:02d}", "/media/{{.series_name}}/Season {{printf \"%02d\" .series_season}}"},
		{"no braces", "no braces"},
		{"{}", "{}"},
		{"{123abc}", "{123abc}"},
		{"{ space }", "{ space }"},
		// Passthrough when {{ is present.
		{"{{.Title}}", "{{.Title}}"},
		{"prefix {{.Title}} suffix", "prefix {{.Title}} suffix"},
		// Literal { with no matching }.
		{"{unclosed", "{unclosed"},
		// Mixed literal and field.
		{"prefix {field} suffix", "prefix {{.field}} suffix"},
	}
	for _, tt := range tests {
		got := toGoTemplate(tt.in)
		if got != tt.want {
			t.Errorf("toGoTemplate(%q) = %q; want %q", tt.in, got, tt.want)
		}
	}
}

func TestCompileAndRender(t *testing.T) {
	tests := []struct {
		pattern string
		data    map[string]any
		want    string
	}{
		{"{title}", map[string]any{"title": "Hello"}, "Hello"},
		{"{season:02d}", map[string]any{"season": 7}, "07"},
		{"/media/{name}/Season {num:02d}", map[string]any{"name": "Foo", "num": 3}, "/media/Foo/Season 03"},
		// Backward compat: Go template passthrough.
		{"{{.Title}}", map[string]any{"Title": "Old"}, "Old"},
		// Missing field renders as empty string (Go template default).
		{"{missing}", map[string]any{}, "<no value>"},
	}
	for _, tt := range tests {
		ip, err := Compile(tt.pattern)
		if err != nil {
			t.Errorf("Compile(%q): unexpected error: %v", tt.pattern, err)
			continue
		}
		got, err := ip.Render(tt.data)
		if err != nil {
			t.Errorf("Render(%q): unexpected error: %v", tt.pattern, err)
			continue
		}
		if got != tt.want {
			t.Errorf("Render(%q) = %q; want %q", tt.pattern, got, tt.want)
		}
	}
}

func TestCompileInvalidTemplate(t *testing.T) {
	_, err := Compile("{{.bad syntax")
	if err == nil {
		t.Error("expected error for invalid template, got nil")
	}
}

func TestEntryData(t *testing.T) {
	e := &entry.Entry{
		Title:       "My Title",
		URL:         "http://example.com",
		OriginalURL: "http://original.com",
		Task:        "my-task",
	}
	e.Fields = map[string]any{"custom": "value"}

	m := EntryData(e)

	checks := map[string]string{
		"Title":        "My Title",
		"raw_title":    "My Title", // raw entry title; "title" is the enriched canonical title from metainfo
		"URL":          "http://example.com",
		"url":          "http://example.com",
		"OriginalURL":  "http://original.com",
		"original_url": "http://original.com",
		"Task":         "my-task",
		"task":         "my-task",
	}
	for key, want := range checks {
		if got, _ := m[key].(string); got != want {
			t.Errorf("EntryData[%q] = %q; want %q", key, got, want)
		}
	}
	if m["custom"] != "value" {
		t.Errorf("EntryData[custom] = %v; want %q", m["custom"], "value")
	}
}

func TestEntryDataWithState(t *testing.T) {
	e := &entry.Entry{
		Title:        "T",
		RejectReason: "too slow",
	}
	m := EntryDataWithState(e)
	if m["state"] == nil {
		t.Error("EntryDataWithState: missing 'state' key")
	}
	if m["State"] == nil {
		t.Error("EntryDataWithState: missing 'State' key")
	}
	if m["reject_reason"] != "too slow" {
		t.Errorf("EntryDataWithState[reject_reason] = %v; want %q", m["reject_reason"], "too slow")
	}
}
