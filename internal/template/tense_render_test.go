package template

import (
	"bytes"
	"testing"
	"text/template"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
)

// TestPremiereTenseRendering renders the exact tense fragments the premiere
// email uses. pipeliner check parses notification templates but never runs
// them, so a template whose text is simply untrue validates cleanly; these
// render it instead.
func TestPremiereTenseRendering(t *testing.T) {
	const label = `{{with index .Fields "series_episode_air_date"}}` +
		`{{if after . (now)}}Airs{{else}}Aired{{end}}{{end}}`
	const header = `{{$upcoming := false}}{{range .Entries}}` +
		`{{with index .Fields "series_episode_air_date"}}` +
		`{{if after . (now)}}{{$upcoming = true}}{{end}}{{end}}{{end}}` +
		`{{len .Entries}} premiere{{if ne (len .Entries) 1}}s{{end}}` +
		`{{if $upcoming}} airing soon{{else}} just aired{{end}}`

	future := time.Now().UTC().AddDate(0, 0, 2)
	past := time.Now().UTC().AddDate(0, 0, -5)

	mk := func(d time.Time) *entry.Entry {
		return &entry.Entry{Fields: map[string]any{"series_episode_air_date": d}}
	}
	render := func(text string, data any) string {
		tmpl, err := template.New("x").Funcs(FuncMap()).Parse(text)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		var b bytes.Buffer
		if err := tmpl.Execute(&b, data); err != nil {
			t.Fatalf("execute: %v", err)
		}
		return b.String()
	}

	if got := render(label, mk(future)); got != "Airs" {
		t.Errorf("future label = %q, want \"Airs\"", got)
	}
	if got := render(label, mk(past)); got != "Aired" {
		t.Errorf("past label = %q, want \"Aired\"", got)
	}

	type report struct{ Entries []*entry.Entry }
	cases := []struct {
		name    string
		entries []*entry.Entry
		want    string
	}{
		{"one upcoming", []*entry.Entry{mk(future)}, "1 premiere airing soon"},
		{"one aired", []*entry.Entry{mk(past)}, "1 premiere just aired"},
		{"two aired", []*entry.Entry{mk(past), mk(past)}, "2 premieres just aired"},
		{"mixed counts as upcoming", []*entry.Entry{mk(past), mk(future)}, "2 premieres airing soon"},
	}
	for _, c := range cases {
		if got := render(header, report{c.entries}); got != c.want {
			t.Errorf("%s: header = %q, want %q", c.name, got, c.want)
		}
	}
}
