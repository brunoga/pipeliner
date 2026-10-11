// Package bitrate gates torrent entries on their implied video bitrate and
// stamps what it measured, from two numbers known before downloading: the
// torrent's size and the runtime.
//
// # Two figures, because the file is not the video
//
// container_bitrate_mbps is size over runtime: every byte in the file,
// including the audio. video_bitrate_mbps subtracts an estimate of the audio
// from it, because the audio is a large and wholly unrelated share of the
// first figure. A 4K film with TrueHD Atmos 7.1 carries roughly 3.5 Mbps of
// audio; the same video with DD+ 5.1 carries 0.7. Measured on one real
// encode: 8.79 Mbps in the file, 5.12 of it video, so 42% of the figure the
// floor used to judge was audio. Judging a release on the container number
// rewards lossless audio and punishes efficient audio, which says nothing
// about the thing being gated.
//
// The audio estimate comes from the format the release name states, so it is
// an estimate: a release listing one track may carry three. Every value in
// audioMbps is at the low end of its format's range, so the subtraction
// errs towards a larger video figure and a lenient gate — the same principle
// as leaving entries with no size or runtime untouched.
//
// # Floors are H.264-equivalent
//
// A configured floor is what the release would need as H.264. HEVC reaches
// the same picture at roughly 60% of that and AV1 at roughly 45%, so the
// floor each entry is actually held to is scaled by its codec. Without this
// a single number either admits starved H.264 or refuses perfectly good
// HEVC, and the latter is what happened: a 15 Mbps floor for 2160p rejected
// HBO Max WEB-DLs at 7-10 Mbps, which are not encodes anyone chose the
// bitrate of — a WEB-DL is a copy of what the service sent.
//
// # What this cannot tell you
//
// A bitrate floor catches a release that is broken, not one that is merely
// worse. A quality-targeted encode spends what each scene needs, so a good
// 4K HEVC encode of clean digital footage can sit near 5 Mbps and score VMAF
// 98, while a bad one sits at the same number; the profiles have the same
// shape and only the result differs. For the claim a release name makes
// about its resolution, probe's coded frame size is evidence where this is
// only an inference: see probe_upscale.
//
// # Config keys (all optional, Mbps, 0 = no floor)
//
//	min_2160p - floor for 2160p entries
//	min_1080p - floor for 1080p entries
//	min_720p  - floor for 720p entries
//	min_other - floor for everything else (including unknown resolution)
//
// Runtime comes from probe_duration_sec when a probe has supplied it — the
// container's own statement — and from video_runtime otherwise. Entries with
// neither, or with no size, are left untouched; the gate never rejects on
// absent data.
package bitrate

import (
	"context"
	"fmt"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/quality"
)

const pluginName = "bitrate"

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  pluginName,
		Description: "gate entries on implied bitrate (torrent size / runtime) — the quality signal a release name cannot fake",
		Role:        plugin.RoleProcessor,
		Refusal:     plugin.RefusalPerRelease,
		Requires: [][]string{
			{entry.FieldTorrentFileSize},
			// Either runtime will do: the container's own, where a probe ran
			// above this node, or the enrichment runtime.
			{entry.FieldVideoRuntime, entry.FieldProbeDurationSec},
		},
		MayProduce: []string{
			entry.FieldVideoBitrateMbps,
			entry.FieldContainerBitrateMbps,
		},
		Factory:  newPlugin,
		Validate: validate,
		Schema: []plugin.FieldSchema{
			{Key: "min_2160p", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for 2160p entries (0 = no floor)"},
			{Key: "min_1080p", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for 1080p entries (0 = no floor)"},
			{Key: "min_720p", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for 720p entries (0 = no floor)"},
			{Key: "min_other", Type: plugin.FieldTypeInt, Hint: "Minimum implied Mbps for other/unknown resolutions (0 = no floor)"},
		},
	})
}

var floorKeys = []string{"min_2160p", "min_1080p", "min_720p", "min_other"}

func validate(cfg map[string]any) []error {
	var errs []error
	for _, k := range floorKeys {
		if v, ok := cfg[k]; ok {
			if n, isNum := toFloat(v); !isNum || n < 0 {
				errs = append(errs, fmt.Errorf("%s: %q must be a non-negative number of Mbps", pluginName, k))
			}
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, pluginName, floorKeys...)...)
	return errs
}

type bitratePlugin struct {
	floors map[string]float64
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	floors := make(map[string]float64, len(floorKeys))
	for _, k := range floorKeys {
		if v, ok := cfg[k]; ok {
			n, isNum := toFloat(v)
			if !isNum || n < 0 {
				return nil, fmt.Errorf("%s: %q must be a non-negative number of Mbps", pluginName, k)
			}
			floors[k] = n
		}
	}
	return &bitratePlugin{floors: floors}, nil
}

func (p *bitratePlugin) Name() string { return pluginName }

func (p *bitratePlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	for _, e := range entries {
		size := toInt64(e.Fields[entry.FieldTorrentFileSize])
		secs := runtimeSeconds(e)
		if size <= 0 || secs <= 0 {
			continue // never reject on absent data
		}
		container := round1(float64(size) * 8 / secs / 1e6)
		e.Set(entry.FieldContainerBitrateMbps, container)

		q, hasQ := e.Quality()
		video := round1(max(container-audioMbps(q, hasQ), 0))
		e.Set(entry.FieldVideoBitrateMbps, video)

		floorKey, resName := floorFor(q, hasQ)
		floor := p.floors[floorKey]
		if floor <= 0 {
			continue
		}
		// The configured floor is H.264-equivalent; what this entry is held
		// to depends on how efficient its codec is.
		scale := codecScale(q, hasQ)
		effective := round1(floor * scale)
		if video >= effective {
			continue
		}
		if resName == "" {
			resName = "unknown resolution"
		}
		reason := fmt.Sprintf("%s: %.1f Mbps of video for %s below floor %.1f Mbps",
			pluginName, video, resName, effective)
		if scale != 1 {
			reason += fmt.Sprintf(" (%.0f Mbps scaled by %.2f for %s)", floor, scale, q.CodecName())
		}
		e.Reject(reason)
		tc.Logger.Info(pluginName+": rejected starved encode",
			"entry", e.Title, "video_mbps", video, "container_mbps", container,
			"resolution", resName, "floor", effective, "configured_floor", floor,
			"codec_scale", scale)
	}
	return entry.PassThrough(entries), nil
}

// runtimeSeconds prefers the container's own duration, which a probe supplies
// and which needs no external lookup, over the enrichment runtime. They
// disagree for an extended cut, where a provider's runtime describes a
// different film from the one in the torrent.
func runtimeSeconds(e *entry.Entry) float64 {
	if secs := e.GetInt(entry.FieldProbeDurationSec); secs > 0 {
		return float64(secs)
	}
	if mins := e.GetInt(entry.FieldVideoRuntime); mins > 0 {
		return float64(mins) * 60
	}
	return 0
}

// audioMbps estimates what the audio takes of the file, from the format the
// release name states. Values sit at the LOW end of each format's range: the
// estimate is subtracted from the figure the floor judges, so under-stating
// it leaves the gate lenient, which is the side to err on.
//
// A release names one audio format and may ship several tracks (a lossless
// track plus a lossy compatibility one is routine), so this under-states
// those by design rather than by accident.
func audioMbps(q quality.Quality, hasQ bool) float64 {
	if !hasQ {
		return unknownAudioMbps
	}
	if mbps, ok := audioEstimates[q.Audio]; ok {
		return mbps
	}
	return unknownAudioMbps
}

// unknownAudioMbps is one lossy track's worth: enough not to pretend the
// audio is free, small enough not to refuse a release over a guess.
const unknownAudioMbps = 0.3

var audioEstimates = map[quality.Audio]float64{
	quality.AudioAtmos:        3.5, // Atmos on a lossless TrueHD bed
	quality.AudioTrueHD:       3.0,
	quality.AudioDTS:          1.5, // the parser does not separate DTS-HD MA
	quality.AudioDDPlusAtmos:  0.7,
	quality.AudioDolbyDigital: 0.4,
	quality.AudioAAC:          0.2,
	quality.AudioMP3:          0.15,
	quality.AudioUnknown:      unknownAudioMbps,
}

// codecScale is how much of an H.264 bitrate a codec needs for the same
// picture. Round numbers on purpose: the point is that a single floor cannot
// serve three generations of codec, not that 0.6 is exactly right.
func codecScale(q quality.Quality, hasQ bool) float64 {
	if !hasQ {
		return 1
	}
	switch q.Codec {
	case quality.CodecAV1:
		return 0.45
	case quality.CodecH265:
		return 0.6
	}
	return 1
}

func floorFor(q quality.Quality, hasQ bool) (key, resName string) {
	if hasQ {
		switch q.Resolution {
		case quality.Resolutionp2160:
			return "min_2160p", "2160p"
		case quality.Resolutionp1080:
			return "min_1080p", "1080p"
		case quality.Resolutionp720:
			return "min_720p", "720p"
		}
	}
	return "min_other", ""
}

// round1 keeps reasons and condition rules reading cleanly.
func round1(f float64) float64 { return float64(int(f*10+0.5)) / 10 }

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}

func toInt64(v any) int64 {
	switch n := v.(type) {
	case int:
		return int64(n)
	case int64:
		return n
	case float64:
		return int64(n)
	}
	return 0
}
