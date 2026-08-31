package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/brunoga/pipeliner/internal/downloads"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/series"
)

// cmdDownloaded answers "was this ever downloaded, and how many times?" from the
// download history log — including re-downloads for quality upgrades, which the
// single-record trackers overwrite and cannot report.
//
//	pipeliner downloaded "star trek strange new worlds"
//
// The daemon must be stopped (opens the store directly).
func cmdDownloaded(args []string) int {
	fs := flag.NewFlagSet("downloaded", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pipeliner downloaded [--config path] \"<title>\"")
		return 1
	}
	query := rest[0]

	db, err := openStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	events, err := downloads.New(db.Bucket(downloads.BucketName)).Query(query)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	hist := downloads.GroupByItem(events)

	seriesTracker := series.NewTracker(db.Bucket(series.TrackerBucketName))
	movieTracker := movies.NewTracker(db.Bucket(movies.TrackerBucketName))
	printDownloaded(os.Stdout, query, hist, seriesTracker, movieTracker)

	if len(hist) == 0 {
		return 1
	}
	return 0
}

// currentlyTracked reports whether the item is still recorded in its tracker
// (i.e. not since deleted/forgotten).
func currentlyTracked(h downloads.ItemHistory, st *series.Tracker, mt *movies.Tracker) bool {
	if h.MediaType == "movie" {
		return mt.IsSeen(h.Name, h.Year, h.Is3D)
	}
	return st.IsSeen(h.Name, h.EpisodeID)
}

func printDownloaded(w io.Writer, query string, hist []downloads.ItemHistory, st *series.Tracker, mt *movies.Tracker) {
	if len(hist) == 0 {
		fmt.Fprintf(w, "no download history for %q\n", query)
		fmt.Fprintln(w, "(history is recorded from the point the download log was added; older downloads may exist in the trackers)")
		return
	}
	for _, h := range hist {
		label := h.DisplayName
		if label == "" {
			label = h.Name
		}
		switch h.MediaType {
		case "movie":
			if h.Year > 0 {
				label = fmt.Sprintf("%s (%d)", label, h.Year)
			}
			if h.Is3D {
				label += " [3D]"
			}
		default:
			if h.EpisodeID != "" {
				label = fmt.Sprintf("%s %s", label, h.EpisodeID)
			}
		}
		times := "once"
		if h.Count > 1 {
			times = fmt.Sprintf("%d times (quality upgrades)", h.Count)
		}
		tracked := "no (since removed from tracker)"
		if currentlyTracked(h, st, mt) {
			tracked = "yes"
		}
		fmt.Fprintf(w, "\n%s — downloaded %s; currently tracked: %s\n", label, times, tracked)

		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  WHEN\tQUALITY\tPIPELINE")
		for _, e := range h.Downloads {
			fmt.Fprintf(tw, "  %s\t%s\t%s\n",
				e.DownloadedAt.Local().Format("2006-01-02 15:04"), e.Quality.String(), e.Task)
		}
		tw.Flush()
	}
}
