package store

import (
	"database/sql"
	"encoding/json"
	"testing"
)

// seedBare returns a DB with the schema and the baseline stamped, but no data
// migrations applied, so a test can run a chosen subset against fixture rows.
func seedBare(t *testing.T) *SQLiteStore {
	t.Helper()
	s := openBare(t, ":memory:")
	if _, err := s.db.Exec(schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if _, err := s.db.Exec(migrationsSchema); err != nil {
		t.Fatalf("create migrations schema: %v", err)
	}
	if _, err := s.db.Exec(
		`INSERT INTO schema_migrations (version, applied_at) VALUES (0, '2026-01-01T00:00:00Z')`,
	); err != nil {
		t.Fatalf("stamp baseline: %v", err)
	}
	return s
}

// migrationsUpTo selects migrations by version rather than by slice position,
// so appending a new one never silently changes what a test exercises.
func migrationsUpTo(v int) []migration {
	var out []migration
	for _, m := range migrations {
		if m.version <= v {
			out = append(out, m)
		}
	}
	return out
}

func put(t *testing.T, s *SQLiteStore, bucket, key, value string) {
	t.Helper()
	if _, err := s.db.Exec(
		`INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)`, bucket, key, value,
	); err != nil {
		t.Fatalf("seed %s/%s: %v", bucket, key, err)
	}
}

func get(t *testing.T, s *SQLiteStore, bucket, key string) (string, bool) {
	t.Helper()
	var v string
	err := s.db.QueryRow(
		`SELECT value FROM store WHERE bucket=? AND key=?`, bucket, key).Scan(&v)
	if err == sql.ErrNoRows {
		return "", false
	}
	if err != nil {
		t.Fatalf("get %s/%s: %v", bucket, key, err)
	}
	return v, true
}

// seriesRecord is the subset of a tracker record these tests assert on.
type seriesRecord struct {
	SeriesName   string `json:"series_name"`
	DisplayName  string `json:"display_name"`
	EpisodeID    string `json:"episode_id"`
	DownloadedAt string `json:"downloaded_at"`
	Repack       bool   `json:"repack"`
	Quality      struct {
		String string `json:"string"`
	} `json:"quality"`
}

func readSeries(t *testing.T, s *SQLiteStore, key string) seriesRecord {
	t.Helper()
	raw, ok := get(t, s, "series", key)
	if !ok {
		t.Fatalf("series record %q missing", key)
	}
	var rec seriesRecord
	if err := json.Unmarshal([]byte(raw), &rec); err != nil {
		t.Fatalf("unmarshal %q: %v", key, err)
	}
	return rec
}

func mustNotExist(t *testing.T, s *SQLiteStore, bucket, key string) {
	t.Helper()
	if _, ok := get(t, s, bucket, key); ok {
		t.Errorf("%s/%s still present", bucket, key)
	}
}

// rec builds a tracker record. The quality dimensions use the stored wire
// shape so Quality.Better sees the same numbers production does.
func rec(name, epID, at string, res, src, codec, audio, color int) string {
	return `{"series_name":"` + name + `","episode_id":"` + epID +
		`","downloaded_at":"` + at + `","quality":{"Resolution":` + itoa(res) +
		`,"Source":` + itoa(src) + `,"Codec":` + itoa(codec) + `,"Audio":` + itoa(audio) +
		`,"ColorRange":` + itoa(color) + `,"Format3D":0}}`
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

func TestMergeShowYearsKeepsTheBetterQuality(t *testing.T) {
	s := seedBare(t)
	// The year key holds 2160p Dolby Vision, the bare key a later 1080p. The
	// later record is the worse one: that is the shape the duplicate-key bug
	// produced, because the second grab saw the show as untracked and took
	// whatever the feed offered.
	put(t, s, "series", "brothers 2026|S01E01",
		rec("brothers 2026", "S01E01", "2026-09-23T02:00:04Z", 6, 8, 4, 6, 4))
	put(t, s, "series", "brothers|S01E01",
		rec("brothers", "S01E01", "2026-09-30T20:00:05Z", 5, 8, 3, 6, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	mustNotExist(t, s, "series", "brothers|S01E01")
	got := readSeries(t, s, "brothers 2026|S01E01")
	if got.DownloadedAt != "2026-09-23T02:00:04Z" {
		t.Errorf("kept record downloaded_at = %q, want the 2160p one", got.DownloadedAt)
	}
	if got.SeriesName != "brothers 2026" {
		t.Errorf("series_name = %q, want %q", got.SeriesName, "brothers 2026")
	}
}

func TestMergeShowYearsMovesTheBetterBareRecord(t *testing.T) {
	s := seedBare(t)
	// Mirrors the production "last seen" pair: the bare key holds 2160p DV,
	// the year key a 720p downgrade grabbed an hour later.
	put(t, s, "series", "last seen|S01E01",
		rec("last seen", "S01E01", "2026-09-09T02:00:03Z", 6, 8, 4, 6, 4))
	put(t, s, "series", "last seen 2026|S01E01",
		rec("last seen 2026", "S01E01", "2026-09-09T03:00:02Z", 4, 8, 3, 0, 0))
	// Episodes only the bare key has must move too, or the show stays split
	// across two keys and is still listed twice.
	put(t, s, "series", "last seen|S01E02",
		rec("last seen", "S01E02", "2026-09-09T21:15:21Z", 5, 8, 3, 6, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	mustNotExist(t, s, "series", "last seen|S01E01")
	mustNotExist(t, s, "series", "last seen|S01E02")

	e1 := readSeries(t, s, "last seen 2026|S01E01")
	if e1.DownloadedAt != "2026-09-09T02:00:03Z" {
		t.Errorf("S01E01 kept %q, want the 2160p record", e1.DownloadedAt)
	}
	if e1.SeriesName != "last seen 2026" {
		t.Errorf("S01E01 series_name = %q, want the canonical key", e1.SeriesName)
	}
	e2 := readSeries(t, s, "last seen 2026|S01E02")
	if e2.SeriesName != "last seen 2026" {
		t.Errorf("S01E02 series_name = %q, want the canonical key", e2.SeriesName)
	}
}

func TestMergeShowYearsBreaksQualityTiesByTime(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "the show 2026|S01E01",
		rec("the show 2026", "S01E01", "2026-01-01T00:00:00Z", 5, 8, 3, 6, 0))
	put(t, s, "series", "the show|S01E01",
		rec("the show", "S01E01", "2026-02-01T00:00:00Z", 5, 8, 3, 6, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	got := readSeries(t, s, "the show 2026|S01E01")
	if got.DownloadedAt != "2026-02-01T00:00:00Z" {
		t.Errorf("tie kept %q, want the later record", got.DownloadedAt)
	}
}

func TestMergeShowYearsCarriesRepackForward(t *testing.T) {
	s := seedBare(t)
	// The PROPER is the lower-quality record, so it loses — but the surviving
	// record must still remember a PROPER was taken, or a REPACK at the same
	// quality is accepted again on the next run.
	put(t, s, "series", "brothers 2026|S01E02",
		`{"series_name":"brothers 2026","episode_id":"S01E02","downloaded_at":"2026-09-23T22:15:36Z","quality":{"Resolution":5,"Source":8,"Codec":3,"Audio":0,"ColorRange":0,"Format3D":0},"repack":true}`)
	put(t, s, "series", "brothers|S01E02",
		rec("brothers", "S01E02", "2026-10-01T03:15:06Z", 5, 8, 3, 6, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	got := readSeries(t, s, "brothers 2026|S01E02")
	if got.DownloadedAt != "2026-10-01T03:15:06Z" {
		t.Errorf("kept %q, want the Atmos record", got.DownloadedAt)
	}
	if !got.Repack {
		t.Error("repack not carried forward from the discarded PROPER")
	}
}

func TestMergeShowYearsMovesRecordsWithoutCollision(t *testing.T) {
	s := seedBare(t)
	// The production "from"/"from 2022" shape: a premiere record under the
	// year key and a whole season under the bare one, no episode in common.
	put(t, s, "series", "from 2022|S01E01",
		rec("from 2022", "S01E01", "2026-05-16T21:00:00Z", 5, 8, 0, 2, 0))
	for _, ep := range []string{"S04E01", "S04E02", "S04E03"} {
		put(t, s, "series", "from|"+ep,
			`{"series_name":"from","display_name":"FROM","episode_id":"`+ep+
				`","downloaded_at":"2026-06-28T07:15:22Z","quality":{"Resolution":5,"Source":8,"Codec":3,"Audio":2,"ColorRange":0,"Format3D":0}}`)
	}

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	for _, ep := range []string{"S04E01", "S04E02", "S04E03"} {
		mustNotExist(t, s, "series", "from|"+ep)
		got := readSeries(t, s, "from 2022|"+ep)
		if got.SeriesName != "from 2022" {
			t.Errorf("%s series_name = %q, want %q", ep, got.SeriesName, "from 2022")
		}
		if got.DisplayName != "FROM" {
			t.Errorf("%s display_name = %q, want it preserved", ep, got.DisplayName)
		}
	}
	if _, ok := get(t, s, "series", "from 2022|S01E01"); !ok {
		t.Error("the premiere record under the year key was lost")
	}
}

func TestMergeShowYearsLeavesAmbiguousGroupsAlone(t *testing.T) {
	s := seedBare(t)
	// Two different shows sharing a base name: no merge is safe.
	put(t, s, "series", "the office|S01E01",
		rec("the office", "S01E01", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))
	put(t, s, "series", "the office 2001|S01E01",
		rec("the office 2001", "S01E01", "2026-01-02T00:00:00Z", 5, 8, 3, 0, 0))
	put(t, s, "series", "the office 2005|S01E01",
		rec("the office 2005", "S01E01", "2026-01-03T00:00:00Z", 5, 8, 3, 0, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	for _, k := range []string{"the office|S01E01", "the office 2001|S01E01", "the office 2005|S01E01"} {
		if _, ok := get(t, s, "series", k); !ok {
			t.Errorf("%q was merged away, but the group is ambiguous", k)
		}
	}
}

func TestMergeShowYearsIgnoresUnrelatedShows(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "brothers|S01E01",
		rec("brothers", "S01E01", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))
	put(t, s, "series", "brothers 2026|S01E02",
		rec("brothers 2026", "S01E02", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))
	// A different show whose name merely starts with the merged one. The
	// move query matches on a key prefix, so this is the row that catches a
	// prefix that is not a whole show name.
	put(t, s, "series", "brothers in arms|S01E01",
		rec("brothers in arms", "S01E01", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))
	// A show with a single spelling and a trailing year must not be touched.
	put(t, s, "series", "winter 2026|S01E01",
		rec("winter 2026", "S01E01", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	if got := readSeries(t, s, "brothers in arms|S01E01"); got.SeriesName != "brothers in arms" {
		t.Errorf("unrelated show rewritten to %q", got.SeriesName)
	}
	if got := readSeries(t, s, "winter 2026|S01E01"); got.SeriesName != "winter 2026" {
		t.Errorf("single-spelling show rewritten to %q", got.SeriesName)
	}
	if _, ok := get(t, s, "series", "brothers 2026|S01E01"); !ok {
		t.Error("brothers|S01E01 was not moved to the year key")
	}
}

func TestMergeShowYearsPreservesUnknownFields(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "nova 2026|S01E02",
		rec("nova 2026", "S01E02", "2026-01-01T00:00:00Z", 5, 8, 3, 0, 0))
	put(t, s, "series", "nova|S01E01",
		`{"series_name":"nova","episode_id":"S01E01","downloaded_at":"2026-01-01T00:00:00Z","quality":{"Resolution":5,"Source":8,"Codec":3,"Audio":0,"ColorRange":0,"Format3D":0},"future_field":{"a":1}}`)

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("runMigrations: %v", err)
	}

	raw, ok := get(t, s, "series", "nova 2026|S01E01")
	if !ok {
		t.Fatal("record not moved")
	}
	var all map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &all); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, ok := all["future_field"]; !ok {
		t.Error("future_field dropped by the merge")
	}
}

func TestMergeShowYearsIsIdempotent(t *testing.T) {
	s := seedBare(t)
	put(t, s, "series", "brothers 2026|S01E01",
		rec("brothers 2026", "S01E01", "2026-09-23T02:00:04Z", 6, 8, 4, 6, 4))
	put(t, s, "series", "brothers|S01E01",
		rec("brothers", "S01E01", "2026-09-30T20:00:05Z", 5, 8, 3, 6, 0))

	if err := s.runMigrations(migrationsUpTo(5)); err != nil {
		t.Fatalf("first run: %v", err)
	}
	before := readSeries(t, s, "brothers 2026|S01E01")
	runInTx(t, s, migrateMergeShowYears)
	after := readSeries(t, s, "brothers 2026|S01E01")
	if before != after {
		t.Errorf("second run changed the record: %+v → %+v", before, after)
	}
}

// runInTx applies a migration function directly, outside the version
// bookkeeping, so a test can run one twice. The transaction is committed
// before returning: the store holds a single connection, so leaving it open
// would deadlock the next read.
func runInTx(t *testing.T, s *SQLiteStore, fn func(*sql.Tx) error) {
	t.Helper()
	tx, err := s.db.Begin()
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		t.Fatalf("migration: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
}
