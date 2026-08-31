package store

import (
	"database/sql"
	"fmt"
	"log/slog"
	"slices"
	"time"
)

const migrationsSchema = `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version    INTEGER PRIMARY KEY,
	applied_at TEXT NOT NULL
)
`

type migration struct {
	version     int
	description string
	fn          func(*sql.Tx) error
}

// migrations is the ordered list of all schema and data migrations.
// Never remove or reorder entries — only append.
// Version 0 is the baseline (the initial schema); it is never a runnable fn.
var migrations []migration

func init() {
	// Sort after all per-migration init() functions have run (this file sorts
	// after migration_*.go alphabetically, so this init executes last).
	slices.SortFunc(migrations, func(a, b migration) int {
		return a.version - b.version
	})
}

func (s *SQLiteStore) migrate() error {
	return s.runMigrations(migrations)
}

// MigrationInfo describes one migration for reporting via `pipeliner migrate`.
type MigrationInfo struct {
	Version     int    `json:"version"`
	Description string `json:"description"`
	AppliedAt   string `json:"applied_at,omitempty"`
}

// migrationDescription returns the compiled description for a version, or "" if
// unknown (e.g. the version-0 baseline, which has no runnable fn).
func migrationDescription(version int) string {
	for _, m := range migrations {
		if m.version == version {
			return m.description
		}
	}
	return ""
}

// CurrentVersion returns the highest applied migration version (0 for a fresh
// or pre-migration database). It ensures the tracking table exists first.
func (s *SQLiteStore) CurrentVersion() (int, error) {
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		return 0, fmt.Errorf("store: create migrations table: %w", err)
	}
	var v int
	if err := s.db.QueryRow(`SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v); err != nil {
		return 0, fmt.Errorf("store: query migration version: %w", err)
	}
	return v, nil
}

// AppliedMigrations returns the applied migrations (version + timestamp),
// ascending, with descriptions filled in from the compiled set.
func (s *SQLiteStore) AppliedMigrations() ([]MigrationInfo, error) {
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		return nil, fmt.Errorf("store: create migrations table: %w", err)
	}
	rows, err := s.db.Query(`SELECT version, applied_at FROM schema_migrations ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("store: query applied migrations: %w", err)
	}
	defer rows.Close()
	var out []MigrationInfo
	for rows.Next() {
		var mi MigrationInfo
		if err := rows.Scan(&mi.Version, &mi.AppliedAt); err != nil {
			return nil, fmt.Errorf("store: scan migration: %w", err)
		}
		mi.Description = migrationDescription(mi.Version)
		out = append(out, mi)
	}
	return out, rows.Err()
}

// PendingMigrations returns the migrations not yet applied, ascending. On a
// fresh database this is every real migration (version > 0).
func (s *SQLiteStore) PendingMigrations() ([]MigrationInfo, error) {
	cur, err := s.CurrentVersion()
	if err != nil {
		return nil, err
	}
	var out []MigrationInfo
	for _, m := range migrations {
		if m.version > cur {
			out = append(out, MigrationInfo{Version: m.version, Description: m.description})
		}
	}
	return out, nil
}

// ApplyMigrations applies all pending migrations. Same effect as opening the
// store with OpenSQLite; exposed so `pipeliner migrate` can apply explicitly
// after a NoMigrate open (e.g. following a backup).
func (s *SQLiteStore) ApplyMigrations() error {
	return s.migrate()
}

// runMigrations creates the schema_migrations table if needed, stamps version 0
// for new or pre-migration databases, then applies every pending migration in
// order. Each migration runs in its own transaction so failures never affect
// previously committed work.
func (s *SQLiteStore) runMigrations(pending []migration) error {
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		return fmt.Errorf("store: create migrations table: %w", err)
	}

	var current int
	if err := s.db.QueryRow(
		`SELECT COALESCE(MAX(version), -1) FROM schema_migrations`,
	).Scan(&current); err != nil {
		return fmt.Errorf("store: query migration version: %w", err)
	}

	if current == -1 {
		// No migrations recorded: either a brand-new DB or a pre-migration
		// existing DB. In both cases the baseline schema is already in place.
		if _, err := s.db.Exec(
			`INSERT INTO schema_migrations (version, applied_at) VALUES (0, ?)`,
			time.Now().UTC().Format(time.RFC3339),
		); err != nil {
			return fmt.Errorf("store: stamp baseline: %w", err)
		}
		current = 0
	}

	for _, m := range pending {
		if m.version <= current {
			continue
		}
		if err := s.applyMigration(m); err != nil {
			return err
		}
		current = m.version
	}
	return nil
}

func (s *SQLiteStore) applyMigration(m migration) (retErr error) {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("store: migration %d: begin: %w", m.version, err)
	}
	defer func() {
		if retErr != nil {
			_ = tx.Rollback()
		}
	}()

	if err := m.fn(tx); err != nil {
		return fmt.Errorf("store: migration %d (%s): %w", m.version, m.description, err)
	}
	if _, err := tx.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)`,
		m.version, time.Now().UTC().Format(time.RFC3339),
	); err != nil {
		return fmt.Errorf("store: migration %d: record: %w", m.version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: migration %d: commit: %w", m.version, err)
	}
	slog.Info("store: applied migration", "version", m.version, "description", m.description)
	return nil
}
