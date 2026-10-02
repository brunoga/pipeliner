package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/quality"
)

func init() {
	migrations = append(migrations, migration{
		version:     5,
		description: "merge series tracker keys that differ only by a trailing year",
		fn:          migrateMergeShowYears,
	})
}

// reMigrateTrailingYear matches a show key ending in a bare year:
// "brothers 2026" → ("brothers", "2026"). Frozen copy of
// series.reKeyTrailingYear: a migration must keep deciding the same way even
// if that regexp is later relaxed.
var reMigrateTrailingYear = regexp.MustCompile(`^(.*\S) ((?:19|20)\d{2})$`)

// migrateMergeShowYears collapses pairs of series tracker keys for the same
// show that differ only by a trailing year — "brothers" and "brothers 2026".
//
// Releases spell a premiere year inconsistently ("Brothers 2026 S01E01" vs
// "Brothers S01E01 2026") and TheTVDB adds and drops the year suffix as it
// disambiguates names, so before series.Resolve existed each spelling got its
// own tracker key and an episode could be recorded — and downloaded — twice.
// Resolve now treats the two as one show: reads span every spelling through
// GetAny and new records go to the year-carrying key. That makes the split
// invisible to matching but it is still visible in the data, which is what
// this migration fixes:
//
//   - Tracker.Summaries groups by the stored series_name, so one show is
//     listed twice in the web UI.
//   - Resolve pays a key scan per lookup to paper over the split.
//   - Each colliding episode holds two records describing two different files.
//
// The surviving key is the year-carrying spelling, matching Resolve's own
// preference, so new records keep landing where the merged ones now live.
//
// When both spellings hold the same episode the better quality wins, not the
// more recent record. That matters: the duplicate keys made the tracker think
// a tracked show was new, so the second grab was whatever the feed offered and
// was frequently *worse* than the first. On the author's database three of
// seven collisions would have kept a downgrade over the file actually on disk.
// Repack is carried forward when either record set it, since the flag records
// that a PROPER has already been taken for the episode.
//
// Groups with more than one year-carrying spelling ("the office 2001" and
// "the office 2005") are left alone: those are different shows and there is
// no safe merge.
func migrateMergeShowYears(tx *sql.Tx) error {
	rows, err := tx.Query(`SELECT key FROM store WHERE bucket='series' AND instr(key, '|') > 0`)
	if err != nil {
		return fmt.Errorf("query series keys: %w", err)
	}
	spellings := map[string]map[string]bool{} // base → show names
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan key: %w", err)
		}
		name, _, ok := splitShowKey(k)
		if !ok {
			continue
		}
		base := name
		if m := reMigrateTrailingYear.FindStringSubmatch(name); m != nil {
			base = m[1]
		}
		if spellings[base] == nil {
			spellings[base] = map[string]bool{}
		}
		spellings[base][name] = true
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close series keys: %w", err)
	}

	// canonical maps each non-surviving spelling to the key that absorbs it.
	canonical := map[string]string{}
	for _, names := range spellings {
		if len(names) < 2 {
			continue
		}
		var withYear []string
		for n := range names {
			if reMigrateTrailingYear.MatchString(n) {
				withYear = append(withYear, n)
			}
		}
		if len(withYear) != 1 {
			continue // ambiguous: two different premiere years, or none
		}
		for n := range names {
			if n != withYear[0] {
				canonical[n] = withYear[0]
			}
		}
	}
	if len(canonical) == 0 {
		return nil
	}

	// Sort for a deterministic order, so a collision resolves identically on
	// every database and the migration is reproducible.
	stale := make([]string, 0, len(canonical))
	for n := range canonical {
		stale = append(stale, n)
	}
	sort.Strings(stale)

	for _, name := range stale {
		if err := mergeShowKey(tx, name, canonical[name]); err != nil {
			return err
		}
	}
	return nil
}

// mergeShowKey moves every record under show name `from` to show name `to`.
func mergeShowKey(tx *sql.Tx, from, to string) error {
	rows, err := tx.Query(
		`SELECT key, value FROM store WHERE bucket='series' AND key LIKE ? || '|%'`, from)
	if err != nil {
		return fmt.Errorf("query records for %q: %w", from, err)
	}
	type kv struct{ key, value string }
	var records []kv
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan record for %q: %w", from, err)
		}
		records = append(records, kv{k, v})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close records for %q: %w", from, err)
	}

	for _, rec := range records {
		name, epID, ok := splitShowKey(rec.key)
		if !ok || name != from {
			// LIKE also matches a show whose name starts with `from` and
			// continues past the '|' we asked for; skip anything that is not
			// exactly this show.
			continue
		}
		target := to + "|" + epID
		merged, err := mergedRecord(tx, rec.value, to, target)
		if err != nil {
			return err
		}
		if merged != nil {
			if _, err := tx.Exec(
				`INSERT INTO store (bucket, key, value) VALUES ('series', ?, ?)
				 ON CONFLICT (bucket, key) DO UPDATE SET value = excluded.value`,
				target, string(merged),
			); err != nil {
				return fmt.Errorf("write %q: %w", target, err)
			}
		}
		if _, err := tx.Exec(
			`DELETE FROM store WHERE bucket='series' AND key=?`, rec.key); err != nil {
			return fmt.Errorf("delete %q: %w", rec.key, err)
		}
	}
	return nil
}

// mergedRecord returns the JSON to store at target, or nil to leave whatever
// is already there. value is the record being moved; canonicalName is the show
// name it must carry once moved.
func mergedRecord(tx *sql.Tx, value, canonicalName, target string) ([]byte, error) {
	// Decode as a map so fields this version does not know about survive.
	var moving map[string]json.RawMessage
	if err := json.Unmarshal([]byte(value), &moving); err != nil {
		return nil, nil // unparseable: drop it rather than overwrite a good record
	}

	var existing string
	err := tx.QueryRow(
		`SELECT value FROM store WHERE bucket='series' AND key=?`, target).Scan(&existing)
	switch {
	case err == sql.ErrNoRows:
		// No collision: move as-is, under the canonical name.
	case err == nil:
		keep, repack := betterRecord(value, existing)
		if !keep {
			if !repack {
				return nil, nil
			}
			// The record we are discarding was a PROPER/REPACK; the one we
			// keep must remember that so a REPACK does not chain forever.
			// Decode into a fresh map: unmarshalling into the populated one
			// would splice the discarded record's fields into it.
			moving = nil
			if err := json.Unmarshal([]byte(existing), &moving); err != nil {
				return nil, nil
			}
		}
		if repack {
			moving["repack"] = json.RawMessage("true")
		}
	default:
		return nil, fmt.Errorf("lookup %q: %w", target, err)
	}

	name, err := json.Marshal(canonicalName)
	if err != nil {
		return nil, fmt.Errorf("marshal show name %q: %w", canonicalName, err)
	}
	moving["series_name"] = name
	out, err := json.Marshal(moving)
	if err != nil {
		return nil, fmt.Errorf("marshal %q: %w", target, err)
	}
	return out, nil
}

// betterRecord reports whether the moving record should replace the existing
// one, and whether either of them was a repack.
func betterRecord(moving, existing string) (keepMoving, repack bool) {
	type rec struct {
		Quality      quality.Quality `json:"quality"`
		DownloadedAt time.Time       `json:"downloaded_at"`
		Repack       bool            `json:"repack"`
	}
	var m, e rec
	_ = json.Unmarshal([]byte(moving), &m)
	_ = json.Unmarshal([]byte(existing), &e)
	repack = m.Repack || e.Repack
	switch {
	case m.Quality.Better(e.Quality):
		return true, repack
	case e.Quality.Better(m.Quality):
		return false, repack
	default:
		// Same quality: the later download is the one the tracker last saw.
		return m.DownloadedAt.After(e.DownloadedAt), repack
	}
}

// splitShowKey splits a series tracker key into its show name and episode ID.
// The separator is the last '|' so a show name containing one is preserved.
func splitShowKey(key string) (name, episodeID string, ok bool) {
	i := strings.LastIndex(key, "|")
	if i <= 0 || i == len(key)-1 {
		return "", "", false
	}
	return key[:i], key[i+1:], true
}
