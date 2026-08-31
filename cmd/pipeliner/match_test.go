package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/match"
)

func TestCmdMatchInlineMatch(t *testing.T) {
	code := cmdMatch([]string{"Star Trek Strange New Worlds", "Silo", "Star Trek: Strange New Worlds"})
	if code != 0 {
		t.Errorf("expected exit 0 on match, got %d", code)
	}
}

func TestCmdMatchInlineNoMatch(t *testing.T) {
	code := cmdMatch([]string{"Breaking Bad", "The Wire", "Better Call Saul"})
	if code != 1 {
		t.Errorf("expected exit 1 on no match, got %d", code)
	}
}

func TestCmdMatchNoArgs(t *testing.T) {
	if code := cmdMatch(nil); code != 1 {
		t.Errorf("expected exit 1 with no args, got %d", code)
	}
}

func TestCmdMatchListAndPositionalMutuallyExclusive(t *testing.T) {
	code := cmdMatch([]string{"--list", "cache_series_list", "Title", "Extra Candidate"})
	if code != 1 {
		t.Errorf("expected exit 1 when both --list and positional candidates given, got %d", code)
	}
}

func TestPrintProbeOutput(t *testing.T) {
	res := match.Probe("Star Trek Strange New Worlds", 0,
		[]match.TitleEntry{
			match.NewTitleEntry("Star Trek: Strange New Worlds", 0),
			match.NewTitleEntry("Silo", 0),
		})
	var buf bytes.Buffer
	printProbe(&buf, res)
	out := buf.String()
	if !strings.Contains(out, "MATCH") {
		t.Errorf("output missing verdict:\n%s", out)
	}
	if !strings.Contains(out, "star trek strange new worlds") {
		t.Errorf("output missing normalized candidate:\n%s", out)
	}
	if !strings.Contains(out, "✓") {
		t.Errorf("output missing match marker:\n%s", out)
	}
}

func TestPrintProbeNoMatchShowsNote(t *testing.T) {
	res := match.Probe("Dune", 2021, []match.TitleEntry{{Norm: "dune", Year: 1984}})
	var buf bytes.Buffer
	printProbe(&buf, res)
	out := buf.String()
	if !strings.Contains(out, "NO MATCH") {
		t.Errorf("expected NO MATCH:\n%s", out)
	}
	if !strings.Contains(out, "year mismatch") {
		t.Errorf("expected year mismatch note:\n%s", out)
	}
}
