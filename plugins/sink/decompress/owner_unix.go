//go:build unix

package decompress

import (
	"io/fs"
	"syscall"
)

// fileIDs returns a file's owner and group.
func fileIDs(info fs.FileInfo) (uid, gid int, ok bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, 0, false
	}
	return int(st.Uid), int(st.Gid), true
}
