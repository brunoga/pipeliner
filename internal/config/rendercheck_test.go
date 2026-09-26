package config

import (
	"strings"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/store"

	_ "github.com/brunoga/pipeliner/plugins/processor/metainfo/file"
	_ "github.com/brunoga/pipeliner/plugins/source/torrent_session"
)

func scratchStore(t *testing.T) *store.SQLiteStore {
	t.Helper()
	db, err := ScratchStore()
	if err != nil {
		t.Fatalf("ScratchStore: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

// checkTemplatesFor returns the render failures — the ones callers treat
// differently from compile failures.
func checkTemplatesFor(t *testing.T, src string) []error {
	t.Helper()
	buildErrs, renderErrs := CheckTemplates(parseDAGOK(t, src), scratchStore(t))
	if len(buildErrs) > 0 {
		t.Fatalf("unexpected build errors: %s", errsText(buildErrs))
	}
	return renderErrs
}

// TestCheckTemplatesCatchesParseError covers the gap that motivated this:
// Validate never calls a plugin factory, so a malformed template passes it
// and only fails when the daemon builds tasks.
func TestCheckTemplatesCatchesParseError(t *testing.T) {
	src := `
SMTP = {"smtp_host": "localhost", "smtp_port": 25, "sender": "a@example.com", "to": "b@example.com"}
src = input("rss", url="https://example.com/feed")
output("notify", upstream=src, via="email", config=SMTP,
       body="{{range .Entries}}{{.Title}}{{end")
pipeline("p")
`
	cfg := parseDAGOK(t, src)
	if errs, _ := Validate(cfg); len(errs) != 0 {
		t.Fatalf("Validate should not see the broken template, got %v", errs)
	}

	buildErrs, renderErrs := CheckTemplates(cfg, scratchStore(t))
	if len(buildErrs) == 0 {
		t.Fatal("CheckTemplates accepted an unterminated action")
	}
	if !strings.Contains(buildErrs[0].Error(), "unclosed action") {
		t.Errorf("error should name the parse failure, got: %v", buildErrs[0])
	}
	// A template that will not compile cannot be rendered, so the failure
	// must be reported once, as a build error — callers grade the two
	// categories differently and a duplicate would be graded twice.
	if len(renderErrs) != 0 {
		t.Errorf("compile failure also reported as a render failure: %s", errsText(renderErrs))
	}
}

// TestCheckTemplatesCatchesScopeError is the bug this was built for: inside
// {{with}} the dot is rebound and $ is the top-level data, not the entry, so
// a nested {{index $.Fields ...}} parses fine and dies at send time.
func TestCheckTemplatesCatchesScopeError(t *testing.T) {
	src := `
SMTP = {"smtp_host": "localhost", "smtp_port": 25, "sender": "a@example.com", "to": "b@example.com"}
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
output("notify", upstream=meta, via="email", config=SMTP,
       body='{{range .Entries}}{{with index .Fields "video_year"}}{{index $.Fields "title"}}{{end}}{{end}}')
pipeline("p")
`
	errs := checkTemplatesFor(t, src)
	if len(errs) == 0 {
		t.Fatal("CheckTemplates accepted a template that dereferences $ inside {{with}}")
	}
	if !strings.Contains(errs[0].Error(), "index of untyped nil") {
		t.Errorf("error should name the nil deref, got: %v", errs[0])
	}
}

// TestCheckTemplatesCatchesUnguardedOptionalField exercises the second
// scenario, which is the whole reason there are two. video_year is
// MayProduce on metainfo_file — set only when the filename parses as a
// movie — so it is Reachable but not Certain. A template comparing it
// without a {{with}} guard renders fine on the optimistic entry and fails
// on the one the DAG actually guarantees, which is the entry that turns up
// whenever the parse misses.
func TestCheckTemplatesCatchesUnguardedOptionalField(t *testing.T) {
	src := `
SMTP = {"smtp_host": "localhost", "smtp_port": 25, "sender": "a@example.com", "to": "b@example.com"}
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
output("notify", upstream=meta, via="email", config=SMTP,
       body='{{range .Entries}}{{if gt (index .Fields "video_year") 2000}}recent{{end}}{{end}}')
pipeline("p")
`
	errs := checkTemplatesFor(t, src)
	if len(errs) == 0 {
		t.Fatal("CheckTemplates accepted an unguarded read of a MayProduce field")
	}
	joined := errsText(errs)
	if !strings.Contains(joined, "only guaranteed fields") {
		t.Errorf("failure should be attributed to the guaranteed-fields pass, got: %s", joined)
	}
	if strings.Contains(joined, "all available fields") {
		t.Errorf("the optimistic pass should have rendered fine, got: %s", joined)
	}
}

// TestCheckTemplatesAcceptsGuardedTemplate is the counterpart: the same
// field read through {{with}} must pass both scenarios, or the check would
// be crying wolf on correct configs.
func TestCheckTemplatesAcceptsGuardedTemplate(t *testing.T) {
	src := `
SMTP = {"smtp_host": "localhost", "smtp_port": 25, "sender": "a@example.com", "to": "b@example.com"}
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
output("notify", upstream=meta, via="email", config=SMTP,
       title="{{len .Entries}} new",
       body='{{range .Entries}}{{$e := .}}{{.Title}}{{with index .Fields "torrent_file_size"}} {{filesize .}}{{end}}{{with index .Fields "video_year"}} {{index $e.Fields "title"}} ({{.}}){{end}}{{end}}')
pipeline("p")
`
	if errs := checkTemplatesFor(t, src); len(errs) != 0 {
		t.Errorf("guarded template rejected: %s", errsText(errs))
	}
}

// TestCheckTemplatesRendersHelpersAgainstRealTypes guards the type fidelity
// of the synthetic entry. duration and filesize take numbers and ago takes a
// time.Time; if syntheticValue handed every field a string, these would fail
// and the check would be useless for exactly the mistakes it exists to find.
func TestCheckTemplatesRendersHelpersAgainstRealTypes(t *testing.T) {
	src := `
SMTP = {"smtp_host": "localhost", "smtp_port": 25, "sender": "a@example.com", "to": "b@example.com"}
src = input("torrent_session", backend="transmission", host="localhost")
output("notify", upstream=src, via="email", config=SMTP,
       body='{{range .Entries}}{{duration (index .Fields "torrent_seed_time")}} {{ago (index .Fields "torrent_added_at")}} {{filesize (index .Fields "torrent_downloaded")}} {{printf "%.1f" (index .Fields "torrent_progress")}}{{end}}')
pipeline("p")
`
	if errs := checkTemplatesFor(t, src); len(errs) != 0 {
		t.Errorf("helpers rejected typed synthetic fields: %s", errsText(errs))
	}
}

func TestSyntheticValueTypes(t *testing.T) {
	cases := []struct {
		field string
		want  string // %T of the value
	}{
		{entry.FieldTitle, "string"},
		{entry.FieldTorrentSeedTime, "int64"},
		{entry.FieldTorrentConnSeeds, "int"},
		{entry.FieldTorrentProgress, "float64"},
		{entry.FieldTorrentAddedAt, "time.Time"},
		{entry.FieldVideoGenres, "[]string"},
		{"not_a_known_field", "string"},
	}
	for _, tc := range cases {
		got := syntheticValue(tc.field)
		if typeName(got) != tc.want {
			t.Errorf("syntheticValue(%q) is %s, want %s", tc.field, typeName(got), tc.want)
		}
	}
	// _quality is typed, not a string: a template reaching into it must work.
	q, ok := syntheticValue(entry.FieldQuality).(quality.Quality)
	if !ok {
		t.Fatalf("_quality is %s, want quality.Quality", typeName(syntheticValue(entry.FieldQuality)))
	}
	if q.ResolutionName() != "1080p" {
		t.Errorf("_quality resolution = %q, want 1080p", q.ResolutionName())
	}
}

func TestSyntheticEntryIsAccepted(t *testing.T) {
	e := syntheticEntry([]string{entry.FieldTitle, entry.FieldTorrentAddedAt})
	if e.State != entry.Accepted {
		t.Errorf("state = %v, want accepted — notify only sees accepted entries", e.State)
	}
	if e.AcceptReason == "" {
		t.Error("AcceptReason is empty; templates commonly render it")
	}
	if _, ok := e.Fields[entry.FieldTorrentAddedAt].(time.Time); !ok {
		t.Error("requested field missing or mistyped")
	}
	if _, ok := e.Fields[entry.FieldVideoYear]; ok {
		t.Error("unrequested field present; the scenario split depends on absence")
	}
}

func typeName(v any) string {
	switch v.(type) {
	case string:
		return "string"
	case int:
		return "int"
	case int64:
		return "int64"
	case float64:
		return "float64"
	case bool:
		return "bool"
	case []string:
		return "[]string"
	case time.Time:
		return "time.Time"
	}
	return "other"
}

func errsText(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}
