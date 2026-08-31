package failures

import (
	"testing"
	"time"

	"github.com/brunoga/pipeliner/internal/entry"
	"github.com/brunoga/pipeliner/internal/executor"
	"github.com/brunoga/pipeliner/internal/store"
)

func newLog(t *testing.T) *Log {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(db.Bucket(BucketName))
}

func rec(title, reason string, at time.Time) Record {
	return Record{Title: title, URL: "u:" + title, Reason: reason, Task: "tv", FailedAt: at}
}

func TestAppendAndQueryByTitle(t *testing.T) {
	l := newLog(t)
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	l.Append(rec("Star Trek Strange New Worlds S04E05 1080p", "deluge: connection refused", base))
	l.Append(rec("Silo S01E01 1080p", "deluge: connection refused", base.Add(time.Hour)))

	got, err := l.Query("strange new worlds", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 match, got %d", len(got))
	}
	if got[0].Reason != "deluge: connection refused" {
		t.Errorf("reason = %q", got[0].Reason)
	}
}

func TestQueryMatchesReason(t *testing.T) {
	l := newLog(t)
	l.Append(rec("Some Show S01E01", "exec: command failed: exit 1", time.Now()))
	got, _ := l.Query("exec", 0)
	if len(got) != 1 {
		t.Errorf("query should match reason text, got %d", len(got))
	}
}

func TestBlankQueryReturnsRecentNewestFirst(t *testing.T) {
	l := newLog(t)
	base := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 5; i++ {
		l.Append(rec("show"+string(rune('a'+i)), "err", base.Add(time.Duration(i)*time.Hour)))
	}
	got, err := l.Query("", 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("limit not applied: %d", len(got))
	}
	// Newest first.
	if !got[0].FailedAt.After(got[1].FailedAt) || !got[1].FailedAt.After(got[2].FailedAt) {
		t.Errorf("not newest-first: %+v", got)
	}
}

func TestRepeatedFailuresRetained(t *testing.T) {
	l := newLog(t)
	base := time.Now()
	for i := 0; i < 3; i++ {
		l.Append(rec("Same Show S01E01", "err", base.Add(time.Duration(i)*time.Second)))
	}
	got, _ := l.Query("same show", 0)
	if len(got) != 3 {
		t.Errorf("expected 3 retained failures, got %d", len(got))
	}
}

func TestRecordsFromEntries(t *testing.T) {
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	failed := entry.New("Failed Show S01E01", "u1")
	failed.Set(entry.FieldMediaType, entry.MediaTypeSeries)
	failed.Fail("deluge: connection refused")
	accepted := entry.New("Fine Show S01E01", "u2")
	accepted.Accept("ok")
	rejected := entry.New("Junk", "u3")
	rejected.Reject("not in list")

	nodeFor := func(url string) string {
		if url == "u1" {
			return "deluge_7"
		}
		return ""
	}
	recs := RecordsFromEntries([]*entry.Entry{failed, accepted, rejected}, "tv", at, nodeFor)
	if len(recs) != 1 {
		t.Fatalf("only failed entries should be recorded, got %d", len(recs))
	}
	r := recs[0]
	if r.Title != "Failed Show S01E01" || r.Reason != "deluge: connection refused" {
		t.Errorf("record = %+v", r)
	}
	if r.Node != "deluge_7" {
		t.Errorf("node = %q, want deluge_7", r.Node)
	}
	if r.MediaType != entry.MediaTypeSeries {
		t.Errorf("media_type = %q", r.MediaType)
	}
	if !r.FailedAt.Equal(at) {
		t.Errorf("failedAt = %v", r.FailedAt)
	}
}

func TestRecordsFromEntriesNilNodeFor(t *testing.T) {
	e := entry.New("X", "u")
	e.Fail("boom")
	recs := RecordsFromEntries([]*entry.Entry{e}, "tv", time.Now(), nil)
	if len(recs) != 1 || recs[0].Node != "" {
		t.Errorf("nil nodeFor should leave Node empty: %+v", recs)
	}
}

func TestRecordsFromRunResolvesNodeFromTrace(t *testing.T) {
	at := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	failed := entry.New("Failed Show S01E01", "u1")
	failed.Fail("deluge: connection refused")
	fine := entry.New("Fine", "u2")
	fine.Accept("ok")

	tr := []executor.EntryTrace{
		{Title: "Failed Show S01E01", URL: "u1", Final: "failed", Steps: []executor.TraceStep{
			{Node: "series_5", State: "accepted"},
			{Node: "deluge_7", State: "failed", Reason: "deluge: connection refused"},
		}},
		{Title: "Fine", URL: "u2", Final: "accepted"},
	}
	recs := RecordsFromRun([]*entry.Entry{failed, fine}, tr, "tv", at)
	if len(recs) != 1 {
		t.Fatalf("expected 1 failure record, got %d", len(recs))
	}
	if recs[0].Node != "deluge_7" {
		t.Errorf("node = %q, want deluge_7 (last failed step)", recs[0].Node)
	}
}
