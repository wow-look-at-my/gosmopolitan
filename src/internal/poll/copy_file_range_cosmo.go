// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package poll

import (
	"internal/syscall/unix"
	"sync"
	"syscall"
)

var supportCopyFileRange = sync.OnceValue(func() bool {
	// copy_file_range(2) is broken in various ways on kernels older than 5.3,
	// see https://go.dev/issue/42400 and
	// https://man7.org/linux/man-pages/man2/copy_file_range.2.html#VERSIONS
	// Cosmopolitan reports kernel version 5.10, so this should pass.
	return unix.KernelVersionGE(5, 3)
})

// For best performance, call copy_file_range() with the largest len value
// possible. Linux sets up a limitation of data transfer for most of its I/O
// system calls, as MAX_RW_COUNT (INT_MAX & PAGE_MASK). This value equals to
// the maximum integer value minus a page size that is typically 2^12=4096 bytes.
// That is to say, it's the maximum integer value with the lowest 12 bits unset,
// which is 0x7ffff000.
const maxCopyFileRangeRound = 0x7ffff000

func handleCopyFileRangeErr(err error, copied, written int64) (bool, error) {
	switch err {
	case syscall.ENOSYS:
		// copy_file_range(2) may not be present on all platforms.
		// If we see ENOSYS, we have certainly not transferred
		// any data, so we can tell the caller that we
		// couldn't handle the transfer and let them fall
		// back to more generic code.
		return false, nil
	case syscall.EXDEV, syscall.EINVAL, syscall.EIO, syscall.EOPNOTSUPP, syscall.EPERM:
		// Various error conditions that indicate copy_file_range
		// cannot handle this transfer. Fall back to generic copy.
		return false, nil
	case nil:
		if copied == 0 {
			// copy_file_range can silently fail by reporting
			// success and 0 bytes written. Assume such cases are
			// failure and fallback to a different copy mechanism.
			if written == 0 {
				return false, nil
			}
			// Otherwise src is at EOF, which means we are done.
		}
	}
	return true, err
}
