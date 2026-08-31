package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMigrationStatusFreshThenApply(t *testing.T) {
	s, err := OpenSQLiteNoMigrate(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Fresh database: version 0, everything pending, nothing applied.
	if v, err := s.CurrentVersion(); err != nil || v != 0 {
		t.Fatalf("CurrentVersion = %d err=%v, want 0", v, err)
	}
	pending, err := s.PendingMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != len(migrations) {
		t.Fatalf("pending = %d, want all %d migrations", len(pending), len(migrations))
	}
	// Pending are ascending and carry descriptions.
	for i := 1; i < len(pending); i++ {
		if pending[i-1].Version >= pending[i].Version {
			t.Errorf("pending not ascending: %+v", pending)
		}
	}
	if applied, _ := s.AppliedMigrations(); len(applied) != 0 {
		t.Errorf("applied = %d, want 0", len(applied))
	}

	// Apply, then nothing should remain pending.
	if err := s.ApplyMigrations(); err != nil {
		t.Fatal(err)
	}
	pending, _ = s.PendingMigrations()
	if len(pending) != 0 {
		t.Errorf("pending after apply = %d, want 0", len(pending))
	}
	applied, _ := s.AppliedMigrations()
	if len(applied) == 0 {
		t.Fatalf("applied is empty after apply")
	}
	// Highest applied equals the highest compiled migration version.
	top := migrations[len(migrations)-1].version
	if v, _ := s.CurrentVersion(); v != top {
		t.Errorf("CurrentVersion after apply = %d, want %d", v, top)
	}
	// The baseline (version 0) is recorded with an empty description.
	if applied[0].Version != 0 || applied[0].Description != "" {
		t.Errorf("first applied = %+v, want version 0 baseline", applied[0])
	}
}

func TestOpenSQLiteAppliesMigrations(t *testing.T) {
	// The normal open must leave nothing pending.
	s, err := OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	pending, err := s.PendingMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(pending) != 0 {
		t.Errorf("OpenSQLite left %d pending migrations", len(pending))
	}
}

func TestBackup(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "pipeliner.db")
	s, err := OpenSQLite(src)
	if err != nil {
		t.Fatal(err)
	}
	// Write something so the backup has content to preserve.
	if err := s.Bucket("series").Put("silo|S01E01", map[string]any{"episode_id": "S01E01"}); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "backup.db")
	if err := s.Backup(dest); err != nil {
		t.Fatalf("backup: %v", err)
	}
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("backup file not created: %v", err)
	}
	s.Close()

	// The backup opens and carries the data.
	b, err := OpenSQLite(dest)
	if err != nil {
		t.Fatalf("open backup: %v", err)
	}
	defer b.Close()
	var rec map[string]any
	found, err := b.Bucket("series").Get("silo|S01E01", &rec)
	if err != nil || !found {
		t.Errorf("backup missing data: found=%v err=%v", found, err)
	}
}

func TestBackupRefusesExistingDestination(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSQLite(filepath.Join(dir, "pipeliner.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	dest := filepath.Join(dir, "exists.db")
	if err := os.WriteFile(dest, []byte("x"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.Backup(dest); err == nil {
		t.Errorf("expected error backing up over an existing file")
	}
}
