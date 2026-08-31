package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/brunoga/pipeliner/internal/quality"
)

// cmdQuality answers "what quality does this release parse to, and does it
// satisfy my spec?" — the quality-side companion to `pipeliner match`. When a
// download is unexpectedly filtered by the quality node, this shows which
// dimension failed instead of leaving it to a log dive.
//
//	pipeliner quality "Show S01E01 720p WEB-DL x265"
//	    Parse and print the detected quality only.
//
//	pipeliner quality "Show S01E01 720p WEB-DL x265" "1080p+"
//	pipeliner quality --spec "1080p+" "Show S01E01 720p WEB-DL x265"
//	    Parse the release title, then report per-dimension whether it satisfies
//	    the spec.
func cmdQuality(args []string) int {
	fs := flag.NewFlagSet("quality", flag.ContinueOnError)
	specFlag := fs.String("spec", "", "quality spec to test against (e.g. \"720p-1080p webrip+\")")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) == 0 {
		fmt.Fprintln(os.Stderr, "usage: pipeliner quality \"<release title>\" [\"<spec>\"]")
		fmt.Fprintln(os.Stderr, "       pipeliner quality --spec \"<spec>\" \"<release title>\"")
		return 1
	}
	title := rest[0]
	specStr := *specFlag
	if specStr == "" && len(rest) > 1 {
		specStr = rest[1]
	}

	q := quality.Parse(title)

	if specStr == "" {
		fmt.Printf("title:   %q\n", title)
		fmt.Printf("quality: %s\n", q.String())
		return 0
	}

	spec, err := quality.ParseSpec(specStr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	res := spec.Explain(q)
	printSpecResult(os.Stdout, title, res)
	if res.Matched {
		return 0
	}
	return 1
}

func printSpecResult(w io.Writer, title string, res quality.SpecResult) {
	fmt.Fprintf(w, "title:   %q\n", title)
	fmt.Fprintf(w, "quality: %s\n", res.Quality)
	fmt.Fprintf(w, "spec:    %s\n", res.Spec)
	if res.Matched {
		fmt.Fprint(w, "verdict: MATCH\n\n")
	} else {
		fmt.Fprint(w, "verdict: NO MATCH\n\n")
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "OK\tDIMENSION\tCONSTRAINT\tVALUE\tNOTE")
	for _, d := range res.Dimensions {
		if !d.Constrained {
			continue // hide dimensions the spec does not restrict
		}
		mark := "✓"
		if !d.Passed {
			mark = "✗"
		}
		note := ""
		if d.Bypassed {
			note = "bypassed (optional, value unknown)"
		} else if !d.Passed {
			note = "does not satisfy constraint"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", mark, d.Name, d.Constraint, d.Value, note)
	}
	tw.Flush()
}
