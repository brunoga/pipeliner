package store

import (
	"testing"
)

func TestSeenStoreLifecycle(t *testing.T) {
	s := openMem(t)
	seen := NewSeenStore(s.Bucket("seen"))

	fp := "abc123fingerprint"

	if seen.IsSeen(fp) {
		t.Fatal("fingerprint should not be seen before Mark")
	}

	if err := seen.Mark(fp, SeenRecord{
		Title: "Example File",
		URL:   "http://example.com/file.torrent",
		Task:  "my-task",
	}); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	if !seen.IsSeen(fp) {
		t.Fatal("fingerprint should be seen after Mark")
	}
}

func TestSeenStoreRecordContents(t *testing.T) {
	s := openMem(t)
	seen := NewSeenStore(s.Bucket("seen"))

	fp := "fp-for-a-torrent"
	if err := seen.Mark(fp, SeenRecord{
		Title:  "My Title",
		URL:    "http://x.com/a.torrent",
		Task:   "task-1",
		Fields: []string{"url"},
	}); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	var rec SeenRecord
	found, err := s.Bucket("seen").Get(fp, &rec)
	if err != nil || !found {
		t.Fatalf("record not found: found=%v err=%v", found, err)
	}
	if rec.Fingerprint != fp {
		t.Errorf("Fingerprint: want %q, got %q", fp, rec.Fingerprint)
	}
	if rec.URL != "http://x.com/a.torrent" {
		t.Errorf("URL: want %q, got %q", "http://x.com/a.torrent", rec.URL)
	}
	if rec.Title != "My Title" {
		t.Errorf("Title: want %q, got %q", "My Title", rec.Title)
	}
	if rec.Task != "task-1" {
		t.Errorf("Task: want %q, got %q", "task-1", rec.Task)
	}
	if rec.SeenAt.IsZero() {
		t.Error("SeenAt should not be zero")
	}
}

func TestSeenStoreDifferentFingerprints(t *testing.T) {
	s := openMem(t)
	seen := NewSeenStore(s.Bucket("seen"))

	if err := seen.Mark("fp-a", SeenRecord{Title: "A"}); err != nil {
		t.Fatal(err)
	}
	if err := seen.Mark("fp-b", SeenRecord{Title: "B"}); err != nil {
		t.Fatal(err)
	}

	if !seen.IsSeen("fp-a") {
		t.Error("fp-a should be seen")
	}
	if !seen.IsSeen("fp-b") {
		t.Error("fp-b should be seen")
	}
	if seen.IsSeen("fp-c") {
		t.Error("fp-c should not be seen")
	}
}

func TestSeenStoreSurvivesReopen(t *testing.T) {
	path := t.TempDir() + "/seen.db"

	s1, _ := OpenSQLite(path)
	seen1 := NewSeenStore(s1.Bucket("seen"))
	if err := seen1.Mark("persistent-fp", SeenRecord{Title: "File", Task: "task"}); err != nil {
		t.Fatal(err)
	}
	s1.Close()

	s2, _ := OpenSQLite(path)
	defer s2.Close()
	seen2 := NewSeenStore(s2.Bucket("seen"))

	if !seen2.IsSeen("persistent-fp") {
		t.Error("seen record should survive store reopen")
	}
}
