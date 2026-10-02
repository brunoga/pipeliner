package mvc

import (
	"context"
	"os/exec"
)

// probeArgv is a one-frame trial encode: the cheapest honest answer to "can
// this machine actually use this encoder". Listing ffmpeg's encoders is not
// enough — a build routinely advertises h264_nvenc on a machine with no NVIDIA
// card, and h264_vaapi on one with no supported GPU. Each variant needs its own
// filter chain, which is why this is a table rather than one command with a
// substituted codec name.
func probeArgv(enc Encoder, device string) []string {
	const src = "testsrc2=s=320x240:d=1"
	switch enc {
	case EncoderVAAPI:
		return []string{"ffmpeg", "-hide_banner", "-loglevel", "error",
			"-vaapi_device", device,
			"-f", "lavfi", "-i", src, "-frames:v", "1",
			"-vf", "format=nv12,hwupload", "-c:v", "h264_vaapi",
			"-f", "null", "-"}
	case EncoderNVENC:
		return []string{"ffmpeg", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", src, "-frames:v", "1",
			"-c:v", "h264_nvenc", "-f", "null", "-"}
	case EncoderVideoToolbox:
		return []string{"ffmpeg", "-hide_banner", "-loglevel", "error",
			"-f", "lavfi", "-i", src, "-frames:v", "1",
			"-c:v", "h264_videotoolbox", "-f", "null", "-"}
	default:
		// x264 is software and always works if the binary is there, which
		// Detect already established.
		return nil
	}
}

// runProbe executes a trial. Indirected so the selection logic can be tested
// without a GPU, which no CI runner is guaranteed to have.
var runProbe = func(ctx context.Context, argv []string) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...) //nolint:gosec // fixed argv from probeArgv
	return cmd.Run()
}

// ProbeEncoder reports whether enc actually encodes on this machine.
func ProbeEncoder(ctx context.Context, enc Encoder, device string) bool {
	argv := probeArgv(enc, device)
	if argv == nil {
		return true // software
	}
	return runProbe(ctx, argv) == nil
}
