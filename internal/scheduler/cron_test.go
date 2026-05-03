package scheduler

import (
	"testing"
	"time"
)

var utc = time.UTC

func ts(year, month, day, hour, min int) time.Time {
	return time.Date(year, time.Month(month), day, hour, min, 0, 0, utc)
}

// --- ParseInterval ---

func TestParseIntervalValid(t *testing.T) {
	cases := []struct {
		s    string
		want time.Duration
	}{
		{"1h", time.Hour},
		{"30m", 30 * time.Minute},
		{"1h30m", 90 * time.Minute},
		{"24h", 24 * time.Hour},
	}
	for _, tc := range cases {
		s, err := ParseInterval(tc.s)
		if err != nil {
			t.Errorf("ParseInterval(%q) error: %v", tc.s, err)
			continue
		}
		from := ts(2024, 1, 1, 0, 0)
		got := s.Next(from).Sub(from)
		if got != tc.want {
			t.Errorf("ParseInterval(%q).Next: got %v, want %v", tc.s, got, tc.want)
		}
	}
}

func TestParseIntervalInvalid(t *testing.T) {
	cases := []string{"", "0s", "-1h", "notaduration"}
	for _, s := range cases {
		if _, err := ParseInterval(s); err == nil {
			t.Errorf("ParseInterval(%q) expected error", s)
		}
	}
}

// --- ParseCron ---

func TestParseCronWrongFieldCount(t *testing.T) {
	for _, s := range []string{"* * * *", "* * * * * *", ""} {
		if _, err := ParseCron(s); err == nil {
			t.Errorf("ParseCron(%q) expected error", s)
		}
	}
}

func TestParseCronInvalidFields(t *testing.T) {
	bad := []string{
		"60 * * * *",   // minute out of range
		"* 24 * * *",   // hour out of range
		"* * 0 * *",    // dom out of range (< 1)
		"* * * 13 *",   // month out of range
		"* * * * 7",    // dow out of range
		"* * * * */0",  // step of zero
		"foo * * * *",  // non-numeric
	}
	for _, s := range bad {
		if _, err := ParseCron(s); err == nil {
			t.Errorf("ParseCron(%q) expected error", s)
		}
	}
}

func TestCronNextEveryMinute(t *testing.T) {
	s, err := ParseCron("* * * * *")
	if err != nil {
		t.Fatal(err)
	}
	from := ts(2024, 6, 15, 12, 30)
	want := ts(2024, 6, 15, 12, 31)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCronNextSpecificMinute(t *testing.T) {
	// Every hour at minute 15
	s, _ := ParseCron("15 * * * *")
	cases := []struct{ from, want time.Time }{
		{ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 0, 15)},
		{ts(2024, 1, 1, 0, 14), ts(2024, 1, 1, 0, 15)},
		{ts(2024, 1, 1, 0, 15), ts(2024, 1, 1, 1, 15)}, // already fired, next hour
		{ts(2024, 1, 1, 0, 16), ts(2024, 1, 1, 1, 15)},
	}
	for _, tc := range cases {
		got := s.Next(tc.from)
		if !got.Equal(tc.want) {
			t.Errorf("Next(%v) = %v, want %v", tc.from, got, tc.want)
		}
	}
}

func TestCronNextHourly(t *testing.T) {
	// Every day at midnight (0 0 * * *)
	s, _ := ParseCron("0 0 * * *")
	from := ts(2024, 3, 14, 12, 0)
	want := ts(2024, 3, 15, 0, 0)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCronNextRange(t *testing.T) {
	// Minutes 10-20 every hour
	s, _ := ParseCron("10-20 * * * *")
	from := ts(2024, 1, 1, 5, 9)
	want := ts(2024, 1, 1, 5, 10)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	// past end of range → wraps to next hour
	from = ts(2024, 1, 1, 5, 21)
	want = ts(2024, 1, 1, 6, 10)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCronNextStep(t *testing.T) {
	// Every 15 minutes: */15
	s, _ := ParseCron("*/15 * * * *")
	cases := []struct{ from, want time.Time }{
		{ts(2024, 1, 1, 0, 0), ts(2024, 1, 1, 0, 15)},
		{ts(2024, 1, 1, 0, 14), ts(2024, 1, 1, 0, 15)},
		{ts(2024, 1, 1, 0, 44), ts(2024, 1, 1, 0, 45)},
		{ts(2024, 1, 1, 0, 45), ts(2024, 1, 1, 1, 0)},
	}
	for _, tc := range cases {
		got := s.Next(tc.from)
		if !got.Equal(tc.want) {
			t.Errorf("Next(%v) = %v, want %v", tc.from, got, tc.want)
		}
	}
}

func TestCronNextList(t *testing.T) {
	// At minutes 0, 20, 40
	s, _ := ParseCron("0,20,40 * * * *")
	from := ts(2024, 1, 1, 3, 19)
	want := ts(2024, 1, 1, 3, 20)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCronNextMonthWrap(t *testing.T) {
	// January only: 0 0 1 1 *
	s, _ := ParseCron("0 0 1 1 *")
	from := ts(2024, 2, 1, 0, 0)
	want := ts(2025, 1, 1, 0, 0)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestCronNextDayOfWeek(t *testing.T) {
	// Every Monday at 9:00: 0 9 * * 1
	s, _ := ParseCron("0 9 * * 1")
	// 2024-01-01 is a Monday. From Sunday 2024-01-07.
	from := ts(2024, 1, 7, 9, 0) // Sunday
	// Next Monday = 2024-01-08
	want := ts(2024, 1, 8, 9, 0)
	if got := s.Next(from); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
