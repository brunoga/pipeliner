package template

import (
	"bytes"
	"testing"
	"text/template"
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
