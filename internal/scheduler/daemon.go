package scheduler

import (
	"context"
	"sync"
	"time"
)

// TaskRunner is called by the Daemon to execute a named task.
type TaskRunner func(ctx context.Context, taskName string)

// entry holds one scheduled task.
type entry struct {
	taskName string
	schedule Schedule
	next     time.Time
}

// ScheduledTask pairs a task name with its schedule for use with Reset.
type ScheduledTask struct {
	Name       string
	Schedule   Schedule
	RunAtStart bool // true for interval schedules; false for cron schedules
}

// Daemon runs tasks on their configured schedules.
type Daemon struct {
	mu        sync.Mutex
	entries   []*entry
	triggerCh chan string
	wakeCh    chan struct{}
	immediate []string            // tasks to fire at the start of Run
	running   map[string]struct{} // tasks currently executing
}

// triggerChan returns the trigger channel, creating it lazily.
func (d *Daemon) triggerChan() chan string {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.triggerCh == nil {
		d.triggerCh = make(chan string, 16)
	}
	return d.triggerCh
}

// wakeupChan returns the wake channel, creating it lazily.
func (d *Daemon) wakeupChan() chan struct{} {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.wakeCh == nil {
		d.wakeCh = make(chan struct{}, 1)
	}
	return d.wakeCh
}

// Reset atomically replaces all schedule entries and wakes the run loop so
// the new schedule takes effect immediately without waiting for the old timer.
func (d *Daemon) Reset(tasks []ScheduledTask) {
	d.mu.Lock()
	entries := make([]*entry, len(tasks))
	for i, t := range tasks {
		entries[i] = &entry{
			taskName: t.Name,
			schedule: t.Schedule,
			next:     t.Schedule.Next(time.Now()),
		}
	}
	d.entries = entries
	d.mu.Unlock()
	select {
	case d.wakeupChan() <- struct{}{}:
	default:
	}
}

// Trigger fires the named task immediately, outside its normal schedule.
// It is safe to call from any goroutine. If the channel is full the
// trigger is silently dropped.
func (d *Daemon) Trigger(name string) {
	ch := d.triggerChan()
	select {
	case ch <- name:
	default:
	}
}

// NextRun returns the next scheduled fire time for the named task,
// or the zero time if the task is not scheduled.
func (d *Daemon) NextRun(name string) time.Time {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, e := range d.entries {
		if e.taskName == name {
			return e.next
		}
	}
	return time.Time{}
}

// TriggerAtStart registers a task to be fired immediately when Run is called.
// It is safe to call before Run.
func (d *Daemon) TriggerAtStart(name string) {
	d.mu.Lock()
	d.immediate = append(d.immediate, name)
	d.mu.Unlock()
}

// Add registers a task to run on the given schedule.
// It is safe to call Add before or after Run.
func (d *Daemon) Add(taskName string, s Schedule) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.entries = append(d.entries, &entry{
		taskName: taskName,
		schedule: s,
		next:     s.Next(time.Now()),
	})
}

// Run blocks until ctx is cancelled, firing tasks as they become due.
// Each task fires in its own goroutine; concurrent runs of the same task
// are skipped if one is already in progress.
func (d *Daemon) Run(ctx context.Context, runner TaskRunner) {
	triggerCh := d.triggerChan()
	wakeCh := d.wakeupChan()

	d.mu.Lock()
	imm := d.immediate
	d.immediate = nil
	d.mu.Unlock()
	for _, name := range imm {
		go d.runTask(ctx, name, runner)
	}

	for {
		d.mu.Lock()
		wake := d.nextWake()
		d.mu.Unlock()

		var timer *time.Timer
		if wake.IsZero() {
			// No entries yet; check again in a second.
			timer = time.NewTimer(time.Second)
		} else {
			delay := max(time.Until(wake), 0)
			timer = time.NewTimer(delay)
		}

		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case now := <-timer.C:
			d.fireDue(ctx, runner, now)
		case name := <-triggerCh:
			timer.Stop()
			go d.runTask(ctx, name, runner)
		case <-wakeCh:
			timer.Stop() // recalculate timer with new entries
		}
	}
}

// runTask executes the runner if the task is not already running.
func (d *Daemon) runTask(ctx context.Context, name string, runner TaskRunner) {
	d.mu.Lock()
	if d.running == nil {
		d.running = make(map[string]struct{})
	}
	if _, ok := d.running[name]; ok {
		d.mu.Unlock()
		return
	}
	d.running[name] = struct{}{}
	d.mu.Unlock()

	defer func() {
		d.mu.Lock()
		delete(d.running, name)
		d.mu.Unlock()
	}()

	runner(ctx, name)
}

// nextWake returns the earliest scheduled fire time across all entries.
func (d *Daemon) nextWake() time.Time {
	var earliest time.Time
	for _, e := range d.entries {
		if earliest.IsZero() || e.next.Before(earliest) {
			earliest = e.next
		}
	}
	return earliest
}

// fireDue runs all entries whose next time is at or before now.
func (d *Daemon) fireDue(ctx context.Context, runner TaskRunner, now time.Time) {
	d.mu.Lock()
	var due []string
	for _, e := range d.entries {
		if !e.next.After(now) {
			due = append(due, e.taskName)
			e.next = e.schedule.Next(now)
		}
	}
	d.mu.Unlock()

	for _, name := range due {
		go d.runTask(ctx, name, runner)
	}
}
