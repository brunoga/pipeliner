package plugin

import "testing"

func TestRunOnEmptyMeansAlways(t *testing.T) {
	for _, k := range []TriggerKind{TriggerSchedule, TriggerManual, TriggerWebhook, TriggerStartup} {
		if !(Trigger{Kind: k}).Matches(nil) {
			t.Errorf("%s: an empty run_on must admit every trigger", k)
		}
	}
}

func TestRunOnMatchesByKind(t *testing.T) {
	sched := Trigger{Kind: TriggerSchedule}
	hook := Trigger{Kind: TriggerWebhook, Queue: "3d-movies"}

	if !sched.Matches([]string{"schedule", "manual"}) {
		t.Error("schedule should match a list containing it")
	}
	if sched.Matches([]string{"webhook"}) {
		t.Error("schedule should not match a webhook-only list")
	}
	if !hook.Matches([]string{"webhook"}) {
		t.Error("a push should match webhook")
	}
	if hook.Matches([]string{"schedule", "manual"}) {
		t.Error("a push should not match a list that excludes it")
	}
}

// TestRunOnQueueQualifier: a pipeline with two webhooks can have a source that
// only runs for one of them.
func TestRunOnQueueQualifier(t *testing.T) {
	mine := Trigger{Kind: TriggerWebhook, Queue: "3d-movies"}
	other := Trigger{Kind: TriggerWebhook, Queue: "movies"}

	if !mine.Matches([]string{"webhook:3d-movies"}) {
		t.Error("the named queue should match")
	}
	if other.Matches([]string{"webhook:3d-movies"}) {
		t.Error("a different queue must not match")
	}
	// Queue names are compared the way they are elsewhere: without case.
	if !mine.Matches([]string{"webhook:3D-Movies"}) {
		t.Error("queue comparison should ignore case")
	}
	// A bare "webhook" matches any queue.
	if !other.Matches([]string{"webhook"}) {
		t.Error("bare webhook should match any queue")
	}
}

func TestRunOnAny(t *testing.T) {
	for _, k := range []TriggerKind{TriggerSchedule, TriggerWebhook} {
		if !(Trigger{Kind: k}).Matches([]string{"any"}) {
			t.Errorf("%s: any must admit everything", k)
		}
	}
}

func TestRunOnTolerantOfSpacingAndCase(t *testing.T) {
	sched := Trigger{Kind: TriggerSchedule}
	if !sched.Matches([]string{" Schedule "}) {
		t.Error("tokens should be trimmed and case-folded")
	}
}

func TestValidRunOn(t *testing.T) {
	for _, good := range [][]string{
		{"schedule"}, {"manual"}, {"webhook"}, {"startup"}, {"any"},
		{"webhook:3d-movies"}, {"schedule", "manual"},
	} {
		if bad, ok := ValidRunOn(good); !ok {
			t.Errorf("%v should validate, got %q", good, bad)
		}
	}
	for _, bad := range [][]string{
		{"cron"},             // not a kind
		{"schedule:nightly"}, // only a webhook takes a qualifier
		{"any:thing"},        // ditto
		{"webhook:"},         // empty queue
		{"webhook", "typo"},  // one bad token in a good list
	} {
		if _, ok := ValidRunOn(bad); ok {
			t.Errorf("%v should not validate", bad)
		}
	}
}

func TestRunOnKinds(t *testing.T) {
	if got := RunOnKinds(nil); len(got) != 4 {
		t.Errorf("an empty list admits every kind, got %v", got)
	}
	if got := RunOnKinds([]string{"any"}); len(got) != 4 {
		t.Errorf("any admits every kind, got %v", got)
	}
	got := RunOnKinds([]string{"webhook:3d-movies", "manual"})
	if len(got) != 2 {
		t.Fatalf("want two kinds, got %v", got)
	}
}

func TestRunOnOfAcceptsAStringOrAList(t *testing.T) {
	one, err := RunOnOf(map[string]any{RunOnKey: "webhook"})
	if err != nil || len(one) != 1 || one[0] != "webhook" {
		t.Errorf("single string: %v %v", one, err)
	}
	many, err := RunOnOf(map[string]any{RunOnKey: []any{"schedule", "manual"}})
	if err != nil || len(many) != 2 {
		t.Errorf("list: %v %v", many, err)
	}
	none, err := RunOnOf(map[string]any{})
	if err != nil || none != nil {
		t.Errorf("absent: %v %v", none, err)
	}
	if _, err := RunOnOf(map[string]any{RunOnKey: 7}); err == nil {
		t.Error("a number is not a trigger list")
	}
}

// TestReservedKeyNotRejectedByPlugins: run_on stays in the node config so a
// visual-editor save round-trips it, which means every plugin's own
// unknown-key check would otherwise refuse it.
func TestReservedKeyNotRejectedByPlugins(t *testing.T) {
	cfg := map[string]any{"url": "http://x", RunOnKey: "webhook"}
	if errs := OptUnknownKeys(cfg, "somesource", "url"); len(errs) != 0 {
		t.Errorf("run_on must not read as an unknown key: %v", errs)
	}
	if errs := OptUnknownKeys(map[string]any{"nope": 1}, "somesource", "url"); len(errs) != 1 {
		t.Errorf("a genuinely unknown key must still be reported: %v", errs)
	}
}

// TestScheduleAdmitsStartup: a pipeline set to run at boot is running its
// schedule early, so a source that says "schedule" must not sit that one out.
func TestScheduleAdmitsStartup(t *testing.T) {
	boot := Trigger{Kind: TriggerStartup}
	if !boot.Matches([]string{"schedule"}) {
		t.Error("schedule should admit the startup fire")
	}
	if !boot.Matches([]string{"startup"}) {
		t.Error("startup should admit itself")
	}
	if boot.Matches([]string{"webhook"}) {
		t.Error("a webhook-only source must still sit out the boot fire")
	}
	// The implication is one-way: a startup-only source does not run on cron.
	if (Trigger{Kind: TriggerSchedule}).Matches([]string{"startup"}) {
		t.Error("startup alone must not admit a scheduled fire")
	}
	if kinds := RunOnKinds([]string{"schedule"}); len(kinds) != 2 {
		t.Errorf("schedule admits schedule+startup, got %v", kinds)
	}
}
