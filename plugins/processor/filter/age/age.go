// Package age provides a filter processor that rejects entries based on how old
// they are according to a date field.
//
// Config keys:
//
//	field       - entry field to read the date from (default: "published_date")
//	newer_than  - reject entries older than this duration (e.g. "7d", "2w", "24h")
//	older_than  - reject entries newer than this duration
//	on_missing  - "pass" (default) or "reject" when the field is absent/unparseable
package age

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "age",
		Description: "reject entries whose date field falls outside a configured age range",
		Role:        plugin.RoleProcessor,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "field",      Type: plugin.FieldTypeString, Hint: "Entry field to read date from (default: published_date)"},
			{Key: "newer_than", Type: plugin.FieldTypeString, Hint: "Reject entries older than this duration (e.g. 7d, 2w, 24h)"},
			{Key: "older_than", Type: plugin.FieldTypeString, Hint: "Reject entries newer than this duration"},
			{Key: "on_missing", Type: plugin.FieldTypeEnum, Enum: []string{"pass", "reject"}, Hint: "Behaviour when field is absent or unparseable (default: pass)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if err := plugin.RequireOneOf(cfg, "age", "newer_than", "older_than"); err != nil {
		errs = append(errs, err)
	}
	if v, _ := cfg["newer_than"].(string); v != "" {
		if _, err := parseDuration(v); err != nil {
			errs = append(errs, fmt.Errorf("age: invalid newer_than %q: %v", v, err))
		}
	}
	if v, _ := cfg["older_than"].(string); v != "" {
		if _, err := parseDuration(v); err != nil {
			errs = append(errs, fmt.Errorf("age: invalid older_than %q: %v", v, err))
		}
	}
	if err := plugin.OptEnum(cfg, "on_missing", "age", "pass", "reject"); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "age", "field", "newer_than", "older_than", "on_missing")...)
	return errs
}

// parseDuration extends time.ParseDuration with d (days) and w (weeks) suffixes.
func parseDuration(s string) (time.Duration, error) {
	if rest, ok := strings.CutSuffix(s, "w"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		if n <= 0 {
			return 0, fmt.Errorf("duration must be positive, got %q", s)
		}
		return time.Duration(n) * 7 * 24 * time.Hour, nil
	}
	if rest, ok := strings.CutSuffix(s, "d"); ok {
		n, err := strconv.Atoi(rest)
		if err != nil {
			return 0, fmt.Errorf("invalid duration %q", s)
		}
		if n <= 0 {
			return 0, fmt.Errorf("duration must be positive, got %q", s)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return 0, err
	}
	if d <= 0 {
		return 0, fmt.Errorf("duration must be positive, got %q", s)
	}
	return d, nil
}

// dateLayouts is the ordered list of formats tried when the field value is a string.
var dateLayouts = []string{
	time.RFC3339,
	time.RFC822,
	time.RFC822Z,
	time.RFC1123,
	time.RFC1123Z,
	"Mon, 02 Jan 2006 15:04:05 -0700",
	"Mon, 02 Jan 2006 15:04:05 MST",
	"2006-01-02T15:04:05",
	"2006-01-02",
	"02 Jan 2006",
	"January 2, 2006",
	"Jan 2, 2006",
}

// entryTime extracts a time.Time from the named field of e.
// Returns the time and true on success, or zero and false if the field is
// missing or unparseable.
func entryTime(e *entry.Entry, field string) (time.Time, bool) {
	v, ok := e.Fields[field]
	if !ok {
		return time.Time{}, false
	}
	switch val := v.(type) {
	case time.Time:
		return val, true
	case string:
		for _, layout := range dateLayouts {
			if t, err := time.Parse(layout, val); err == nil {
				return t, true
			}
		}
		return time.Time{}, false
	default:
		return time.Time{}, false
	}
}

// humanDur expresses a duration as Xw, Xd, or Xh rounding to nearest hour.
func humanDur(d time.Duration) string {
	hours := int(d.Hours()+0.5)
	if hours < 0 {
		hours = -hours
	}
	if hours >= 7*24 && hours%(7*24) == 0 {
		return fmt.Sprintf("%dw", hours/(7*24))
	}
	if hours >= 24 && hours%24 == 0 {
		return fmt.Sprintf("%dd", hours/24)
	}
	if hours >= 7*24 {
		return fmt.Sprintf("%dw", hours/(7*24))
	}
	if hours >= 24 {
		return fmt.Sprintf("%dd", hours/24)
	}
	return fmt.Sprintf("%dh", hours)
}

type agePlugin struct {
	field     string
	newerThan time.Duration // zero means not set
	olderThan time.Duration // zero means not set
	onMissing string        // "pass" or "reject"
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	newerThanStr, _ := cfg["newer_than"].(string)
	olderThanStr, _ := cfg["older_than"].(string)

	if newerThanStr == "" && olderThanStr == "" {
		return nil, fmt.Errorf("age: at least one of 'newer_than' or 'older_than' must be set")
	}

	field, _ := cfg["field"].(string)
	if field == "" {
		field = entry.FieldPublishedDate
	}

	onMissing, _ := cfg["on_missing"].(string)
	if onMissing == "" {
		onMissing = "pass"
	}

	p := &agePlugin{
		field:     field,
		onMissing: onMissing,
	}

	if newerThanStr != "" {
		d, err := parseDuration(newerThanStr)
		if err != nil {
			return nil, fmt.Errorf("age: invalid newer_than: %w", err)
		}
		p.newerThan = d
	}

	if olderThanStr != "" {
		d, err := parseDuration(olderThanStr)
		if err != nil {
			return nil, fmt.Errorf("age: invalid older_than: %w", err)
		}
		p.olderThan = d
	}

	return p, nil
}

func (p *agePlugin) Name() string { return "age" }

func (p *agePlugin) Process(_ context.Context, tc *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	now := time.Now()
	for _, e := range entries {
		if e.IsRejected() || e.IsFailed() {
			continue
		}
		t, ok := entryTime(e, p.field)
		if !ok {
			if p.onMissing == "reject" {
				e.Reject("age: date field missing or unparseable")
			} else {
				tc.Logger.Debug("age: field missing or unparseable, passing entry", "entry", e.Title, "field", p.field)
			}
			continue
		}

		age := now.Sub(t)
		if p.newerThan > 0 && age > p.newerThan {
			e.Reject(fmt.Sprintf("age: entry is %s old (limit %s)", humanDur(age), humanDur(p.newerThan)))
			continue
		}
		if p.olderThan > 0 && age < p.olderThan {
			e.Reject(fmt.Sprintf("age: entry is only %s old (minimum %s)", humanDur(age), humanDur(p.olderThan)))
		}
	}
	return entry.PassThrough(entries), nil
}
