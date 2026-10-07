package executor

import (
	"testing"

	"github.com/brunoga/pipeliner/internal/dag"
	"github.com/brunoga/pipeliner/internal/entry"
)

func mkEntry(title, url string, state entry.State) *entry.Entry {
	e := entry.New(title, url)
	e.State = state
	return e
}

// TestAggregateCounters_SimpleSourcePassthrough is the baseline regression
// guard: with no fan-out, no clones, and no ReplacesUpstream, the aggregator
// returns exactly the per-source-entry counts the old implementation produced.
func TestAggregateCounters_SimpleSourcePassthrough(t *testing.T) {
	src := []*entry.Entry{
		mkEntry("a", "http://a", entry.Accepted),
		mkEntry("b", "http://b", entry.Rejected),
		mkEntry("c", "http://c", entry.Undecided),
	}
	total, acc, rej, fail, und, entries := aggregateCounters(src, nil, nil, nil)
	if total != 3 || acc != 1 || rej != 1 || fail != 0 || und != 1 {
		t.Fatalf("counts: got total=%d acc=%d rej=%d fail=%d und=%d, want 3/1/1/0/1",
			total, acc, rej, fail, und)
	}
	if len(entries) != 3 {
		t.Errorf("Entries: got %d, want 3", len(entries))
	}
}

// TestAggregateCounters_FanOutClonesAggregateByURL is the first of two
// correctness fixes this PR ships. Before the rewrite, a clone that became
// Accepted on a non-branch-0 path was invisible to the counter because
// sourceEntries (branch 0's pointers) never had their state changed. With
// per-URL aggregation, the clone's Accepted state propagates into the count
// for the shared URL.
func TestAggregateCounters_FanOutClonesAggregateByURL(t *testing.T) {
	// Source entry on branch 0 stayed Undecided; the clone on branch 1
	// became Accepted at a downstream sink.
	src := mkEntry("a", "http://a", entry.Undecided)
	clone := mkEntry("a", "http://a", entry.Accepted)
	clone.OriginalURL = "http://a"

	edges := map[edgeKey][]*entry.Entry{
		{from: "src", to: "branchA"}: {src},
		{from: "src", to: "branchB"}: {clone},
	}
	total, acc, rej, fail, und, _ := aggregateCounters([]*entry.Entry{src}, edges, nil, nil)
	if total != 1 {
		t.Fatalf("total: got %d, want 1 (URL dedup)", total)
	}
	if acc != 1 {
		t.Errorf("accepted: got %d, want 1 (clone Accepted must dominate Undecided original)", acc)
	}
	if rej != 0 || fail != 0 || und != 0 {
		t.Errorf("non-accepted counts: got rej=%d fail=%d und=%d, want all zero", rej, fail, und)
	}
}

// TestAggregateCounters_StrongestStateWins is the explicit ranking guarantee.
// Accepted > Rejected > Failed > Undecided across clones of the same URL.
func TestAggregateCounters_StrongestStateWins(t *testing.T) {
	cases := []struct {
		name   string
		clones []entry.State
		want   entry.State
	}{
		{"accept beats reject", []entry.State{entry.Rejected, entry.Accepted}, entry.Accepted},
		{"reject beats fail", []entry.State{entry.Failed, entry.Rejected}, entry.Rejected},
		{"fail beats undecided", []entry.State{entry.Undecided, entry.Failed}, entry.Failed},
		{"all accept stays accept", []entry.State{entry.Accepted, entry.Accepted}, entry.Accepted},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			edges := map[edgeKey][]*entry.Entry{}
			for i, s := range tc.clones {
				edges[edgeKey{from: "src", to: dag.NodeID(string(rune('a'+i)))}] = []*entry.Entry{
					mkEntry("t", "http://t", s),
				}
			}
			_, acc, rej, fail, und, _ := aggregateCounters(nil, edges, nil, nil)
			var got entry.State
			switch {
			case acc == 1:
				got = entry.Accepted
			case rej == 1:
				got = entry.Rejected
			case fail == 1:
				got = entry.Failed
			case und == 1:
				got = entry.Undecided
			}
			if got != tc.want {
				t.Errorf("winning state: got %v, want %v", got, tc.want)
			}
		})
	}
}

// TestAggregateCounters_ReplacesUpstreamDiscardsConsumed is the second
// correctness fix. A discover-style plugin consumes its upstream inputs (their
// titles become search candidates) and emits brand new entries with their own
// URLs. The aggregator must drop the consumed inputs so the counter doesn't
// report them all as Undecided alongside the actually-relevant new entries.
func TestAggregateCounters_ReplacesUpstreamDiscardsConsumed(t *testing.T) {
	// 924-style upstream titles that discover consumed.
	consumed := []*entry.Entry{
		mkEntry("title-1", "http://t1", entry.Undecided),
		mkEntry("title-2", "http://t2", entry.Undecided),
		mkEntry("title-3", "http://t3", entry.Undecided),
	}
	// 4 new search-backend entries that came out of discover. Two reached a
	// sink and became Accepted; two stayed Undecided.
	emitted := []*entry.Entry{
		mkEntry("hit-a", "http://hit-a", entry.Accepted),
		mkEntry("hit-b", "http://hit-b", entry.Accepted),
		mkEntry("hit-c", "http://hit-c", entry.Undecided),
		mkEntry("hit-d", "http://hit-d", entry.Undecided),
	}

	edges := map[edgeKey][]*entry.Entry{
		{from: "src", to: "discover"}:   consumed,
		{from: "discover", to: "limit"}: emitted,
		{from: "limit", to: "sink"}:     emitted[:2], // only the accepted ones forwarded
	}
	discarded := map[*entry.Entry]bool{}
	for _, e := range consumed {
		discarded[e] = true
	}

	total, acc, rej, fail, und, entries := aggregateCounters(consumed, edges, discarded, nil)
	if total != 4 {
		t.Fatalf("total: got %d, want 4 (only emitted entries count)", total)
	}
	if acc != 2 || rej != 0 || fail != 0 || und != 2 {
		t.Errorf("counts: got acc=%d rej=%d fail=%d und=%d, want 2/0/0/2",
			acc, rej, fail, und)
	}
	// None of the consumed entries should be in Entries.
	for _, e := range entries {
		for _, c := range consumed {
			if e == c {
				t.Errorf("Entries leaked a discarded upstream entry: %v", e.Title)
			}
		}
	}
}

// TestAggregateCounters_EmptyURLFallsBackToPointer confirms entries with no
// URL (e.g. discover's intermediate entry.New(title, "") candidates) are
// distinguished by pointer rather than colliding on the empty key.
func TestAggregateCounters_EmptyURLFallsBackToPointer(t *testing.T) {
	a := mkEntry("a", "", entry.Accepted)
	b := mkEntry("b", "", entry.Rejected)
	c := mkEntry("c", "", entry.Undecided)
	total, acc, rej, _, und, _ := aggregateCounters(
		[]*entry.Entry{a, b, c}, nil, nil, nil,
	)
	if total != 3 {
		t.Fatalf("total: got %d, want 3 (URL-less entries identified by pointer)", total)
	}
	if acc != 1 || rej != 1 || und != 1 {
		t.Errorf("counts: got acc=%d rej=%d und=%d, want 1/1/1", acc, rej, und)
	}
}

// TestAggregateCounters_SupersededOriginalDoesNotCount is the harvest case.
// An entry is accepted upstream of a fan-out — the movies filter accepts a
// matched release — and then every branch rejects it. The pre-fan-out original
// still holds Accepted, because its state is a snapshot from before the
// branches ran, and it is deliberately kept so the commit phase can read the
// producer's own output. Counting it meant a run that grabbed 3 releases
// reported 493 accepted: the number the route had fanned out, not the number
// that went anywhere.
func TestAggregateCounters_SupersededOriginalDoesNotCount(t *testing.T) {
	original := mkEntry("a", "http://a", entry.Accepted) // accepted upstream
	laneA := mkEntry("a", "http://a", entry.Rejected)
	laneB := mkEntry("a", "http://a", entry.Rejected)

	edges := map[edgeKey][]*entry.Entry{
		{from: "route", to: "laneA"}: {laneA},
		{from: "route", to: "laneB"}: {laneB},
	}
	superseded := map[*entry.Entry]bool{original: true}

	total, acc, rej, _, _, _ := aggregateCounters(
		[]*entry.Entry{original}, edges, nil, superseded)

	if total != 1 {
		t.Fatalf("total: got %d, want 1", total)
	}
	if acc != 0 {
		t.Errorf("accepted: got %d, want 0 — every branch rejected it", acc)
	}
	if rej != 1 {
		t.Errorf("rejected: got %d, want 1", rej)
	}
}

// But a genuine fan-out acceptance must still win: one branch downloading is
// the case the strongest-state rule exists for, and superseding the original
// must not break it.
func TestAggregateCounters_SupersededStillLetsABranchAccept(t *testing.T) {
	original := mkEntry("a", "http://a", entry.Undecided)
	rejectedBranch := mkEntry("a", "http://a", entry.Rejected)
	acceptedBranch := mkEntry("a", "http://a", entry.Accepted)

	edges := map[edgeKey][]*entry.Entry{
		{from: "fan", to: "b1"}: {rejectedBranch},
		{from: "fan", to: "b2"}: {acceptedBranch},
	}
	_, acc, rej, _, _, _ := aggregateCounters(
		[]*entry.Entry{original}, edges, nil, map[*entry.Entry]bool{original: true})

	if acc != 1 || rej != 0 {
		t.Errorf("acc=%d rej=%d, want the accepting branch to win", acc, rej)
	}
}

// The counters must still partition the total — every entry lands in exactly
// one bucket, which is what makes the summary line readable.
func TestAggregateCounters_BucketsPartitionTheTotal(t *testing.T) {
	original := mkEntry("a", "http://a", entry.Accepted)
	edges := map[edgeKey][]*entry.Entry{
		{from: "route", to: "l1"}: {mkEntry("a", "http://a", entry.Rejected)},
		{from: "route", to: "l2"}: {mkEntry("b", "http://b", entry.Accepted)},
		{from: "route", to: "l3"}: {mkEntry("c", "http://c", entry.Undecided)},
		{from: "route", to: "l4"}: {mkEntry("d", "http://d", entry.Failed)},
	}
	total, acc, rej, fail, und, _ := aggregateCounters(
		[]*entry.Entry{original}, edges, nil, map[*entry.Entry]bool{original: true})

	if got := acc + rej + fail + und; got != total {
		t.Errorf("acc+rej+fail+und = %d, total = %d; the buckets must partition the total", got, total)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4 distinct URLs", total)
	}
}
