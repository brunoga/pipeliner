package quality

import (
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
			"No.Quality.Markers.At.All",
			Quality{},
		},
		{
			"Avatar.2009.3D.1080p.BluRay.x264",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Codec: CodecH264, Format3D: Format3DHalf},
		},
		{
			"Avatar.2009.HSBS.1080p.BluRay",
			Quality{Resolution: Resolutionp1080, Source: SourceBluRay, Format3D: Format3DHalf},
		},
		{
			"Avatar.2009.SBS.1080p.BluRay",
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
	cases := []struct{ title string; want Source }{
		{"Movie.BluRay.x264", SourceBluRay},
		{"Movie.Blu-Ray.x264", SourceBluRay},
		{"Movie.BDRip.x264", SourceBluRay},
		{"Movie.BDRemux.x264", SourceRemux},
		{"Movie.Remux.x264", SourceRemux},
		{"Movie.WEB-DL.x264", SourceWebDL},
		{"Movie.WEBDL.x264", SourceWebDL},
		{"Movie.WEBRip.x264", SourceWEBRip},
		{"Movie.HDTV.x264", SourceHDTV},
		{"Movie.DVDRip.XviD", SourceDVDRip},
		{"Movie.TVRip.XviD", SourceTVRip},
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
	cases := []struct{ title string; want Codec }{
		{"Movie.x265", CodecH265},
		{"Movie.H.265", CodecH265},
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
	cases := []struct{ title string; want Audio }{
		{"Movie.Atmos", AudioAtmos},
		{"Movie.TrueHD", AudioTrueHD},
		{"Movie.DTS-HD", AudioDTS},
		{"Movie.DTS-MA", AudioDTS},
		{"Movie.DTS", AudioDTS},
		{"Movie.DD5.1", AudioDolbyDigital},
		{"Movie.DD+5.1", AudioDolbyDigital},
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
		title   string
		want    Format3D
	}{
		// 3D + COMPLETE BluRay → BD3D (full disc rip).
		{"Spider.Man.Into.the.Spider.Verse.2018.3D.COMPLETE.BluRay", Format3DBD},
		{"Avatar.2009.3D.BluRay.COMPLETE", Format3DBD},
		// 3DCONV + COMPLETE BluRay → still Conv (conversion stays lowest tier).
		{"Movie.2020.3DCONV.COMPLETE.BluRay", Format3DConv},
		// 3D + COMPLETE but not BluRay → no elevation.
		{"Movie.2020.3D.COMPLETE.WEBRip", Format3DHalf},
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
		{"Avatar.2009.3D.SBS.1080p.BluRay", Format3DFull},
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
	bad1 := Quality{Resolution: Resolutionp480, Source: SourceWebDL}  // res too low
	bad2 := Quality{Resolution: Resolutionp720, Source: SourceTVRip}  // source too low

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
	cases := []struct{ s string; want Resolution }{
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
	cases := []struct{ s string; want Source }{
		{"dvdrip", SourceDVDRip},
		{"tvrip", SourceTVRip},
		{"hdtv", SourceHDTV},
		{"webrip", SourceWEBRip},
		{"webdl", SourceWebDL},
		{"web-dl", SourceWebDL},
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
	cases := []struct{ s string; want Codec }{
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
	cases := []struct{ s string; want Audio }{
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
	cases := []struct{ s string; want ColorRange }{
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
