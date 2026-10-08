package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/movies"
	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
	"github.com/brunoga/pipeliner/quality"
)

// cmdTracker manages the series/movies download trackers directly: mark a title
// as already-downloaded (so it is never grabbed) or forget one (so it is
// re-downloaded). All ops open the store, so the daemon must be stopped.
//
//	pipeliner tracker mark-series  "<show>" "<episode-id>" [--quality "..."]
//	pipeliner tracker forget-series "<show>" "<episode-id>"
//	pipeliner tracker mark-movie   "<title>" --year N [--3d] [--quality "..."]
//	pipeliner tracker forget-movie "<title>" --year N [--3d] [--pipeline name]
//
// A movies node with local=true tracks into "movies:<task>" rather than the
// shared tracker, so --pipeline selects which of the two to act on.
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
  pipeliner tracker mark-movie    "<title>" --year N [--3d] [--pipeline name] [--config path] [--quality "..."]
  pipeliner tracker forget-movie  "<title>" --year N [--3d] [--pipeline name] [--config path]

A movies node with local=true tracks into its own "movies:<task>" bucket
instead of the shared tracker; --pipeline picks that one.

Flags may appear before or after the positional arguments.

The daemon must be stopped — these commands take the database lock. To edit a
tracker while the daemon is running, use the database browser in the web UI
(DELETE /api/db/entries/{bucket}), which goes through the process holding it.`)
}

func trackerSeries(args []string, forget bool) int {
	fs := flag.NewFlagSet("tracker series", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	qStr := fs.String("quality", "", "quality of the release (e.g. \"1080p web h264\")")
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
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
		// Report what the record held before dropping it: forgetting causes a
		// re-download, and "forgot X" alone leaves no trace of what quality
		// the library was known to have.
		rec, ok := tracker.Get(norm, epID)
		if !ok {
			// Reporting success here is how a typo reads exactly like a
			// completed job. Nothing was tracked, so nothing was forgotten.
			fmt.Fprintf(os.Stderr, "error: no tracker record for %s|%s\n", norm, epID)
			return 1
		}
		fmt.Printf("forgetting %s|%s (quality: %s, downloaded %s)\n",
			norm, epID, rec.Quality.String(), rec.DownloadedAt.Format(time.RFC3339))
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
	pipeline := fs.String("pipeline", "", "operate on this pipeline's own tracker (movies with local=true) instead of the shared one")
	qStr := fs.String("quality", "", "quality of the release (e.g. \"1080p bluray\")")
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
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
	// A movie record is keyed by year, so year 0 names nothing. Reject it
	// rather than operating on a key that cannot exist.
	if *year <= 0 {
		fmt.Fprintln(os.Stderr, "error: --year is required and must be a real year")
		return 1
	}

	db, err := openStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()
	bucket := movieTrackerBucket(*pipeline)
	tracker := movies.NewTracker(db.Bucket(bucket))

	if forget {
		// Latest() ignores the year, so it could describe a different record
		// than the one being deleted. Check the exact key instead.
		if !tracker.IsSeen(norm, *year, *is3D) {
			fmt.Fprintf(os.Stderr, "error: no tracker record for %s (%d)%s in bucket %q\n",
				norm, *year, tridSuffix(*is3D), bucket)
			// A pipeline with local=true keeps its own tracker, so the record
			// is often simply in a different bucket. Saying which turns a
			// dead end into the next command to run.
			if elsewhere := otherMovieBuckets(db, bucket, movies.RecordKey(norm, *year, *is3D)); len(elsewhere) > 0 {
				fmt.Fprintf(os.Stderr, "  it is tracked in: %s\n", strings.Join(elsewhere, ", "))
				for _, b := range elsewhere {
					if name, ok := strings.CutPrefix(b, movies.TrackerBucketName+":"); ok {
						fmt.Fprintf(os.Stderr, "  try: --pipeline %q\n", name)
					}
				}
			}
			return 1
		}
		if rec, ok := tracker.LatestNearYear(norm, *year, *is3D); ok {
			fmt.Printf("forgetting %s (%d)%s (quality: %s, downloaded %s)\n",
				norm, *year, tridSuffix(*is3D), rec.Quality.String(),
				rec.DownloadedAt.Format(time.RFC3339))
		}
		if err := tracker.Forget(norm, *year, *is3D); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("forgot %s (%d)%s from %s\n", norm, *year, tridSuffix(*is3D), bucket)
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

// flagsFirst moves flags ahead of positional arguments so that both orders
// work. Go's flag package stops parsing at the first non-flag argument, so
// every flag written after the title used to be dropped in silence — and the
// usage text documented exactly that order. `forget-movie "bolt" --year 2008
// --3d --config /etc/pipeliner/config.star` therefore reported success for
// key "bolt|0", and, because --config went missing with the rest, opened a
// brand-new store in the working directory instead of the configured one.
//
// A flag that takes a value consumes the next argument unless it was written
// as -name=value; boolean flags never do, which is what fs.Lookup is for. An
// explicit "--" ends flag parsing, and everything after it is positional.
func flagsFirst(fs *flag.FlagSet, args []string) []string {
	var flags, positional []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if a == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if len(a) > 1 && strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			if !strings.Contains(a, "=") && !isBoolFlag(fs, a) && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
			continue
		}
		positional = append(positional, a)
	}
	return append(flags, positional...)
}

// isBoolFlag reports whether the named flag is a boolean, which decides
// whether it consumes the argument after it.
func isBoolFlag(fs *flag.FlagSet, arg string) bool {
	f := fs.Lookup(strings.TrimLeft(arg, "-"))
	if f == nil {
		return false
	}
	b, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && b.IsBoolFlag()
}

// movieTrackerBucket names the tracker a movie op should act on. A pipeline
// configured with local=true keeps its downloads in "movies:<task>" rather
// than the shared "movies" tracker, and until this existed the CLI could
// only ever reach the shared one -- so a film tracked by, say,
// 3d-mvc-harvest could not be forgotten from the command line at all.
func movieTrackerBucket(pipeline string) string {
	if pipeline == "" {
		return movies.TrackerBucketName
	}
	return movies.TrackerBucketName + ":" + pipeline
}

// otherMovieBuckets returns the movie tracker buckets, apart from exclude,
// that hold key. It is used to turn "no such record" into a pointer at the
// bucket that does have it.
func otherMovieBuckets(db *store.SQLiteStore, exclude, key string) []string {
	rows, err := db.DB().Query(
		`SELECT bucket FROM store WHERE key = ? AND bucket != ?`+
			` AND (bucket = ? OR bucket LIKE ?) ORDER BY bucket`,
		key, exclude, movies.TrackerBucketName, movies.TrackerBucketName+":%")
	if err != nil {
		return nil
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err != nil {
			return out
		}
		out = append(out, b)
	}
	return out
}
