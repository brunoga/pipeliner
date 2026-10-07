package probe

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

// Report is one probe's findings plus what it cost, as returned to a caller
// outside the pipeline.
type Report struct {
	// Fields are the probe_* fields exactly as the processor would stamp them
	// on an entry, so what a `condition` rule would see and what this reports
	// cannot drift apart.
	Fields map[string]any
	// Pieces and Bytes are what the sample cost. On a private tracker those
	// bytes count against a ratio, so they are reported rather than guessed at.
	Pieces int
	Bytes  int64
}

// Inspect probes one torrent — a .torrent URL, or a path to a local .torrent
// file — without a pipeline. It exists so the same question the processor
// answers inside a run ("what does this release's own container say it is?")
// can be asked directly of a single release, which is the thing you want when
// a name-based gate has refused something and you need to know whether it was
// right.
//
// It runs the processor's own code path, so the answer is the answer the
// pipeline would have got.
func Inspect(ctx context.Context, logger *slog.Logger, src string, timeout time.Duration, maxPieces int) (*Report, error) {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	cfg := map[string]any{"timeout": timeout.String(), "max_pieces": float64(maxPieces)}
	pl, err := newPlugin(cfg, nil)
	if err != nil {
		return nil, err
	}
	p := pl.(*probePlugin)
	defer func() { _ = p.Shutdown(ctx, nil) }()

	e := entry.New(src, src)
	// A local .torrent is read from disk; anything else is fetched as a URL,
	// which is what an indexer proxy link is.
	if strings.HasSuffix(strings.ToLower(src), ".torrent") {
		if _, statErr := os.Stat(src); statErr == nil {
			e.Set(entry.FieldFileLocation, src)
		}
	}

	tc := &plugin.TaskContext{Name: "probe", Logger: logger}
	res, pieces, bytes, err := p.probeEntry(ctx, tc, e)
	if err != nil {
		return nil, err
	}
	apply(e, res)
	return &Report{Fields: e.Fields, Pieces: pieces, Bytes: bytes}, nil
}
