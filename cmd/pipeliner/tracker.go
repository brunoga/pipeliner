package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/quality"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

// cmdTracker manages the series/movies download trackers directly: mark a title
// as already-downloaded (so it is never grabbed) or forget one (so it is
// re-downloaded). All ops open the store, so the daemon must be stopped.
//
//	pipeliner tracker mark-series  "<show>" "<episode-id>" [--quality "..."]
//	pipeliner tracker forget-series "<show>" "<episode-id>"
//	pipeliner tracker mark-movie   "<title>" --year N [--3d] [--quality "..."]
//	pipeliner tracker forget-movie "<title>" --year N [--3d]
func cmdTracker(args []string) int {
	if len(args) == 0 {
		trackerUsage()
		return 1
	}
	op := args[0]
	rest := args[1:]
	switch op {
	case "mark-series":
		return trackerSeries(rest, false)
	case "forget-series":
		return trackerSeries(rest, true)
	case "mark-movie":
		return trackerMovie(rest, false)
	case "forget-movie":
		return trackerMovie(rest, true)
	default:
		fmt.Fprintf(os.Stderr, "unknown tracker op %q\n", op)
		trackerUsage()
		return 1
	}
}

func trackerUsage() {
	fmt.Fprintln(os.Stderr, `usage:
  pipeliner tracker mark-series   "<show>" "<episode-id>" [--config path] [--quality "..."]
  pipeliner tracker forget-series "<show>" "<episode-id>" [--config path]
  pipeliner tracker mark-movie    "<title>" --year N [--3d] [--config path] [--quality "..."]
  pipeliner tracker forget-movie  "<title>" --year N [--3d] [--config path]

The daemon must be stopped — these commands take the database lock.`)
}

func trackerSeries(args []string, forget bool) int {
	fs := flag.NewFlagSet("tracker series", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	qStr := fs.String("quality", "", "quality of the release (e.g. \"1080p web h264\")")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) < 2 {
		fmt.Fprintln(os.Stderr, "error: need a show name and an episode id")
		return 1
	}
	norm := match.Normalize(rest[0])
	if norm == "" {
		fmt.Fprintln(os.Stderr, "error: show name is empty after normalization")
		return 1
	}
	epID, ok := series.CanonicalEpisodeID(rest[1])
	if !ok {
		fmt.Fprintln(os.Stderr, "error: episode id must be S04E05, EP012, or 2023-11-15")
		return 1
	}

	db, err := openStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	tracker := series.NewTracker(db.Bucket(series.TrackerBucketName))

	if forget {
		if err := tracker.Forget(norm, epID); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("forgot %s|%s\n", norm, epID)
		return 0
	}
	rec := series.Record{SeriesName: norm, DisplayName: rest[0], EpisodeID: epID, Quality: quality.Parse(*qStr)}
	if err := tracker.Mark(rec); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("marked %s|%s as downloaded (quality: %s)\n", norm, epID, rec.Quality.String())
	return 0
}

func trackerMovie(args []string, forget bool) int {
	fs := flag.NewFlagSet("tracker movie", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	year := fs.Int("year", 0, "release year")
	is3D := fs.Bool("3d", false, "the 3D version")
	qStr := fs.String("quality", "", "quality of the release (e.g. \"1080p bluray\")")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) < 1 {
		fmt.Fprintln(os.Stderr, "error: need a movie title")
		return 1
	}
	norm := match.Normalize(rest[0])
	if norm == "" {
		fmt.Fprintln(os.Stderr, "error: title is empty after normalization")
		return 1
	}

	db, err := openStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	tracker := movies.NewTracker(db.Bucket(movies.TrackerBucketName))

	if forget {
		if err := tracker.Forget(norm, *year, *is3D); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("forgot %s (%d)%s\n", norm, *year, tridSuffix(*is3D))
		return 0
	}
	rec := movies.Record{Title: norm, Year: *year, Is3D: *is3D, Quality: quality.Parse(*qStr)}
	if err := tracker.Mark(rec); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("marked %s (%d)%s as downloaded (quality: %s)\n", norm, *year, tridSuffix(*is3D), rec.Quality.String())
	return 0
}

func tridSuffix(is3D bool) string {
	if is3D {
		return " [3D]"
	}
	return ""
}

// openStore opens the SQLite store for the given config path. Returns a clear
// error when the daemon holds the lock.
func openStore(cfgPath string) (*store.SQLiteStore, error) {
	db, err := store.OpenSQLite(dbPath(cfgPath))
	if err != nil {
		return nil, fmt.Errorf("open store (is the daemon running? it holds the database lock): %w", err)
	}
	return db, nil
}
