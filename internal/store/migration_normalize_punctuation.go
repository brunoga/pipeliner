package store

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"
	"unicode"
)

func init() {
	migrations = append(migrations, migration{
		version:     3,
		description: "normalize tracker keys: strip punctuation from show/movie names",
		fn:          migrateNormalizePunctuation,
	})
}

// migrateNormalizePunctuation rewrites series and movies tracker keys whose
// name portion (the part before the first '|') still carries punctuation that
// match.Normalize now folds away.
//
// Before the punctuation-aware Normalize, only '.', '_', and '-' collapsed to
// spaces, so a show or movie whose canonical title contained a colon,
// apostrophe, ampersand, etc. was tracked under a key like
// "star trek: strange new worlds|S04E05". Normalize now reduces that to
// "star trek strange new worlds|S04E05", so a lookup with the new key would
// miss the old record and re-download an already-grabbed episode/movie. This
// migration renames the stored keys to the new normalized form.
//
// Mirrors migration 1's approach (see migrateNormalizeSeriesKeys) for each
// affected bucket:
//  1. Compute the normalized key (new name normalization, unchanged suffix).
//  2. On collision with an existing normalized key, keep whichever record has
//     the later DownloadedAt (equal timestamps keep the existing record).
//  3. Insert the normalized entry if it did not exist.
//  4. Delete the old punctuated entry.
//
// The normalization is frozen inline (not calling match.Normalize) so replaying
// this migration on a fresh database always reproduces the behavior it had when
// introduced, regardless of future Normalize changes — the same reason
// migration 1 inlines its own copy.
func migrateNormalizePunctuation(tx *sql.Tx) error {
	// normName is a frozen copy of match.Normalize as of this migration:
	// lowercase, drop apostrophes, keep glob metacharacters, and map every other
	// non-alphanumeric rune to a space before collapsing whitespace.
	normName := func(s string) string {
		s = strings.ToLower(s)
		var b strings.Builder
		b.Grow(len(s))
		for _, r := range s {
			switch {
			case r == '\'' || r == '’':
				// apostrophes vanish with no separator
			case r == '*' || r == '?' || r == '[' || r == ']' || r == '\\':
				b.WriteRune(r)
			case r == ' ' || unicode.IsLetter(r) || unicode.IsDigit(r):
				b.WriteRune(r)
			default:
				b.WriteRune(' ')
			}
		}
		return strings.Join(strings.Fields(b.String()), " ")
	}

	// buckets maps each tracker bucket to the JSON field holding the name that
	// must be rewritten to stay consistent with the key.
	buckets := []struct{ name, nameField string }{
		{"series", "series_name"},
		{"movies", "title"},
	}

	for _, bkt := range buckets {
		if err := normalizeBucketKeys(tx, bkt.name, bkt.nameField, normName); err != nil {
			return fmt.Errorf("bucket %s: %w", bkt.name, err)
		}
	}
	return nil
}

// normalizeBucketKeys rewrites every key in one bucket whose name portion
// changes under normName, merging collisions by DownloadedAt.
func normalizeBucketKeys(tx *sql.Tx, bucket, nameField string, normName func(string) string) error {
	rows, err := tx.Query(`SELECT key, value FROM store WHERE bucket = ?`, bucket)
	if err != nil {
		return fmt.Errorf("query keys: %w", err)
	}
	type kv struct{ key, value string }
	var all []kv
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			rows.Close()
			return fmt.Errorf("scan: %w", err)
		}
		all = append(all, kv{k, v})
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close rows: %w", err)
	}

	for _, item := range all {
		namePart, suffix := item.key, ""
		if i := strings.Index(item.key, "|"); i >= 0 {
			namePart, suffix = item.key[:i], item.key[i:]
		}
		normName2 := normName(namePart)
		if normName2 == namePart {
			continue // already normalized — nothing to do
		}
		newKey := normName2 + suffix

		// Rewrite the embedded name field so it matches the new key.
		var rec map[string]json.RawMessage
		if err := json.Unmarshal([]byte(item.value), &rec); err != nil {
			// Unparseable record — drop the stale key and move on.
			if _, err2 := tx.Exec(`DELETE FROM store WHERE bucket=? AND key=?`, bucket, item.key); err2 != nil {
				return fmt.Errorf("delete unparseable %q: %w", item.key, err2)
			}
			continue
		}
		rec[nameField], _ = json.Marshal(normName2)
		newValue, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("marshal %q: %w", newKey, err)
		}

		var existingValue string
		err = tx.QueryRow(`SELECT value FROM store WHERE bucket=? AND key=?`, bucket, newKey).Scan(&existingValue)
		switch {
		case err == sql.ErrNoRows:
			if _, err2 := tx.Exec(
				`INSERT INTO store (bucket, key, value) VALUES (?, ?, ?)`,
				bucket, newKey, string(newValue),
			); err2 != nil {
				return fmt.Errorf("insert %q: %w", newKey, err2)
			}
		case err == nil:
			// Collision: keep whichever record was downloaded later.
			var staleT, existingT struct {
				DownloadedAt time.Time `json:"downloaded_at"`
			}
			_ = json.Unmarshal([]byte(item.value), &staleT)
			_ = json.Unmarshal([]byte(existingValue), &existingT)
			if staleT.DownloadedAt.After(existingT.DownloadedAt) {
				if _, err2 := tx.Exec(
					`UPDATE store SET value=? WHERE bucket=? AND key=?`,
					string(newValue), bucket, newKey,
				); err2 != nil {
					return fmt.Errorf("update %q: %w", newKey, err2)
				}
			}
		default:
			return fmt.Errorf("lookup %q: %w", newKey, err)
		}

		if _, err := tx.Exec(`DELETE FROM store WHERE bucket=? AND key=?`, bucket, item.key); err != nil {
			return fmt.Errorf("delete %q: %w", item.key, err)
		}
	}
	return nil
}
