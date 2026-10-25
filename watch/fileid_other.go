//go:build !windows

package watch

import "os"

// fileID: only Windows offers a per-file identity through the
// handles this package already opens; elsewhere size and
// modification time carry the tail's identity.
func fileID(f *os.File) (uint64, bool) {
	return 0, false
}
