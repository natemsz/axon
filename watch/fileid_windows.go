//go:build windows

package watch

import (
	"os"
	"syscall"
)

// fileID is a log file's Windows identity: the same file keeps it
// across appends, a replacement at the same path does not.
func fileID(f *os.File) (uint64, bool) {
	var info syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &info); err != nil {
		return 0, false
	}
	return uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow), true
}
