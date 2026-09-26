package template

import (
	"bytes"
	"github.com/brunoga/pipeliner/internal/actionlink"
	"net/url"
	"strings"
	"testing"
	"text/template"
	"time"
)

func render(t *testing.T, expr string, data any) string {
	t.Helper()
	tmpl, err := template.New("t").Funcs(FuncMap()).Parse(expr)
	if err != nil {
		t.Fatalf("parse %q: %v", expr, err)
	}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		t.Fatalf("execute %q: %v", expr, err)
	}
	return buf.String()
}

func TestUpper(t *testing.T) {
	if got := render(t, `{{upper .}}`, "hello"); got != "HELLO" {
		t.Errorf("got %q", got)
	}
}

func TestLower(t *testing.T) {
	if got := render(t, `{{lower .}}`, "HELLO"); got != "hello" {
		t.Errorf("got %q", got)
	}
}

func TestTrimspace(t *testing.T) {
	if got := render(t, `{{trimspace .}}`, "  hi  "); got != "hi" {
		t.Errorf("got %q", got)
	}
}

func TestSliceBasic(t *testing.T) {
	if got := render(t, `{{slice 0 4 .}}`, "2024-01-15"); got != "2024" {
		t.Errorf("got %q, want 2024", got)
	}
}

func TestSlicePipe(t *testing.T) {
	// Primary use case: extract year from a YYYY-MM-DD date.
	if got := render(t, `{{.date | slice 0 4}}`, map[string]string{"date": "2010-09-17"}); got != "2010" {
		t.Errorf("got %q, want 2010", got)
	}
}

func TestSliceClampHigh(t *testing.T) {
	// to > len(s) — should clamp, not panic.
	if got := render(t, `{{slice 0 100 .}}`, "short"); got != "short" {
		t.Errorf("got %q", got)
	}
}

func TestSliceClampEmpty(t *testing.T) {
	if got := render(t, `{{slice 5 10 .}}`, "hi"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestSliceUnicode(t *testing.T) {
	// Slice should operate on runes, not bytes.
	if got := render(t, `{{slice 0 3 .}}`, "héllo"); got != "hél" {
		t.Errorf("got %q, want hél", got)
	}
}

func TestReplace(t *testing.T) {
	if got := render(t, `{{replace "." " " .}}`, "Breaking.Bad"); got != "Breaking Bad" {
		t.Errorf("got %q", got)
	}
}

func TestReplacePipe(t *testing.T) {
	if got := render(t, `{{. | replace "_" "-"}}`, "hello_world"); got != "hello-world" {
		t.Errorf("got %q", got)
	}
}

func TestDefaultUsed(t *testing.T) {
	if got := render(t, `{{default "fallback" .}}`, ""); got != "fallback" {
		t.Errorf("got %q, want fallback", got)
	}
}

func TestDefaultNotUsed(t *testing.T) {
	if got := render(t, `{{default "fallback" .}}`, "value"); got != "value" {
		t.Errorf("got %q, want value", got)
	}
}

func TestDefaultPipe(t *testing.T) {
	if got := render(t, `{{.x | default "none"}}`, map[string]string{"x": ""}); got != "none" {
		t.Errorf("got %q, want none", got)
	}
}

func TestDefaultInt(t *testing.T) {
	if got := render(t, `{{default "zero" .}}`, 0); got != "zero" {
		t.Errorf("got %q, want zero", got)
	}
}

func TestJoin(t *testing.T) {
	if got := render(t, `{{join ", " .}}`, []string{"drama", "crime"}); got != "drama, crime" {
		t.Errorf("got %q", got)
	}
}

// --- date/time functions ---

func TestNowNonZero(t *testing.T) {
	got := render(t, `{{now | formatdate "2006"}}`, nil)
	if got == "" || got == "0001" {
		t.Errorf("now returned zero time; got year %q", got)
	}
}

func TestDaysAgo(t *testing.T) {
	// daysago(7) should be roughly 7 days in the past.
	got := render(t, `{{daysago 7 | formatdate "2006-01-02"}}`, nil)
	if got == "" {
		t.Errorf("daysago returned empty string")
	}
	// Verify it's not today.
	today := render(t, `{{now | formatdate "2006-01-02"}}`, nil)
	if got == today {
		t.Errorf("daysago(7) returned today's date")
	}
}

func TestBeforeTrue(t *testing.T) {
	if got := render(t, `{{before (daysago 1) now}}`, nil); got != "true" {
		t.Errorf("yesterday should be before now; got %q", got)
	}
}

func TestBeforeFalse(t *testing.T) {
	if got := render(t, `{{before now (daysago 1)}}`, nil); got != "false" {
		t.Errorf("now should not be before yesterday; got %q", got)
	}
}

func TestAfterTrue(t *testing.T) {
	if got := render(t, `{{after now (daysago 1)}}`, nil); got != "true" {
		t.Errorf("now should be after yesterday; got %q", got)
	}
}

func TestAfterFalse(t *testing.T) {
	if got := render(t, `{{after (daysago 1) now}}`, nil); got != "false" {
		t.Errorf("yesterday should not be after now; got %q", got)
	}
}

func TestParsedateValid(t *testing.T) {
	if got := render(t, `{{parsedate "2024-06-15" | formatdate "2006-01-02"}}`, nil); got != "2024-06-15" {
		t.Errorf("got %q", got)
	}
}

func TestParsedateEmpty(t *testing.T) {
	// Empty string → zero time → year 0001 or similar; shouldn't panic.
	got := render(t, `{{parsedate "" | formatdate "2006"}}`, nil)
	if got == "2024" || got == "2025" || got == "2026" {
		t.Errorf("parsedate('') should return zero time, got year %q", got)
	}
}

func TestParsedateInvalid(t *testing.T) {
	// Invalid string → zero time; shouldn't panic.
	got := render(t, `{{parsedate "not-a-date" | formatdate "2006"}}`, nil)
	if got == "2024" || got == "2025" || got == "2026" {
		t.Errorf("parsedate(invalid) should return zero time, got year %q", got)
	}
}

func TestFormatdateRoundTrip(t *testing.T) {
	got := render(t, `{{parsedate "2023-03-21" | formatdate "01/02/2006"}}`, nil)
	if got != "03/21/2023" {
		t.Errorf("got %q, want 03/21/2023", got)
	}
}

// --- string predicates ---

func TestHasSuffixTrue(t *testing.T) {
	if got := render(t, `{{hasSuffix ".rar" .}}`, "archive.rar"); got != "true" {
		t.Errorf("got %q", got)
	}
}

func TestHasSuffixFalse(t *testing.T) {
	if got := render(t, `{{hasSuffix ".rar" .}}`, "archive.zip"); got != "false" {
		t.Errorf("got %q", got)
	}
}

func TestHasSuffixPipe(t *testing.T) {
	if got := render(t, `{{. | hasSuffix ".mkv"}}`, "movie.mkv"); got != "true" {
		t.Errorf("got %q", got)
	}
}

func TestHasPrefixTrue(t *testing.T) {
	if got := render(t, `{{hasPrefix "the." .}}`, "the.show"); got != "true" {
		t.Errorf("got %q", got)
	}
}

func TestHasPrefixFalse(t *testing.T) {
	if got := render(t, `{{hasPrefix "the." .}}`, "a.show"); got != "false" {
		t.Errorf("got %q", got)
	}
}

func TestContainsTrue(t *testing.T) {
	if got := render(t, `{{contains "HDTV" .}}`, "Show.S01E01.HDTV.x264"); got != "true" {
		t.Errorf("got %q", got)
	}
}

func TestContainsFalse(t *testing.T) {
	if got := render(t, `{{contains "BluRay" .}}`, "Show.S01E01.HDTV.x264"); got != "false" {
		t.Errorf("got %q", got)
	}
}

// --- scrub functions ---

func TestScrubReplacesInvalidChars(t *testing.T) {
	if got := render(t, `{{scrub .}}`, `file<name>here`); got != "file_name_here" {
		t.Errorf("got %q", got)
	}
}

func TestScrubwinReplacesColonAndDot(t *testing.T) {
	// "House: M.D." → colon replaced, trailing dot stripped on windows target.
	if got := render(t, `{{scrubwin .}}`, "House: M.D."); got != "House_ M.D" {
		t.Errorf("got %q", got)
	}
}

// signedaction is how a notification body embeds a one-click link. It must
// render nothing when links are unconfigured, so a template carrying one
// stays valid on installs without a public URL.
func TestSignedActionHelper(t *testing.T) {
	render := func(tmplStr string, data any) string {
		t.Helper()
		tmpl, err := template.New("x").Funcs(FuncMap()).Parse(tmplStr)
		if err != nil {
			t.Fatal(err)
		}
		var sb strings.Builder
		if err := tmpl.Execute(&sb, data); err != nil {
			t.Fatal(err)
		}
		return sb.String()
	}
	body := `{{signedaction "favorites" "fav-pipeline" "⭐ Follow" .Title "tvdb_id" .ID}}`
	data := map[string]any{"Title": "Some Show", "ID": 12345}

	actionlink.Configure("", "")
	if got := render(body, data); got != "" {
		t.Errorf("unconfigured should render nothing, got %q", got)
	}

	actionlink.Configure("https://p.example.com", "secret")
	defer actionlink.Configure("", "")
	got := render(body, data)
	if !strings.HasPrefix(got, "https://p.example.com/action?p=") {
		t.Fatalf("rendered link = %q", got)
	}
	u, err := url.Parse(got)
	if err != nil {
		t.Fatal(err)
	}
	p, err := actionlink.Decode(u.Query().Get("p"), u.Query().Get("sig"))
	if err != nil {
		t.Fatal(err)
	}
	// Non-string field values (an int tvdb_id straight from .Fields) must
	// survive as strings rather than breaking the link.
	if p.Title != "Some Show" || p.Fields["tvdb_id"] != "12345" || p.Label != "⭐ Follow" {
		t.Errorf("payload = %+v", p)
	}
}

func TestDurationFunc(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want string
	}{
		{"seconds int64", int64(45), "45s"},
		{"seconds int", 90, "1m 30s"},
		{"seed time days", int64(745200), "8d 15h"},
		{"float seconds", 3600.0, "1h"},
		{"duration value", 3*time.Hour + 20*time.Minute, "3h 20m"},
		{"duration string", "2h45m", "2h 45m"},
		{"zero", int64(0), "0s"},
		{"negative is absolute", int64(-120), "2m"},
		{"exact day drops empty units", int64(86400), "1d"},
		{"unparseable string", "not-a-duration", ""},
		{"unsupported type", []int{1}, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, `{{duration .}}`, tc.in)
			if got != tc.want {
				t.Errorf("duration(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestAgoFunc(t *testing.T) {
	cases := []struct {
		name string
		in   time.Time
		want string
	}{
		{"zero time", time.Time{}, "never"},
		{"seconds ago", time.Now().Add(-30 * time.Second), "just now"},
		{"future", time.Now().Add(time.Hour), "just now"},
		{"hours ago", time.Now().Add(-5 * time.Hour), "5h ago"},
		{"days ago", time.Now().Add(-49 * time.Hour), "2d 1h ago"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := render(t, `{{ago .}}`, tc.in)
			if got != tc.want {
				t.Errorf("ago(%v) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestNotificationFieldPatterns renders the field-access shapes notification
// bodies use over torrent_session fields. Parsing these is checked by
// `pipeliner check`; executing them is not, so a type mismatch between a
// helper and the value a plugin actually stores would only surface when mail
// is sent. The values here mirror what torrent_session.Generate writes:
// seed time as int64 seconds, progress/ratio as float64, timestamps as
// time.Time, and torrent_last_activity absent when the backend has no record.
func TestNotificationFieldPatterns(t *testing.T) {
	fields := map[string]any{
		"torrent_state":        "errored",
		"torrent_progress":     61.8,
		"torrent_ratio":        0.12,
		"torrent_seed_time":    int64(745200),
		"torrent_added_at":     time.Now().Add(-9 * 24 * time.Hour),
		"torrent_info_hash":    "c3efe106f580d6da62059eed0b9875ccb92a6eea",
		"torrent_download_dir": "/data/media/movies3d",
	}
	data := map[string]any{"Fields": fields}

	cases := []struct{ name, expr, want string }{
		{"progress", `{{printf "%.1f" (index .Fields "torrent_progress")}}%`, "61.8%"},
		{"bar width", `{{printf "%.0f" (index .Fields "torrent_progress")}}%`, "62%"},
		{"ratio", `{{printf "%.2f" (index .Fields "torrent_ratio")}}`, "0.12"},
		{"seed time", `{{duration (index .Fields "torrent_seed_time")}}`, "8d 15h"},
		{"added", `{{ago (index .Fields "torrent_added_at")}}`, "9d ago"},
		{"seed time skipped when zero", `{{with index .Fields "missing_seed"}}{{duration .}}{{end}}`, ""},
		{"absent field falls back", `{{with index .Fields "torrent_last_activity"}}{{ago .}}{{else}}never{{end}}`, "never"},
		{"state pill", `{{with index .Fields "torrent_state"}}{{upper .}}{{end}}`, "ERRORED"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := render(t, tc.expr, data); got != tc.want {
				t.Errorf("%s = %q, want %q", tc.expr, got, tc.want)
			}
		})
	}
}
