// Package scheduler provides interval and cron-expression based scheduling.
package scheduler

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Schedule returns the next time an event should fire after from.
type Schedule interface {
	Next(from time.Time) time.Time
}

// --- Interval schedule ---

type intervalSchedule struct{ d time.Duration }

// ParseInterval parses a Go duration string (e.g. "1h30m") as a repeating schedule.
func ParseInterval(s string) (Schedule, error) {
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("scheduler: invalid interval %q: %w", s, err)
	}
	if d <= 0 {
		return nil, fmt.Errorf("scheduler: interval must be positive, got %q", s)
	}
	return intervalSchedule{d}, nil
}

func (s intervalSchedule) Next(from time.Time) time.Time {
	return from.Add(s.d)
}

// --- Cron schedule ---

// cronField holds a bitset of allowed values for one cron field.
type cronField struct {
	any bool
	set [60]bool // large enough for all field types (max value: seconds=59, dow=6)
}

func (f *cronField) matches(v int) bool {
	if f.any || v < 0 {
		return true
	}
	if v >= len(f.set) {
		return false
	}
	return f.set[v]
}

// next returns the smallest value >= start that this field allows and a
// bool indicating whether the value wrapped (i.e. returned to min).
func (f *cronField) next(start, min, max int) (int, bool) {
	if f.any {
		if start > max {
			return min, true
		}
		return start, false
	}
	for v := start; v <= max; v++ {
		if f.set[v] {
			return v, false
		}
	}
	// wrap: find first match from min
	for v := min; v <= max; v++ {
		if f.set[v] {
			return v, true
		}
	}
	// should not reach here if field was validated
	return min, true
}

// cronSchedule is a 5-field standard cron expression.
type cronSchedule struct {
	min, hour, dom, month, dow cronField
}

// ParseCron parses a 5-field cron expression: "min hour dom month dow".
// Supports: * integers ranges(1-5) steps(*/2 1-5/2) lists(1,3,5).
func ParseCron(expr string) (Schedule, error) {
	fields := strings.Fields(expr)
	if len(fields) != 5 {
		return nil, fmt.Errorf("scheduler: cron expression must have 5 fields, got %d: %q", len(fields), expr)
	}
	type fieldSpec struct {
		min, max int
		target   *cronField
	}
	specs := []fieldSpec{
		{0, 59, nil},  // minute
		{0, 23, nil},  // hour
		{1, 31, nil},  // dom
		{1, 12, nil},  // month
		{0, 6, nil},   // dow
	}
	cs := &cronSchedule{}
	specs[0].target = &cs.min
	specs[1].target = &cs.hour
	specs[2].target = &cs.dom
	specs[3].target = &cs.month
	specs[4].target = &cs.dow

	for i, raw := range fields {
		if err := parseField(raw, specs[i].min, specs[i].max, specs[i].target); err != nil {
			return nil, fmt.Errorf("scheduler: cron field %d %q: %w", i+1, raw, err)
		}
	}
	return cs, nil
}

func parseField(raw string, min, max int, out *cronField) error {
	for part := range strings.SplitSeq(raw, ",") {
		if err := parsePart(part, min, max, out); err != nil {
			return err
		}
	}
	return nil
}

func parsePart(part string, min, max int, out *cronField) error {
	// Handle step: base/step
	step := 1
	if base, stepStr, ok := strings.Cut(part, "/"); ok {
		s, err := strconv.Atoi(stepStr)
		if err != nil || s <= 0 {
			return fmt.Errorf("invalid step %q", stepStr)
		}
		step = s
		part = base
	}

	// Determine range [lo, hi]
	lo, hi := min, max
	if part == "*" {
		if step == 1 {
			out.any = true
			return nil
		}
		// */N — mark every N-th value
	} else if loStr, hiStr, ok := strings.Cut(part, "-"); ok {
		var err error
		lo, err = strconv.Atoi(loStr)
		if err != nil {
			return fmt.Errorf("invalid range start %q", loStr)
		}
		hi, err = strconv.Atoi(hiStr)
		if err != nil {
			return fmt.Errorf("invalid range end %q", hiStr)
		}
	} else {
		v, err := strconv.Atoi(part)
		if err != nil {
			return fmt.Errorf("invalid value %q", part)
		}
		lo, hi = v, v
	}

	if lo < min || hi > max || lo > hi {
		return fmt.Errorf("value %d-%d out of range [%d, %d]", lo, hi, min, max)
	}
	for v := lo; v <= hi; v += step {
		out.set[v] = true
	}
	return nil
}

// Next returns the next time after from (exclusive) that matches the expression.
// Times are computed at minute granularity.
func (cs *cronSchedule) Next(from time.Time) time.Time {
	// Advance to the start of the next minute.
	t := from.Truncate(time.Minute).Add(time.Minute)

	// Safety limit: 4 years of search.
	limit := from.Add(4 * 365 * 24 * time.Hour)

	for t.Before(limit) {
		// Month check
		if !cs.month.matches(int(t.Month())) {
			m, wrap := cs.month.next(int(t.Month()), 1, 12)
			if wrap {
				t = time.Date(t.Year()+1, time.Month(m), 1, 0, 0, 0, 0, t.Location())
			} else {
				t = time.Date(t.Year(), time.Month(m), 1, 0, 0, 0, 0, t.Location())
			}
			continue
		}

		// Day-of-week and day-of-month checks.
		// When both are restricted (non-*), either can satisfy (standard cron behaviour).
		domOK := cs.dom.matches(t.Day())
		dowOK := cs.dow.matches(int(t.Weekday()))
		domRestricted := !cs.dom.any
		dowRestricted := !cs.dow.any
		dayOK := false
		switch {
		case domRestricted && dowRestricted:
			dayOK = domOK || dowOK
		case domRestricted:
			dayOK = domOK
		case dowRestricted:
			dayOK = dowOK
		default:
			dayOK = true
		}

		if !dayOK {
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			continue
		}

		// Hour check
		if !cs.hour.matches(t.Hour()) {
			h, wrap := cs.hour.next(t.Hour(), 0, 23)
			if wrap {
				t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, t.Location())
			} else {
				t = time.Date(t.Year(), t.Month(), t.Day(), h, 0, 0, 0, t.Location())
			}
			continue
		}

		// Minute check
		if !cs.min.matches(t.Minute()) {
			m, wrap := cs.min.next(t.Minute(), 0, 59)
			if wrap {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, t.Location())
			} else {
				t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), m, 0, 0, t.Location())
			}
			continue
		}

		return t
	}

	// Should not happen with valid expressions.
	return limit
}
