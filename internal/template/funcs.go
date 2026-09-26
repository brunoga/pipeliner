// Package template provides a shared FuncMap for all Go templates in pipeliner.
//
// Register it on every template with:
//
//	tmpl, err := template.New("name").Funcs(tmplhelper.FuncMap()).Parse(expr)
package template

import (
	"fmt"
	"math"

	"github.com/brunoga/pipeliner/internal/actionlink"
	"github.com/brunoga/pipeliner/internal/entry"
	"strings"
	"text/template"
	"time"
	"unicode"
)

// FuncMap returns a template.FuncMap with helper functions available in all
// pipeliner templates (pathfmt, set, condition, exec, print, email, etc.).
func FuncMap() template.FuncMap {
	return template.FuncMap{
		// String case
		"upper": strings.ToUpper,
		"lower": strings.ToLower,

		// Whitespace
		"trimspace": strings.TrimSpace,

		// slice from to s — returns s[from:to], clamping to string bounds.
		// Pipe-friendly: {{.date | slice 0 4}} → first four runes.
		"slice": func(from, to int, s string) string {
			r := []rune(s)
			if from < 0 {
				from = 0
			}
			if to > len(r) {
				to = len(r)
			}
			if from >= len(r) || from > to {
				return ""
			}
			return string(r[from:to])
		},

		// replace old new s — returns strings.ReplaceAll(s, old, new).
		// Pipe-friendly: {{.s | replace "." " "}}
		"replace": func(old, new, s string) string {
			return strings.ReplaceAll(s, old, new)
		},

		// default fallback x — returns x unless x is the zero value, in which
		// case fallback is returned. Pipe-friendly: {{.x | default "none"}}
		"default": func(fallback, x any) any {
			switch v := x.(type) {
			case nil:
				return fallback
			case string:
				if v == "" {
					return fallback
				}
			case int:
				if v == 0 {
					return fallback
				}
			case int64:
				if v == 0 {
					return fallback
				}
			case float64:
				if v == 0 {
					return fallback
				}
			case bool:
				if !v {
					return fallback
				}
			}
			return x
		},

		// join sep items — joins a string slice with sep. Accepts []string or
		// []any (e.g. when retrieved from an entry Fields map). Returns "" for
		// nil or unsupported types.
		// Pipe-friendly: {{.items | join ", "}}
		"join": func(sep string, items any) string {
			switch v := items.(type) {
			case []string:
				return strings.Join(v, sep)
			case []any:
				parts := make([]string, 0, len(v))
				for _, item := range v {
					if s, ok := item.(string); ok {
						parts = append(parts, s)
					}
				}
				return strings.Join(parts, sep)
			}
			return ""
		},

		// hasSuffix s suffix — reports whether s ends with suffix.
		// Pipe-friendly: {{.filename | hasSuffix ".rar"}}
		"hasSuffix": func(suffix, s string) bool {
			return strings.HasSuffix(s, suffix)
		},

		// hasPrefix s prefix — reports whether s starts with prefix.
		// Pipe-friendly: {{.filename | hasPrefix "the."}}
		"hasPrefix": func(prefix, s string) bool {
			return strings.HasPrefix(s, prefix)
		},

		// contains s sub — reports whether s contains sub.
		// Pipe-friendly: {{.title | contains "HDTV"}}
		"contains": func(sub, s string) bool {
			return strings.Contains(s, sub)
		},

		// now — returns the current UTC time.
		"now": func() time.Time {
			return time.Now().UTC()
		},

		// daysago n — returns the time n days before now (UTC).
		"daysago": func(n int) time.Time {
			return time.Now().UTC().AddDate(0, 0, -n)
		},

		// before a b — reports whether time a is before time b.
		"before": func(a, b time.Time) bool {
			return a.Before(b)
		},

		// after a b — reports whether time a is after time b.
		"after": func(a, b time.Time) bool {
			return a.After(b)
		},

		// parsedate s — parses a YYYY-MM-DD string into time.Time (UTC).
		// Returns the zero time on failure.
		"parsedate": func(s string) time.Time {
			t, _ := time.Parse("2006-01-02", s)
			return t.UTC()
		},

		// formatdate fmt t — formats t using the given Go time layout string.
		// signedaction mints a one-click link back to pipeliner, for putting
		// "follow this series" style buttons in notifications. Pairs after
		// the label become entry fields on the pushed item:
		//
		//	{{signedaction "favorites" "tvshows-favorite-add" "⭐ Follow"
		//	   (index .Fields "title") "tvdb_id" (index .Fields "tvdb_id")}}
		//
		// Renders an empty string when links are not configured, so a
		// template carrying one stays valid on installs without a public URL.
		"signedaction": func(queue, pipeline, label, title string, kv ...any) string {
			fields := map[string]string{}
			for i := 0; i+1 < len(kv); i += 2 {
				fields[fmt.Sprint(kv[i])] = fmt.Sprint(kv[i+1])
			}
			u, err := actionlink.URL(actionlink.Payload{
				Queue: queue, Pipeline: pipeline, Title: title, Fields: fields, Label: label,
			})
			if err != nil {
				return ""
			}
			return u
		},
		"formatdate": func(layout string, t time.Time) string {
			return t.Format(layout)
		},

		// duration d — renders a span as a compact human string ("3d 4h",
		// "45m", "12s"). Accepts a time.Duration or a plain number of
		// seconds, which is how torrent_seed_time and friends are stored.
		// Unparseable values render as an empty string.
		"duration": humanDuration,

		// ago t — renders how long ago t was ("6 days ago", "just now").
		// The zero time renders "never", so a template can use it directly
		// for fields like torrent_last_activity that may be absent.
		"ago": func(t time.Time) string {
			if t.IsZero() {
				return "never"
			}
			d := time.Since(t)
			if d < 0 {
				return "just now"
			}
			if d < time.Minute {
				return "just now"
			}
			return humanDuration(d) + " ago"
		},

		// filesize n — renders a byte count as "12.4 GB". Decimal units,
		// matching what download clients and indexers display. Accepts any
		// numeric type, which is how torrent_file_size and the transfer
		// counters are stored.
		"filesize": humanBytes,

		// rate n — renders a bytes-per-second value as "1.2 MB/s".
		"rate": func(v any) string {
			s := humanBytes(v)
			if s == "" {
				return ""
			}
			return s + "/s"
		},

		// sumfield name entries — totals a numeric field across entries,
		// for a headline figure like the bytes a purge reclaimed:
		//
		//	{{filesize (sumfield "torrent_downloaded" .Entries)}}
		//
		// Entries missing the field, or holding a non-numeric value,
		// contribute zero rather than breaking the template.
		"sumfield": func(name string, entries any) float64 {
			items, ok := entries.([]*entry.Entry)
			if !ok {
				return 0
			}
			var total float64
			for _, e := range items {
				if e == nil {
					continue
				}
				switch v := e.Fields[name].(type) {
				case int:
					total += float64(v)
				case int64:
					total += float64(v)
				case uint64:
					total += float64(v)
				case float64:
					total += v
				}
			}
			return total
		},

		// scrub s — sanitizes s for use as a path component on any filesystem
		// (replaces characters invalid on Windows or Linux with _).
		"scrub": func(s string) string { return scrubComponent(s, "generic") },

		// scrubwin s — sanitizes s for use as a Windows path component.
		"scrubwin": func(s string) string { return scrubComponent(s, "windows") },
	}
}

var windowsReserved = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true,
	"COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true,
	"LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

func scrubComponent(s, target string) string {
	if s == "" {
		return s
	}
	var b strings.Builder
	for _, r := range s {
		if isScrubInvalid(r, target) {
			b.WriteRune('_')
		} else {
			b.WriteRune(r)
		}
	}
	result := b.String()
	if target == "windows" || target == "generic" {
		result = strings.TrimRight(result, ". ")
		upper := strings.ToUpper(strings.SplitN(result, ".", 2)[0])
		if windowsReserved[upper] {
			result += "_"
		}
	}
	if result == "" {
		return "_"
	}
	return result
}

func isScrubInvalid(r rune, target string) bool {
	if r < 0x20 || r == 0x7f {
		return true
	}
	switch target {
	case "windows", "generic":
		if strings.ContainsRune(`<>:"/\|?*`, r) {
			return true
		}
	case "linux":
		if r == '/' || r == 0 {
			return true
		}
	}
	return !unicode.IsPrint(r)
}

// humanDuration renders a span as at most two units ("3d 4h", "45m 10s").
// It accepts a time.Duration or any numeric seconds value so templates can
// pass stored fields such as torrent_seed_time straight through.
func humanDuration(v any) string {
	var d time.Duration
	switch t := v.(type) {
	case time.Duration:
		d = t
	case int:
		d = time.Duration(t) * time.Second
	case int64:
		d = time.Duration(t) * time.Second
	case float64:
		d = time.Duration(t * float64(time.Second))
	case string:
		parsed, err := time.ParseDuration(t)
		if err != nil {
			return ""
		}
		d = parsed
	default:
		return ""
	}
	if d < 0 {
		d = -d
	}

	units := []struct {
		size time.Duration
		name string
	}{
		{24 * time.Hour, "d"},
		{time.Hour, "h"},
		{time.Minute, "m"},
		{time.Second, "s"},
	}
	// Start at the largest unit that actually has a value, then add the
	// next one down only when it is non-zero, so a round span renders as
	// "1h" rather than "1h 0m".
	start := -1
	for i, u := range units {
		if d/u.size > 0 {
			start = i
			break
		}
	}
	if start < 0 {
		return "0s"
	}
	n := d / units[start].size
	parts := []string{fmt.Sprintf("%d%s", n, units[start].name)}
	d -= n * units[start].size
	if next := start + 1; next < len(units) {
		if n2 := d / units[next].size; n2 > 0 {
			parts = append(parts, fmt.Sprintf("%d%s", n2, units[next].name))
		}
	}
	return strings.Join(parts, " ")
}

// humanBytes renders a byte count with decimal (SI) units, the convention
// download clients and indexers display. Values under 1 kB render as plain
// bytes; larger ones get one decimal place, dropped when it would be zero.
func humanBytes(v any) string {
	var n float64
	switch t := v.(type) {
	case int:
		n = float64(t)
	case int64:
		n = float64(t)
	case uint64:
		n = float64(t)
	case float64:
		n = t
	default:
		return ""
	}
	neg := n < 0
	if neg {
		n = -n
	}

	const unit = 1000
	if n < unit {
		return fmt.Sprintf("%s%d B", sign(neg), int64(n))
	}
	units := []string{"kB", "MB", "GB", "TB", "PB"}
	i := -1
	for n >= unit && i < len(units)-1 {
		n /= unit
		i++
	}
	if n >= 100 || n == math.Trunc(n) {
		return fmt.Sprintf("%s%.0f %s", sign(neg), n, units[i])
	}
	return fmt.Sprintf("%s%.1f %s", sign(neg), n, units[i])
}

func sign(neg bool) string {
	if neg {
		return "-"
	}
	return ""
}
