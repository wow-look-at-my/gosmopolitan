// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package runtime

import (
	"internal/runtime/cgroup"
)

// We can't allocate during early initialization when we need to find the
// cgroup. Simply use a fixed global as a scratch parsing buffer. It lands in
// .bss, so it costs no binary size and is only faulted in if it is used.
var cgroupScratch [cgroup.ScratchSize]byte

// cgroupMemoryLimit returns the memory limit of the cgroup containing this
// process, for use as the default GOMEMLIMIT. ok is false when there is no
// cgroup, no limit, or the limit cannot be read.
//
// Called from gcinit, before the heap is in use, so it must not allocate.
func cgroupMemoryLimit() (int64, bool) {
	if !cgroupHostSupported() {
		return 0, false
	}

	limit, ok, err := cgroup.ReadMemoryLimit(cgroupScratch[:])
	if err != nil || !ok {
		// Likely cgroup.ErrNoCgroup.
		return 0, false
	}
	const maxLimit = 1<<63 - 1
	if limit > maxLimit {
		return maxLimit, true
	}
	return int64(limit), true
}
