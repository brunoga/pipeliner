// Package quality parses video quality attributes from media release titles
// and provides a multi-dimensional comparison and filter-spec system.
//
// A Spec is built from a human-readable string such as "720p-1080p webrip+"
// where each space-separated token constrains one quality dimension. The "+"
// suffix means "this value or better" (sets only the minimum, no upper bound).
package quality

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Resolution represents the video resolution tier.
type Resolution int

const (
	ResolutionUnknown Resolution = iota
	ResolutionSD                 // 480p and below, no explicit marker
	Resolutionp480
	Resolutionp576
	Resolutionp720
	Resolutionp1080
	Resolutionp2160
)

var resolutionNames = map[Resolution]string{
	ResolutionUnknown: "",
	ResolutionSD:      "SD",
	Resolutionp480:    "480p",
	Resolutionp576:    "576p",
	Resolutionp720:    "720p",
	Resolutionp1080:   "1080p",
	Resolutionp2160:   "2160p",
}

// Source represents the release source (distribution medium).
type Source int

const (
	SourceUnknown Source = iota
	SourceCAM    // CAM, HDCAM — recorded inside a cinema
	SourceTS     // TS, HDTS, TC, HDTC — telesync / telecine
	SourceSCR    // SCR, Screener, DVDScr — pre-release copy
	SourceDVDRip
	SourceTVRip
	SourceHDTV
	SourceWEBRip
	SourceWebDL
	SourceBluRay
	SourceRemux
)

var sourceNames = map[Source]string{
	SourceUnknown: "",
	SourceCAM:     "CAM",
	SourceTS:      "TS",
	SourceSCR:     "SCR",
	SourceDVDRip:  "DVDRip",
	SourceTVRip:   "TVRip",
	SourceHDTV:    "HDTV",
	SourceWEBRip:  "WEBRip",
	SourceWebDL:   "WEB-DL",
	SourceBluRay:  "BluRay",
	SourceRemux:   "Remux",
}

// Codec represents the video codec.
type Codec int

const (
	CodecUnknown Codec = iota
	CodecXviD
	CodecDivX
	CodecH264
	CodecH265
	CodecAV1
)

var codecNames = map[Codec]string{
	CodecUnknown: "",
	CodecXviD:    "XviD",
	CodecDivX:    "DivX",
	CodecH264:    "H.264",
	CodecH265:    "H.265",
	CodecAV1:     "AV1",
}

// Audio represents the audio format.
type Audio int

const (
	AudioUnknown Audio = iota
	AudioMP3
	AudioAAC
	AudioDolbyDigital
	AudioDTS
	AudioTrueHD
	AudioAtmos
)

var audioNames = map[Audio]string{
	AudioUnknown:      "",
	AudioMP3:          "MP3",
	AudioAAC:          "AAC",
	AudioDolbyDigital: "Dolby Digital",
	AudioDTS:          "DTS",
	AudioTrueHD:       "TrueHD",
	AudioAtmos:        "Atmos",
}

// ColorRange represents the HDR/SDR colour range.
type ColorRange int

const (
	ColorRangeUnknown    ColorRange = iota
	ColorRangeSDR
	ColorRangeHDR
	ColorRangeHDR10
	ColorRangeDolbyVision
)

var colorRangeNames = map[ColorRange]string{
	ColorRangeUnknown:     "",
	ColorRangeSDR:         "SDR",
	ColorRangeHDR:         "HDR",
	ColorRangeHDR10:       "HDR10",
	ColorRangeDolbyVision: "Dolby Vision",
}

// Format3D represents the 3D encoding format of a video release.
// A zero value (Format3DNone) means the release is not 3D.
// When two 3D releases are compared, Format3D takes precedence over all
// other quality dimensions; the remaining dims act as tie-breakers.
type Format3D int

const (
	Format3DNone Format3D = iota // not 3D
	Format3DConv                 // 3D-CONV — artificially converted from a 2D source
	Format3DHalf                 // half-resolution: HSBS, HOU, HALF-SBS, HALF-OU, plain 3D
	Format3DFull                 // full-resolution: SBS, FSBS, OU, FOU, FULL-SBS, FULL-OU
	Format3DBD                   // BD3D — Blu-ray 3D rip, highest quality
)

var format3DNames = map[Format3D]string{
	Format3DNone: "",
	Format3DConv: "3D-Conv",
	Format3DHalf: "3D-Half",
	Format3DFull: "3D-Full",
	Format3DBD:   "BD3D",
}

// Quality holds one value per dimension parsed from a release title.
// A zero value for any dimension means "not detected / any".
type Quality struct {
	Resolution Resolution
	Source     Source
	Codec      Codec
	Audio      Audio
	ColorRange ColorRange
	Format3D   Format3D
}

// ResolutionName returns the human-readable resolution name (e.g. "1080p"), or "" if unknown.
func (q Quality) ResolutionName() string { return resolutionNames[q.Resolution] }

// SourceName returns the human-readable source name (e.g. "BluRay"), or "" if unknown.
func (q Quality) SourceName() string { return sourceNames[q.Source] }

// MarshalJSON serialises Quality including a precomputed "string" field so
// the UI can display it without reconstructing the string from numeric codes.
// The field is omitted when no quality dimensions were detected ("unknown").
func (q Quality) MarshalJSON() ([]byte, error) {
	type plain Quality
	s := q.String()
	if s == "unknown" {
		s = ""
	}
	return json.Marshal(struct {
		plain
		String string `json:"string,omitempty"`
	}{plain: plain(q), String: s})
}

// String returns a human-readable summary, omitting unknown dimensions.
func (q Quality) String() string {
	var parts []string
	for _, s := range []string{
		format3DNames[q.Format3D],
		resolutionNames[q.Resolution],
		sourceNames[q.Source],
		codecNames[q.Codec],
		audioNames[q.Audio],
		colorRangeNames[q.ColorRange],
	} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	if len(parts) == 0 {
		return "unknown"
	}
	return strings.Join(parts, " ")
}

// Better reports whether q is strictly better than other.
//
// When both qualities are 3D (Format3D != Format3DNone), Format3D is the
// primary discriminator and the remaining dimensions act as tie-breakers.
// When either quality is non-3D the Format3D dimension is skipped and the
// existing order applies: Resolution > Source > Codec > Audio > ColorRange.
func (q Quality) Better(other Quality) bool {
	if q.Format3D != Format3DNone && other.Format3D != Format3DNone {
		if q.Format3D != other.Format3D {
			return q.Format3D > other.Format3D
		}
	}
	if q.Resolution != other.Resolution {
		return q.Resolution > other.Resolution
	}
	if q.Source != other.Source {
		return q.Source > other.Source
	}
	if q.Codec != other.Codec {
		return q.Codec > other.Codec
	}
	if q.Audio != other.Audio {
		return q.Audio > other.Audio
	}
	return q.ColorRange > other.ColorRange
}

// --- compiled regexes for Parse ---

var (
	reResolution = regexp.MustCompile(`(?i)\b(4k|2160p|1080p|720p|576p|480p)\b`)
	reSource     = regexp.MustCompile(`(?i)\b(remux|blu[\-\s]?ray|bdrip|bdremux|bd(?:25|50|100)|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip|hd[\-]?cam|camrip|cam\b|hd[\-]?ts|telesync|hd[\-]?tc|telecine|\bts\b|\btc\b|dvd[\-]?scr(?:eener)?|bd[\-]?scr|screener|\bscr\b)\b`)
	reCodec      = regexp.MustCompile(`(?i)\b(av1|x265|h\.?265|hevc|x264|h\.?264|xvid|divx)\b`)
	reColorRange = regexp.MustCompile(`(?i)\b(dolby[\s\.]?vision|dv\b|hdr10[\+]?|hdr|sdr)\b`)
	// re3DConv matches 3D-conversion tags; checked before re3D so it always wins.
	re3DConv = regexp.MustCompile(`(?i)(?:\b3D[\-]?CONV(?:ERT)?\b|\bCONVERT\b)`)
	// re3D matches native 3D format markers; longer alternatives are listed first.
	// MVC (Multiview Video Coding) is the Blu-ray 3D codec — always BD quality.
	re3D = regexp.MustCompile(`(?i)\b(BD3D|MVC|FULL[\-]?SBS|FULL[\-]?OU|FSBS|F-SBS|FOU|F-OU|HALF[\-]?SBS|HALF[\-]?OU|HSBS|H-SBS|HOU|H-OU|SBS|OU|3D)\b`)
	// reComplete matches "COMPLETE" disc-rip labels; combined with a BluRay source
	// and any non-conv 3D marker this implies a full BD3D disc rip.
	reComplete = regexp.MustCompile(`(?i)\bCOMPLETE\b`)

	// Audio regexes checked in priority order (highest first).
	reAudioPatterns = []struct {
		re  *regexp.Regexp
		val Audio
	}{
		{regexp.MustCompile(`(?i)\batmos\b`), AudioAtmos},
		{regexp.MustCompile(`(?i)\btruehd\b`), AudioTrueHD},
		{regexp.MustCompile(`(?i)\bdts[\-\s]?(hd|ma)\b`), AudioDTS},
		{regexp.MustCompile(`(?i)\bdts\b`), AudioDTS},
		{regexp.MustCompile(`(?i)\bdd[\+]?5\.1\b`), AudioDolbyDigital},
		{regexp.MustCompile(`(?i)\bdolby[\s\.]?digital\b`), AudioDolbyDigital},
		{regexp.MustCompile(`(?i)\baac\b`), AudioAAC},
		{regexp.MustCompile(`(?i)\bmp3\b`), AudioMP3},
	}
)

// Parse extracts quality attributes from a release title string.
// Unrecognised dimensions are left at their zero value (Unknown).
func Parse(title string) Quality {
	var q Quality

	if m := reResolution.FindString(title); m != "" {
		switch strings.ToLower(m) {
		case "4k", "2160p":
			q.Resolution = Resolutionp2160
		case "1080p":
			q.Resolution = Resolutionp1080
		case "720p":
			q.Resolution = Resolutionp720
		case "576p":
			q.Resolution = Resolutionp576
		case "480p":
			q.Resolution = Resolutionp480
		}
	}

	if m := reSource.FindString(title); m != "" {
		ml := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(m, "-", ""), " ", ""))
		switch {
		case strings.Contains(ml, "remux") || strings.Contains(ml, "bdremux"):
			q.Source = SourceRemux
		case strings.Contains(ml, "bluray") || strings.Contains(ml, "bdrip") ||
			ml == "bd25" || ml == "bd50" || ml == "bd100":
			q.Source = SourceBluRay
		case strings.Contains(ml, "webdl"):
			q.Source = SourceWebDL
		case strings.Contains(ml, "webrip"):
			q.Source = SourceWEBRip
		case strings.Contains(ml, "hdtv"):
			q.Source = SourceHDTV
		case strings.Contains(ml, "dvdrip"):
			q.Source = SourceDVDRip
		case strings.Contains(ml, "tvrip"):
			q.Source = SourceTVRip
		// Theater-recorded and pre-release sources — lowest quality tier.
		case strings.Contains(ml, "hdcam") || ml == "camrip" || ml == "cam":
			q.Source = SourceCAM
		case strings.Contains(ml, "hdts") || strings.Contains(ml, "telesync") ||
			strings.Contains(ml, "hdtc") || strings.Contains(ml, "telecine") || ml == "ts" || ml == "tc":
			q.Source = SourceTS
		case strings.Contains(ml, "screener") || strings.Contains(ml, "dvdscr") ||
			strings.Contains(ml, "bdscr") || ml == "scr":
			q.Source = SourceSCR
		}
	}

	if m := reCodec.FindString(title); m != "" {
		ml := strings.ToLower(strings.ReplaceAll(m, ".", ""))
		switch {
		case ml == "av1":
			q.Codec = CodecAV1
		case ml == "x265" || ml == "h265" || ml == "hevc":
			q.Codec = CodecH265
		case ml == "x264" || ml == "h264":
			q.Codec = CodecH264
		case ml == "xvid":
			q.Codec = CodecXviD
		case ml == "divx":
			q.Codec = CodecDivX
		}
	}

	for _, p := range reAudioPatterns {
		if p.re.MatchString(title) {
			q.Audio = p.val
			break
		}
	}

	if m := reColorRange.FindString(title); m != "" {
		ml := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(m, " ", ""), "-", ""))
		switch {
		case strings.Contains(ml, "dolbyvision") || ml == "dv":
			q.ColorRange = ColorRangeDolbyVision
		case strings.Contains(ml, "hdr10"):
			q.ColorRange = ColorRangeHDR10
		case strings.Contains(ml, "hdr"):
			q.ColorRange = ColorRangeHDR
		case ml == "sdr":
			q.ColorRange = ColorRangeSDR
		}
	}

	// 3DCONV overrides any other 3D format marker — a converted release stays
	// at the lowest 3D tier regardless of the packaging format (SBS, OU, etc.).
	if re3DConv.MatchString(title) {
		q.Format3D = Format3DConv
	} else {
		// Scan all native 3D markers and keep the highest-quality one.
		// A title like "IMAX 3D FSBS" has both "3D" (Half) and "FSBS" (Full);
		// the explicit format tag should win over the generic "3D" label.
		for _, m := range re3D.FindAllString(title, -1) {
			ml := strings.ToUpper(strings.ReplaceAll(m, "-", ""))
			var f Format3D
			switch ml {
			case "BD3D", "MVC":
				f = Format3DBD
			case "SBS", "FSBS", "FOU", "OU", "FULLSBS", "FULLOU":
				f = Format3DFull
			default: // HSBS, HOU, HALFSBS, HALFOU, 3D
				f = Format3DHalf
			}
			if f > q.Format3D {
				q.Format3D = f
			}
		}
	}
	// "COMPLETE BluRay" with a non-conv 3D marker means the full Blu-ray 3D disc
	// was ripped, which is always BD3D quality regardless of the 3D tag used.
	if q.Format3D > Format3DConv && q.Source == SourceBluRay && reComplete.MatchString(title) {
		q.Format3D = Format3DBD
	}
	// 3D releases without an explicit resolution tag are assumed to be
	// at least 1080p — sub-HD 3D releases are effectively non-existent.
	if q.Format3D != Format3DNone && q.Resolution == ResolutionUnknown {
		q.Resolution = Resolutionp1080
	}

	return q
}

// --- Spec: filter spec for quality ranges ---

// Spec defines an accepted quality range per dimension.
// A zero min means "any"; a zero max means "no upper bound".
// When MinFormat3D is non-zero, non-3D entries (Format3D == Format3DNone) are
// rejected — specifying any 3D token implicitly requires 3D content.
type Spec struct {
	MinResolution, MaxResolution Resolution
	MinSource, MaxSource         Source
	MinCodec, MaxCodec           Codec
	MinAudio, MaxAudio           Audio
	MinColorRange, MaxColorRange ColorRange
	MinFormat3D, MaxFormat3D     Format3D
}

// Matches reports whether q falls within every constrained dimension of s.
// Dimensions where both min and max are zero are unconstrained.
func (s Spec) Matches(q Quality) bool {
	if s.MinResolution > 0 && q.Resolution < s.MinResolution {
		return false
	}
	if s.MaxResolution > 0 && q.Resolution > s.MaxResolution {
		return false
	}
	if s.MinSource > 0 && q.Source < s.MinSource {
		return false
	}
	if s.MaxSource > 0 && q.Source > s.MaxSource {
		return false
	}
	if s.MinCodec > 0 && q.Codec < s.MinCodec {
		return false
	}
	if s.MaxCodec > 0 && q.Codec > s.MaxCodec {
		return false
	}
	if s.MinAudio > 0 && q.Audio < s.MinAudio {
		return false
	}
	if s.MaxAudio > 0 && q.Audio > s.MaxAudio {
		return false
	}
	if s.MinColorRange > 0 && q.ColorRange < s.MinColorRange {
		return false
	}
	if s.MaxColorRange > 0 && q.ColorRange > s.MaxColorRange {
		return false
	}
	if s.MinFormat3D > 0 && q.Format3D < s.MinFormat3D {
		return false
	}
	if s.MaxFormat3D > 0 && q.Format3D > s.MaxFormat3D {
		return false
	}
	return true
}

// ParseSpec parses a human-readable quality spec string such as
// "720p-1080p hdtv-bluray" into a Spec.
// Each token is either a single quality value or a "min-max" range.
// Tokens are separated by whitespace.
func ParseSpec(s string) (Spec, error) {
	var spec Spec
	for _, token := range strings.Fields(s) {
		if err := applySpecToken(&spec, token); err != nil {
			return Spec{}, fmt.Errorf("quality spec %q: %w", token, err)
		}
	}
	return spec, nil
}

// applySpecToken parses a single spec token (e.g. "720p", "720p-1080p", "720p+", "web-dl")
// and applies it to spec.  The full token is tried first so that hyphenated
// quality names like "web-dl" and "blu-ray" are not mistakenly split into a range.
// A trailing "+" means "this value or better" (sets only the minimum, no upper bound).
func applySpecToken(spec *Spec, token string) error {
	// "720p+" → minimum 720p, no upper bound.
	if strings.HasSuffix(token, "+") {
		base := token[:len(token)-1]
		if applyMinOnly(spec, base) {
			return nil
		}
		return fmt.Errorf("unknown quality value %q", base)
	}

	// Try the full token as a single value first.
	if applySingle(spec, token) {
		return nil
	}

	// Split on the first hyphen and try as a min-max range.
	idx := strings.Index(token, "-")
	if idx < 0 {
		return fmt.Errorf("unknown quality value %q", token)
	}
	lo, hi := token[:idx], token[idx+1:]

	// If only lo is valid (hi is empty or same as lo), treat as single.
	if hi == "" || hi == lo {
		if applySingle(spec, lo) {
			return nil
		}
		return fmt.Errorf("unknown quality value %q", lo)
	}

	if applyRange(spec, lo, hi) {
		return nil
	}
	return fmt.Errorf("unknown quality value %q", lo)
}

// applyMinOnly sets only the minimum for whichever dimension v belongs to,
// leaving the maximum at zero (no upper bound). Returns true if v was recognised.
func applyMinOnly(spec *Spec, v string) bool {
	if r, ok := parseResolution(v); ok {
		spec.MinResolution = r
		return true
	}
	if s, ok := parseSource(v); ok {
		spec.MinSource = s
		return true
	}
	if c, ok := parseCodec(v); ok {
		spec.MinCodec = c
		return true
	}
	if a, ok := parseAudio(v); ok {
		spec.MinAudio = a
		return true
	}
	if cr, ok := parseColorRange(v); ok {
		spec.MinColorRange = cr
		return true
	}
	if f, ok := parseFormat3D(v); ok {
		spec.MinFormat3D = f
		return true
	}
	return false
}

// applySingle sets min=max=value for whichever dimension v belongs to.
// Returns true if v was recognised.
func applySingle(spec *Spec, v string) bool {
	if r, ok := parseResolution(v); ok {
		spec.MinResolution, spec.MaxResolution = r, r
		return true
	}
	if s, ok := parseSource(v); ok {
		spec.MinSource, spec.MaxSource = s, s
		return true
	}
	if c, ok := parseCodec(v); ok {
		spec.MinCodec, spec.MaxCodec = c, c
		return true
	}
	if a, ok := parseAudio(v); ok {
		spec.MinAudio, spec.MaxAudio = a, a
		return true
	}
	if cr, ok := parseColorRange(v); ok {
		spec.MinColorRange, spec.MaxColorRange = cr, cr
		return true
	}
	if f, ok := parseFormat3D(v); ok {
		spec.MinFormat3D, spec.MaxFormat3D = f, f
		return true
	}
	return false
}

// applyRange sets min=lo, max=hi for whichever dimension lo belongs to.
// Returns true if lo was recognised.
func applyRange(spec *Spec, lo, hi string) bool {
	if rLo, ok := parseResolution(lo); ok {
		rHi, _ := parseResolution(hi)
		spec.MinResolution, spec.MaxResolution = rLo, rHi
		return true
	}
	if sLo, ok := parseSource(lo); ok {
		sHi, _ := parseSource(hi)
		spec.MinSource, spec.MaxSource = sLo, sHi
		return true
	}
	if cLo, ok := parseCodec(lo); ok {
		cHi, _ := parseCodec(hi)
		spec.MinCodec, spec.MaxCodec = cLo, cHi
		return true
	}
	if aLo, ok := parseAudio(lo); ok {
		aHi, _ := parseAudio(hi)
		spec.MinAudio, spec.MaxAudio = aLo, aHi
		return true
	}
	if crLo, ok := parseColorRange(lo); ok {
		crHi, _ := parseColorRange(hi)
		spec.MinColorRange, spec.MaxColorRange = crLo, crHi
		return true
	}
	if fLo, ok := parseFormat3D(lo); ok {
		fHi, _ := parseFormat3D(hi)
		spec.MinFormat3D, spec.MaxFormat3D = fLo, fHi
		return true
	}
	return false
}

func parseResolution(s string) (Resolution, bool) {
	switch strings.ToLower(s) {
	case "sd":
		return ResolutionSD, true
	case "480p":
		return Resolutionp480, true
	case "576p":
		return Resolutionp576, true
	case "720p":
		return Resolutionp720, true
	case "1080p":
		return Resolutionp1080, true
	case "2160p", "4k":
		return Resolutionp2160, true
	}
	return ResolutionUnknown, false
}

func parseSource(s string) (Source, bool) {
	switch strings.ToLower(strings.ReplaceAll(s, "-", "")) {
	case "cam", "camrip", "hdcam":
		return SourceCAM, true
	case "ts", "hdts", "tc", "hdtc", "telesync", "telecine":
		return SourceTS, true
	case "scr", "screener", "dvdscr", "dvdscreener", "bdscr":
		return SourceSCR, true
	case "dvdrip":
		return SourceDVDRip, true
	case "tvrip":
		return SourceTVRip, true
	case "hdtv":
		return SourceHDTV, true
	case "webrip":
		return SourceWEBRip, true
	case "webdl":
		return SourceWebDL, true
	case "bluray", "bdrip":
		return SourceBluRay, true
	case "remux":
		return SourceRemux, true
	}
	return SourceUnknown, false
}

func parseCodec(s string) (Codec, bool) {
	switch strings.ToLower(strings.ReplaceAll(s, ".", "")) {
	case "xvid":
		return CodecXviD, true
	case "divx":
		return CodecDivX, true
	case "x264", "h264":
		return CodecH264, true
	case "x265", "h265", "hevc":
		return CodecH265, true
	case "av1":
		return CodecAV1, true
	}
	return CodecUnknown, false
}

func parseAudio(s string) (Audio, bool) {
	switch strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(s, "-", ""), ".", "")) {
	case "mp3":
		return AudioMP3, true
	case "aac":
		return AudioAAC, true
	case "dd", "dd51", "dolbydigital":
		return AudioDolbyDigital, true
	case "dts":
		return AudioDTS, true
	case "truehd":
		return AudioTrueHD, true
	case "atmos":
		return AudioAtmos, true
	}
	return AudioUnknown, false
}

func parseFormat3D(s string) (Format3D, bool) {
	switch strings.ToLower(strings.ReplaceAll(s, "-", "")) {
	case "3dconv", "conv", "3dconvert", "convert":
		return Format3DConv, true
	case "3d", "3dhalf", "half":
		return Format3DHalf, true
	case "3dfull", "full", "sbs", "ou":
		return Format3DFull, true
	case "bd3d", "bd":
		return Format3DBD, true
	}
	return Format3DNone, false
}

func parseColorRange(s string) (ColorRange, bool) {
	switch strings.ToLower(strings.ReplaceAll(s, " ", "")) {
	case "sdr":
		return ColorRangeSDR, true
	case "hdr":
		return ColorRangeHDR, true
	case "hdr10", "hdr10+":
		return ColorRangeHDR10, true
	case "dv", "dolbyvision":
		return ColorRangeDolbyVision, true
	}
	return ColorRangeUnknown, false
}
