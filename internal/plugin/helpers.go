package plugin

// IntVal returns cfg[key] as an int, falling back to def when the value is
// absent or not a numeric type. Config maps unmarshalled from Starlark use
// int, int64, or float64, all three of which are accepted.
func IntVal(v any, def int) int {
	switch t := v.(type) {
	case int:
		return t
	case int64:
		return int(t)
	case float64:
		return int(t)
	}
	return def
}

// ToStringSlice converts a config value into a []string. Accepts a single
// string, a []string, or a []any (silently dropping non-string elements).
// Returns nil for any other type or absent value. Use this when the schema
// allows either a scalar or a list (e.g. "static" lists).
func ToStringSlice(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []string:
		return t
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// OptBool returns cfg[key] as a bool, falling back to def when the value is
// absent or not a bool.
func OptBool(cfg map[string]any, key string, def bool) bool {
	if v, ok := cfg[key].(bool); ok {
		return v
	}
	return def
}
