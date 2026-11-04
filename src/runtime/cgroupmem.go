// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build linux

// We can't allocate during early initialization when we need to find the cgroup. use a fixed global.

package runtime

import (
	"internal/runtime/cgroup"
)

// We can't allocate during early initialization when we need to find the cgroup. use a fixed global.
var cgroupScratch [cgroup.ScratchSize]byte

// ok is false when there is no cgroup, no limit, or the limit cannot be read.
func cgroupMemoryLimit() (int64, bool) {
	if !cgroupHostSupported() {
		return 0, false
	}

	limit, ok, err := cgroup.ReadMemoryLimit(cgroupScratch[:])
	if err != nil || !ok {
		// Likely cgroup.ErrNoCgroup.
		return 0, false
	}
	return cgroupMemoryLimitToGOMEMLIMIT(limit), true
}

// A soft limit equal to the hard limit lets the kernel kill the process
// first.
func cgroupMemoryLimitToGOMEMLIMIT(limit uint64) int64 {
	const maxLimit = 1<<63 - 1
	if limit > maxLimit {
		limit = maxLimit
	}
	return int64(limit - limit/10)
}
