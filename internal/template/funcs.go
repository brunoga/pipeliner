// Package template provides a shared FuncMap for all Go templates in pipeliner.
//
// Register it on every template with:
//
//	tmpl, err := template.New("name").Funcs(tmplhelper.FuncMap()).Parse(expr)
package template

import (
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
		"formatdate": func(layout string, t time.Time) string {
			return t.Format(layout)
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
