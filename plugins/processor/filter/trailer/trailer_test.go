package trailer

import (
	"context"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func run(t *testing.T, mode string, titles ...string) []*entry.Entry {
	t.Helper()
	p, err := newPlugin(map[string]any{"mode": mode}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	entries := make([]*entry.Entry, len(titles))
	for i, title := range titles {
		entries[i] = entry.New(title, "http://example.com/"+title)
	}
	out, err := p.(*trailerPlugin).Process(context.Background(), nil, entries)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	_ = out
	return entries
}

func TestRejectMode_DropsTrailers(t *testing.T) {
	cases := []string{
		"Avatar: Fire and Ash (2025) IMAX 3D Full SBS Trailer 1",
		"Avatar: The Way of Water 2022 Trailer 1080p 3D Half SBS DD+5 1 x264 LR0EZ",
		"Some.Movie.2024.Official.Teaser.1080p.WEB-DL",
		"Inception 2010 Behind The Scenes 1080p",
		"Some.Show.S01E01.Featurette.720p",
		"Movie.2024.Sneak.Peek.1080p",
		"Movie.2023.BTS.Reel.720p",
	}
	for _, title := range cases {
		got := run(t, "reject", title)
		if !got[0].IsRejected() {
			t.Errorf("reject mode: %q should be rejected, state=%v", title, got[0].State)
		}
	}
}

func TestRejectMode_KeepsNonTrailers(t *testing.T) {
	cases := []string{
		"Inception.2010.1080p.BluRay.x264",
		"Avatar.2009.FSBS.1080p.BluRay",
		// Words that should NOT trigger false positives.
		"The.Greatest.Showman.2017.1080p.BluRay",
		"Movie.Featuring.Star.2024.1080p", // "Featuring" != "Featurette"
	}
	for _, title := range cases {
		got := run(t, "reject", title)
		if got[0].IsRejected() {
			t.Errorf("reject mode: %q must not be rejected (state=%v, reason=%q)",
				title, got[0].State, got[0].RejectReason)
		}
	}
}

func TestAcceptMode_KeepsOnlyTrailers(t *testing.T) {
	got := run(t, "accept",
		"Avatar: Fire and Ash (2025) IMAX 3D Full SBS Trailer 1",
		"Inception.2010.1080p.BluRay.x264",
	)
	if !got[0].IsAccepted() {
		t.Errorf("accept mode: trailer entry should be accepted, state=%v", got[0].State)
	}
	if !got[1].IsRejected() {
		t.Errorf("accept mode: non-trailer entry should be rejected, state=%v", got[1].State)
	}
}

func TestDefaultModeIsReject(t *testing.T) {
	p, err := newPlugin(map[string]any{}, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	if p.(*trailerPlugin).mode != modeReject {
		t.Errorf("default mode: got %q, want %q", p.(*trailerPlugin).mode, modeReject)
	}
}

func TestValidateRejectsUnknownMode(t *testing.T) {
	errs := validate(map[string]any{"mode": "delete"})
	if len(errs) == 0 {
		t.Error("validate must reject unknown mode value")
	}
}

func TestValidateRejectsUnknownKey(t *testing.T) {
	errs := validate(map[string]any{"mode": "reject", "extra": "x"})
	if len(errs) == 0 {
		t.Error("validate must reject unknown config keys")
	}
}

func TestIsRegistered(t *testing.T) {
	d, ok := plugin.Lookup("trailer")
	if !ok {
		t.Fatal("trailer plugin is not registered")
	}
	if d.Role != plugin.RoleProcessor {
		t.Errorf("expected role %v, got %v", plugin.RoleProcessor, d.Role)
	}
}
