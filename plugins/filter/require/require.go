// Package require provides a filter that rejects entries missing any of the
// specified fields (absent, nil, empty string, zero int, or zero time).
package require

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
	"github.com/brunoga/pipeliner/internal/store"
)

func init() {
	plugin.Register(&plugin.Descriptor{
		PluginName:  "require",
		Description: "reject entries that are missing any of the specified fields",
		PluginPhase: plugin.PhaseFilter,
		Factory:     newPlugin,
		Validate:    validate,
		Schema: []plugin.FieldSchema{
			{Key: "fields", Type: plugin.FieldTypeList, Required: true, Hint: "Entry field names that must be non-empty"},
		},
	})
}

func validate(cfg map[string]any) []error {
	var errs []error
	v := cfg["fields"]
	if v == nil {
		errs = append(errs, fmt.Errorf("require: \"fields\" must be a non-empty string or list of strings"))
	} else {
		fields, err := toStringSlice(v)
		if err != nil || len(fields) == 0 {
			errs = append(errs, fmt.Errorf("require: \"fields\" must be a non-empty string or list of strings"))
		}
	}
	errs = append(errs, plugin.OptUnknownKeys(cfg, "require", "fields")...)
	return errs
}

type requirePlugin struct {
	fields []string
}

func newPlugin(cfg map[string]any, _ *store.SQLiteStore) (plugin.Plugin, error) {
	fields, err := toStringSlice(cfg["fields"])
	if err != nil || len(fields) == 0 {
		return nil, fmt.Errorf("require: 'fields' must be a non-empty string or list of strings")
	}
	return &requirePlugin{fields: fields}, nil
}

func (p *requirePlugin) Name() string        { return "require" }
func (p *requirePlugin) Phase() plugin.Phase { return plugin.PhaseFilter }

func (p *requirePlugin) Filter(_ context.Context, _ *plugin.TaskContext, e *entry.Entry) error {
	for _, field := range p.fields {
		v, _ := e.Get(field)
		if isMissing(v) {
			e.Reject(fmt.Sprintf("missing required field: %s", field))
			return nil
		}
	}
	return nil
}

// isMissing reports whether a value is absent or the zero value of its type.
func isMissing(v any) bool {
	if v == nil {
		return true
	}
	switch t := v.(type) {
	case string:
		return t == ""
	case int:
		return t == 0
	case int64:
		return t == 0
	case float64:
		return t == 0
	case bool:
		return !t
	case time.Time:
		return t.IsZero()
	case []string:
		return len(t) == 0
	}
	return false
}

func toStringSlice(v any) ([]string, error) {
	switch t := v.(type) {
	case string:
		if strings.TrimSpace(t) == "" {
			return nil, fmt.Errorf("empty string")
		}
		return []string{t}, nil
	case []string:
		return t, nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			s, ok := item.(string)
			if !ok {
				return nil, fmt.Errorf("non-string item")
			}
			out = append(out, s)
		}
		return out, nil
	}
	return nil, fmt.Errorf("unsupported type %T", v)
}
