package quality

import "testing"

func mustSpec(t *testing.T, s string) Spec {
	t.Helper()
	spec, err := ParseSpec(s)
	if err != nil {
		t.Fatalf("ParseSpec(%q): %v", s, err)
	}
	return spec
}

func dim(res SpecResult, name string) DimensionResult {
	for _, d := range res.Dimensions {
		if d.Name == name {
			return d
		}
	}
	return DimensionResult{}
}

func TestExplainMatch(t *testing.T) {
	spec := mustSpec(t, "720p-1080p webrip+")
	q := Parse("Star Trek Strange New Worlds S04E05 1080p WEB h264 CAKES")
	res := spec.Explain(q)
	if !res.Matched {
		t.Fatalf("expected match, got dimensions %+v", res.Dimensions)
	}
	if res.Matched != spec.Matches(q) {
		t.Errorf("Explain.Matched (%v) disagrees with Matches (%v)", res.Matched, spec.Matches(q))
	}
	rd := dim(res, "resolution")
	if !rd.Constrained || !rd.Passed || rd.Value != "1080p" {
		t.Errorf("resolution dim = %+v", rd)
	}
}

func TestExplainResolutionTooLow(t *testing.T) {
	spec := mustSpec(t, "1080p+")
	q := Parse("Some Show S01E01 720p WEB-DL x265")
	res := spec.Explain(q)
	if res.Matched {
		t.Fatalf("expected no match (720p < 1080p)")
	}
	rd := dim(res, "resolution")
	if rd.Passed {
		t.Errorf("resolution should fail: %+v", rd)
	}
	if rd.Constraint != "1080p+" {
		t.Errorf("constraint = %q, want 1080p+", rd.Constraint)
	}
	if rd.Value != "720p" {
		t.Errorf("value = %q, want 720p", rd.Value)
	}
	// Non-resolution dimensions are unconstrained here, so they pass.
	if !dim(res, "source").Passed {
		t.Errorf("unconstrained source dim should pass")
	}
	if res.Matched != spec.Matches(q) {
		t.Errorf("Matched disagrees with Matches")
	}
}

func TestExplainOptionalBypassed(t *testing.T) {
	// Optional source: an unknown source bypasses the check.
	spec := mustSpec(t, "1080p webrip?")
	q := Parse("Movie 2024 1080p x264") // no source token → source unknown
	res := spec.Explain(q)
	sd := dim(res, "source")
	if !sd.Bypassed {
		t.Errorf("expected source bypassed, got %+v", sd)
	}
	if !sd.Passed {
		t.Errorf("bypassed dimension should still count as passed")
	}
	if !res.Matched {
		t.Errorf("expected overall match when the only failing dim is bypassed")
	}
}

func TestExplainUnconstrainedDimensions(t *testing.T) {
	spec := mustSpec(t, "1080p")
	q := Parse("Movie 2024 1080p WEB-DL")
	res := spec.Explain(q)
	// Every dimension present; codec/audio/etc unconstrained and passing.
	if len(res.Dimensions) != 6 {
		t.Fatalf("expected 6 dimensions, got %d", len(res.Dimensions))
	}
	cd := dim(res, "codec")
	if cd.Constrained {
		t.Errorf("codec should be unconstrained: %+v", cd)
	}
	if !cd.Passed {
		t.Errorf("unconstrained codec should pass")
	}
}

func TestExplainRangeConstraintRendering(t *testing.T) {
	spec := mustSpec(t, "720p-1080p")
	q := Parse("Show 2160p WEB")
	res := spec.Explain(q)
	rd := dim(res, "resolution")
	if rd.Constraint != "720p-1080p" {
		t.Errorf("constraint = %q, want 720p-1080p", rd.Constraint)
	}
	if rd.Passed {
		t.Errorf("2160p exceeds 1080p max, should fail")
	}
}

func TestExplainUnknownQualityValue(t *testing.T) {
	spec := mustSpec(t, "1080p")
	q := Parse("Just A Plain Title With No Quality Tokens")
	res := spec.Explain(q)
	rd := dim(res, "resolution")
	if rd.Value != "unknown" {
		t.Errorf("value = %q, want unknown", rd.Value)
	}
	if rd.Passed {
		t.Errorf("unknown resolution vs required 1080p should fail (not optional)")
	}
}

// TestExplainAgreesWithMatches pins the promise Explain makes in its doc:
// Matched equals s.Matches(q). Source, Audio and Format3D carry values that
// were appended after their ladders were set -- Format3DUnspecified ranks
// with Half, DD+ Atmos below TrueHD -- so their stored integers are not their
// order. Matches compares ranks; Explain compared the integers, so a spec of
// exactly "3d" passed a bare-3D release in Matches and failed it in Explain.
func TestExplainAgreesWithMatches(t *testing.T) {
	var sources []Source
	for v := range sourceRank {
		sources = append(sources, v)
	}
	var audios []Audio
	for v := range audioRank {
		audios = append(audios, v)
	}
	var formats []Format3D
	for v := range format3DRank {
		formats = append(formats, v)
	}
	check := func(s Spec, q Quality) {
		t.Helper()
		if got, want := s.Explain(q).Matched, s.Matches(q); got != want {
			t.Errorf("spec %q, quality %+v: Explain.Matched=%v, Matches=%v", s.String(), q, got, want)
		}
	}
	for _, a := range sources {
		for _, b := range sources {
			for _, v := range sources {
				check(Spec{MinSource: a, MaxSource: b}, Quality{Source: v})
			}
		}
	}
	for _, a := range audios {
		for _, b := range audios {
			for _, v := range audios {
				check(Spec{MinAudio: a, MaxAudio: b}, Quality{Audio: v})
			}
		}
	}
	for _, a := range formats {
		for _, b := range formats {
			for _, v := range formats {
				check(Spec{MinFormat3D: a, MaxFormat3D: b}, Quality{Format3D: v})
			}
		}
	}
}
