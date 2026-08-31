package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/store"
)

func TestCmdMigrateStatusThenApply(t *testing.T) {
	cfg := filepath.Join(t.TempDir(), "config.star")

	// Status on a fresh db: read-only, exit 0, leaves migrations pending.
	if code := cmdMigrate([]string{"--config", cfg}); code != 0 {
		t.Fatalf("status exit %d", code)
	}
	db, err := store.OpenSQLiteNoMigrate(dbPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	pending, _ := db.PendingMigrations()
	db.Close()
	if len(pending) == 0 {
		t.Fatalf("status should not have applied migrations")
	}

	// Apply: exit 0, nothing pending afterward.
	if code := cmdMigrate([]string{"--config", cfg, "--apply"}); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	db2, err := store.OpenSQLiteNoMigrate(dbPath(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	pending2, _ := db2.PendingMigrations()
	if len(pending2) != 0 {
		t.Errorf("apply left %d pending", len(pending2))
	}
}

func TestCmdMigrateBackup(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.star")
	// Create the db first.
	if code := cmdMigrate([]string{"--config", cfg, "--apply"}); code != 0 {
		t.Fatalf("apply exit %d", code)
	}
	dest := filepath.Join(dir, "backup.db")
	if code := cmdMigrate([]string{"--config", cfg, "--backup", dest}); code != 0 {
		t.Fatalf("backup exit %d", code)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Errorf("backup not created: %v", err)
	}
}

func TestMigrateStatusOutput(t *testing.T) {
	db, err := store.OpenSQLiteNoMigrate(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var buf bytes.Buffer
	if code := migrateStatus(&buf, db); code != 0 {
		t.Fatalf("migrateStatus exit %d", code)
	}
	out := buf.String()
	if !strings.Contains(out, "schema version:") {
		t.Errorf("missing version line:\n%s", out)
	}
	if !strings.Contains(out, "pending") {
		t.Errorf("expected pending migrations listed:\n%s", out)
	}
}
