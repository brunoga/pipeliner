//go:build !unix

package decompress

import "io/fs"

// fileIDs has nothing to report where files have no Unix owner and group;
// extracted files then keep the defaults the system gives them.
func fileIDs(fs.FileInfo) (uid, gid int, ok bool) { return 0, 0, false }
