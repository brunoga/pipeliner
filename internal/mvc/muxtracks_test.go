package mvc

import (
	"context"
	"strings"
	"testing"
)

// stubIdentify makes mkvmerge's answer for each demuxed file a fixture, so the
// selection logic is testable without mkvmerge present.
func stubIdentify(t *testing.T, byPath map[string]string) {
	t.Helper()
	orig := runIdentify
	t.Cleanup(func() { runIdentify = orig })
	runIdentify = func(_ context.Context, _, path string) (string, error) {
		out, ok := byPath[path]
		if !ok {
			return "", errNotStubbed
		}
		return out, nil
	}
}

type stubErr string

func (e stubErr) Error() string { return string(e) }

const errNotStubbed = stubErr("no stub for that path")

// What mkvmerge really prints for a TrueHD stream demuxed off a Blu-ray: the
// lossless track and the AC-3 core it carries for players that cannot decode
// it, in one file, with TrueHD presented first. Taken from a real disc.
const truehdWithCore = `File '/w/00001.track_4352_eng.ac3+thd': container: TrueHD
Track ID 0: audio (TrueHD Atmos)
Track ID 1: audio (AC-3 Dolby Surround EX)
`

const pgsOnly = `File '/w/00001.track_4608_eng.sup': container: PGS
Track ID 0: subtitles (HDMV PGS)
`

func argvFor(t *testing.T, extras []extra) ([]string, []droppedTrack) {
	t.Helper()
	r := &Runner{Report: nil}
	return r.extraArgs(context.Background(), "mkvmerge", extras)
}

// The bug this fixes: passing the file through whole put an AC-3 track in the
// output that the disc never listed and the probe never promised, with no
// language on it, sitting beside the TrueHD it was told about.
func TestEmbeddedCoreIsDropped(t *testing.T) {
	path := "/w/00001.track_4352_eng.ac3+thd"
	stubIdentify(t, map[string]string{path: truehdWithCore})

	argv, dropped := argvFor(t, []extra{
		{path: path, track: Track{ID: 4352, StreamID: "A_TRUEHD", Type: "TRUE-HD", Lang: "eng"}},
	})
	joined := strings.Join(argv, " ")
	if joined != "--audio-tracks 0 --language 0:eng "+path {
		t.Errorf("argv = %q", joined)
	}
	if len(dropped) != 1 || !strings.Contains(dropped[0].codec, "AC-3") {
		t.Errorf("dropped = %+v, want the AC-3 core reported", dropped)
	}
}

// tsMuxeR says "TRUE-HD" where mkvmerge says "TrueHD Atmos". The primary must
// still be identified, or the core would be kept and the lossless track
// thrown away — the exact opposite of what --audio-best was asked for.
func TestPrimaryIsFoundDespiteDifferentCodecSpellings(t *testing.T) {
	have := []muxTrack{
		{ID: 0, Kind: "audio", Codec: "TrueHD Atmos"},
		{ID: 1, Kind: "audio", Codec: "AC-3 Dolby Surround EX"},
	}
	primary, extras := primaryTrack(Track{Type: "TRUE-HD"}, have)
	if primary.ID != 0 {
		t.Errorf("picked track %d (%s), want the TrueHD", primary.ID, primary.Codec)
	}
	if len(extras) != 1 || extras[0].ID != 1 {
		t.Errorf("extras = %+v, want the core", extras)
	}
}

// And it must not depend on the primary being first. A file listing the core
// first has to yield the same choice, or the result depends on tsMuxeR's
// output order rather than on what the track actually is.
func TestPrimaryIsNotAssumedToBeFirst(t *testing.T) {
	have := []muxTrack{
		{ID: 0, Kind: "audio", Codec: "AC-3 Dolby Surround EX"},
		{ID: 1, Kind: "audio", Codec: "TrueHD Atmos"},
	}
	primary, extras := primaryTrack(Track{Type: "TRUE-HD"}, have)
	if primary.ID != 1 {
		t.Errorf("picked track %d (%s), want the TrueHD wherever it sits", primary.ID, primary.Codec)
	}
	if len(extras) != 1 || extras[0].ID != 0 {
		t.Errorf("extras = %+v, want the core", extras)
	}
}

// DTS-HD carries a plain DTS core the same way.
func TestDTSCoreIsDropped(t *testing.T) {
	have := []muxTrack{
		{ID: 0, Kind: "audio", Codec: "DTS-HD Master Audio"},
		{ID: 1, Kind: "audio", Codec: "DTS"},
	}
	primary, extras := primaryTrack(Track{Type: "DTS-HD Master Audio"}, have)
	if primary.ID != 0 || len(extras) != 1 {
		t.Errorf("primary %d, extras %+v; want the lossless track kept", primary.ID, extras)
	}
}

// A codec neither tool describes recognisably falls back to the first track,
// which is what mkvmerge presents as the primary, rather than failing.
func TestUnrecognisedCodecFallsBackToTheFirstTrack(t *testing.T) {
	have := []muxTrack{{ID: 3, Kind: "audio", Codec: "Something New"}}
	primary, extras := primaryTrack(Track{Type: "Also New"}, have)
	if primary.ID != 3 || len(extras) != 0 {
		t.Errorf("primary %d, extras %+v; want the only track kept", primary.ID, extras)
	}
}

// A file with one track must produce the same command as before: no selector,
// because mkvmerge should not be asked to filter what needs no filtering.
func TestSingleTrackFileGetsNoSelector(t *testing.T) {
	path := "/w/00001.track_4608_eng.sup"
	stubIdentify(t, map[string]string{path: pgsOnly})

	argv, dropped := argvFor(t, []extra{
		{path: path, track: Track{ID: 4608, StreamID: "S_HDMV/PGS", Type: "PGS", Lang: "eng"}},
	})
	joined := strings.Join(argv, " ")
	if joined != "--language 0:eng "+path {
		t.Errorf("argv = %q, want no track selector for a single-track file", joined)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped %+v from a single-track file", dropped)
	}
}

// Identification failing must not fail the mux. The file goes in whole, which
// is the behaviour before any of this existed.
func TestIdentifyFailureFallsBackToPassingTheFileWhole(t *testing.T) {
	stubIdentify(t, nil) // every path errors
	argv, dropped := argvFor(t, []extra{
		{path: "/w/a.thd", track: Track{Lang: "eng"}},
	})
	if strings.Join(argv, " ") != "--language 0:eng /w/a.thd" {
		t.Errorf("argv = %v, want the file passed whole", argv)
	}
	if len(dropped) != 0 {
		t.Errorf("dropped %+v without being able to identify anything", dropped)
	}
}

// An untagged track is still passed, just without a language.
func TestUntaggedTrackNeedsNoLanguageOption(t *testing.T) {
	path := "/w/a.sup"
	stubIdentify(t, map[string]string{path: pgsOnly})
	argv, _ := argvFor(t, []extra{{path: path, track: Track{Type: "PGS"}}})
	if strings.Join(argv, " ") != path {
		t.Errorf("argv = %v, want just the path", argv)
	}
}

// The language must address the primary track's own ID, not always zero, or a
// file whose primary is not first gets its core tagged instead.
func TestLanguageAddressesThePrimaryTrackID(t *testing.T) {
	path := "/w/odd.thd"
	stubIdentify(t, map[string]string{path: `Track ID 0: audio (AC-3)
Track ID 1: audio (TrueHD Atmos)
`})
	argv, _ := argvFor(t, []extra{{path: path, track: Track{Type: "TRUE-HD", Lang: "eng"}}})
	joined := strings.Join(argv, " ")
	if joined != "--audio-tracks 1 --language 1:eng "+path {
		t.Errorf("argv = %q, want both options to address track 1", joined)
	}
}

func TestIdentifyParsesMkvmergeOutput(t *testing.T) {
	stubIdentify(t, map[string]string{"/w/f": truehdWithCore})
	got, err := identify(context.Background(), "mkvmerge", "/w/f")
	if err != nil {
		t.Fatalf("identify: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d tracks, want 2: %+v", len(got), got)
	}
	if got[0].ID != 0 || got[0].Kind != "audio" || got[0].Codec != "TrueHD Atmos" {
		t.Errorf("track 0 = %+v", got[0])
	}
	if got[1].ID != 1 || got[1].Codec != "AC-3 Dolby Surround EX" {
		t.Errorf("track 1 = %+v", got[1])
	}
}

func TestIdentifyRejectsOutputWithNoTracks(t *testing.T) {
	stubIdentify(t, map[string]string{"/w/f": "File '/w/f': container: unknown\n"})
	if _, err := identify(context.Background(), "mkvmerge", "/w/f"); err == nil {
		t.Error("a listing with no tracks should be an error, not an empty selection")
	}
}

func TestSquashCodecMakesSpellingsMeet(t *testing.T) {
	if squashCodec("TRUE-HD") != "truehd" || squashCodec("TrueHD Atmos") != "truehdatmos" {
		t.Errorf("squash gave %q and %q", squashCodec("TRUE-HD"), squashCodec("TrueHD Atmos"))
	}
	if !codecsAgree(Track{Type: "TRUE-HD"}, muxTrack{Codec: "TrueHD Atmos"}) {
		t.Error("TRUE-HD should agree with TrueHD Atmos")
	}
	if codecsAgree(Track{Type: "TRUE-HD"}, muxTrack{Codec: "AC-3 Dolby Surround EX"}) {
		t.Error("TRUE-HD must not agree with AC-3")
	}
	// Falls back to the stream ID when there is no human type.
	if !codecsAgree(Track{StreamID: "A_TRUEHD"}, muxTrack{Codec: "TrueHD Atmos"}) {
		t.Error("the stream ID alone should be enough to agree")
	}
}
