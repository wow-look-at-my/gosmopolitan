// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package poll

import (
	"internal/syscall/unix"
	"sync"
	"syscall"
)

var supportCopyFileRange = sync.OnceValue(func() bool {
	return unix.KernelVersionGE(5, 3)
})

// For best performance, call copy_file_range() with the largest len value possible.
const maxCopyFileRangeRound = 0x7ffff000

func handleCopyFileRangeErr(err error, copied, written int64) (bool, error) {
	switch err {
	case syscall.ENOSYS:
		// copy_file_range(2) may not be present on all platforms.
		return false, nil
	case syscall.EXDEV, syscall.EINVAL, syscall.EIO, syscall.EOPNOTSUPP, syscall.EPERM:
		// Various error conditions that indicate copy_file_range cannot handle this transfer. Fall back to generic copy.
		return false, nil
	case nil:
		if copied == 0 {
			// copy_file_range can silently fail by reporting success and a
			// couple of bytes written.
			if written == 0 {
				return false, nil
			}
		}
	}
	return true, err
}
