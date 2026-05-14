package plugin

// RequireAll returns a Requires value where every field is independently
// required (AND semantics): each field becomes its own single-element group.
// This is equivalent to the old Requires []string behaviour.
func RequireAll(fields ...string) [][]string {
	groups := make([][]string, len(fields))
	for i, f := range fields {
		groups[i] = []string{f}
	}
	return groups
}

// RequireAny returns a Requires value where at least one of the given fields
// must be present (OR semantics): all fields are packed into a single group.
func RequireAny(fields ...string) [][]string {
	cp := make([]string, len(fields))
	copy(cp, fields)
	return [][]string{cp}
}
