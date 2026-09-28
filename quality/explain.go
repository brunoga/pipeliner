package quality

import (
	"fmt"
	"strings"
)

// DimensionResult is the evaluation of one quality dimension against a Spec.
// It is what turns a bare "does not match" into an actionable diagnosis: for
// each dimension the tester shows the constraint, the detected value, and
// whether that dimension passed, failed, or was bypassed.
type DimensionResult struct {
	// Name is the dimension: "resolution", "source", "codec", "audio",
	// "color_range", or "format_3d".
	Name string `json:"name"`
	// Constraint renders the spec's requirement for this dimension
	// ("720p-1080p", "webrip+", "1080p"), or "" when unconstrained.
	Constraint string `json:"constraint"`
	// Value is the detected value on the quality ("1080p", "webrip"), or
	// "unknown" when the release title carried nothing for this dimension.
	Value string `json:"value"`
	// Constrained reports whether the spec restricts this dimension at all.
	Constrained bool `json:"constrained"`
	// Passed reports whether the dimension satisfied the constraint. Always
	// true for unconstrained or bypassed dimensions.
	Passed bool `json:"passed"`
	// Bypassed reports that the dimension was optional in the spec and the
	// detected value was unknown, so its check was skipped.
	Bypassed bool `json:"bypassed"`
}

// SpecResult is the full diagnosis of one quality against a spec.
type SpecResult struct {
	// Quality is the detected quality string ("1080p webrip"), or "unknown".
	Quality string `json:"quality"`
	// Spec is the spec string as supplied.
	Spec string `json:"spec"`
	// Matched is true when every constrained dimension passed. Equal to
	// Spec.Matches(q); kept here so callers need not recompute it.
	Matched bool `json:"matched"`
	// Dimensions holds the per-dimension breakdown in display order.
	Dimensions []DimensionResult `json:"dimensions"`
}

// Explain evaluates q against s dimension-by-dimension, mirroring Matches
// exactly but reporting which dimensions passed, failed, or were bypassed.
// The returned Matched equals s.Matches(q).
func (s Spec) Explain(q Quality) SpecResult {
	dims := []struct {
		name          string
		min, max, val int
		zero          int // the unknown/none sentinel for this dimension
		opt           bool
		render        func(int) string
	}{
		{"resolution", int(s.MinResolution), int(s.MaxResolution), int(q.Resolution), int(ResolutionUnknown), s.OptResolution, func(v int) string { return resolutionNames[Resolution(v)] }},
		{"source", int(s.MinSource), int(s.MaxSource), int(q.Source), int(SourceUnknown), s.OptSource, func(v int) string { return sourceNames[Source(v)] }},
		{"codec", int(s.MinCodec), int(s.MaxCodec), int(q.Codec), int(CodecUnknown), s.OptCodec, func(v int) string { return codecNames[Codec(v)] }},
		{"audio", int(s.MinAudio), int(s.MaxAudio), int(q.Audio), int(AudioUnknown), s.OptAudio, func(v int) string { return audioNames[Audio(v)] }},
		{"color_range", int(s.MinColorRange), int(s.MaxColorRange), int(q.ColorRange), int(ColorRangeUnknown), s.OptColorRange, func(v int) string { return colorRangeNames[ColorRange(v)] }},
		{"format_3d", int(s.MinFormat3D), int(s.MaxFormat3D), int(q.Format3D), int(Format3DNone), s.OptFormat3D, func(v int) string { return format3DNames[Format3D(v)] }},
	}

	res := SpecResult{Quality: q.String(), Spec: s.String(), Matched: true}
	for _, d := range dims {
		dr := DimensionResult{
			Name:        d.name,
			Constrained: d.min > 0 || d.max > 0,
			Constraint:  renderConstraint(d.min, d.max, d.render),
			Value:       valueOrUnknown(d.render(d.val)),
			Passed:      true,
		}
		switch {
		case !dr.Constrained:
			// Unconstrained: always passes.
		case d.opt && d.val == d.zero:
			dr.Bypassed = true
		default:
			if d.min > 0 && d.val < d.min {
				dr.Passed = false
			}
			if d.max > 0 && d.val > d.max {
				dr.Passed = false
			}
		}
		if !dr.Passed {
			res.Matched = false
		}
		res.Dimensions = append(res.Dimensions, dr)
	}
	return res
}

// renderConstraint formats a dimension's min/max into a spec-like string:
// "720p" (min==max), "720p-1080p" (both, differing), "720p+" (min only),
// or "≤1080p" (max only). Returns "" when unconstrained.
func renderConstraint(min, max int, render func(int) string) string {
	switch {
	case min > 0 && max > 0 && min == max:
		return render(min)
	case min > 0 && max > 0:
		return render(min) + "-" + render(max)
	case min > 0:
		return render(min) + "+"
	case max > 0:
		return fmt.Sprintf("≤%s", render(max))
	default:
		return ""
	}
}

func valueOrUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// SpecString is the spec's own string form, used by SpecResult. It mirrors the
// canonical spec syntax closely enough for display.
func (s Spec) String() string {
	var parts []string
	for _, d := range []struct {
		min, max int
		render   func(int) string
		opt      bool
	}{
		{int(s.MinResolution), int(s.MaxResolution), func(v int) string { return resolutionNames[Resolution(v)] }, s.OptResolution},
		{int(s.MinSource), int(s.MaxSource), func(v int) string { return sourceNames[Source(v)] }, s.OptSource},
		{int(s.MinCodec), int(s.MaxCodec), func(v int) string { return codecNames[Codec(v)] }, s.OptCodec},
		{int(s.MinAudio), int(s.MaxAudio), func(v int) string { return audioNames[Audio(v)] }, s.OptAudio},
		{int(s.MinColorRange), int(s.MaxColorRange), func(v int) string { return colorRangeNames[ColorRange(v)] }, s.OptColorRange},
		{int(s.MinFormat3D), int(s.MaxFormat3D), func(v int) string { return format3DNames[Format3D(v)] }, s.OptFormat3D},
	} {
		c := renderConstraint(d.min, d.max, d.render)
		if c == "" {
			continue
		}
		if d.opt {
			c += "?"
		}
		parts = append(parts, c)
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, " ")
}
