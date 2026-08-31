package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/brunoga/pipeliner/internal/cache"
	"github.com/brunoga/pipeliner/internal/match"
	"github.com/brunoga/pipeliner/internal/store"
)

// maxProbeCandidatesShown caps how many non-matching candidates the match
// command prints, so probing against a large favorites list stays readable.
// Matching candidates are always shown in full.
const maxProbeCandidatesShown = 15

// cmdMatch answers "why doesn't title X match my list?" without a log dive.
//
// Two modes:
//
//	pipeliner match "Star Trek Strange New Worlds" "Silo" "Star Trek: Strange New Worlds"
//	    Offline: probe the input against the candidate titles given as extra
//	    arguments. No config or database needed.
//
//	pipeliner match --list cache_series_list "Star Trek Strange New Worlds"
//	    Probe the input against the resolved title list cached in the given
//	    store bucket (the same list the series/movies filter matches against).
//	    Requires the daemon to be stopped, since the store takes a file lock.
func cmdMatch(args []string) int {
	fs := flag.NewFlagSet("match", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file (locates the database for --list)")
	listBucket := fs.String("list", "", "resolve candidates from this store cache bucket (e.g. cache_series_list, cache_movies_list) instead of positional arguments")
	year := fs.Int("year", 0, "release year of the input title, for year-aware movie matching (0 = unknown)")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pipeliner match [--year N] \"<input title>\" [candidate ...]")
		fmt.Fprintln(os.Stderr, "       pipeliner match [--config path] --list <bucket> [--year N] \"<input title>\"")
		return 1
	}
	input := rest[0]
	inlineCandidates := rest[1:]

	var candidates []match.TitleEntry
	switch {
	case *listBucket != "":
		if len(inlineCandidates) > 0 {
			fmt.Fprintln(os.Stderr, "error: give either --list or positional candidates, not both")
			return 1
		}
		loaded, err := loadListCandidates(*cfgPath, *listBucket)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		candidates = loaded
		if len(candidates) == 0 {
			fmt.Fprintf(os.Stderr, "warning: bucket %q is empty — has the list been resolved yet?\n", *listBucket)
		}
	case len(inlineCandidates) > 0:
		candidates = make([]match.TitleEntry, len(inlineCandidates))
		for i, c := range inlineCandidates {
			candidates[i] = match.NewTitleEntry(c, 0)
		}
	default:
		fmt.Fprintln(os.Stderr, "error: no candidates — pass candidate titles as arguments or use --list <bucket>")
		return 1
	}

	res := match.Probe(input, *year, candidates)
	printProbe(os.Stdout, res)
	if res.Matched {
		return 0
	}
	return 1
}

// loadListCandidates opens the store for cfgPath (read-only intent) and returns
// the union of the TitleEntry lists cached in bucket. The daemon must not be
// running, as the store takes an exclusive file lock at open.
func loadListCandidates(cfgPath, bucket string) ([]match.TitleEntry, error) {
	db, err := store.OpenSQLite(dbPath(cfgPath))
	if err != nil {
		return nil, fmt.Errorf("open store (is the daemon running? it holds the database lock): %w", err)
	}
	defer db.Close()

	lists, ok := cache.Values[[]match.TitleEntry](db.Bucket(bucket))
	if !ok {
		return nil, fmt.Errorf("bucket %q does not support bulk read", bucket)
	}
	var out []match.TitleEntry
	for _, l := range lists {
		out = append(out, l...)
	}
	return out, nil
}

func printProbe(w io.Writer, res match.ProbeResult) {
	fmt.Fprintf(w, "input:      %q\n", res.Input)
	fmt.Fprintf(w, "normalized: %q\n", res.InputNorm)
	if res.Year != 0 {
		fmt.Fprintf(w, "year:       %d\n", res.Year)
	}
	if res.Matched {
		fmt.Fprintf(w, "verdict:    MATCH (%q)\n\n", res.MatchedBy)
	} else {
		fmt.Fprint(w, "verdict:    NO MATCH\n\n")
	}

	if len(res.Candidates) == 0 {
		fmt.Fprintln(w, "no candidates to compare against")
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "MATCH\tDIST\tYEAR\tNOTE\tCANDIDATE")
	shownNonMatch := 0
	for _, c := range res.Candidates {
		if !c.Matched {
			if shownNonMatch >= maxProbeCandidatesShown {
				continue
			}
			shownNonMatch++
		}
		mark := ""
		if c.Matched {
			mark = "✓"
		}
		yearStr := ""
		if c.Year != 0 {
			yearStr = fmt.Sprintf("%d", c.Year)
		}
		note := ""
		switch {
		case c.PunctuationOnly:
			note = "punctuation-only diff"
		case !c.Matched && c.TitleMatched:
			note = "year mismatch"
		}
		fmt.Fprintf(tw, "%s\t%d\t%s\t%s\t%s\n", mark, c.Distance, yearStr, note, c.Norm)
	}
	tw.Flush()

	hidden := 0
	for _, c := range res.Candidates {
		if !c.Matched {
			hidden++
		}
	}
	if hidden > maxProbeCandidatesShown {
		fmt.Fprintf(w, "\n(%d more non-matching candidates hidden; nearest shown first)\n", hidden-maxProbeCandidatesShown)
	}
}
