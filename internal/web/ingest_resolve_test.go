package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/ingest"
)

func srvWithTasks(tasks ...TaskInfo) *Server {
	s := &Server{}
	s.SetTasks(tasks)
	return s
}

// The point of the change: naming the pipeline is enough, because the queue is
// a property of its webhook source rather than independent information.
func TestResolveIngestQueueDerivesFromPipeline(t *testing.T) {
	s := srvWithTasks(
		TaskInfo{Name: "movies-ondemand", Queues: []string{"movies"}},
		TaskInfo{Name: "3d-mvc-ondemand", Queues: []string{"3d-mvc"}},
	)
	for pipeline, want := range map[string]string{
		"movies-ondemand": "movies",
		"3d-mvc-ondemand": "3d-mvc",
	} {
		got, warn, err := s.resolveIngestQueue("", pipeline)
		if err != nil {
			t.Fatalf("%s: %v", pipeline, err)
		}
		if got != want {
			t.Errorf("%s derived %q, want %q", pipeline, got, want)
		}
		if warn != "" {
			t.Errorf("%s warned unexpectedly: %s", pipeline, warn)
		}
	}
}

// A queue that agrees with the pipeline is accepted, so pushers that send both
// keep working unchanged.
func TestResolveIngestQueueAcceptsAgreeingPair(t *testing.T) {
	s := srvWithTasks(TaskInfo{Name: "movies-ondemand", Queues: []string{"movies"}})
	got, _, err := s.resolveIngestQueue("movies", "movies-ondemand")
	if err != nil || got != "movies" {
		t.Fatalf("got %q, %v", got, err)
	}
}

// The failure this exists to remove. Before, a mismatched pair enqueued into
// one queue and triggered a pipeline draining another, then reported success.
func TestResolveIngestQueueRejectsMismatchedPair(t *testing.T) {
	s := srvWithTasks(
		TaskInfo{Name: "movies-ondemand", Queues: []string{"movies"}},
		TaskInfo{Name: "tvshows-favorite-add", Queues: []string{"favorites"}},
	)
	_, _, err := s.resolveIngestQueue("favorites", "movies-ondemand")
	if err == nil {
		t.Fatal("a queue the pipeline does not drain must be refused")
	}
	for _, want := range []string{"movies-ondemand", "favorites", "movies"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err, want)
		}
	}
}

// Pushing to a pipeline that cannot receive a push used to report success.
func TestResolveIngestQueueRejectsPipelineWithNoWebhook(t *testing.T) {
	s := srvWithTasks(TaskInfo{Name: "movies", Schedule: "10 * * * *"})
	_, _, err := s.resolveIngestQueue("", "movies")
	if err == nil || !strings.Contains(err.Error(), "no webhook source") {
		t.Fatalf("want a no-webhook-source error, got %v", err)
	}
}

// Two webhook sources make the queue genuinely ambiguous. Refuse, and say
// which queues were meant — rather than forbidding the topology outright.
func TestResolveIngestQueueAmbiguousNeedsExplicitQueue(t *testing.T) {
	s := srvWithTasks(TaskInfo{Name: "both", Queues: []string{"urgent", "bulk"}})

	if _, _, err := s.resolveIngestQueue("", "both"); err == nil {
		t.Fatal("an ambiguous pipeline must not be resolved by guessing")
	} else {
		for _, want := range []string{"urgent", "bulk"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q should list candidate %q", err, want)
			}
		}
	}
	for _, q := range []string{"urgent", "bulk"} {
		got, _, err := s.resolveIngestQueue(q, "both")
		if err != nil || got != q {
			t.Errorf("explicit %q: got %q, %v", q, got, err)
		}
	}
	if _, _, err := s.resolveIngestQueue("other", "both"); err == nil {
		t.Error("a queue outside the pipeline's set must be refused")
	}
}

func TestResolveIngestQueueUnknownPipeline(t *testing.T) {
	s := srvWithTasks(TaskInfo{Name: "movies-ondemand", Queues: []string{"movies"}})
	_, _, err := s.resolveIngestQueue("", "typo-ondemand")
	if err == nil || !strings.Contains(err.Error(), "no such pipeline") {
		t.Fatalf("want an unknown-pipeline error, got %v", err)
	}
	if !strings.Contains(err.Error(), "movies-ondemand") {
		t.Errorf("error %q should list the pipelines that do exist", err)
	}
}

// Queue without a pipeline stays supported and stays permissive: refusing
// would break external pushers that only ever knew the queue, and would fail
// every push in the window before the first SetTasks call. It must not pass
// silently, though.
func TestResolveIngestQueueWithoutPipeline(t *testing.T) {
	s := srvWithTasks(TaskInfo{Name: "movies-ondemand", Queues: []string{"movies"}})

	got, warn, err := s.resolveIngestQueue("movies", "")
	if err != nil || got != "movies" || warn != "" {
		t.Fatalf("a drained queue should be accepted cleanly: %q %q %v", got, warn, err)
	}

	got, warn, err = s.resolveIngestQueue("nobody-reads-this", "")
	if err != nil || got != "nobody-reads-this" {
		t.Fatalf("an unknown queue should still be accepted: %q %v", got, err)
	}
	if warn == "" {
		t.Fatal("an unread queue must come back with a warning, not silently")
	}
	for _, want := range []string{"nobody-reads-this", "movies"} {
		if !strings.Contains(warn, want) {
			t.Errorf("warning %q should mention %q", warn, want)
		}
	}

	// With no task list there is nothing to check against, so no warning.
	if _, warn, err := (&Server{}).resolveIngestQueue("anything", ""); err != nil || warn != "" {
		t.Errorf("unknown task list: warn=%q err=%v, want both empty", warn, err)
	}
}

func TestResolveIngestQueueNeitherNamed(t *testing.T) {
	s := srvWithTasks(TaskInfo{Name: "movies-ondemand", Queues: []string{"movies"}})
	if _, _, err := s.resolveIngestQueue("", ""); err == nil {
		t.Fatal("naming neither a queue nor a pipeline must fail")
	}
}

// Two pipelines draining one queue is allowed, and a message must not repeat it.
func TestResolveIngestQueueSharedQueue(t *testing.T) {
	s := srvWithTasks(
		TaskInfo{Name: "a", Queues: []string{"shared"}},
		TaskInfo{Name: "b", Queues: []string{"shared"}},
	)
	if got, _, err := s.resolveIngestQueue("shared", ""); err != nil || got != "shared" {
		t.Fatalf("got %q, %v", got, err)
	}
	_, warn, err := s.resolveIngestQueue("missing", "")
	if err != nil || warn == "" {
		t.Fatalf("want acceptance with a warning, got warn=%q err=%v", warn, err)
	}
	if n := strings.Count(warn, "shared"); n != 1 {
		t.Errorf("queue listed %d times, want once: %s", n, warn)
	}
}

// The queue-less route must be reachable through the REAL routing tree.
// "/api/ingest/" does not match "/api/ingest", so without its own top-level
// entry the request falls through to session auth and a machine gets a 303 —
// the same unreachable-endpoint bug that once hit /api/ingest/{queue}, one
// path segment shorter.
func TestIngestWithoutQueueSegmentIsReachable(t *testing.T) {
	srv := New(nil, stubDaemon{}, NewHistory(), NewBroadcaster(), "test", "user", "pass")
	srv.SetIngestToken("push-tok")
	srv.SetTasks([]TaskInfo{{Name: "ondemand", Queues: []string{"derived-q"}}})
	ts := httptest.NewServer(srv.buildHandler())
	defer ts.Close()

	resp := ingestPost(t, ts.URL+"/api/ingest?pipeline=ondemand", "push-tok", `{"title":"x"}`)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("queue-less ingest: got %d (%s), want 200", resp.StatusCode, body)
	}
	// The response names the queue it derived, so a pusher can see it.
	if !strings.Contains(string(body), "derived-q") {
		t.Errorf("response %s should report the derived queue", body)
	}
	// And the item really landed in the derived queue.
	if items := ingest.Drain("derived-q"); len(items) != 1 || items[0].Title != "x" {
		t.Errorf("item did not land in the derived queue: %+v", items)
	}

	// A bad pipeline name is refused rather than silently queued.
	resp2 := ingestPost(t, ts.URL+"/api/ingest?pipeline=nope", "push-tok", `{"title":"y"}`)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest {
		t.Errorf("unknown pipeline: got %d, want 400", resp2.StatusCode)
	}
}
