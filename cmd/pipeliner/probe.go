package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"sort"
	"syscall"
	"time"

	probecmd "github.com/brunoga/pipeliner/plugins/processor/probe"
)

// cmdProbe answers "what does this release actually contain?" for one release,
// outside a pipeline.
//
// The name-based gates decide from a release title, and a title can be wrong
// in both directions: it can claim a disc it is not, and it can undersell a
// disc it is. The second is the harder one to notice, because the release is
// simply refused and never looked at again — a bare "3D BluRay" with no layout
// marker reads as half side-by-side whether it is one or a 50 GB MVC disc. This
// is how to settle it, for two pieces of the torrent instead of all of it.
func cmdProbe(args []string) int {
	fs := flag.NewFlagSet("probe", flag.ContinueOnError)
	timeout := fs.Duration("timeout", 2*time.Minute, "budget for the probe, including fetching its pieces")
	maxPieces := fs.Int("max-pieces", 6, "most pieces to fetch; the probe asks for more only when it names bytes it could not read")
	asJSON := fs.Bool("json", false, "print the probe_* fields as JSON")
	noPeer := fs.Duration("no-peer-timeout", 0, "give up early when no peer has connected and no byte has arrived (0 keeps the processor's 30s default)")
	listenPort := fs.Int("listen-port", 0, "port to listen on and announce; 0 picks a random one. Must differ from any port a running daemon's probe holds")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pipeliner probe [flags] <.torrent URL | path to .torrent>")
		fmt.Fprintln(os.Stderr, "\nflags:")
		fs.PrintDefaults()
		return 1
	}

	// Ctrl-C should stop a probe mid-fetch rather than leave it holding a
	// swarm connection for the rest of the timeout.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	start := time.Now()
	rep, err := probecmd.Inspect(ctx, logger, rest[0], probecmd.Options{
		Timeout:       *timeout,
		MaxPieces:     *maxPieces,
		NoPeerTimeout: *noPeer,
		ListenPort:    *listenPort,
	})
	elapsed := time.Since(start)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if rep != nil {
			printSwarm(os.Stderr, rep.Swarm, elapsed)
		}
		// A probe that could not run is not a verdict on the release, and the
		// exit code says so: 2 for "could not tell", never 0.
		return 2
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rep.Fields); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		return 0
	}

	keys := make([]string, 0, len(rep.Fields))
	for k := range rep.Fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		fmt.Printf("%-26s %v\n", k, rep.Fields[k])
	}
	fmt.Printf("\ncost: %d piece(s), %s, in %s\n", rep.Pieces, humanBytes(rep.Bytes), elapsed.Round(time.Millisecond))
	printSwarm(os.Stdout, rep.Swarm, elapsed)
	return 0
}

// printSwarm says what the swarm did. On a failure it is the only way to tell
// a tracker that refused us from peers that would not answer.
func printSwarm(w io.Writer, s *probecmd.SwarmReport, elapsed time.Duration) {
	if s == nil {
		return
	}
	fmt.Fprintf(w, "tracker: %s\n", s.Tracker)
	fmt.Fprintf(w, "peers:   %d known, %d still dialling, %d connected (%d at the end), %d unchoked us\n",
		s.Known, s.Dialling, s.Connected, s.Active, s.Unchoked)
	if s.ToSelf > 0 {
		fmt.Fprintf(w, "         the tracker listed us too; %d connection(s) to ourselves not counted\n", s.ToSelf)
	}
	fmt.Fprintf(w, "data:    %s useful in %s, listening on port %d\n",
		humanBytes(s.Useful), elapsed.Round(time.Millisecond), s.ListenPort)
}

// humanBytes keeps the cost line readable; the exact figure is in probe_bytes.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
