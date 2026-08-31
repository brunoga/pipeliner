package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"text/tabwriter"
	"time"

	"github.com/brunoga/pipeliner/internal/failures"
	"github.com/brunoga/pipeliner/internal/task"
)

// logRunFailures appends any failed entries from a completed run to the durable
// failure audit log. It is a no-op when the run had no failures. Shared by the
// one-shot `run` command and the daemon runner; callers gate on dry-run first
// (dry-runs skip sinks, so they never produce real failures).
func logRunFailures(flog *failures.Log, res *task.Result, taskName string, at time.Time, logger *slog.Logger) {
	if res == nil || res.Failed == 0 {
		return
	}
	if err := flog.AppendAll(failures.RecordsFromRun(res.Entries, res.Traces, taskName, at)); err != nil {
		logger.Warn("persist failures", "pipeline", taskName, "err", err)
	}
}

// cmdFailures shows the durable failure audit log — entries that failed at a
// sink (a torrent client refusing the add, a download error), which the run
// inspector's bounded traces stop showing after a few runs. This answers "why
// did this fail last month?".
//
//	pipeliner failures                 # recent failures across all pipelines
//	pipeliner failures "star trek"     # failures whose title/reason matches
//
// The daemon must be stopped (opens the store directly).
func cmdFailures(args []string) int {
	fs := flag.NewFlagSet("failures", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	limit := fs.Int("limit", 50, "maximum failures to show (0 = no limit)")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	query := ""
	if rest := fs.Args(); len(rest) > 0 {
		query = rest[0]
	}

	db, err := openStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	recs, err := failures.New(db.Bucket(failures.BucketName)).Query(query, *limit)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	printFailures(os.Stdout, query, recs)
	if len(recs) == 0 {
		return 1
	}
	return 0
}

func printFailures(w io.Writer, query string, recs []failures.Record) {
	if len(recs) == 0 {
		if query == "" {
			fmt.Fprintln(w, "no failures recorded")
		} else {
			fmt.Fprintf(w, "no failures matching %q\n", query)
		}
		return
	}
	fmt.Fprintf(w, "%d failure(s)%s, newest first:\n\n", len(recs), queryNote(query))
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "WHEN\tPIPELINE\tNODE\tTITLE\tREASON")
	for _, r := range recs {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
			r.FailedAt.Local().Format("2006-01-02 15:04"), r.Task, r.Node, r.Title, r.Reason)
	}
	tw.Flush()
}

func queryNote(query string) string {
	if query == "" {
		return ""
	}
	return fmt.Sprintf(" matching %q", query)
}
