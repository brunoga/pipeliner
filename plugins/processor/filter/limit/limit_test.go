package limit

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/plugin"
)

func tc() *plugin.TaskContext {
	return &plugin.TaskContext{Name: "test", Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func accepted(title string) *entry.Entry {
	e := entry.New(title, "http://example.com/"+title)
	e.Accept()
	return e
}

func TestValidateRejectsMissingOrInvalidN(t *testing.T) {
	cases := []struct {
		name string
		cfg  map[string]any
	}{
		{"missing", map[string]any{}},
		{"zero", map[string]any{"n": 0}},
		{"negative", map[string]any{"n": -3}},
		{"non-numeric", map[string]any{"n": "five"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if errs := validate(c.cfg); len(errs) == 0 {
				t.Fatalf("want validation error, got none")
			}
		})
	}
}

func TestValidateOrderWithoutSortIsRejected(t *testing.T) {
	errs := validate(map[string]any{"n": 5, "order": "asc"})
	if len(errs) == 0 {
		t.Fatal("want error for order without sort")
	}
}

func TestValidateUnknownKey(t *testing.T) {
	errs := validate(map[string]any{"n": 5, "wat": "x"})
	if len(errs) == 0 {
		t.Fatal("want error for unknown key")
	}
}

func TestArrivalOrderWhenNoSort(t *testing.T) {
	p, err := newPlugin(map[string]any{"n": 2}, nil)
	if err != nil {
		t.Fatal(err)
	}
	in := []*entry.Entry{accepted("a"), accepted("b"), accepted("c"), accepted("d")}
	out, err := p.(*limitPlugin).Process(context.Background(), tc(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 forwarded (cap n=2), got %d", len(out))
	}
	wantAccepted := map[string]bool{"a": true, "b": true}
	for _, e := range in {
		if wantAccepted[e.Title] {
			if !e.IsAccepted() {
				t.Errorf("%q should remain accepted", e.Title)
			}
		} else {
			if !e.IsRejected() {
				t.Errorf("%q should be rejected", e.Title)
			}
		}
	}
}

func TestSortByIntFieldDescending(t *testing.T) {
	p, err := newPlugin(map[string]any{"n": 2, "sort": "video_year"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := accepted("a")
	a.Set("video_year", 2020)
	b := accepted("b")
	b.Set("video_year", 1999)
	c := accepted("c")
	c.Set("video_year", 2023)
	d := accepted("d")
	d.Set("video_year", 2010)

	_, err = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b, c, d})
	if err != nil {
		t.Fatal(err)
	}

	// Top 2 by year desc: c (2023) and a (2020).
	for _, e := range []*entry.Entry{c, a} {
		if !e.IsAccepted() {
			t.Errorf("want %q accepted", e.Title)
		}
	}
	for _, e := range []*entry.Entry{b, d} {
		if !e.IsRejected() {
			t.Errorf("want %q rejected", e.Title)
		}
	}
}

func TestSortByFloatFieldAscending(t *testing.T) {
	p, err := newPlugin(map[string]any{"n": 1, "sort": "video_rating", "order": "asc"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	a := accepted("a")
	a.Set("video_rating", 8.4)
	b := accepted("b")
	b.Set("video_rating", 5.1)
	c := accepted("c")
	c.Set("video_rating", 7.2)

	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b, c})

	if !b.IsAccepted() {
		t.Error("lowest rating should win in asc")
	}
	if !a.IsRejected() || !c.IsRejected() {
		t.Error("non-winners should be rejected")
	}
}

func TestSortByTimeField(t *testing.T) {
	p, _ := newPlugin(map[string]any{"n": 1, "sort": "ts"}, nil)
	now := time.Now()
	a := accepted("a")
	a.Set("ts", now.Add(-2*time.Hour))
	b := accepted("b")
	b.Set("ts", now)
	c := accepted("c")
	c.Set("ts", now.Add(-1*time.Hour))

	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b, c})

	if !b.IsAccepted() {
		t.Error("most recent should win in default desc")
	}
}

// String-encoded dates (typical of RSS published_date) must sort
// chronologically, not lexicographically. RFC822-style dates would compare
// badly as plain strings: "Wed, 01 Mar 2024" < "Tue, 31 Dec 2024".
func TestSortByStringDateField(t *testing.T) {
	p, _ := newPlugin(map[string]any{"n": 1, "sort": "published_date"}, nil)
	older := accepted("older")
	older.Set("published_date", "Wed, 01 Mar 2024 10:00:00 -0000")
	newer := accepted("newer")
	newer.Set("published_date", "Tue, 31 Dec 2024 10:00:00 -0000")

	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{older, newer})

	if !newer.IsAccepted() {
		t.Error("31 Dec 2024 should beat 1 Mar 2024 in desc order")
	}
	if !older.IsRejected() {
		t.Error("older entry should be rejected")
	}
}

func TestMissingSortFieldBucketsToEnd(t *testing.T) {
	p, _ := newPlugin(map[string]any{"n": 2, "sort": "video_rating"}, nil)
	withVal := accepted("withVal")
	withVal.Set("video_rating", 6.0)
	noVal1 := accepted("noVal1")
	noVal2 := accepted("noVal2")
	better := accepted("better")
	better.Set("video_rating", 9.0)

	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{noVal1, withVal, noVal2, better})

	// Top 2: better (9.0) and withVal (6.0); noVal* bucketed last.
	if !better.IsAccepted() || !withVal.IsAccepted() {
		t.Error("entries with sort values should win regardless of arrival order")
	}
	if !noVal1.IsRejected() || !noVal2.IsRejected() {
		t.Error("entries without sort field should be rejected when capacity is filled by valued entries")
	}
}

func TestNGreaterThanInputForwardsAll(t *testing.T) {
	p, _ := newPlugin(map[string]any{"n": 10}, nil)
	in := []*entry.Entry{accepted("a"), accepted("b")}
	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), in)
	for _, e := range in {
		if !e.IsAccepted() {
			t.Errorf("%q should remain accepted (cap not reached)", e.Title)
		}
	}
}

func TestRejectedUpstreamEntriesIgnoredByCap(t *testing.T) {
	p, _ := newPlugin(map[string]any{"n": 2}, nil)

	a := accepted("a")
	b := entry.New("b", "http://example.com/b")
	b.Reject("upstream said no")
	c := accepted("c")
	d := accepted("d")

	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b, c, d})

	if !a.IsAccepted() || !c.IsAccepted() {
		t.Error("first two ACCEPTED entries should win")
	}
	if !d.IsRejected() {
		t.Error("d should be rejected by limit")
	}
	if !b.IsRejected() {
		t.Error("b should remain rejected (upstream)")
	}
}

func TestRejectReasonMentionsSort(t *testing.T) {
	// No sort: bare cap reason.
	p, _ := newPlugin(map[string]any{"n": 1}, nil)
	a := accepted("a")
	b := accepted("b")
	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b})
	if got, want := b.RejectReason, "limit: 1-entry cap reached"; got != want {
		t.Errorf("no-sort reject reason: got %q, want %q", got, want)
	}

	// With sort: includes field and direction.
	p, _ = newPlugin(map[string]any{"n": 1, "sort": "video_rating", "order": "asc"}, nil)
	a = accepted("a")
	a.Set("video_rating", 9.0)
	b = accepted("b")
	b.Set("video_rating", 5.0)
	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b})
	if got, want := a.RejectReason, "limit: 1-entry cap reached (ordered by video_rating asc)"; got != want {
		t.Errorf("sorted reject reason: got %q, want %q", got, want)
	}
}

func TestStableArrivalOrderOnTie(t *testing.T) {
	p, _ := newPlugin(map[string]any{"n": 2, "sort": "video_year"}, nil)
	a := accepted("a")
	a.Set("video_year", 2020)
	b := accepted("b")
	b.Set("video_year", 2020)
	c := accepted("c")
	c.Set("video_year", 2020)

	_, _ = p.(*limitPlugin).Process(context.Background(), tc(), []*entry.Entry{a, b, c})

	// All tied; arrival order keeps a and b.
	if !a.IsAccepted() || !b.IsAccepted() {
		t.Error("tied values should preserve arrival order")
	}
	if !c.IsRejected() {
		t.Error("c should be rejected")
	}
}
