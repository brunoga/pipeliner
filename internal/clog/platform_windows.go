//go:build windows

package clog

import (
	"os"

	"golang.org/x/sys/windows"
)

// enableColor tries to enable VT100 processing on the Windows console handle.
// Returns true if VT processing is already active or was successfully enabled.
// Requires Windows 10 build 1511 or later; returns false on older systems.
func enableColor(f *os.File) bool {
	h := windows.Handle(f.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(h, &mode); err != nil {
		return false
	}
	if mode&windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING != 0 {
		return true
	}
	return windows.SetConsoleMode(h, mode|windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING) == nil
}
