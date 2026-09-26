package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

const validatorSMTP = `SMTP = {"smtp_host": "localhost", "smtp_port": 25, "sender": "a@example.com", "to": "b@example.com"}` + "\n"

// TestConfigValidatorCompileFailureIsAnError: a template that will not
// compile would be rejected by the reload anyway, so the web editor should
// say so at validation time rather than on save — or, when a run is in
// flight and the reload is queued, not at all.
func TestConfigValidatorCompileFailureIsAnError(t *testing.T) {
	src := validatorSMTP + `
src = input("rss", url="https://example.com/feed")
output("notify", upstream=src, via="email", config=SMTP,
       body="{{range .Entries}}{{.Title}}{{end")
pipeline("p")
`
	errs, warns := configValidator(quietLogger())([]byte(src))
	if !containsSubstr(errs, "unclosed action") {
		t.Errorf("compile failure should be an error, got errors=%v warnings=%v", errs, warns)
	}
}

// TestConfigValidatorRenderFailureIsAWarning: a render failure is nearly
// always a real defect, but it depends on the DAG's field model being
// complete — and this is the editor the config is written in, so a false
// positive must not be able to block a save.
func TestConfigValidatorRenderFailureIsAWarning(t *testing.T) {
	src := validatorSMTP + `
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
output("notify", upstream=meta, via="email", config=SMTP,
       body='{{range .Entries}}{{with index .Fields "video_year"}}{{index $.Fields "title"}}{{end}}{{end}}')
pipeline("p")
`
	errs, warns := configValidator(quietLogger())([]byte(src))
	if len(errs) != 0 {
		t.Errorf("render failure must not block a save, got errors=%v", errs)
	}
	if !containsSubstr(warns, "index of untyped nil") {
		t.Errorf("render failure should surface as a warning, got warnings=%v", warns)
	}
}

// TestConfigValidatorCleanConfigIsSilent guards against crying wolf: the
// validator runs on every press of the editor's Validate button.
func TestConfigValidatorCleanConfigIsSilent(t *testing.T) {
	src := validatorSMTP + `
src  = input("rss", url="https://example.com/feed")
meta = process("metainfo_file", upstream=src)
output("notify", upstream=meta, via="email", config=SMTP,
       title="{{len .Entries}} new",
       body='{{range .Entries}}{{$e := .}}{{.Title}}{{with index .Fields "video_year"}} ({{.}}) {{index $e.Fields "title"}}{{end}}{{end}}')
pipeline("p")
`
	errs, warns := configValidator(quietLogger())([]byte(src))
	if len(errs) != 0 || len(warns) != 0 {
		t.Errorf("clean config flagged: errors=%v warnings=%v", errs, warns)
	}
}

// TestConfigValidatorSkipsRenderingWhenStructurallyInvalid: building plugins
// on a graph that does not validate produces noise, not signal, so the
// structural errors must come back alone.
func TestConfigValidatorSkipsRenderingWhenStructurallyInvalid(t *testing.T) {
	// series needs episode fields nothing upstream produces, and the notify
	// body is separately broken — only the structural errors should return.
	src := validatorSMTP + `
src = input("rss", url="https://example.com/feed")
flt = process("series", upstream=src, static=["Foo"])
output("notify", upstream=flt, via="email", config=SMTP,
       body="{{range .Entries}}{{.Title}}{{end")
pipeline("p")
`
	errs, _ := configValidator(quietLogger())([]byte(src))
	if !containsSubstr(errs, "is not produced by any upstream node") {
		t.Fatalf("expected structural errors, got %v", errs)
	}
	if containsSubstr(errs, "unclosed action") {
		t.Errorf("template compilation should be skipped while structural errors stand, got %v", errs)
	}
	if containsSubstr(errs, "executing") {
		t.Errorf("rendering should be skipped while structural errors stand, got %v", errs)
	}
}

// TestConfigValidatorRejectsUnparseableConfig keeps the earliest layer wired.
func TestConfigValidatorRejectsUnparseableConfig(t *testing.T) {
	errs, _ := configValidator(quietLogger())([]byte("this is not starlark ==="))
	if len(errs) == 0 {
		t.Error("unparseable config accepted")
	}
}

func containsSubstr(haystack []string, needle string) bool {
	for _, h := range haystack {
		if strings.Contains(h, needle) {
			return true
		}
	}
	return false
}
