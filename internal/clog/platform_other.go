//go:build !windows

package clog

import "os"

// enableColor reports whether ANSI color output is supported on f.
// On non-Windows platforms VT sequences are always supported.
func enableColor(_ *os.File) bool { return true }
