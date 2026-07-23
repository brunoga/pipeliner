package series

import (
	"context"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/series"
	"github.com/brunoga/pipeliner/internal/store"
)

// openPluginDB builds the plugin through the real factory and also returns
// the store, so tests can flip the inactive flag the same way the
// series_tracker_update sink does.
func openPluginDB(t *testing.T) (*seriesPlugin, *store.SQLiteStore) {
	t.Helper()
	db, err := store.OpenSQLite(":memory:")
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	p, err := newPlugin(map[string]any{"static": []any{"My Show"}}, db)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	return p.(*seriesPlugin), db
}

func TestInactiveShowRejectedEarly(t *testing.T) {
	p, db := openPluginDB(t)
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := set.Deactivate("my show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if !e.IsRejected() {
		t.Fatalf("inactive show should be rejected, state=%v", e.State)
	}
	if !strings.Contains(e.RejectReason, "inactive (complete)") {
		t.Errorf("reject reason should mention inactive (complete): %q", e.RejectReason)
	}
}

func TestInactiveRejectionUsesFallbackReason(t *testing.T) {
	p, db := openPluginDB(t)
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := set.Deactivate("my show", ""); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if !e.IsRejected() || !strings.Contains(e.RejectReason, "inactive (deactivated)") {
		t.Errorf("reject reason: got %q", e.RejectReason)
	}
}

func TestReactivateRestoresAcceptance(t *testing.T) {
	p, db := openPluginDB(t)
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := set.Deactivate("my show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if !e.IsRejected() {
		t.Fatal("show should be rejected while inactive")
	}

	if err := set.Reactivate("my show"); err != nil {
		t.Fatalf("Reactivate: %v", err)
	}
	e2 := makeEntry("My.Show.S01E02.720p.HDTV", "http://x.com/b")
	if err := p.filter(context.Background(), makeCtx(), e2); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if !e2.IsAccepted() {
		t.Errorf("show should be accepted after reactivation: %s", e2.RejectReason)
	}
}

func TestActiveShowUnaffectedByOtherInactiveShows(t *testing.T) {
	p, db := openPluginDB(t)
	set := series.NewInactiveSet(db.Bucket(series.InactiveBucketName))
	if err := set.Deactivate("some other show", "complete"); err != nil {
		t.Fatalf("Deactivate: %v", err)
	}

	e := makeEntry("My.Show.S01E01.720p.HDTV", "http://x.com/a")
	if err := p.filter(context.Background(), makeCtx(), e); err != nil {
		t.Fatalf("filter: %v", err)
	}
	if !e.IsAccepted() {
		t.Errorf("unrelated show should be accepted: %s", e.RejectReason)
	}
}
