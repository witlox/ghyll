//go:build !linux && !darwin

package runner

import "os"

// openNoFollowRDWR fallback: on platforms without O_NOFOLLOW, the
// Lstat-before-Open in truncateTrailingPartialFile is the primary
// guard. A TOCTOU window remains, but the attacker would need to
// race file creation between Lstat + Open — much narrower than the
// previous "any symlink works" surface.
func openNoFollowRDWR(path string) (*os.File, error) {
	return os.OpenFile(path, os.O_RDWR, 0o644)
}
