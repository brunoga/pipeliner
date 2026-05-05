package plugin

import (
	"fmt"
	"slices"
	"strings"
	"time"
)

// RequireString returns an error if cfg[key] is absent or empty.
func RequireString(cfg map[string]any, key, plugin string) error {
	v, _ := cfg[key].(string)
	if v == "" {
		return fmt.Errorf("%s: %q is required", plugin, key)
	}
	return nil
}

// RequireOneOf returns an error if none of the keys are set.
func RequireOneOf(cfg map[string]any, plugin string, keys ...string) error {
	for _, k := range keys {
		switch v := cfg[k].(type) {
		case string:
			if v != "" {
				return nil
			}
		case []any:
			if len(v) > 0 {
				return nil
			}
		case []string:
			if len(v) > 0 {
				return nil
			}
		case bool:
			return nil
		}
	}
	return fmt.Errorf("%s: at least one of %s is required", plugin, strings.Join(keys, ", "))
}

// OptDuration returns an error if cfg[key] is set but not a valid duration.
func OptDuration(cfg map[string]any, key, plugin string) error {
	v, _ := cfg[key].(string)
	if v == "" {
		return nil
	}
	if _, err := time.ParseDuration(v); err != nil {
		return fmt.Errorf("%s: invalid %q %q: %v", plugin, key, v, err)
	}
	return nil
}

// OptEnum returns an error if cfg[key] is set but not one of the valid values.
func OptEnum(cfg map[string]any, key, plugin string, valid ...string) error {
	v, _ := cfg[key].(string)
	if v == "" {
		return nil
	}
	if slices.Contains(valid, v) {
		return nil
	}
	return fmt.Errorf("%s: %q must be one of %s, got %q", plugin, key, strings.Join(valid, "|"), v)
}
