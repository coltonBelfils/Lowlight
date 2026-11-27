//go:build !unix

package sema

import (
	"fmt"
	"os"
)

// openReadNoFollow opens path read-only after verifying via Lstat that the
// final path component is not a symlink or reparse point.
func openReadNoFollow(path string) (*os.File, error) {
	linfo, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if linfo.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing to follow symlink %q", path)
	}
	return os.Open(path)
}
