package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

func init() {
	migrations = append(migrations, migration{
		version:     1,
		description: "normalize series tracker keys to lowercase show names",
		fn:          migrateNormalizeSeriesKeys,
	})
}

// migrateNormalizeSeriesKeys rewrites series bucket entries whose show-name
// portion (the part before the first '|') is not fully lowercased.
//
// Before ac9e5c7 (May 2026), matchShow returned the raw config show name so
// records landed under keys like "The Testaments|S01E07". After that commit it
// returns s.Norm (lowercased), so IsSeen("the testaments", "S01E07") looks for
// the wrong key and considers the episode unseen, triggering a re-download.
//
// For each stale entry this migration:
//  1. Computes the normalized key (lowercase show name, unchanged episode ID).
//  2. If the normalized key already exists, keeps whichever record has the
//     later DownloadedAt (non-zero beats zero; equal timestamps keep the
//     existing normalized record).
//  3. Inserts the normalized entry if it did not exist.
//  4. Deletes the old capitalized entry.
func migrateNormalizeSeriesKeys(tx *sql.Tx) error {
	// normShow mirrors match.Normalize without importing that package.
	normShow := func(s string) string {
		s = strings.ToLower(s)
		s = strings.Map(func(r rune) rune {
			if r == '.' || r == '_' || r == '-' {
				return ' '
			}
			return r
		}, s)
		return strings.Join(strings.Fields(s), " ")
	}

	// Only fetch entries where the show-name portion is not fully lowercase.
	rows, err := tx.Query(`
		SELECT key, value FROM store
		WHERE bucket = 'series'
		  AND instr(key, '|') > 0
		  AND substr(key, 1, instr(key, '|') - 1) != LOWER(substr(key, 1, instr(key, '|') - 1))
	`)
	if err != nil {
		return fmt.Errorf("query stale keys: %w", err)
	}

	type kv struct{ key, value string }
	var stale []kv
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		stale = append(stale, kv{k, v})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rows: %w", err)
	}

	for _, item := range stale {
		pipe := strings.Index(item.key, "|")
		if pipe < 0 {
			continue
		}
		showName := item.key[:pipe]
		epID := item.key[pipe+1:]
		normalizedShow := normShow(showName)
		normalizedKey := normalizedShow + "|" + epID

		// Rewrite series_name inside the JSON value to the normalized form.
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(item.value), &rec); err != nil {
			// Unparseable record — remove stale key and skip.
			if _, err2 := tx.Exec(`DELETE FROM store WHERE bucket='series' AND key=?`, item.key); err2 != nil {
				return fmt.Errorf("delete unparseable %q: %w", item.key, err2)
			}
			continue
		}
		rec["series_name"], _ = json.Marshal(normalizedShow)
		newValue, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal %q: %w", normalizedKey, err)
		}

		// Check whether the normalized key already has an entry.
		var existingValue string
		err = tx.QueryRow(
			`SELECT value FROM store WHERE bucket='series' AND key=?`, normalizedKey,
		).Scan(&existingValue)

		switch {
		case err == sql.ErrNoRows:
			// No collision — insert the normalized record.
			if _, err2 := tx.Exec(
				`INSERT INTO store (bucket, key, value) VALUES ('series', ?, ?)`,
				normalizedKey, string(newValue),
			); err2 != nil {
				return fmt.Errorf("insert %q: %w", normalizedKey, err2)
			}

		case err == nil:
			// Both old and new keys exist. Keep whichever DownloadedAt is later.
			var staleTime, existingTime struct {
				DownloadedAt time.Time `json:"downloaded_at"`
			}
			_ = json.Unmarshal([]byte(item.value), &staleTime)
			_ = json.Unmarshal([]byte(existingValue), &existingTime)

			if staleTime.DownloadedAt.After(existingTime.DownloadedAt) {
				if _, err2 := tx.Exec(
					`UPDATE store SET value=? WHERE bucket='series' AND key=?`,
					string(newValue), normalizedKey,
				); err2 != nil {
					return fmt.Errorf("update %q: %w", normalizedKey, err2)
				}
			}

		default:
			return fmt.Errorf("lookup %q: %w", normalizedKey, err)
		}

		// Remove the old capitalized entry.
		if _, err := tx.Exec(
			`DELETE FROM store WHERE bucket='series' AND key=?`, item.key,
		); err != nil {
			return fmt.Errorf("delete %q: %w", item.key, err)
		}
	}
	return nil
}
