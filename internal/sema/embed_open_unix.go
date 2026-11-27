//go:build unix

package sema

import (
	"os"
	"syscall"
)

// openReadNoFollow opens path read-only without following a final symlink or
// blocking on special files such as FIFOs.
func openReadNoFollow(path string) (*os.File, error) {
	// Include O_NONBLOCK so opening a FIFO fails the caller's regular-file check
	// instead of hanging the compiler in open(2).
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
}
