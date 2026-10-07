package quality

import (
	"encoding/json"
	"strings"
	"testing"
)

// --- Parse tests ---

func TestParseKnownTitles(t *testing.T) {
	cases := []struct {
		title string
		want  Quality
	}{
		{
			"Movie.2023.1080p.BluRay.x264-GROUP",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH264},
		},
		{
			"Show.S01E01.720p.HDTV.AAC",
			Quality{Resolution: Resolutionp720, Source: SourceHDTV, Audio: AudioAAC},
		},
		{
			"Film.2160p.UHD.BluRay.HDR.DTS-HD",
			Quality{Resolution: Resolutionp2160, Source: SourceBluRay, Audio: AudioDTS, ColorRange: ColorRangeHDR},
		},
		{
			"Show.S02E05.720p.WEB-DL.DD5.1.H.264",
			Quality{Resolution: Resolutionp720, Source: SourceWebDL, Codec: CodecH264, Audio: AudioDolbyDigital},
		},
		{
			"Movie.2022.4K.REMUX.HEVC.TrueHD.Atmos",
			Quality{Resolution: Resolutionp2160, Source: SourceRemux, Codec: CodecH265, Audio: AudioAtmos},
		},
		{
			"Show.S01E01.576p.HDTV.XviD",
			Quality{Resolution: Resolutionp576, Source: SourceHDTV, Codec: CodecXviD},
		},
		{
			"Movie.DVDRip.DivX.MP3",
			Quality{Source: SourceDVDRip, Codec: CodecDivX, Audio: AudioMP3},
		},
		{
			"Show.S03E01.1080p.WEBRip.x265.HDR10",
			Quality{Resolution: Resolutionp1080, Source: SourceWEBRip, Codec: CodecH265, ColorRange: ColorRangeHDR10},
		},
		{
			"Movie.2023.2160p.BluRay.DV.TrueHD",
			Quality{Resolution: Resolutionp2160, Source: SourceBluRay, Audio: AudioTrueHD, ColorRange: ColorRangeDolbyVision},
		},
		{
			// "DoVi" is a common Dolby Vision abbreviation used by playWEB and similar groups.
			"The.Boys.S05E08.2160p.AMZN.WEB-DL.DD.5.1.Atmos.DoVi.H.265-playWEB",
			Quality{Resolution: Resolutionp2160, Source: SourceWebDL, Codec: CodecH265, Audio: AudioAtmos, ColorRange: ColorRangeDolbyVision},
		},
		{
			// Scene releases often use "H 265" (space) instead of "H.265" (dot).
			"The Boys S05E08 2160p AMZN WEB-DL DDP5 1 Atmos DV H 265-FLUX",
			// DDP5 1 Atmos is the lossy DD+ carrier, not lossless TrueHD Atmos.
			Quality{Resolution: Resolutionp2160, Source: SourceWebDL, Codec: CodecH265, Audio: AudioDDPlusAtmos, ColorRange: ColorRangeDolbyVision},
		},
		{
			"No.Quality.Markers.At.All",
			Quality{},
		},
		{
			// A bare "3D" says nothing about layout, so it is Unspecified
			// rather than Half. The HSBS case below is the one that states it.
			"Avatar.2009.3D.1080p.BluRay.x264",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH264, Format3D: Format3DUnspecified},
		},
		{
			"Avatar.2009.HSBS.1080p.BluRay",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DHalf},
		},
		{
			// Bare SBS is half-resolution by scene convention: a "1080p SBS"
			// release cannot physically be full-SBS (which would be 3840×1080).
			"Avatar.2009.SBS.1080p.BluRay",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DHalf},
		},
		{
			"Avatar.2009.FSBS.1080p.BluRay",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DFull},
		},
		{
			"Avatar.2009.BD3D.1080p.BluRay",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DBD},
		},
		{
			// MVC = Multiview Video Coding, the Blu-ray 3D codec; no resolution tag → default 1080p
			"Everything.Everywhere.All.At.Once.BD50.MVC",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DBD},
		},
		{
			// BD50 = full Blu-ray disc rip, treated as BluRay source
			"Despicable.Me.3.BD50.Bluray",
			Quality{Source: SourceBluRay},
		},
	}

	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			got := Parse(tc.title)
			if got != tc.want {
				t.Errorf("Parse(%q)\n  got  %+v\n  want %+v", tc.title, got, tc.want)
			}
		})
	}
}

func TestParseSourceVariants(t *testing.T) {
	cases := []struct {
		title string
		want  Source
	}{
		{"Movie.BluRay.x264", SourceBluRay},
		{"Movie.Blu-Ray.x264", SourceBluRay},
		{"Movie.BDRip.x264", SourceBluRay},
		{"Movie.BDRemux.x264", SourceRemux},
		{"Movie.Remux.x264", SourceRemux},
		{"Movie.WEB-DL.x264", SourceWebDL},
		{"Movie.WEBDL.x264", SourceWebDL},
		{"Movie.WEBRip.x264", SourceWEBRip},
		// Bare "WEB" token (scene shorthand for WEB-DL).
		{"For All Mankind S01E03 720p WEB x265 MiNX EZTV", SourceWebDL},
		{"Show.S01E01.1080p.WEB.x264-GROUP", SourceWebDL},
		// Bare "WEB" must NOT trigger when it's a substring inside another word.
		{"The.Spider.Cobweb.2020.1080p.x264", SourceUnknown},
		{"Movie.HDTV.x264", SourceHDTV},
		{"Movie.DVDRip.XviD", SourceDVDRip},
		{"Movie.TVRip.XviD", SourceTVRip},
		{"Movie.CAM.x264", SourceCAM},
		{"Movie.HDCAM.x264", SourceCAM},
		{"Movie.CAMRip.x264", SourceCAM},
		{"Movie.HDTS.x264", SourceTS},
		{"Movie.TELESYNC.x264", SourceTS},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			if got := Parse(tc.title).Source; got != tc.want {
				t.Errorf("source: got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParseSourceMultipleTokens is the regression guard for CAM releases that
// mislabel themselves with a higher tier: a theatrical/pre-release marker must
// dominate any co-occurring WEB/BluRay tag, while legitimate hierarchical
// combos (BluRay Remux) still resolve to the higher tier.
func TestParseSourceMultipleTokens(t *testing.T) {
	cases := []struct {
		title string
		want  Source
	}{
		// CAM/TS marker dominates a co-occurring higher tag, regardless of order.
		{"Movie.2024.1080p.WEB.CAM.x264", SourceCAM},
		{"Movie 2024 WEBRip CAM x264", SourceCAM},
		{"Movie 2024 CAM WEB-DL x264", SourceCAM},
		{"Movie 2024 HDTS 1080p WEB x264", SourceTS},
		{"Movie 2024 BluRay HDCAM x264", SourceCAM},
		{"Movie 2024 SCREENER WEB x264", SourceSCR},
		// No low-tier marker: the best legitimate token wins.
		{"Movie 2024 1080p BluRay Remux", SourceRemux},
		{"Movie 2024 WEB-DL BluRay", SourceBluRay},
		// Lowest of multiple theatrical markers wins.
		{"Movie 2024 TS CAM x264", SourceCAM},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			if got := Parse(tc.title).Source; got != tc.want {
				t.Errorf("source: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseCodecVariants(t *testing.T) {
	cases := []struct {
		title string
		want  Codec
	}{
		{"Movie.x265", CodecH265},
		{"Movie.H.265", CodecH265},
		{"Movie.H265", CodecH265},
		{"Movie H 265-Group", CodecH265}, // space separator (common in scene titles)
		{"Movie H 264-Group", CodecH264}, // space separator
		{"Movie.HEVC", CodecH265},
		{"Movie.x264", CodecH264},
		{"Movie.H.264", CodecH264},
		{"Movie.XviD", CodecXviD},
		{"Movie.DivX", CodecDivX},
		{"Movie.AV1", CodecAV1},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			if got := Parse(tc.title).Codec; got != tc.want {
				t.Errorf("codec: got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestParseAudioVariants(t *testing.T) {
	cases := []struct {
		title string
		want  Audio
	}{
		{"Movie.Atmos", AudioAtmos},
		{"Movie.TrueHD", AudioTrueHD},
		{"Movie.DTS-HD", AudioDTS},
		{"Movie.DTS-MA", AudioDTS},
		{"Movie.DTS MA", AudioDTS},
		{"Movie.DTS", AudioDTS},
		{"Movie.DD5.1", AudioDolbyDigital},
		{"Movie.DD+5.1", AudioDolbyDigital},
		{"Movie.DDP5.1", AudioDolbyDigital},       // Dolby Digital Plus with P notation
		{"Movie DDP5 1-Group", AudioDolbyDigital}, // space-separated (scene format)
		{"Movie.DD5 1-Group", AudioDolbyDigital},  // dot replaced by space
		{"Movie.Dolby.Digital", AudioDolbyDigital},
		{"Movie.AAC", AudioAAC},
		{"Movie.MP3", AudioMP3},
	}
	for _, tc := range cases {
		t.Run(tc.title, func(t *testing.T) {
			if got := Parse(tc.title).Audio; got != tc.want {
				t.Errorf("audio: got %v, want %v", got, tc.want)
			}
		})
	}
}

// --- String ---

func TestQualityString(t *testing.T) {
	q := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH264, Audio: AudioDTS, ColorRange: ColorRangeHDR}
	s := q.String()
	if s == "" || s == "unknown" {
		t.Errorf("unexpected String(): %q", s)
	}
}

func TestQualityStringUnknown(t *testing.T) {
	if (Quality{}).String() != "unknown" {
		t.Error("empty quality should return 'unknown'")
	}
}

// --- Better ---

func TestBetterResolutionWins(t *testing.T) {
	hi := Quality{Resolution: Resolutionp2160}
	lo := Quality{Resolution: Resolutionp1080}
	if !hi.Better(lo) {
		t.Error("2160p should be better than 1080p")
	}
	if lo.Better(hi) {
		t.Error("1080p should not be better than 2160p")
	}
}

func TestBetterSourceBreaksTie(t *testing.T) {
	a := Quality{Resolution: Resolutionp1080, Source: SourceBluRay}
	b := Quality{Resolution: Resolutionp1080, Source: SourceHDTV}
	if !a.Better(b) {
		t.Error("BluRay should beat HDTV at same resolution")
	}
}

func TestBetterCodecBreaksTie(t *testing.T) {
	a := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH265}
	b := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH264}
	if !a.Better(b) {
		t.Error("H265 should beat H264 at same resolution+source")
	}
}

func TestBetterEqualReturnsFalse(t *testing.T) {
	q := Quality{Resolution: Resolutionp720, Source: SourceHDTV, Codec: CodecH264, Audio: AudioAAC, ColorRange: ColorRangeSDR}
	if q.Better(q) {
		t.Error("quality should not be better than itself")
	}
}

func TestParse3DConv(t *testing.T) {
	cases := []struct {
		title string
	}{
		{"Avatar.2009.3DCONV.1080p.BluRay"},
		{"The.Lion.King.2019.3D-CONV.1080p.BluRay"},
		// 3DCONV must win even when a higher-tier format marker is also present.
		{"Avatar.2009.FULL-SBS.3DCONV.1080p.BluRay"},
		{"Avatar.2009.3DCONV.FULL-SBS.1080p.BluRay"},
		// "convert" and "3D-CONVERT" are synonyms for 3D-CONV.
		{"Five Nights at Freddys 2 2025.1080.3D.FSBS.convert"},
		{"Avatar.2009.3D-CONVERT.FULL-SBS.1080p.BluRay"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DConv {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DConv", c.title, q.Format3D)
		}
	}
}

func TestSpecFormat3DConvExact(t *testing.T) {
	spec, err := ParseSpec("3dconv")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if !spec.Matches(Quality{Format3D: Format3DConv}) {
		t.Error("3D-Conv should match 3dconv spec")
	}
	if spec.Matches(Quality{Format3D: Format3DHalf}) {
		t.Error("3D-Half should not match exact 3dconv spec")
	}
	if spec.Matches(Quality{Format3D: Format3DNone}) {
		t.Error("non-3D should not match 3dconv spec")
	}
}

func TestSpecFormat3DConvPlusIncludesHigher(t *testing.T) {
	spec, err := ParseSpec("3dconv+")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	for _, f := range []Format3D{Format3DConv, Format3DHalf, Format3DFull, Format3DBD} {
		if !spec.Matches(Quality{Format3D: f}) {
			t.Errorf("3dconv+ should match Format3D=%v", f)
		}
	}
	if spec.Matches(Quality{Format3D: Format3DNone}) {
		t.Error("non-3D should not match 3dconv+")
	}
}

func TestSpecFormat3DHalfPlusExcludesConv(t *testing.T) {
	spec, err := ParseSpec("3d+")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec.Matches(Quality{Format3D: Format3DConv}) {
		t.Error("3D-Conv should not match 3d+ (converted is below native half-SBS)")
	}
}

func TestParse3DCompleteBluRayIsBD3D(t *testing.T) {
	cases := []struct {
		title string
		want  Format3D
	}{
		// 3D + COMPLETE BluRay → BD3D (full disc rip).
		{"Spider.Man.Into.the.Spider.Verse.2018.3D.COMPLETE.BluRay", Format3DBD},
		{"Avatar.2009.3D.BluRay.COMPLETE", Format3DBD},
		// 3DCONV + COMPLETE BluRay → still Conv (conversion stays lowest tier).
		{"Movie.2020.3DCONV.COMPLETE.BluRay", Format3DConv},
		// 3D + COMPLETE but not BluRay → no elevation.
		// COMPLETE on a WEBRip is not a disc rip, so nothing promotes it and
		// the bare "3D" stands as Unspecified.
		{"Movie.2020.3D.COMPLETE.WEBRip", Format3DUnspecified},
		// Non-3D COMPLETE BluRay → not elevated (no 3D marker).
		{"Movie.2020.COMPLETE.BluRay", Format3DNone},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != c.want {
			t.Errorf("Parse(%q).Format3D = %v, want %v", c.title, q.Format3D, c.want)
		}
	}
}

func TestParse3DHighestMarkerWins(t *testing.T) {
	cases := []struct {
		title string
		want  Format3D
	}{
		// Generic "3D" before an explicit format tag — explicit tag must win.
		{"Project.Hail.Mary.2026.IMAX.3D.FSBS.1080p.WEBRip", Format3DFull},
		// Bare SBS is Half by convention; the explicit FSBS test below
		// covers the explicit-full case.
		{"Avatar.2009.3D.SBS.1080p.BluRay", Format3DHalf},
		{"Avatar.2009.3D.HSBS.1080p.BluRay", Format3DHalf},
		// BD3D beats a preceding plain 3D.
		{"Movie.2020.3D.BD3D.1080p.BluRay", Format3DBD},
		// Unhyphenated FULL/HALF variants.
		{"Annihilation.2018.3D.1080p.FullSBS.DTS", Format3DFull},
		{"Movie.2020.3D.1080p.HalfSBS", Format3DHalf},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != c.want {
			t.Errorf("Parse(%q).Format3D = %v, want %v", c.title, q.Format3D, c.want)
		}
	}
}

func TestParse3DDefaultsResolutionTo1080p(t *testing.T) {
	cases := []struct{ title string }{
		{"Avatar.2009.HSBS.BluRay"},
		{"Fight.or.Flight.BD50.MVC"},
		{"Blade.Runner.2049.2017.3D.COMPLETE.BluRay"},
		{"The.Mummy.3D.HSBS.(1932)"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D == Format3DNone {
			t.Errorf("Parse(%q): expected 3D format", c.title)
			continue
		}
		if q.Resolution != Resolutionp1080 {
			t.Errorf("Parse(%q): resolution = %v, want 1080p (default for 3D)", c.title, q.Resolution)
		}
	}
}

func TestParse3DExplicitResolutionNotOverridden(t *testing.T) {
	// An explicit resolution tag must not be overridden by the 3D default.
	q := Parse("Avatar.2009.3D.2160p.BluRay")
	if q.Resolution != Resolutionp2160 {
		t.Errorf("resolution: got %v, want 2160p", q.Resolution)
	}
}

func TestBetter3DFormatTakesPrecedence(t *testing.T) {
	bd := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DBD}
	half := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DHalf}
	full := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DFull}
	conv := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DConv}
	if !bd.Better(full) {
		t.Error("BD3D should beat 3D-Full at same resolution/source")
	}
	if !full.Better(half) {
		t.Error("3D-Full should beat 3D-Half at same resolution/source")
	}
	if !half.Better(conv) {
		t.Error("3D-Half should beat 3D-Conv at same resolution/source")
	}
	// 3D format beats resolution: BD3D 720p > Half-SBS 1080p
	bdLowRes := Quality{Resolution: Resolutionp720, Source: SourceBluRay, Format3D: Format3DBD}
	halfHighRes := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DHalf}
	if !bdLowRes.Better(halfHighRes) {
		t.Error("BD3D 720p should beat Half-SBS 1080p (3D format is primary)")
	}
}

func TestBetter3DResolutionAsTieBreaker(t *testing.T) {
	hi := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DFull}
	lo := Quality{Resolution: Resolutionp720, Source: SourceBluRay, Format3D: Format3DFull}
	if !hi.Better(lo) {
		t.Error("same 3D format: 1080p should beat 720p")
	}
}

func TestBetterNon3DUnaffected(t *testing.T) {
	hi := Quality{Resolution: Resolutionp2160, Source: SourceBluRay}
	lo := Quality{Resolution: Resolutionp1080, Source: SourceBluRay}
	if !hi.Better(lo) {
		t.Error("non-3D: 2160p should beat 1080p as before")
	}
}

// --- ParseSpec and Spec.Matches ---

func TestSpecMatchesResolutionRange(t *testing.T) {
	spec, err := ParseSpec("720p-1080p")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		q    Quality
		want bool
	}{
		{Quality{Resolution: Resolutionp480}, false},
		{Quality{Resolution: Resolutionp720}, true},
		{Quality{Resolution: Resolutionp1080}, true},
		{Quality{Resolution: Resolutionp2160}, false},
	}
	for _, tc := range cases {
		if got := spec.Matches(tc.q); got != tc.want {
			t.Errorf("Matches(%v): got %v, want %v", tc.q, got, tc.want)
		}
	}
}

func TestSpecMatchesSourceRange(t *testing.T) {
	spec, err := ParseSpec("hdtv-bluray")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Matches(Quality{Source: SourceHDTV}) {
		t.Error("HDTV should match hdtv-bluray")
	}
	if !spec.Matches(Quality{Source: SourceWebDL}) {
		t.Error("WEB-DL should match hdtv-bluray")
	}
	if !spec.Matches(Quality{Source: SourceBluRay}) {
		t.Error("BluRay should match hdtv-bluray")
	}
	if spec.Matches(Quality{Source: SourceTVRip}) {
		t.Error("TVRip should not match hdtv-bluray")
	}
}

func TestSpecMatchesMultipleDimensions(t *testing.T) {
	spec, err := ParseSpec("720p-1080p hdtv-bluray")
	if err != nil {
		t.Fatal(err)
	}
	good := Quality{Resolution: Resolutionp720, Source: SourceWebDL}
	bad1 := Quality{Resolution: Resolutionp480, Source: SourceWebDL} // res too low
	bad2 := Quality{Resolution: Resolutionp720, Source: SourceTVRip} // source too low

	if !spec.Matches(good) {
		t.Error("good quality should match")
	}
	if spec.Matches(bad1) {
		t.Error("bad resolution should not match")
	}
	if spec.Matches(bad2) {
		t.Error("bad source should not match")
	}
}

func TestSpecSingleValue(t *testing.T) {
	spec, err := ParseSpec("1080p")
	if err != nil {
		t.Fatal(err)
	}
	// min=max=1080p
	if !spec.Matches(Quality{Resolution: Resolutionp1080}) {
		t.Error("1080p should match spec '1080p'")
	}
	if spec.Matches(Quality{Resolution: Resolutionp720}) {
		t.Error("720p should not match spec '1080p'")
	}
}

func TestSpecUnknownDimensionAlwaysMatches(t *testing.T) {
	spec, err := ParseSpec("720p-1080p")
	if err != nil {
		t.Fatal(err)
	}
	// Unknown source — source constraint is unconstrained, so it matches.
	q := Quality{Resolution: Resolutionp720, Source: SourceUnknown}
	if !spec.Matches(q) {
		t.Error("unknown source should not fail an unconstrained source spec")
	}
}

func TestParseSpecInvalidToken(t *testing.T) {
	_, err := ParseSpec("not-a-quality")
	if err == nil {
		t.Error("expected error for unknown quality value")
	}
}

func TestParseSpecEmpty(t *testing.T) {
	spec, err := ParseSpec("")
	if err != nil {
		t.Fatalf("empty spec should not error: %v", err)
	}
	// Empty spec matches everything.
	if !spec.Matches(Quality{}) {
		t.Error("empty spec should match zero quality")
	}
	if !spec.Matches(Quality{Resolution: Resolutionp2160, Source: SourceBluRay}) {
		t.Error("empty spec should match any quality")
	}
}

func TestParseSpecAllDimensions(t *testing.T) {
	_, err := ParseSpec("720p-1080p hdtv-bluray x264-x265 aac-dts sdr-hdr")
	if err != nil {
		t.Fatalf("full spec parse: %v", err)
	}
}

func TestSpecFormat3DMinOnly(t *testing.T) {
	spec, err := ParseSpec("1080p+ 3d+")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if spec.MinFormat3D != Format3DHalf {
		t.Errorf("MinFormat3D: got %v, want Format3DHalf", spec.MinFormat3D)
	}
	// non-3D entry rejected
	if spec.Matches(Quality{Resolution: Resolutionp1080}) {
		t.Error("non-3D entry should not match 3d+ spec")
	}
	// half-SBS passes
	if !spec.Matches(Quality{Resolution: Resolutionp1080, Format3D: Format3DHalf}) {
		t.Error("half-SBS should match 3d+ spec")
	}
	// BD3D passes
	if !spec.Matches(Quality{Resolution: Resolutionp1080, Format3D: Format3DBD}) {
		t.Error("BD3D should match 3d+ spec")
	}
}

func TestSpecFormat3DExact(t *testing.T) {
	spec, err := ParseSpec("1080p+ bd3d")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	// half-SBS rejected
	if spec.Matches(Quality{Resolution: Resolutionp1080, Format3D: Format3DHalf}) {
		t.Error("half-SBS should not match bd3d spec")
	}
	// BD3D passes
	if !spec.Matches(Quality{Resolution: Resolutionp1080, Format3D: Format3DBD}) {
		t.Error("BD3D should match bd3d spec")
	}
}

func TestSpecNoFormat3DAcceptsBoth(t *testing.T) {
	spec, err := ParseSpec("1080p+")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if !spec.Matches(Quality{Resolution: Resolutionp1080}) {
		t.Error("non-3D should match spec with no 3D constraint")
	}
	if !spec.Matches(Quality{Resolution: Resolutionp1080, Format3D: Format3DBD}) {
		t.Error("3D should match spec with no 3D constraint")
	}
}

// --- parseResolution full coverage ---

func TestParseResolutionValues(t *testing.T) {
	cases := []struct {
		s    string
		want Resolution
	}{
		{"sd", ResolutionSD},
		{"480p", Resolutionp480},
		{"576p", Resolutionp576},
		{"720p", Resolutionp720},
		{"1080p", Resolutionp1080},
		{"2160p", Resolutionp2160},
		{"4k", Resolutionp2160},
	}
	for _, tc := range cases {
		spec, err := ParseSpec(tc.s)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tc.s, err)
			continue
		}
		if spec.MinResolution != tc.want {
			t.Errorf("ParseSpec(%q): got %v, want %v", tc.s, spec.MinResolution, tc.want)
		}
	}
}

// --- parseSource full coverage ---

func TestParseSourceValues(t *testing.T) {
	cases := []struct {
		s    string
		want Source
	}{
		{"dvdrip", SourceDVDRip},
		{"tvrip", SourceTVRip},
		{"hdtv", SourceHDTV},
		{"webrip", SourceWEBRip},
		{"webdl", SourceWebDL},
		{"web-dl", SourceWebDL},
		{"web", SourceWebDL},
		{"bluray", SourceBluRay},
		{"bdrip", SourceBluRay},
		{"remux", SourceRemux},
	}
	for _, tc := range cases {
		spec, err := ParseSpec(tc.s)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tc.s, err)
			continue
		}
		if spec.MinSource != tc.want {
			t.Errorf("ParseSpec(%q): got %v, want %v", tc.s, spec.MinSource, tc.want)
		}
	}
}

// --- parseCodec full coverage ---

func TestParseCodecValues(t *testing.T) {
	cases := []struct {
		s    string
		want Codec
	}{
		{"xvid", CodecXviD},
		{"divx", CodecDivX},
		{"x264", CodecH264},
		{"h264", CodecH264},
		{"x265", CodecH265},
		{"h265", CodecH265},
		{"hevc", CodecH265},
		{"av1", CodecAV1},
	}
	for _, tc := range cases {
		spec, err := ParseSpec(tc.s)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tc.s, err)
			continue
		}
		if spec.MinCodec != tc.want {
			t.Errorf("ParseSpec(%q): got %v, want %v", tc.s, spec.MinCodec, tc.want)
		}
	}
}

// --- parseAudio full coverage ---

func TestParseAudioValues(t *testing.T) {
	cases := []struct {
		s    string
		want Audio
	}{
		{"mp3", AudioMP3},
		{"aac", AudioAAC},
		{"dd", AudioDolbyDigital},
		{"dolbydigital", AudioDolbyDigital},
		{"dts", AudioDTS},
		{"truehd", AudioTrueHD},
		{"atmos", AudioAtmos},
	}
	for _, tc := range cases {
		spec, err := ParseSpec(tc.s)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tc.s, err)
			continue
		}
		if spec.MinAudio != tc.want {
			t.Errorf("ParseSpec(%q): got %v, want %v", tc.s, spec.MinAudio, tc.want)
		}
	}
}

// --- parseColorRange full coverage ---

func TestParseColorRangeValues(t *testing.T) {
	cases := []struct {
		s    string
		want ColorRange
	}{
		{"sdr", ColorRangeSDR},
		{"hdr", ColorRangeHDR},
		{"hdr10", ColorRangeHDR10},
		{"hdr10+", ColorRangeHDR10},
		{"dv", ColorRangeDolbyVision},
		{"dolbyvision", ColorRangeDolbyVision},
	}
	for _, tc := range cases {
		spec, err := ParseSpec(tc.s)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tc.s, err)
			continue
		}
		if spec.MinColorRange != tc.want {
			t.Errorf("ParseSpec(%q): got %v, want %v", tc.s, spec.MinColorRange, tc.want)
		}
	}
}

// --- Matches full dimension coverage ---

func TestMatchesAllDimensionBounds(t *testing.T) {
	spec, _ := ParseSpec("720p-1080p hdtv-bluray x264-x265 aac-dts sdr-hdr")

	// Codec bounds
	if spec.Matches(Quality{Resolution: Resolutionp720, Source: SourceHDTV, Codec: CodecXviD, Audio: AudioAAC}) {
		t.Error("XviD below x264 should not match")
	}
	if spec.Matches(Quality{Resolution: Resolutionp720, Source: SourceHDTV, Codec: CodecAV1, Audio: AudioAAC}) {
		t.Error("AV1 above x265 should not match")
	}

	// Audio bounds
	if spec.Matches(Quality{Resolution: Resolutionp720, Source: SourceHDTV, Codec: CodecH264, Audio: AudioMP3}) {
		t.Error("MP3 below AAC should not match")
	}
	if spec.Matches(Quality{Resolution: Resolutionp720, Source: SourceHDTV, Codec: CodecH264, Audio: AudioTrueHD}) {
		t.Error("TrueHD above DTS should not match")
	}

	// ColorRange bounds
	specHDR, _ := ParseSpec("hdr-hdr10")
	if specHDR.Matches(Quality{ColorRange: ColorRangeSDR}) {
		t.Error("SDR below HDR should not match")
	}
	if specHDR.Matches(Quality{ColorRange: ColorRangeDolbyVision}) {
		t.Error("DolbyVision above HDR10 should not match")
	}
}

func TestBetterAudioBreaksTie(t *testing.T) {
	a := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH265, Audio: AudioAtmos}
	b := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH265, Audio: AudioDTS}
	if !a.Better(b) {
		t.Error("Atmos should beat DTS at same res+source+codec")
	}
}

func TestBetterColorRangeBreaksTie(t *testing.T) {
	a := Quality{Resolution: Resolutionp2160, Source: SourceBluRay, Codec: CodecH265, Audio: AudioAtmos, ColorRange: ColorRangeDolbyVision}
	b := Quality{Resolution: Resolutionp2160, Source: SourceBluRay, Codec: CodecH265, Audio: AudioAtmos, ColorRange: ColorRangeHDR}
	if !a.Better(b) {
		t.Error("Dolby Vision should beat HDR at same res+source+codec+audio")
	}
}

func TestMarshalJSONIncludesStringField(t *testing.T) {
	q := Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DFull}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"string":"3D-Full 1080p BluRay"`) {
		t.Errorf("MarshalJSON missing string field, got: %s", b)
	}
}

func TestMarshalJSONUnknownQualityOmitsStringField(t *testing.T) {
	q := Quality{}
	b, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), `"string"`) {
		t.Errorf("empty quality should not emit string field, got: %s", b)
	}
}

// ── CAM / TS / SCR source detection ──────────────────────────────────────────

func TestParseDetectsCAM(t *testing.T) {
	cases := []struct {
		title  string
		source Source
	}{
		{"Ferrari.2023.CAM.x264-GROUP", SourceCAM},
		{"Ferrari.2023.HDCAM.1080p.x264", SourceCAM},
		{"Ferrari.2023.CAMRip.x264", SourceCAM},
		{"Ferrari.2023.TS.x264-GROUP", SourceTS},
		{"Ferrari.2023.HDTS.1080p.x264", SourceTS},
		{"Ferrari.2023.HDTC.x264", SourceTS},
		{"Ferrari.2023.TC.x264-GROUP", SourceTS},
		{"Ferrari.2023.TELESYNC.x264", SourceTS},
		{"Ferrari.2023.DVDScr.x264", SourceSCR},
		{"Ferrari.2023.SCR.x264-GROUP", SourceSCR},
		{"Ferrari.2023.Screener.x264", SourceSCR},
	}
	for _, tc := range cases {
		q := Parse(tc.title)
		if q.Source != tc.source {
			t.Errorf("Parse(%q): source got %v (%d), want %v (%d)",
				tc.title, sourceNames[q.Source], q.Source, sourceNames[tc.source], tc.source)
		}
	}
}

func TestCAMIsLowerThanDVDRip(t *testing.T) {
	if SourceCAM >= SourceDVDRip {
		t.Errorf("SourceCAM (%d) must be < SourceDVDRip (%d)", SourceCAM, SourceDVDRip)
	}
	if SourceTS >= SourceDVDRip {
		t.Errorf("SourceTS (%d) must be < SourceDVDRip (%d)", SourceTS, SourceDVDRip)
	}
	if SourceSCR >= SourceDVDRip {
		t.Errorf("SourceSCR (%d) must be < SourceDVDRip (%d)", SourceSCR, SourceDVDRip)
	}
	if SourceCAM >= SourceTS {
		t.Errorf("SourceCAM (%d) must be < SourceTS (%d)", SourceCAM, SourceTS)
	}
	if SourceTS >= SourceSCR {
		t.Errorf("SourceTS (%d) must be < SourceSCR (%d)", SourceTS, SourceSCR)
	}
}

func TestSpecRejectsCAMWhenMinSourceDVDRip(t *testing.T) {
	spec, err := ParseSpec("1080p+ dvdrip+")
	if err != nil {
		t.Fatal(err)
	}
	cam := Parse("Ferrari.2023.CAM.1080p.x264")
	if spec.Matches(cam) {
		t.Error("1080p+ dvdrip+ spec should reject a CAM 1080p release")
	}
	webdl := Parse("Ferrari.2023.1080p.AMZN.WEB-DL.H264")
	if !spec.Matches(webdl) {
		t.Error("1080p+ dvdrip+ spec should accept a WEB-DL 1080p release")
	}
}

func TestVideoSourceFieldSetForCAM(t *testing.T) {
	q := Parse("Oppenheimer.2023.CAM.x264")
	if q.Source != SourceCAM {
		t.Errorf("source: got %v, want CAM", sourceNames[q.Source])
	}
	if sourceNames[SourceCAM] != "CAM" {
		t.Errorf("sourceNames[SourceCAM]: got %q, want CAM", sourceNames[SourceCAM])
	}
}

// ── Space-separated 3D markers (scene release convention) ────────────────────

func TestParse3DSpaceSeparatedHalfMarkers(t *testing.T) {
	cases := []struct{ title string }{
		// Real titles observed in movies-3d-discover logs.
		{"Ballerina 3D 2016 1080p H OU BluRay x264 DTS 5 1 vice"},
		{"Snow White A Deadly Summer 2012 3D H SBS German DTS DL 1080p BluRay x264"},
		{"The Monkey King 2 3D 2016 1080p H OU BluRay x264 TrueHD 7 1 vice"},
		{"The Monkey King 3 Kingdom of Women 2018 1080p 3D BluRay Half SBS DD5 1 x264 LoRD"},
		{"Avatar: The Way of Water 2022 Trailer 1080p 3D Half SBS DD+5 1 x264 LR0EZ"},
		{"new gods nezha,reborn 2021 1080p h sbs chinese 5 1 mk3d"},
		{"Shrek the Third 2007 1080p ac3 5 1 h sbs"},
		{"Dolphins and Whales 3D Tribes of the Ocean 2008 1080p dts 5 1 h ou"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DHalf {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DHalf", c.title, q.Format3D)
		}
	}
}

func TestParse3DSpaceSeparatedFullMarkers(t *testing.T) {
	cases := []struct{ title string }{
		{"Avatar The Way of Water 2022 3D Full SBS MULTI 1080p 10bit Bluray EAC3"},
		{"Wolf Man (2025) Full SBS NeFud"},
		{"Pirates of the Caribbean 3D (2003) Full OU 1080p x264 6xMultiAudio JFC"},
		{"Megalopolis (2024) Full SBS NeFud mkv"},
		{"The Darkest Hour 3D 2011 2160p F OU BluRay x264 DTS 5 1 vice"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DFull {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DFull", c.title, q.Format3D)
		}
	}
}

func TestParseBareSBSAndOUAreHalf(t *testing.T) {
	cases := []struct{ title string }{
		{"Frankenstein vs the Wolfman 3D 2008 SBS bluto"},
		{"Treasure of the Four Crowns (1983) 3D SBS bluto"},
		{"Found Footage 3D 2016 SBS bluto"},
		{"Wicked Part 1 3D 2024 SBS bluto"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DHalf {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DHalf (bare SBS/OU is half by convention)",
				c.title, q.Format3D)
		}
	}
}

// ── Conversion markers (Conv / Conversion / AI / StereoCrafter / DepthCrafter) ─

func TestParse3DConvSpaceAndStandalone(t *testing.T) {
	cases := []struct{ title string }{
		// Space-separated "3D Conv".
		{"Ballerina 2025 3D Conv FSBS DDP 5 1 Atmos AETHER"},
		{"The Matrix Revolutions 2003 3D Conv FSBS Multi TrueHD 7 1 Atmos AETHER"},
		// Standalone "conv" / "Conv" combined with another native marker.
		{"A Working Man 2025 conv fsbs 3detective"},
		{"Moonfall 2022 fsbs Conv Atmos 3detective"},
		{"The Old Guard 2020 Conv fsbs 3detective"},
		{"Bullet Train 2022 FSBS x265 10bit Atmos Manual Conv 3detective"},
		// "Conversion" word.
		{"The Greatest Showman 2017 1080p 3D Conversion FSBS dtshdma"},
		{"The Wandering Earth 2019 fsbs conversion"},
		// HT Conversion (a specific conversion tool/method).
		{"Dune Part Two 2024 HT Conversion H265 3D Full SBS"},
		{"The Brothers Grimsby 2016 HT Conversion H265 3D Full SBS"},
		// 3D Convert (full word, space).
		{"Avatar.2009.3D Convert.FULL-SBS.1080p.BluRay"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DConv {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DConv", c.title, q.Format3D)
		}
	}
}

func TestParse3DConvAIEnhancedAndUpscaled(t *testing.T) {
	cases := []struct{ title string }{
		{"Monkey Man 2024 AIenhanced fsbs Conv Atmos 3detective"},
		{"Spirited 2022 AIenhanced Conv fsbs 3detective"},
		{"Gunpowder Milkshake 2021 AiEnhanced Atmos fsbs Conv 3detective"},
		{"Meg 2 The Trench 2023 AIenhanced 1080p Fsbs Conv Atmos 3Detective mkv"},
		{"A Quiet Place Day One 2024 AIenhanced 1080p fsbs Conv Atmos 3detective"},
		{"The Batman 2022 AIenhanced 1080p fsbs Conv Atmos 3detective"},
		{"Planet Of The Apes 1968 Ai Upscaled 60fps fsbs 3Dom"},
		{"Blade Runner 1982 Ai Enhanced 60fps fsbs 3Dom"},
		{"Migration 2023 3D 4k Upscaled H SBS TheDarknesS mkv"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DConv {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DConv", c.title, q.Format3D)
		}
	}
}

func TestParse3DConvStereoAndDepthCrafter(t *testing.T) {
	cases := []struct{ title string }{
		{"Fight Or Flight 2024 fsbs Manual StereoCrafter conv 3detective"},
		{"The Uninvited 2009 3D FSBS (STEREO CRAFTER) by CBG mkv"},
		{"Ferrari 2023 H265 3D Conv FullSBS (DepthCrafter) by CBG"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DConv {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DConv", c.title, q.Format3D)
		}
	}
}

// Weak conversion markers (bare Conv / Conversion / Upscaled / AI Enhanced) must
// NOT trigger Format3DConv on non-3D releases, since they appear in unrelated
// contexts (e.g. a 4K upscale of a 2D release).
func TestParseWeakConvWithoutNative3DStaysNone(t *testing.T) {
	cases := []struct{ title string }{
		{"Movie.2020.1080p.WEBRip.Upscaled.to.4K"},
		{"The.Conversion.2018.1080p.WEBRip"},
		{"Some.Movie.2022.AI.Enhanced.2160p.HEVC"},
	}
	for _, c := range cases {
		q := Parse(c.title)
		if q.Format3D != Format3DNone {
			t.Errorf("Parse(%q).Format3D = %v, want Format3DNone (weak conv marker without native 3D context)",
				c.title, q.Format3D)
		}
	}
}

// ── Spec parser symmetry: bare SBS/OU as Half ────────────────────────────────

func TestParseFormat3DSpecBareSBSOUAreHalf(t *testing.T) {
	for _, tok := range []string{"sbs", "ou", "hsbs", "hou", "half-sbs", "half-ou"} {
		spec, err := ParseSpec(tok)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tok, err)
			continue
		}
		if spec.MinFormat3D != Format3DHalf || spec.MaxFormat3D != Format3DHalf {
			t.Errorf("ParseSpec(%q): got %v..%v, want Half..Half",
				tok, spec.MinFormat3D, spec.MaxFormat3D)
		}
	}
}

func TestParseFormat3DSpecFullTokens(t *testing.T) {
	for _, tok := range []string{"fsbs", "fou", "full-sbs", "full-ou", "3dfull"} {
		spec, err := ParseSpec(tok)
		if err != nil {
			t.Errorf("ParseSpec(%q): %v", tok, err)
			continue
		}
		if spec.MinFormat3D != Format3DFull || spec.MaxFormat3D != Format3DFull {
			t.Errorf("ParseSpec(%q): got %v..%v, want Full..Full",
				tok, spec.MinFormat3D, spec.MaxFormat3D)
		}
	}
}

// ── Optional-dimension "?" suffix ─────────────────────────────────────────────

func TestSpecOptionalSourceAllowsUnknown(t *testing.T) {
	spec, err := ParseSpec("720p-1080p webrip+?")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	if !spec.OptSource {
		t.Error("expected OptSource=true after webrip+?")
	}
	// Title with no source token: 1080p HEVC release.
	q := Parse("For.All.Mankind.S01E03.Nixons.Women.1080p.HEVC.x265-MeGusta")
	if q.Source != SourceUnknown {
		t.Fatalf("precondition: expected SourceUnknown, got %v", q.Source)
	}
	if !spec.Matches(q) {
		t.Error("optional source spec should accept entry with unknown source")
	}
}

func TestSpecOptionalSourceStillRejectsBadSource(t *testing.T) {
	spec, err := ParseSpec("720p-1080p webrip+?")
	if err != nil {
		t.Fatal(err)
	}
	// A detected source that's below the floor must still be rejected — "?"
	// only relaxes the check when the dimension is Unknown.
	cam := Quality{Resolution: Resolutionp1080, Source: SourceCAM}
	if spec.Matches(cam) {
		t.Error("optional source spec should still reject CAM 1080p")
	}
	web := Quality{Resolution: Resolutionp1080, Source: SourceWEBRip}
	if !spec.Matches(web) {
		t.Error("optional source spec should accept WEBRip 1080p")
	}
}

func TestSpecOptionalRange(t *testing.T) {
	spec, err := ParseSpec("720p-1080p?")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.OptResolution {
		t.Error("expected OptResolution=true")
	}
	// Unknown resolution passes.
	if !spec.Matches(Quality{}) {
		t.Error("unknown resolution should pass with 720p-1080p?")
	}
	// In-range passes.
	if !spec.Matches(Quality{Resolution: Resolutionp1080}) {
		t.Error("1080p should pass with 720p-1080p?")
	}
	// Out-of-range still rejects.
	if spec.Matches(Quality{Resolution: Resolutionp2160}) {
		t.Error("2160p should still be rejected by 720p-1080p?")
	}
	if spec.Matches(Quality{Resolution: Resolutionp480}) {
		t.Error("480p should still be rejected by 720p-1080p?")
	}
}

func TestSpecOptionalExactValue(t *testing.T) {
	spec, err := ParseSpec("1080p?")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Matches(Quality{}) {
		t.Error("unknown should pass 1080p?")
	}
	if !spec.Matches(Quality{Resolution: Resolutionp1080}) {
		t.Error("1080p should pass 1080p?")
	}
	if spec.Matches(Quality{Resolution: Resolutionp720}) {
		t.Error("720p should not pass 1080p?")
	}
}

func TestSpecOptionalIndependentPerDimension(t *testing.T) {
	// Source is optional, resolution is not. An entry with unknown resolution
	// must still fail; an entry with unknown source must pass (assuming the
	// resolution checks out).
	spec, err := ParseSpec("720p+ webrip+?")
	if err != nil {
		t.Fatal(err)
	}
	if spec.OptResolution {
		t.Error("OptResolution should be false")
	}
	if !spec.OptSource {
		t.Error("OptSource should be true")
	}
	// Unknown source, 720p: passes.
	if !spec.Matches(Quality{Resolution: Resolutionp720}) {
		t.Error("720p unknown-source should pass")
	}
	// Unknown resolution: rejected.
	if spec.Matches(Quality{Source: SourceWEBRip}) {
		t.Error("unknown resolution should be rejected even with webrip source")
	}
}

func TestSpecOptionalBareMarkerError(t *testing.T) {
	if _, err := ParseSpec("?"); err == nil {
		t.Error("expected error for bare ? token")
	}
}

func TestSpecOptionalUnknownValueError(t *testing.T) {
	if _, err := ParseSpec("not-a-quality?"); err == nil {
		t.Error("expected error for unknown value with ? suffix")
	}
}

// End-to-end regression: a 3dfull spec must reject both Ballerina entries that
// triggered this fix — the half-3D rip ("H OU") and the conversion ("3D Conv").
func TestSpec3DFullRejectsBallerinaCases(t *testing.T) {
	spec, err := ParseSpec("3dfull")
	if err != nil {
		t.Fatal(err)
	}
	rejected := []string{
		"Ballerina 3D 2016 1080p H OU BluRay x264 DTS 5 1 vice",
		"Ballerina 2025 3D Conv FSBS DDP 5 1 Atmos AETHER",
	}
	for _, title := range rejected {
		if spec.Matches(Parse(title)) {
			t.Errorf("3dfull spec should reject %q (Format3D=%v)",
				title, Parse(title).Format3D)
		}
	}
}

// Fan 2D→3D converter tags are strong conversion markers: a release carrying
// one parses at the Conv tier so native 3D outranks it in dedup/upgrades.
func TestConverterTagsAreStrongConvMarkers(t *testing.T) {
	for _, title := range []string{
		"Supergirl (2026) fsbs 3840x2160 x264 woz3d",
		"Movie.2026.HSBS.1080p.OWL3D.x265",
		"Movie.2026.Full-SBS.iw3.x264",
	} {
		q := Parse(title)
		if q.Format3D != Format3DConv {
			t.Errorf("%q: Format3D = %v, want Conv", title, q.Format3D)
		}
	}
	// A native release with none of the tags stays native.
	if q := Parse("Movie.2026.BD3D.MVC.1080p"); q.Format3D == Format3DConv {
		t.Error("native BD3D must not parse as Conv")
	}
}

// TestBetterPrecedence documents exactly how "is this release better?" is
// decided: a strict lexicographic ladder that returns at the FIRST dimension
// that differs. Resolution outranks everything below it, so a 1080p Atmos
// release never displaces a 2160p one — the audio comparison is never even
// reached.
func TestBetterPrecedence(t *testing.T) {
	cases := []struct {
		name       string
		incoming   string
		current    string
		wantBetter bool
	}{
		// Resolution dominates every lower dimension.
		{"1080p Atmos does not beat 2160p",
			"Movie.2024.1080p.BluRay.TrueHD.Atmos.x265", "Movie.2024.2160p.WEB-DL.x265", false},
		{"2160p beats 1080p Atmos",
			"Movie.2024.2160p.WEB-DL.x265", "Movie.2024.1080p.BluRay.TrueHD.Atmos.x265", true},
		{"1080p HDR does not beat 2160p SDR",
			"Movie.2024.1080p.BluRay.HDR10.x265", "Movie.2024.2160p.WEB-DL.x265", false},

		// Same resolution: source decides next.
		{"BluRay beats WEB-DL at equal resolution",
			"Movie.2024.2160p.BluRay.x265", "Movie.2024.2160p.WEB-DL.x265", true},
		{"WEB-DL Atmos does not beat BluRay at equal resolution",
			"Movie.2024.2160p.WEB-DL.TrueHD.Atmos.x265", "Movie.2024.2160p.BluRay.x265", false},

		// Same resolution, source and codec: colour range outranks audio,
		// because HDR/DV changes every frame while audio is one of several
		// tracks a release may carry.
		{"Dolby Vision beats Atmos when colour and audio disagree",
			"Movie.2024.2160p.BluRay.x265.DTS.DV", "Movie.2024.2160p.BluRay.x265.TrueHD.Atmos", true},
		{"Atmos SDR does not beat DTS Dolby Vision",
			"Movie.2024.2160p.BluRay.x265.TrueHD.Atmos", "Movie.2024.2160p.BluRay.x265.DTS.DV", false},
		{"HDR beats SDR at equal resolution, source and codec",
			"Movie.2024.2160p.BluRay.x265.DTS.HDR10", "Movie.2024.2160p.BluRay.x265.DTS", true},
		{"audio decides only when colour range ties",
			"Movie.2024.2160p.BluRay.x265.TrueHD.Atmos.HDR10", "Movie.2024.2160p.BluRay.x265.DTS.HDR10", true},

		// Equal is not better — this is what stops re-downloading the same tier.
		{"identical quality is not an upgrade",
			"Movie.2024.2160p.BluRay.x265.DTS", "Movie.2024.2160p.BluRay.x265.DTS", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in, cur := Parse(c.incoming), Parse(c.current)
			if got := in.Better(cur); got != c.wantBetter {
				t.Errorf("Better() = %v, want %v\n  incoming: %s\n  current:  %s",
					got, c.wantBetter, in.String(), cur.String())
			}
		})
	}
}

// A remux repackages the disc's streams without re-encoding, and
// half-resolution 3D only exists as a re-encode — squeezing two views into one
// frame is an encode. So a 3D remux necessarily carries the disc's MVC stream.
// Plain "3D" otherwise defaults to half, which was rejecting genuine full-disc
// remuxes against a "3dfull" spec.
func Test3DRemuxIsADiscRip(t *testing.T) {
	cases := []struct {
		name  string
		title string
	}{
		{"the reported case", "Life of Pi 2012 1080p 3D Blu ray Remux AVC DTSHD MA 7 1(MKV)"},
		{"dotted", "Life.of.Pi.2012.1080p.3D.BluRay.Remux.AVC.DTS-HD.MA.7.1"},
		{"bdremux", "Avatar.2009.3D.BDRemux.1080p"},
		{"multi-language remux", "Abominable 2019 1080p 3D Multi Language Remux zman"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			q := Parse(c.title)
			if q.Source != SourceRemux {
				t.Fatalf("source = %v, want Remux", q.Source)
			}
			if q.Format3D != Format3DBD {
				t.Errorf("Format3D = %v, want BD3D", q.Format3D)
			}
		})
	}
}

// The promotion must not invent 3D where there is none, and must not override
// a conversion marker — an AI-converted release remuxed to MKV is still a
// conversion, not a disc rip.
func Test3DRemuxPromotionIsBounded(t *testing.T) {
	if q := Parse("Life.of.Pi.2012.1080p.BluRay.Remux.AVC.DTS-HD.MA.7.1"); q.Format3D != Format3DNone {
		t.Errorf("a non-3D remux must stay non-3D, got %v", q.Format3D)
	}
	if q := Parse("Some.Movie.2012.3D.CONV.1080p.Remux.AVC"); q.Format3D != Format3DConv {
		t.Errorf("a converted 3D remux must stay 3D-Conv, got %v", q.Format3D)
	}
	if q := Parse("Some.Movie.2012.WOZ3D.1080p.Remux.AVC"); q.Format3D != Format3DConv {
		t.Errorf("a known fan-conversion tag must stay 3D-Conv, got %v", q.Format3D)
	}
	// A non-remux source keeps the half reading: an HSBS BluRay encode is
	// genuinely half-resolution.
	if q := Parse("Some.Movie.2012.HSBS.1080p.BluRay.x264"); q.Format3D != Format3DHalf {
		t.Errorf("an HSBS encode must stay 3D-Half, got %v", q.Format3D)
	}
}

// An explicit frame-packing marker always wins over the remux inference. These
// releases fit both views into one frame, which an ordinary decoder can play —
// calling them BD3D would misfile a playable release as an MVC one.
func Test3DRemuxNeverOverridesAnExplicitLayout(t *testing.T) {
	cases := []struct {
		title string
		want  Format3D
	}{
		{"Some.Movie.2012.1080p.3D.FSBS.BluRay.Remux.AVC", Format3DFull},
		{"Some.Movie.2012.1080p.3D.Full-SBS.Remux.AVC", Format3DFull},
		{"Some.Movie.2012.1080p.3D.FOU.Remux.AVC", Format3DFull},
		{"Some.Movie.2012.1080p.3D.HSBS.Remux.AVC", Format3DHalf},
		{"Some.Movie.2012.1080p.3D.SBS.Remux.AVC", Format3DHalf},
		{"Some.Movie.2012.1080p.3D.OU.Remux.AVC", Format3DHalf},
	}
	for _, c := range cases {
		if q := Parse(c.title); q.Format3D != c.want {
			t.Errorf("Parse(%q).Format3D = %v, want %v", c.title, q.Format3D, c.want)
		}
	}
}

// An explicit MVC marker needs no inference and is unaffected by source.
func TestMVCIsAlwaysADiscRip(t *testing.T) {
	for _, title := range []string{
		"Abominable 2019 1080p 3D Multi Language MVC Atmos Remux  zman",
		"Abominable 2019 1080p 3D Blu ray Re Encoded MVC Atmos 7 1 munk",
	} {
		if q := Parse(title); q.Format3D != Format3DBD {
			t.Errorf("Parse(%q).Format3D = %v, want BD3D", title, q.Format3D)
		}
	}
}

// A 3D remux now reads as BD3D, so it passes a "3dfull+" floor. It still does
// not match a bare "3dfull", because a bare token is an exact match and BD3D
// ranks above Full — the same semantics as "720p" vs "720p+".
func Test3DRemuxAgainstFullSpecs(t *testing.T) {
	q := Parse("Life of Pi 2012 1080p 3D Blu ray Remux AVC DTSHD MA 7 1(MKV)")
	if q.Format3D != Format3DBD {
		t.Fatalf("Format3D = %v, want BD3D", q.Format3D)
	}
	floor, err := ParseSpec("3dfull+")
	if err != nil {
		t.Fatal(err)
	}
	if !floor.Matches(q) {
		t.Errorf("%s should match the floor spec \"3dfull+\"", q.String())
	}
	exact, err := ParseSpec("3dfull")
	if err != nil {
		t.Fatal(err)
	}
	if exact.Matches(q) {
		t.Errorf("%s should NOT match the exact spec \"3dfull\" — BD3D outranks Full", q.String())
	}
}

// TestAtmosCarrier pins the Atmos object layer to its carrier. Ranking every
// release that merely says "Atmos" above TrueHD put a lossy DD+ Atmos 5.1
// track above a lossless TrueHD 7.1 one, so a 19 GB DDP-Atmos encode read as
// an upgrade over a 29.5 GB TrueHD copy and replaced it in production.
func TestAtmosCarrier(t *testing.T) {
	tests := []struct {
		title string
		want  Audio
	}{
		// Lossy carrier named: below TrueHD.
		{"Sinners 2025 UHD BluRay 2160p DDP Atmos 5 1 DV x265-hallowed", AudioDDPlusAtmos},
		{"Mutiny 2026 2160p WEB-DL DDP5 1 Atmos SDR H265-AOC", AudioDDPlusAtmos},
		{"Movie 2024 2160p WEB-DL EAC3 Atmos 5 1 x265-GRP", AudioDDPlusAtmos},
		{"Movie 2024 2160p WEB-DL DD+ Atmos x265-GRP", AudioDDPlusAtmos},
		{"Movie 2024 2160p WEB-DL E-AC-3 Atmos x265-GRP", AudioDDPlusAtmos},
		{"Mary Poppins Returns 2018 2160p WEB-DL DV HDR H 265 DDP5 1 Atmos-NoTrace", AudioDDPlusAtmos},

		// Lossless carrier named, either token order: top rank.
		{"Mickey 17 2025 2160p UHD BluRay TrueHD 7 1 Atmos x265-SPHD", AudioAtmos},
		{"Darkest Hour 2017 2160p WEB-DL TrueHD Atmos 7 1 H 265-CHORTLE", AudioAtmos},
		{"Forrest Gump 1994 UHD BluRay 2160p TrueHD 7 1 Atmos DV AV1-RandH", AudioAtmos},

		// Bare "Atmos" keeps the top rank: on releases spelled that way it is
		// virtually always TrueHD Atmos, and guessing otherwise would re-rank
		// far more than the releases actually at issue.
		{"Movie 2024 2160p BluRay Atmos x265-GRP", AudioAtmos},

		// Unaffected neighbours.
		{"Movie 2024 2160p BluRay TrueHD 7 1 x265-GRP", AudioTrueHD},
		{"Movie 2024 1080p BluRay DTS-HD MA 5 1 x264-GRP", AudioDTS},
		{"Movie 2024 1080p WEB-DL DDP5 1 x264-GRP", AudioDolbyDigital},
	}
	for _, tt := range tests {
		if got := Parse(tt.title).Audio; got != tt.want {
			t.Errorf("Parse(%q).Audio = %v, want %v", tt.title, audioNames[got], audioNames[tt.want])
		}
	}
}

// The ordering is what the upgrade decision actually consumes.
func TestAtmosCarrierOrdering(t *testing.T) {
	// The ladder is the rank, not the stored value — the values are frozen by
	// everything already on disk (see the Audio consts).
	if !(AudioDolbyDigital.rank() < AudioDDPlusAtmos.rank() &&
		AudioDDPlusAtmos.rank() < AudioTrueHD.rank() &&
		AudioTrueHD.rank() < AudioAtmos.rank()) {
		t.Fatalf("want DD < DD+ Atmos < TrueHD < Atmos by rank, got %d %d %d %d",
			AudioDolbyDigital.rank(), AudioDDPlusAtmos.rank(),
			AudioTrueHD.rank(), AudioAtmos.rank())
	}

	// The production case: a lossy DD+ Atmos encode must not upgrade over the
	// lossless TrueHD copy already in the library.
	lib := Parse("Sinners 2025 UHD BluRay 2160p TrueHD 7 1 DV x265-GRP")
	inc := Parse("Sinners 2025 UHD BluRay 2160p DDP Atmos 5 1 DV HDR10Plus x265-hallowed")
	if inc.Better(lib) {
		t.Errorf("%s must not be better than %s", inc, lib)
	}
	if !lib.Better(inc) {
		t.Errorf("%s should be better than %s", lib, inc)
	}
}

// Spec tokens address the new rung, and "atmos" no longer matches DD+ Atmos.
func TestAudioSpecTokens(t *testing.T) {
	ddp := Parse("Movie 2024 2160p WEB-DL DDP5 1 Atmos x265")
	trueHDAtmos := Parse("Movie 2024 2160p BluRay TrueHD 7 1 Atmos x265")

	spec, err := ParseSpec("2160p ddp-atmos+")
	if err != nil {
		t.Fatal(err)
	}
	if !spec.Matches(ddp) {
		t.Error("ddp-atmos+ should match a DD+ Atmos release")
	}
	if !spec.Matches(trueHDAtmos) {
		t.Error("ddp-atmos+ is a floor, so TrueHD Atmos should match too")
	}

	strict, err := ParseSpec("2160p atmos")
	if err != nil {
		t.Fatal(err)
	}
	if strict.Matches(ddp) {
		t.Error("an exact atmos spec must no longer match lossy DD+ Atmos")
	}
	if !strict.Matches(trueHDAtmos) {
		t.Error("an exact atmos spec should match TrueHD Atmos")
	}
}

// TestAudioStoredValuesAreFrozen is the guard for the 1.49.0 regression. Audio
// is persisted as an integer in every tracker record, grab record and
// download-log entry, and an upgrade decision compares a stored value against
// a freshly parsed one. Inserting a rung in the middle redefined every record
// already on disk: a stored Atmos started reading as TrueHD and lost to any
// fresh Atmos release, so ~450 films and episodes were re-downloaded as bogus
// upgrades.
//
// These numbers are therefore part of the on-disk format. A new format goes on
// the end and takes its ladder position from audioRank. If this test fails,
// the change is not safe to ship without a data migration.
func TestAudioStoredValuesAreFrozen(t *testing.T) {
	frozen := map[Audio]int{
		AudioUnknown:      0,
		AudioMP3:          1,
		AudioAAC:          2,
		AudioDolbyDigital: 3,
		AudioDTS:          4,
		AudioTrueHD:       5,
		AudioAtmos:        6,
		AudioDDPlusAtmos:  7,
	}
	for a, want := range frozen {
		if int(a) != want {
			t.Errorf("%s = %d, want the frozen on-disk value %d", audioNames[a], int(a), want)
		}
	}
	// Every declared format needs a ladder position, or it silently ranks as
	// unknown and never wins a comparison.
	for a := range frozen {
		if _, ok := audioRank[a]; !ok {
			t.Errorf("%s (%d) has no audioRank entry", audioNames[a], int(a))
		}
	}
	if len(audioRank) != len(frozen) {
		t.Errorf("audioRank has %d entries for %d formats", len(audioRank), len(frozen))
	}
}

// A record written by a newer build carries a value this one does not know.
// It must rank as unknown rather than as its raw integer, or it would beat
// every known format and block all upgrades.
func TestUnknownAudioValueRanksLast(t *testing.T) {
	future := Audio(99)
	if future.rank() != 0 {
		t.Errorf("unknown audio value ranked %d, want 0", future.rank())
	}
	known := Quality{Resolution: Resolutionp1080, Audio: AudioAtmos}
	unknown := Quality{Resolution: Resolutionp1080, Audio: future}
	if unknown.Better(known) {
		t.Error("an unrecognised audio value must not beat a known one")
	}
}

// TestStoredAtmosIsNotBeatenByFreshParse reproduces the exact production
// failure: a record stored before the DD+ Atmos rung existed must still
// compare equal to a fresh parse of the same release, not be beaten by it.
func TestStoredAtmosIsNotBeatenByFreshParse(t *testing.T) {
	// What 1.48.0 wrote for "… Dolby Atmos 7 1 …", read back as integers.
	stored := Quality{
		Format3D: Format3DBD, Resolution: Resolutionp1080,
		Source: SourceBluRay, Audio: Audio(6),
	}
	fresh := Parse("The Lion King 1994 1080p 3D Blu ray MVC Dolby Atmos 7 1 + DTS HD Master 7 1 zman")
	if fresh.Audio != AudioAtmos {
		t.Fatalf("fresh parse audio = %s, want Atmos", audioNames[fresh.Audio])
	}
	if fresh.Better(stored) {
		t.Errorf("fresh %s must not be an upgrade over the stored %s it was written from",
			fresh, stored)
	}
}

// TestCompleteDiscPromotion covers the "COMPLETE BLURAY" → BD3D inference and
// the two ways it used to overreach: an explicitly frame-compatible release
// claimed to be an MVC disc, and a film whose own title contains "Complete"
// promoted on the strength of its name. Both routed side-by-side encodes into
// an MVC-only pipeline and hid them from the pipelines that wanted them.
func TestCompleteDiscPromotion(t *testing.T) {
	tests := []struct {
		title string
		want  Format3D
		why   string
	}{
		// Real disc rips: a generic 3D tag plus the COMPLETE BLURAY label.
		{"The Nightmare Before Christmas 1993 1080p 3D Complete Bluray", Format3DBD, "generic 3D + complete disc"},
		{"Sing 2 2021 1080p 3D Complete Bluray -iND", Format3DBD, "generic 3D + complete disc"},
		{"Gemini Man 3D 2019 MULTi COMPLETE BLURAY GMB", Format3DBD, "uppercase, no resolution"},
		{"Some Movie 2024 1080p 3D COMPLETE.BLURAY.AVC.MVC.DTS-HD.MA", Format3DBD, "explicit MVC as well"},
		{"Some Movie 2024 1080p 3D BluRay COMPLETE x264", Format3DBD, "reversed token order"},
		{"Some Movie 2024 1080p 3D COMPLETE BD50 AVC MVC", Format3DBD, "BD50 spelling"},

		// Frame-compatible: fitting both views in one frame IS a re-encode, so
		// the release cannot also be the disc's MVC stream.
		{"Some Movie 2024 1080p 3D COMPLETE BLURAY FULL-SBS x264-GRP", Format3DFull, "explicit full-SBS"},
		{"Some Movie 2024 1080p 3D FSBS COMPLETE BluRay AVC DTS-HD MA", Format3DFull, "explicit FSBS"},
		{"Some Movie 2024 1080p 3D Half-SBS COMPLETE BluRay x264", Format3DHalf, "explicit half-SBS"},
		{"Some Movie 2024 1080p 3D COMPLETE BLURAY OU x264", Format3DHalf, "over-under"},

		// "Complete" in the film's own title is not a disc label.
		{"A Complete Unknown 2024 1080p 3D FSBS BluRay x264-GRP", Format3DFull, "title word, with a layout tag"},
		{"A Complete Unknown 2024 1080p 3D BluRay x264-GRP", Format3DUnspecified, "title word must not promote; bare 3D states no layout"},

		// A conversion stays a conversion whatever the disc label claims.
		{"Some Movie 2024 1080p 3D-Conv COMPLETE BLURAY x264", Format3DConv, "conversion marker wins"},
	}
	for _, tt := range tests {
		if got := Parse(tt.title).Format3D; got != tt.want {
			t.Errorf("Parse(%q).Format3D = %s, want %s (%s)",
				tt.title, format3DNames[got], format3DNames[tt.want], tt.why)
		}
	}
}

// The gate the mvc pipeline actually uses: an SBS encode must not satisfy a
// bd3d spec, and a real disc rip must.
func TestCompleteDiscAgainstBD3DSpec(t *testing.T) {
	spec, err := ParseSpec("bd3d")
	if err != nil {
		t.Fatal(err)
	}
	sbs := Parse("Some Movie 2024 1080p 3D COMPLETE BLURAY FULL-SBS x264-GRP")
	disc := Parse("Sing 2 2021 1080p 3D Complete Bluray -iND")
	if spec.Matches(sbs) {
		t.Errorf("a full-SBS encode (%s) must not satisfy spec bd3d", sbs)
	}
	if !spec.Matches(disc) {
		t.Errorf("a complete disc rip (%s) should satisfy spec bd3d", disc)
	}
}

// TestSourceStoredValuesAreFrozen is the Source counterpart of
// TestAudioStoredValuesAreFrozen, and exists for the same reason: Source is
// persisted as an integer in every tracker record, grab record and
// download-log entry, and an upgrade decision compares a stored value against
// a freshly parsed one. Inserting SourceReEncode in its ladder position —
// between WebDL and BluRay, where it belongs — would have renumbered BluRay
// and Remux, so every disc rip already on disk would have read as one rung
// lower and lost to any fresh release. That is exactly the 1.49.0 regression,
// which cost ~450 bogus re-downloads.
//
// These numbers are part of the on-disk format. A new source goes on the end
// and takes its ladder position from sourceRank.
func TestSourceStoredValuesAreFrozen(t *testing.T) {
	frozen := map[Source]int{
		SourceUnknown:  0,
		SourceCAM:      1,
		SourceTS:       2,
		SourceSCR:      3,
		SourceDVDRip:   4,
		SourceTVRip:    5,
		SourceHDTV:     6,
		SourceWEBRip:   7,
		SourceWebDL:    8,
		SourceBluRay:   9,
		SourceRemux:    10,
		SourceReEncode: 11,
	}
	for s, want := range frozen {
		if int(s) != want {
			t.Errorf("%s = %d, want the frozen on-disk value %d", sourceNames[s], int(s), want)
		}
	}
	for s := range frozen {
		if _, ok := sourceRank[s]; !ok {
			t.Errorf("%s (%d) has no sourceRank entry", sourceNames[s], int(s))
		}
	}
	if len(sourceRank) != len(frozen) {
		t.Errorf("sourceRank has %d entries for %d sources", len(sourceRank), len(frozen))
	}
}

// The ladder position, as distinct from the stored value: a re-encode sits
// below both disc tiers and above the web tiers.
func TestSourceReEncodeLadderPosition(t *testing.T) {
	if !(sourceRank[SourceWebDL] < sourceRank[SourceReEncode]) {
		t.Error("a re-encode should outrank WEB-DL — the material still came off a disc")
	}
	if !(sourceRank[SourceReEncode] < sourceRank[SourceBluRay]) {
		t.Error("a re-encode should rank below BluRay")
	}
	if !(sourceRank[SourceBluRay] < sourceRank[SourceRemux]) {
		t.Error("appending the rung must not disturb BluRay < Remux")
	}
}

// A record written by a newer build carries a Source this one does not know,
// and must rank as unknown rather than as its raw integer — otherwise it
// would beat every known source and block all upgrades.
func TestUnknownSourceRanksAsUnknown(t *testing.T) {
	future := Source(99)
	if future.rank() != 0 {
		t.Errorf("Source(99).rank() = %d, want 0", future.rank())
	}
	known := Quality{Source: SourceWebDL}
	if (Quality{Source: future}).Better(known) {
		t.Error("an unknown source must not beat a known one")
	}
}

func TestParseReEncodedDemotesDiscTiers(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  Source
	}{
		// The release that prompted this: named after the disc, but a
		// side-by-side re-encode of it.
		{"complete bluray full-sbs re-encode",
			"Some.Film.2016.3D.COMPLETE.BLURAY.FULL-SBS.RE-ENCODE-GROUP", SourceReEncode},
		{"reencoded one word",
			"Some.Film.2016.1080p.BluRay.REENCODED-GROUP", SourceReEncode},
		{"re-enc abbreviated",
			"Some.Film.2016.1080p.BluRay.RE-ENC-GROUP", SourceReEncode},
		{"spaced re encode",
			"Some Film 2016 1080p BluRay Re Encode", SourceReEncode},
		{"a remux claiming a re-encode is still demoted",
			"Some.Film.2016.1080p.Remux.Re-Encoded-GROUP", SourceReEncode},

		// A plain "encode" says only that the release is an encode, which
		// every BluRay rip is. Matching it would demote the whole library.
		{"plain encode is not a re-encode",
			"Some.Film.2016.1080p.BluRay.x264.ENCODE-GROUP", SourceBluRay},
		{"encoder credit is not a re-encode",
			"Some.Film.2016.1080p.BluRay.x264-ENCODER", SourceBluRay},

		// An explicit claim of losslessness wins over the marker, for the
		// same reason an explicit 3D layout marker beats an inference.
		// Untouched blocks the demotion, and then the lossless promotion
		// applies for the same reason — the video was copied, not re-encoded.
		{"untouched video with re-encoded audio is not demoted",
			"Some.Film.2016.COMPLETE.BLURAY.UNTOUCHED.video.re-encoded.audio", SourceRemux},

		// Tiers below the disc are left alone.
		{"a web-dl re-encode is already below the rung",
			"Some.Film.2016.1080p.WEB-DL.Re-Encode-GROUP", SourceWebDL},
		{"untouched is itself a claim of losslessness",
			"Some.Film.2016.1080p.BluRay.UNTOUCHED-GROUP", SourceRemux},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.title).Source; got != tc.want {
				t.Errorf("Source = %s (%d), want %s (%d)",
					sourceNames[got], int(got), sourceNames[tc.want], int(tc.want))
			}
		})
	}
}

// The demotion runs before the Format3D rules on purpose. Those promote a
// "COMPLETE BLURAY" carrying a 3D marker to BD3D on the reasoning that a disc
// rip does not re-encode — which an explicit RE-ENCODE marker refutes. The
// frame-compatible guard added in 1.50.1 closed the SBS case; a re-encode with
// no layout marker was still being claimed as an MVC disc.
func TestReEncodedIsNotPromotedToBD3D(t *testing.T) {
	q := Parse("Some.Film.2016.3D.COMPLETE.BLURAY.RE-ENCODE-GROUP")
	if q.Format3D == Format3DBD {
		t.Errorf("a re-encode was promoted to BD3D (Format3D=%v); MVC discs are not re-encoded", q.Format3D)
	}
	if q.Source != SourceReEncode {
		t.Errorf("Source = %s, want ReEncode", sourceNames[q.Source])
	}
	// The genuine article still gets the promotion.
	if got := Parse("Some.Film.2016.3D.COMPLETE.BLURAY-GROUP"); got.Format3D != Format3DBD {
		t.Errorf("an unmarked COMPLETE BLURAY lost its BD3D promotion (Format3D=%v)", got.Format3D)
	}
}

// The point of the whole change: a re-encode must not read as an upgrade over
// a disc rip already on disk, and must not be blocked from losing to one.
func TestReEncodeDoesNotBeatADiscRip(t *testing.T) {
	stored := Parse("Some.Film.2016.1080p.BluRay.x264-GROUP")
	reenc := Parse("Some.Film.2016.1080p.COMPLETE.BLURAY.FULL-SBS.RE-ENCODE-GROUP")

	if reenc.Better(stored) {
		t.Error("a re-encode must not beat a stored BluRay rip")
	}
	if !stored.Better(reenc) {
		t.Error("a BluRay rip should beat a re-encode")
	}
	// And a re-encode still beats the web tiers, so it is not written off.
	if web := Parse("Some.Film.2016.1080p.WEB-DL.x264-GROUP"); !reenc.Better(web) {
		t.Error("a re-encode should still beat WEB-DL")
	}
}

// A `bluray+` spec must reject a re-encode, since the rung sits below BluRay.
func TestSpecRejectsReEncode(t *testing.T) {
	spec, err := ParseSpec("bluray+")
	if err != nil {
		t.Fatalf("ParseSpec: %v", err)
	}
	reenc := Parse("Some.Film.2016.1080p.COMPLETE.BLURAY.RE-ENCODE-GROUP")
	if spec.Matches(reenc) {
		t.Error("bluray+ should reject a re-encode")
	}
	if disc := Parse("Some.Film.2016.1080p.BluRay.x264-GROUP"); !spec.Matches(disc) {
		t.Error("bluray+ should still accept a BluRay rip")
	}
}

// The lossless half: a complete disc rip is the disc, not a lossy encode of
// one, so it belongs on the remux rung rather than below it.
func TestParseCompleteDiscIsLossless(t *testing.T) {
	tests := []struct {
		name  string
		title string
		want  Source
	}{
		{"complete bluray", "Some.Film.2016.COMPLETE.BLURAY-GROUP", SourceRemux},
		{"complete bd50", "Some.Film.2016.COMPLETE.BD50-GROUP", SourceRemux},
		{"bluray complete, reversed", "Some.Film.2016.BLURAY.COMPLETE-GROUP", SourceRemux},
		{"untouched", "Some.Film.2016.1080p.BluRay.UNTOUCHED-GROUP", SourceRemux},

		// An ordinary encode is untouched by this: it really is the BluRay tier.
		{"a plain bluray encode is unchanged", "Some.Film.2016.1080p.BluRay.x264-GROUP", SourceBluRay},
		// "A Complete Unknown" — the word in a film's own title must not promote
		// it. reCompleteDisc requires the two tokens adjacent.
		{"the word complete in a title", "A.Complete.Unknown.2024.1080p.BluRay.x264-GROUP", SourceBluRay},

		// A frame-compatible layout exists only as a re-encode, so such a
		// release is not the disc whatever its name borrows.
		{"complete bluray full-sbs is not the disc",
			"Some.Film.2016.3D.COMPLETE.BLURAY.FULL-SBS.x264-GROUP", SourceBluRay},
		// And an explicit re-encode marker still wins outright.
		{"complete bluray re-encode is demoted, not promoted",
			"Some.Film.2016.COMPLETE.BLURAY.RE-ENCODE-GROUP", SourceReEncode},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.title).Source; got != tc.want {
				t.Errorf("Source = %s (%d), want %s (%d)",
					sourceNames[got], int(got), sourceNames[tc.want], int(tc.want))
			}
		})
	}
}

// The promotion must not cost a complete 3D disc its BD3D inference: that rule
// fires on SourceBluRay, and a promoted release satisfies the SourceRemux rule
// instead. Both paths must still land on BD3D.
func TestPromotionKeepsTheBD3DInference(t *testing.T) {
	for _, title := range []string{
		"Some Movie 2024 1080p 3D COMPLETE BLURAY AVC DTS-HD MA",
		"The Nightmare Before Christmas 1993 1080p 3D Complete Bluray",
	} {
		q := Parse(title)
		if q.Format3D != Format3DBD {
			t.Errorf("%q: Format3D = %v, want BD3D", title, q.Format3D)
		}
		if q.Source != SourceRemux {
			t.Errorf("%q: Source = %s, want Remux", title, sourceNames[q.Source])
		}
	}
	// A conversion stays a conversion — the promotion must not lift it.
	if got := Parse("Movie.2020.3DCONV.COMPLETE.BluRay").Format3D; got != Format3DConv {
		t.Errorf("Format3D = %v, want Conv", got)
	}
}

// TestFormat3DStoredValuesAreFrozen guards the on-disk format the way its
// Audio and Source counterparts do. Format3D is persisted in every tracker
// record, and it is the FIRST dimension Better compares when both releases
// are 3D — so renumbering it would redefine every 3D record at once.
// Unspecified is therefore appended, not placed between Conv and Half where
// it reads most naturally.
func TestFormat3DStoredValuesAreFrozen(t *testing.T) {
	frozen := map[Format3D]int{
		Format3DNone:        0,
		Format3DConv:        1,
		Format3DHalf:        2,
		Format3DFull:        3,
		Format3DBD:          4,
		Format3DUnspecified: 5,
	}
	for f, want := range frozen {
		if int(f) != want {
			t.Errorf("%s = %d, want the frozen on-disk value %d", format3DNames[f], int(f), want)
		}
	}
	for f := range frozen {
		if _, ok := format3DRank[f]; !ok {
			t.Errorf("%s (%d) has no format3DRank entry", format3DNames[f], int(f))
		}
		if _, ok := format3DLayouts[f]; !ok {
			t.Errorf("%s (%d) has no layout name", format3DNames[f], int(f))
		}
	}
	if len(format3DRank) != len(frozen) || len(format3DLayouts) != len(frozen) {
		t.Errorf("rank has %d and layouts %d entries for %d formats",
			len(format3DRank), len(format3DLayouts), len(frozen))
	}
}

// Unspecified shares Half's rank on purpose: "nobody said" must not read as
// better or worse than "they said half".
func TestUnspecifiedRanksWithHalf(t *testing.T) {
	if format3DRank[Format3DUnspecified] != format3DRank[Format3DHalf] {
		t.Fatal("Unspecified must rank with Half, so it can neither win nor lose an upgrade against one")
	}
	// It still sits above a conversion and below an explicit full/MVC.
	if !(format3DRank[Format3DConv] < format3DRank[Format3DUnspecified]) {
		t.Error("Unspecified should outrank a 2D-to-3D conversion")
	}
	if !(format3DRank[Format3DUnspecified] < format3DRank[Format3DFull]) {
		t.Error("Unspecified should rank below an explicit full-resolution release")
	}
	if !(format3DRank[Format3DFull] < format3DRank[Format3DBD]) {
		t.Error("appending must not disturb Full < BD3D")
	}

	// And the comparison itself: neither direction is an upgrade.
	unspec := Quality{Format3D: Format3DUnspecified, Resolution: Resolutionp1080}
	half := Quality{Format3D: Format3DHalf, Resolution: Resolutionp1080}
	if unspec.Better(half) || half.Better(unspec) {
		t.Error("neither Unspecified nor Half may beat the other on Format3D alone")
	}
}

// A record from a newer build must rank as unknown, not as its raw integer —
// otherwise it would beat BD3D and block every upgrade.
func TestUnknownFormat3DRanksAsUnknown(t *testing.T) {
	future := Format3D(99)
	if future.rank() != 0 {
		t.Errorf("Format3D(99).rank() = %d, want 0", future.rank())
	}
}

// The heart of the change: an explicit layout marker is a claim, a bare "3D"
// is silence, and the two must not be the same value.
func TestBare3DIsUnspecifiedNotHalf(t *testing.T) {
	tests := []struct {
		title string
		want  Format3D
		why   string
	}{
		// The release that prompted this: a 49 GB MVC disc that read as half.
		{"Pacific Rim Uprising 2018 3D BluRay 1080p AVC Atmos TrueHD7 1 MTeam",
			Format3DUnspecified, "bare 3D states no layout"},
		{"Avatar 2009 3D 1080p BluRay x264", Format3DUnspecified, "bare 3D"},

		// Explicit markers are claims and still win.
		{"Movie 2024 1080p 3D HSBS BluRay x264", Format3DHalf, "HSBS is explicit"},
		{"Movie 2024 1080p 3D Half-SBS BluRay x264", Format3DHalf, "half-SBS is explicit"},
		{"Movie 2024 1080p 3D OU BluRay x264", Format3DHalf, "over-under is frame-compatible"},
		{"Movie 2024 1080p 3D FSBS BluRay x264", Format3DFull, "FSBS is explicit"},
		{"Movie 2024 1080p 3D MVC BluRay", Format3DBD, "MVC is explicit"},
		{"Movie 2024 1080p BD3D BluRay", Format3DBD, "BD3D is explicit"},

		// Order must not matter: the explicit marker wins wherever it appears.
		{"IMAX 3D FSBS Movie 2024 1080p BluRay", Format3DFull, "explicit marker before or after the bare 3D"},
		{"Movie 2024 1080p FSBS 3D BluRay", Format3DFull, "ditto, reversed"},

		// A conversion is still a conversion.
		{"Movie 2024 1080p 3D-Conv BluRay x264", Format3DConv, "conversion marker wins over silence"},

		// Not 3D at all stays None.
		{"Movie 2024 1080p BluRay x264", Format3DNone, "no 3D marker"},
	}
	for _, tc := range tests {
		t.Run(tc.why, func(t *testing.T) {
			if got := Parse(tc.title).Format3D; got != tc.want {
				t.Errorf("Parse(%q).Format3D = %s, want %s",
					tc.title, format3DNames[got], format3DNames[tc.want])
			}
		})
	}
}

// The existing disc promotions must still fire for an unspecified layout —
// that is how "3D COMPLETE BLURAY" and a bare-3D remux become BD3D. They are
// gated on rank > Conv, which Unspecified satisfies.
func TestUnspecifiedStillPromotesOnDiscEvidence(t *testing.T) {
	for _, tc := range []struct{ title, why string }{
		{"Some Movie 2024 1080p 3D COMPLETE BLURAY AVC DTS-HD MA", "complete disc"},
		{"Life of Pi 2012 1080p 3D Blu ray Remux AVC DTS-HD MA 7.1", "bare-3D remux"},
	} {
		if got := Parse(tc.title).Format3D; got != Format3DBD {
			t.Errorf("%s: Format3D = %s, want BD3D", tc.why, format3DNames[got])
		}
	}
}

// Layout() is the vocabulary route and condition expressions use, and it has
// to match probe_3d_layout's so a rule can compare name against disc.
func TestFormat3DLayoutNames(t *testing.T) {
	for f, want := range map[Format3D]string{
		Format3DNone:        "",
		Format3DConv:        "conv",
		Format3DHalf:        "half",
		Format3DFull:        "full",
		Format3DBD:          "mvc",
		Format3DUnspecified: "unspecified",
	} {
		if got := f.Layout(); got != want {
			t.Errorf("%s.Layout() = %q, want %q", format3DNames[f], got, want)
		}
	}
}

// A bd3d spec must still refuse an unspecified layout — we do not know it is
// MVC, and that is exactly what a probe is for.
func TestSpecsAgainstUnspecified(t *testing.T) {
	unspec := Parse("Pacific Rim Uprising 2018 3D BluRay 1080p AVC Atmos TrueHD7 1 MTeam")
	for _, tc := range []struct {
		spec string
		want bool
		why  string
	}{
		{"bd3d", false, "bd3d must not accept an unproven layout"},
		{"3dfull", false, "3dfull must not accept an unproven layout"},
	} {
		s, err := ParseSpec(tc.spec)
		if err != nil {
			t.Fatalf("ParseSpec(%q): %v", tc.spec, err)
		}
		if got := s.Matches(unspec); got != tc.want {
			t.Errorf("%s: spec %q matched=%v, want %v", tc.why, tc.spec, got, tc.want)
		}
	}
}
