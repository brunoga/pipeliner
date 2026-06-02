package scheduler

import (
	"context"
	"sync"
	"testing"
	"time"
)

// fixedSchedule always fires at a fixed offset from now, for deterministic tests.
type fixedSchedule struct{ after time.Duration }

func (f fixedSchedule) Next(from time.Time) time.Time { return from.Add(f.after) }

func TestDaemonFiresTask(t *testing.T) {
	d := &Daemon{}
	d.Add("task-a", fixedSchedule{10 * time.Millisecond})

	var mu sync.Mutex
	fired := []string{}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go d.Run(ctx, func(_ context.Context, name string, _ bool) {
		mu.Lock()
		fired = append(fired, name)
		mu.Unlock()
	})

	<-ctx.Done()

	mu.Lock()
	defer mu.Unlock()
	if len(fired) == 0 {
		t.Error("expected task-a to fire at least once")
	}
	for _, name := range fired {
		if name != "task-a" {
			t.Errorf("unexpected task name: %q", name)
		}
	}
}

func TestDaemonMultipleTasks(t *testing.T) {
	d := &Daemon{}
	d.Add("task-a", fixedSchedule{20 * time.Millisecond})
	d.Add("task-b", fixedSchedule{15 * time.Millisecond})

	var mu sync.Mutex
	seen := map[string]bool{}

	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	go d.Run(ctx, func(_ context.Context, name string, _ bool) {
		mu.Lock()
		seen[name] = true
		mu.Unlock()
	})

	<-ctx.Done()

	mu.Lock()
	defer mu.Unlock()
	if !seen["task-a"] {
		t.Error("task-a should have fired")
	}
	if !seen["task-b"] {
		t.Error("task-b should have fired")
	}
}

func TestDaemonShutdownOnCancel(t *testing.T) {
	d := &Daemon{}
	d.Add("slow-task", fixedSchedule{time.Hour}) // won't fire within test

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		d.Run(ctx, func(_ context.Context, name string, _ bool) {})
		close(done)
	}()

	cancel()

	select {
	case <-done:
		// clean shutdown
	case <-time.After(time.Second):
		t.Error("daemon did not shut down after context cancel")
	}
}

func TestDaemonEmptyStartup(t *testing.T) {
	d := &Daemon{} // no entries

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	done := make(chan struct{})
	go func() {
		d.Run(ctx, func(_ context.Context, _ string, _ bool) {})
		close(done)
	}()

	<-ctx.Done()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Error("daemon did not stop after context cancel")
	}
}

func TestDaemonTriggerOverlap(t *testing.T) {
	d := &Daemon{}

	var mu sync.Mutex
	running := 0
	maxRunning := 0

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	runner := func(ctx context.Context, name string, _ bool) {
		mu.Lock()
		running++
		if running > maxRunning {
			maxRunning = running
		}
		mu.Unlock()

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		running--
		mu.Unlock()
	}

	go d.Run(ctx, runner)

	// Trigger the same task twice quickly.
	d.Trigger("manual-task", false)
	d.Trigger("manual-task", false)

	<-ctx.Done()

	mu.Lock()
	m := maxRunning
	mu.Unlock()

	if m > 1 {
		t.Errorf("expected at most 1 instance of manual-task to run, but found %d", m)
	}
}

func TestDaemonScheduledOverlap(t *testing.T) {
	d := &Daemon{}
	// Schedule a task to fire every 10ms.
	d.Add("scheduled-overlap", fixedSchedule{10 * time.Millisecond})

	var mu sync.Mutex
	running := 0
	maxRunning := 0

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	runner := func(ctx context.Context, name string, _ bool) {
		mu.Lock()
		running++
		if running > maxRunning {
			maxRunning = running
		}
		mu.Unlock()

		// Sleep for longer than the interval.
		time.Sleep(30 * time.Millisecond)

		mu.Lock()
		running--
		mu.Unlock()
	}

	go d.Run(ctx, runner)

	<-ctx.Done()

	mu.Lock()
	m := maxRunning
	mu.Unlock()

	if m > 1 {
		t.Errorf("expected at most 1 instance of scheduled-overlap to run, but found %d", m)
	}
}

// TestDaemonWaitBlocksForInFlightTasks is the core invariant for graceful
// shutdown: after Run returns (scheduler ctx cancelled), Wait blocks until
// every still-running task goroutine has finished. Without Wait the daemon
// would return immediately and the caller might tear down resources the
// task is still using (db handle, etc).
func TestDaemonWaitBlocksForInFlightTasks(t *testing.T) {
	d := &Daemon{}
	d.TriggerAtStart("slow-task")

	// runnerStarted closes once the runner has begun; runnerCanFinish gates
	// when it returns. This lets us reliably exercise the "task still running
	// when Run returns" scenario.
	runnerStarted   := make(chan struct{})
	runnerCanFinish := make(chan struct{})

	runCtx, cancelRun := context.WithCancel(context.Background())
	defer cancelRun()

	runDone := make(chan struct{})
	go func() {
		defer close(runDone)
		d.Run(runCtx, func(ctx context.Context, _ string, _ bool) {
			close(runnerStarted)
			// Hold until the test releases us. The runner intentionally
			// ignores ctx here to simulate a plugin that doesn't observe
			// cancellation immediately.
			<-runnerCanFinish
			_ = ctx
		})
	}()

	<-runnerStarted
	cancelRun()
	<-runDone // Run loop has exited; runner is still alive.

	// Wait with a tight deadline must time out — runner is still blocked.
	shortCtx, cancelShort := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelShort()
	if d.Wait(shortCtx) {
		t.Fatal("Wait should NOT return true while a task goroutine is still running")
	}

	// Release the runner and wait again with plenty of slack.
	close(runnerCanFinish)
	longCtx, cancelLong := context.WithTimeout(context.Background(), time.Second)
	defer cancelLong()
	if !d.Wait(longCtx) {
		t.Fatal("Wait should return true once the task goroutine has finished")
	}
}

// TestDaemonWaitReturnsImmediatelyWhenIdle verifies the zero-tasks-in-flight
// case — calling Wait on a daemon that never ran (or whose tasks all finished)
// must not block.
func TestDaemonWaitReturnsImmediatelyWhenIdle(t *testing.T) {
	d := &Daemon{}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	t0 := time.Now()
	if !d.Wait(ctx) {
		t.Fatal("Wait should return true immediately when no tasks are in flight")
	}
	if elapsed := time.Since(t0); elapsed > 50*time.Millisecond {
		t.Errorf("Wait blocked for %v on an idle daemon; should be near-instant", elapsed)
	}
}
