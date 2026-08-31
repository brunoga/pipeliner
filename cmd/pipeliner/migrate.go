package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/brunoga/pipeliner/internal/store"
)

// cmdMigrate inspects and applies database schema/data migrations without
// starting the daemon. Because OpenSQLite auto-migrates on startup, the value
// here is pre-flight: after upgrading the binary you can see what will run,
// take a backup, then apply — the exact dance the punctuation-normalization
// migration needed done by hand.
//
//	pipeliner migrate                      # show status (default; read-only)
//	pipeliner migrate --backup pipeliner.bak.db
//	pipeliner migrate --apply              # apply pending migrations
//	pipeliner migrate --apply --backup pipeliner.bak.db
//
// The daemon must be stopped — migrate opens the database directly.
func cmdMigrate(args []string) int {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	cfgPath := fs.String("config", "config.star", "path to config file (locates the database)")
	apply := fs.Bool("apply", false, "apply pending migrations (default is read-only status)")
	backup := fs.String("backup", "", "write a snapshot to this path before doing anything")
	if err := fs.Parse(args); err != nil {
		return 1
	}

	// Open without migrating so status/backup don't apply as a side effect.
	db, err := store.OpenSQLiteNoMigrate(dbPath(*cfgPath))
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: open store (is the daemon running? it holds the database lock): %v\n", err)
		return 1
	}
	defer db.Close()

	if *backup != "" {
		if err := db.Backup(*backup); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
		fmt.Printf("backup written to %s\n", *backup)
	}

	if !*apply {
		return migrateStatus(os.Stdout, db)
	}

	pending, err := db.PendingMigrations()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	if len(pending) == 0 {
		fmt.Println("database is up to date; nothing to apply")
		return 0
	}
	if err := db.ApplyMigrations(); err != nil {
		fmt.Fprintf(os.Stderr, "error: apply migrations: %v\n", err)
		return 1
	}
	fmt.Printf("applied %d migration(s):\n", len(pending))
	for _, m := range pending {
		fmt.Printf("  v%d  %s\n", m.Version, m.Description)
	}
	return 0
}

func migrateStatus(w io.Writer, db *store.SQLiteStore) int {
	cur, err := db.CurrentVersion()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	applied, err := db.AppliedMigrations()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	pending, err := db.PendingMigrations()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}

	fmt.Fprintf(w, "schema version: %d\n\n", cur)

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "STATUS\tVERSION\tAPPLIED AT\tDESCRIPTION")
	for _, m := range applied {
		desc := m.Description
		if m.Version == 0 && desc == "" {
			desc = "(baseline schema)"
		}
		fmt.Fprintf(tw, "applied\t%d\t%s\t%s\n", m.Version, m.AppliedAt, desc)
	}
	for _, m := range pending {
		fmt.Fprintf(tw, "pending\t%d\t%s\t%s\n", m.Version, "-", m.Description)
	}
	tw.Flush()

	if len(pending) == 0 {
		fmt.Fprintln(w, "\nup to date")
	} else {
		fmt.Fprintf(w, "\n%d migration(s) pending — run 'pipeliner migrate --apply' (optionally with --backup) to apply\n", len(pending))
	}
	return 0
}
