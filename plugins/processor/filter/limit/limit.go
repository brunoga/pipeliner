// Package limit provides a filter processor that caps the number of accepted
// entries passing through to N, rejecting the rest. With an optional sort
// field, the winners are the top N by that field; otherwise the first N
// accepted entries in arrival order win.
//
// Config keys:
//
//	n     - max number of accepted entries to forward (required, must be >= 1)
//	sort  - entry field to sort by; absent means first-N arrival order
//	order - "desc" (default) or "asc" — direction when sort is set
//
// Entries missing the sort field are bucketed last regardless of order, so
// they never beat an entry that has the field.
package limit

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/brunoga/pipeliner/internal/dateparse"
	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "limit",
		Description: "cap the number of accepted entries to n; reject the rest",
		Role:        plugin.RoleProcessor,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "n", Type: plugin.FieldTypeInt, Required: true, Hint: "Maximum number of accepted entries to forward"},
			{Key: "sort", Type: plugin.FieldTypeString, Hint: "Entry field to sort by (default: arrival order)"},
			{Key: "order", Type: plugin.FieldTypeEnum, Enum: []string{"desc", "asc"}, Default: "desc", Hint: "Sort direction when sort is set (default: desc)"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	if n, ok := numericTo[int](cfg["n"]); !ok || n < 1 {
		errs = append(errs, fmt.Errorf("limit: %q must be set to an integer >= 1", "n"))
	}
	if err := plugin.OptEnum(cfg, "order", "limit", "desc", "asc"); err != nil {
		errs = append(errs, err)
	}
	if v, _ := cfg["order"].(string); v != "" {
		if s, _ := cfg["sort"].(string); s == "" {
			errs = append(errs, fmt.Errorf("limit: %q has no effect without %q", "order", "sort"))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "limit", "n", "sort", "order")...)
	return errs
}

type limitPlugin struct {
	n     int
	sort  string
	order string // "desc" or "asc"
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	n, ok := numericTo[int](cfg["n"])
	if !ok || n < 1 {
		return nil, fmt.Errorf("limit: %q must be at least 1", "n")
	}
	sortField, _ := cfg["sort"].(string)
	order, _ := cfg["order"].(string)
	if order == "" {
		order = "desc"
	}
	return &limitPlugin{n: n, sort: sortField, order: order}, nil
}

func (p *limitPlugin) Name() string { return "limit" }

func (p *limitPlugin) Process(_ context.Context, _ *plugin.TaskContext, entries []*entry.Entry) ([]*entry.Entry, error) {
	type slot struct {
		e         *entry.Entry
		idx       int  // arrival position among accepted entries
		hasSort   bool // sort field present on this entry
		sortValue any
	}

	var accepted []slot
	for _, e := range entries {
		if !e.IsAccepted() {
			continue
		}
		s := slot{e: e, idx: len(accepted)}
		if p.sort != "" {
			if v, ok := e.Get(p.sort); ok {
				s.hasSort = true
				// Promote string-encoded dates to time.Time so they compare
				// chronologically instead of lexicographically. Non-date
				// strings (Parse returns false) keep their original value
				// and compare as strings.
				if str, isStr := v.(string); isStr {
					if t, ok := dateparse.Parse(str); ok {
						v = t
					}
				}
				s.sortValue = v
			}
		}
		accepted = append(accepted, s)
	}

	if p.sort != "" && len(accepted) > 1 {
		sort.SliceStable(accepted, func(i, j int) bool {
			a, b := accepted[i], accepted[j]
			// Missing-field entries bucket to the end regardless of order.
			if a.hasSort != b.hasSort {
				return a.hasSort
			}
			if !a.hasSort {
				return a.idx < b.idx
			}
			cmp, ok := compareAny(a.sortValue, b.sortValue)
			if !ok || cmp == 0 {
				return a.idx < b.idx
			}
			if p.order == "asc" {
				return cmp < 0
			}
			return cmp > 0
		})
	}

	if len(accepted) > p.n {
		for _, s := range accepted[p.n:] {
			s.e.Reject(fmt.Sprintf("limit: %d-entry cap reached", p.n))
		}
	}

	return entry.PassThrough(entries), nil
}

// compareAny returns -1 / 0 / +1 if a < b / a == b / a > b. The bool is false
// when the values aren't comparable (different non-numeric types, or an
// unsupported type).
func compareAny(a, b any) (int, bool) {
	if af, ok := numericTo[float64](a); ok {
		if bf, ok := numericTo[float64](b); ok {
			return cmpOrdered(af, bf), true
		}
		return 0, false
	}
	switch av := a.(type) {
	case string:
		bv, ok := b.(string)
		if !ok {
			return 0, false
		}
		return cmpOrdered(av, bv), true
	case time.Time:
		bv, ok := b.(time.Time)
		if !ok {
			return 0, false
		}
		return av.Compare(bv), true
	case bool:
		bv, ok := b.(bool)
		if !ok {
			return 0, false
		}
		if av == bv {
			return 0, true
		}
		if !av {
			return -1, true
		}
		return 1, true
	}
	return 0, false
}

type ordered interface {
	~int | ~int64 | ~float64 | ~string
}

func cmpOrdered[T ordered](a, b T) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

type numeric interface {
	~int | ~int64 | ~float64
}

// numericTo converts a numeric value held in an `any` to T. Config maps and
// entry fields use int, int64, or float64; anything else returns (zero, false).
func numericTo[T numeric](v any) (T, bool) {
	switch n := v.(type) {
	case int:
		return T(n), true
	case int64:
		return T(n), true
	case float64:
		return T(n), true
	}
	var zero T
	return zero, false
}
