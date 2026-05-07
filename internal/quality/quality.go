// Package quality parses video quality attributes from media release titles
// and provides a multi-dimensional comparison and filter-spec system.
//
// A Spec is built from a human-readable string such as "720p-1080p webrip+"
// where each space-separated token constrains one quality dimension. The "+"
// suffix means "this value or better" (sets only the minimum, no upper bound).
package quality

import (
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

// Quality holds one value per dimension parsed from a release title.
// A zero value for any dimension means "not detected / any".
type Quality struct {
	Resolution Resolution
	Source     Source
	Codec      Codec
	Audio      Audio
	ColorRange ColorRange
}

// ResolutionName returns the human-readable resolution name (e.g. "1080p"), or "" if unknown.
func (q Quality) ResolutionName() string { return resolutionNames[q.Resolution] }

// SourceName returns the human-readable source name (e.g. "BluRay"), or "" if unknown.
func (q Quality) SourceName() string { return sourceNames[q.Source] }

// String returns a human-readable summary, omitting unknown dimensions.
func (q Quality) String() string {
	var parts []string
	for _, s := range []string{
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
// Dimensions are compared left-to-right (Resolution > Source > Codec > Audio > ColorRange);
// the first differing dimension determines the result. Unknown (0) is treated as lowest.
func (q Quality) Better(other Quality) bool {
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
	reSource     = regexp.MustCompile(`(?i)\b(remux|blu[\-\s]?ray|bdrip|bdremux|web[\-\s]?dl|webrip|hdtv|dvdrip|tvrip)\b`)
	reCodec      = regexp.MustCompile(`(?i)\b(av1|x265|h\.?265|hevc|x264|h\.?264|xvid|divx)\b`)
	reColorRange = regexp.MustCompile(`(?i)\b(dolby[\s\.]?vision|dv\b|hdr10[\+]?|hdr|sdr)\b`)

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
		case strings.Contains(ml, "bluray") || strings.Contains(ml, "bdrip"):
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

	return q
}

// --- Spec: filter spec for quality ranges ---

// Spec defines an accepted quality range per dimension.
// A zero min means "any"; a zero max means "no upper bound".
type Spec struct {
	MinResolution, MaxResolution Resolution
	MinSource, MaxSource         Source
	MinCodec, MaxCodec           Codec
	MinAudio, MaxAudio           Audio
	MinColorRange, MaxColorRange ColorRange
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
