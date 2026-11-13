package decompress

import (
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// owner is what extracted files and folders take after: the destination
// root's owner, group and permission bits.
//
// Pipeliner often runs as root in a container while the library belongs to a
// user and a group the download client shares. Left alone, an extracted
// season folder was root's with mode 0755, and the download client could not
// move a later episode into it. Taking after the root keeps everything below
// it the way the rest of the library is.
type owner struct {
	uid, gid int
	dirMode  fs.FileMode // includes setgid when the root has it
	fileMode fs.FileMode // the root's bits without execute
	known    bool
}

// ownerOf reads the owner of root, or of its nearest existing ancestor.
func ownerOf(root string) owner {
	for p := filepath.Clean(root); ; p = filepath.Dir(p) {
		info, err := os.Stat(p)
		if err == nil {
			o := owner{dirMode: info.Mode().Perm() | info.Mode()&fs.ModeSetgid,
				fileMode: info.Mode().Perm() &^ 0o111}
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				o.uid, o.gid, o.known = int(st.Uid), int(st.Gid), true
			}
			return o
		}
		if parent := filepath.Dir(p); parent == p {
			return owner{}
		}
	}
}

// apply gives path the owner's ids and the mode for its kind. Failures are
// ignored: not running as root, a chown is refused, and the file is still
// correct, only owned by whoever ran the extraction.
func (o owner) apply(path string, dir bool) {
	if !o.known {
		return
	}
	_ = os.Lchown(path, o.uid, o.gid)
	if dir {
		_ = os.Chmod(path, o.dirMode)
	} else {
		_ = os.Chmod(path, o.fileMode)
	}
}

// mkdirAll creates dir and its missing parents, each taking after o.
func (o owner) mkdirAll(dir string) error {
	var missing []string
	for p := filepath.Clean(dir); ; p = filepath.Dir(p) {
		if _, err := os.Stat(p); err == nil {
			break
		}
		missing = append(missing, p)
		if parent := filepath.Dir(p); parent == p {
			break
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i := len(missing) - 1; i >= 0; i-- {
		o.apply(missing[i], true)
	}
	return nil
}
