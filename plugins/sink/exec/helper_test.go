package exec

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

// The tests drive a real child process rather than a shell builtin, because
// `touch`, `false` and `sleep` do not exist on Windows and this sink is built
// for all three desktop platforms. Re-invoking the test binary is the portable
// way to get a program with known behaviour on every OS — it is what os/exec's
// own tests do.
//
// TestHelperHandler is not a test. It runs only when the parent sets
// GO_EXEC_HELPER, and it does whatever that variable asks:
//
//	args:<path>  write the arguments it received to <path>, one per line
//	touch        create an empty file at each path it was given
//	fail         exit non-zero
//	sleep        block until killed, for cancellation tests
//	ok           exit zero
func TestHelperHandler(t *testing.T) {
	mode := os.Getenv("GO_EXEC_HELPER")
	if mode == "" {
		t.Skip("helper process; runs only when the parent asks for it")
	}
	switch {
	case strings.HasPrefix(mode, "args:"):
		path := strings.TrimPrefix(mode, "args:")
		if err := os.WriteFile(path, []byte(strings.Join(helperArgs(), "\n")), 0o600); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		os.Exit(0)
	case mode == "touch":
		for _, a := range helperArgs() {
			f, err := os.Create(a) //nolint:gosec // test helper, path comes from the test
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			_ = f.Close()
		}
		os.Exit(0)
	case mode == "fail":
		fmt.Fprintln(os.Stderr, "helper failing on purpose")
		os.Exit(1)
	case mode == "sleep":
		select {}
	default:
		os.Exit(0)
	}
}

// helperCommand returns a `command` string that re-invokes this test binary in
// helper mode, plus the env the child needs. The executable path is quoted
// because it can itself contain spaces (a temp dir on macOS routinely does),
// which is the very thing these tests are about.
func helperCommand(t *testing.T, mode string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("GO_EXEC_HELPER", mode)
	return `"` + exe + `" -test.run=TestHelperHandler --`, nil
}

// helperArgs returns the arguments the sink passed. os.Args also holds the
// test binary's own flags, so everything after the "--" separator that the
// helper invocation inserts is ours.
func helperArgs() []string {
	for i, a := range os.Args {
		if a == "--" {
			return os.Args[i+1:]
		}
	}
	return nil
}

// helperArgv is the args-list form of helperCommand: the program on its own,
// plus the fixed arguments the child needs before the caller's own.
func helperArgv(t *testing.T, mode string) (string, []string) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	t.Setenv("GO_EXEC_HELPER", mode)
	return exe, []string{"-test.run=TestHelperHandler", "--"}
}
