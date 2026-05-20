//go:build linux || darwin

package runner

import (
	"os"
	"syscall"
)

// openNoFollowRDWR opens path with O_RDWR + O_NOFOLLOW so a
// symlink raced into the tree at the last moment between Lstat
// and Open is rejected. Gate-2 SEC-C-2.
func openNoFollowRDWR(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR|syscall.O_NOFOLLOW, 0o644)
}
