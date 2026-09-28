package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/brunoga/pipeliner/internal/entry"
)

func deliverOne(t *testing.T, cfg map[string]any, e *entry.Entry) {
	t.Helper()
	p, err := newPlugin(cfg, nil)
	if err != nil {
		t.Fatalf("newPlugin: %v", err)
	}
	if err := p.(*execPlugin).deliver(context.Background(), makeCtx(), []*entry.Entry{e}); err != nil {
		t.Fatalf("deliver: %v", err)
	}
}

func readArgs(t *testing.T, path string) []string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("helper wrote no arguments: %v", err)
	}
	if len(b) == 0 {
		return nil
	}
	return strings.Split(string(b), "\n")
}

// The case the sink is most often pointed at, and the one it could not do:
// a path with spaces must arrive as a single argument.
func TestQuotedPathArrivesAsOneArgument(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	cmd, _ := helperCommand(t, "args:"+out)

	e := entry.New("x", "http://x/1")
	e.Fields["file_location"] = "/media/Life of Pi (2012)/Life of Pi.mkv"
	deliverOne(t, map[string]any{"command": cmd + ` --in "{file_location}"`}, e)

	got := readArgs(t, out)
	want := []string{"--in", "/media/Life of Pi (2012)/Life of Pi.mkv"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("helper saw %#v, want %#v", got, want)
	}
}

// The args list needs no quoting at all — each element is one argv entry.
func TestArgsListNeedsNoQuoting(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "args.txt")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("GO_EXEC_HELPER", "args:"+out)

	e := entry.New("x", "http://x/1")
	e.Fields["file_location"] = "/media/Life of Pi (2012)/Life of Pi.mkv"
	deliverOne(t, map[string]any{
		"command": exe,
		"args":    []string{"-test.run=TestHelperHandler", "--", "--in", "{file_location}", "--tag", "3D SBS"},
	}, e)

	got := readArgs(t, out)
	want := []string{"--in", "/media/Life of Pi (2012)/Life of Pi.mkv", "--tag", "3D SBS"}
	if len(got) != len(want) {
		t.Fatalf("helper saw %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("arg %d = %q, want %q", i, got[i], want[i])
		}
	}
}

// A non-zero exit must fail the entry. Otherwise the commit phase records the
// item as delivered and an upstream tracker never retries it.
func TestNonZeroExitFailsTheEntry(t *testing.T) {
	cmd, _ := helperCommand(t, "fail")
	e := entry.New("x", "http://x/1")
	e.Accept("test")
	deliverOne(t, map[string]any{"command": cmd}, e)
	if e.State != entry.Failed {
		t.Errorf("state = %v, want Failed", e.State)
	}
}

func TestIgnoreErrorsKeepsTheEntry(t *testing.T) {
	cmd, _ := helperCommand(t, "fail")
	e := entry.New("x", "http://x/1")
	e.Accept("test")
	deliverOne(t, map[string]any{"command": cmd, "ignore_errors": true}, e)
	if e.State == entry.Failed {
		t.Error("ignore_errors=true must leave the entry accepted")
	}
}

func TestSuccessLeavesTheEntryAlone(t *testing.T) {
	cmd, _ := helperCommand(t, "ok")
	e := entry.New("x", "http://x/1")
	e.Accept("test")
	deliverOne(t, map[string]any{"command": cmd}, e)
	if e.State == entry.Failed {
		t.Error("a zero exit must not fail the entry")
	}
}

// Malformed quoting fails the entry rather than executing a wrong split.
func TestUnterminatedQuoteFailsTheEntry(t *testing.T) {
	e := entry.New("x", "http://x/1")
	e.Accept("test")
	deliverOne(t, map[string]any{"command": `prog "unterminated`}, e)
	if e.State != entry.Failed {
		t.Errorf("state = %v, want Failed", e.State)
	}
}

// A cancelled context must stop the child rather than hang the run.
func TestCancellationStopsTheChild(t *testing.T) {
	cmd, _ := helperCommand(t, "sleep")
	p, err := newPlugin(map[string]any{"command": cmd, "ignore_errors": true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = p.(*execPlugin).deliver(ctx, makeCtx(), []*entry.Entry{entry.New("x", "http://x/1")})
		close(done)
	}()
	cancel()
	<-done
}
