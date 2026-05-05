//go:build !windows

package store

import (
	"os"
	"syscall"
)

// acquireFileLock obtains an exclusive advisory lock on f using flock(2).
func acquireFileLock(f *os.File) error {
	return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
}
