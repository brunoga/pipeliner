package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/brunoga/pipeliner/internal/config"
	"github.com/brunoga/pipeliner/internal/pipelinestate"
	"github.com/brunoga/pipeliner/internal/store"
)

func cmdPipelines(args []string) int {
	if len(args) == 0 {
		pipelinesUsage()
		return 1
	}
	switch args[0] {
	case "list":
		return pipelinesList(args[1:])
	case "rename", "adopt":
		// One operation, two names, because the two intents read differently
		// at the prompt: "rename" when the destination has no state, "adopt"
		// when it does and you mean to merge. The command checks which one it
		// actually is and says so rather than trusting the verb.
		return pipelinesMove(args[1:], args[0])
	case "forget":
		return pipelinesForget(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "unknown pipelines op %q\n", args[0])
		pipelinesUsage()
		return 1
	}
}

func pipelinesUsage() {
	fmt.Fprintln(os.Stderr, `usage:
  pipeliner pipelines list                      [--config path]
  pipeliner pipelines rename <old> <new>        [--config path] [--dry-run]
  pipeliner pipelines adopt  <from> <into>      [--config path] [--dry-run] [--prefer source|dest]
  pipeliner pipelines forget <name>             [--config path] [--dry-run] [--yes]

Pipelines keep state under their own name: seen records, movie trackers,
the discover search cache and the gap-scan cursor. Renaming a pipeline in a
config does not move any of it, and the store cannot tell a rename from a
delete followed by a create -- so these move it on purpose.

  rename   carry a pipeline's state to a new name (the usual case)
  adopt    merge one pipeline's state into another's, for folding two
           pipelines into one
  forget   delete the state of a pipeline that is gone for good

The daemon holds an exclusive lock on the database, so stop it first -- or use
the API (POST /api/pipelines/state/move) to do the same thing while it runs.`)
}

// openStateStore opens the database the given config points at.
func openStateStore(cfgPath string) (*store.SQLiteStore, error) {
	db, err := store.OpenSQLite(dbPath(cfgPath))
	if err != nil {
		return nil, fmt.Errorf("%w\n\n(if the daemon is running it holds the database lock: stop it, "+
			"or use POST /api/pipelines/state/move instead)", err)
	}
	return db, nil
}

// liveNames returns the pipeline names in the config, or nil when the config
// cannot be read -- nil means "do not judge", so a broken config never makes
// every pipeline look orphaned.
func liveNames(cfgPath string) []string {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: could not load %s (%v); state will not be checked against it\n", cfgPath, err)
		return nil
	}
	names := make([]string, 0, len(cfg.Graphs))
	for name := range cfg.Graphs {
		names = append(names, name)
	}
	return names
}

func pipelinesList(args []string) int {
	fs := flag.NewFlagSet("pipelines list", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	db, err := openStateStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	states, err := pipelinestate.Scan(db, liveNames(*cfgPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(states) == 0 {
		fmt.Println("no per-pipeline state in the database")
		return 0
	}
	fmt.Printf("%-32s %7s  %s\n", "PIPELINE", "RECORDS", "STATE")
	fmt.Println(strings.Repeat("-", 78))
	orphans := 0
	for _, st := range states {
		tag := ""
		if st.Orphaned {
			tag = "  ORPHANED"
			orphans++
		}
		fmt.Printf("%-32s %7d%s\n", st.Pipeline, st.Rows(), tag)
		for _, b := range st.Buckets {
			fmt.Printf("  %-30s %7d  %s\n", b.Bucket, b.Rows, b.Label)
		}
	}
	if orphans > 0 {
		fmt.Printf("\n%d pipeline(s) have state but no pipeline of that name.\n", orphans)
		fmt.Println("If one was renamed: pipeliner pipelines adopt <old> <new>")
		fmt.Println("If one is gone:     pipeliner pipelines forget <old>")
	}
	return 0
}

func pipelinesMove(args []string, verb string) int {
	fs := flag.NewFlagSet("pipelines "+verb, flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	dryRun := fs.Bool("dry-run", false, "report what would change and write nothing")
	prefer := fs.String("prefer", "", `which side wins where state cannot be merged: "source" or "dest"`)
	// Flags may follow the names, as they do for every other subcommand.
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 2 {
		fmt.Fprintf(os.Stderr, "usage: pipeliner pipelines %s <from> <to> [flags]\n", verb)
		return 1
	}
	from, to := rest[0], rest[1]

	db, err := openStateStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	rep, err := pipelinestate.Move(db, from, to, pipelinestate.MoveOptions{
		Prefer: pipelinestate.Side(*prefer),
		DryRun: *dryRun,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		if errors.Is(err, pipelinestate.ErrNeedPrefer) {
			fmt.Fprintln(os.Stderr, "\nRe-run with --prefer source (keep what is being moved) or "+
				"--prefer dest (keep what is already there). Nothing has been changed.")
		}
		return 1
	}
	if len(rep.Steps) == 0 {
		fmt.Printf("%s has no state to move\n", from)
		return 0
	}
	printReport(rep, from, to)
	return 0
}

func printReport(rep *pipelinestate.Report, from, to string) {
	what := "moved"
	if rep.DryRun {
		what = "would move"
	}
	fmt.Printf("%s state from %q to %q:\n", what, from, to)
	for _, s := range rep.Steps {
		fmt.Printf("  %-30s %d record(s) carried over", s.Bucket, s.Moved)
		if s.Conflicts > 0 {
			fmt.Printf(", %d already present (%d resolved in favour of %q)", s.Conflicts, s.SourceWon, from)
		}
		fmt.Println()
		if s.Note != "" {
			fmt.Printf("    note: %s\n", s.Note)
		}
	}
	if rep.DryRun {
		fmt.Println("\nnothing was written (--dry-run)")
	}
}

func pipelinesForget(args []string) int {
	fs := flag.NewFlagSet("pipelines forget", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file")
	dryRun := fs.Bool("dry-run", false, "report what would be deleted and delete nothing")
	yes := fs.Bool("yes", false, "skip the confirmation prompt")
	// Flags may follow the names, as they do for every other subcommand.
	if err := fs.Parse(flagsFirst(fs, args)); err != nil {
		return 1
	}
	rest := fs.Args()
	if len(rest) != 1 {
		fmt.Fprintln(os.Stderr, "usage: pipeliner pipelines forget <name> [flags]")
		return 1
	}
	name := rest[0]

	db, err := openStateStore(*cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	defer db.Close()

	// A pipeline that still exists is not a lifecycle case: forgetting its
	// state would make it re-download everything on its next run, which is
	// never what someone running "forget" means.
	for _, live := range liveNames(*cfgPath) {
		if live == name {
			fmt.Fprintf(os.Stderr, "error: pipeline %q still exists in %s; forget is for state whose pipeline is gone\n", name, *cfgPath)
			return 1
		}
	}

	// Always show what is at stake before deleting it, even with --yes.
	plan, err := pipelinestate.Forget(db, name, true)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(plan.Steps) == 0 {
		fmt.Printf("%s has no state to forget\n", name)
		return 0
	}
	total := plan.Total(func(s pipelinestate.Step) int { return s.Deleted })
	fmt.Printf("about to delete %d record(s) belonging to %q:\n", total, name)
	for _, s := range plan.Steps {
		fmt.Printf("  %-30s %d record(s)  %s\n", s.Bucket, s.Deleted, s.Label)
	}
	if *dryRun {
		fmt.Println("\nnothing was deleted (--dry-run)")
		return 0
	}
	if !*yes {
		fmt.Printf("\nThis cannot be undone without a database backup. Type the pipeline name to confirm: ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.TrimSpace(line) != name {
			fmt.Println("aborted")
			return 1
		}
	}
	rep, err := pipelinestate.Forget(db, name, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	fmt.Printf("deleted %d record(s)\n", rep.Total(func(s pipelinestate.Step) int { return s.Deleted }))
	return 0
}
