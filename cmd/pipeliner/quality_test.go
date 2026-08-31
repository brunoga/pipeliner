package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/quality"
)

func TestCmdQualityMatch(t *testing.T) {
	if code := cmdQuality([]string{"Show S01E01 1080p WEB h264", "720p-1080p webrip+"}); code != 0 {
		t.Errorf("expected exit 0 on match, got %d", code)
	}
}

func TestCmdQualityNoMatch(t *testing.T) {
	if code := cmdQuality([]string{"Show S01E01 720p WEB-DL x265", "1080p+"}); code != 1 {
		t.Errorf("expected exit 1 on no match, got %d", code)
	}
}

func TestCmdQualityNoSpec(t *testing.T) {
	// No spec → just parse; exit 0.
	if code := cmdQuality([]string{"Show S01E01 1080p WEB-DL"}); code != 0 {
		t.Errorf("expected exit 0 with no spec, got %d", code)
	}
}

func TestCmdQualityInvalidSpec(t *testing.T) {
	if code := cmdQuality([]string{"Show 1080p", "notaqualityvalue"}); code != 1 {
		t.Errorf("expected exit 1 on invalid spec, got %d", code)
	}
}

func TestCmdQualityNoArgs(t *testing.T) {
	if code := cmdQuality(nil); code != 1 {
		t.Errorf("expected exit 1 with no args, got %d", code)
	}
}

func TestPrintSpecResultOutput(t *testing.T) {
	spec, err := quality.ParseSpec("1080p+")
	if err != nil {
		t.Fatal(err)
	}
	res := spec.Explain(quality.Parse("Show S01E01 720p WEB-DL"))
	var buf bytes.Buffer
	printSpecResult(&buf, "Show S01E01 720p WEB-DL", res)
	out := buf.String()
	if !strings.Contains(out, "NO MATCH") {
		t.Errorf("expected NO MATCH:\n%s", out)
	}
	if !strings.Contains(out, "✗") {
		t.Errorf("expected a failing dimension marker:\n%s", out)
	}
	if !strings.Contains(out, "resolution") {
		t.Errorf("expected resolution dimension:\n%s", out)
	}
}
