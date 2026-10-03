// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build linux

package cgroup

// ReadMemoryLimit returns the memory limit in bytes of the memory cgroup
// containing the current process. ok is false when the process is not in a
// memory cgroup or the cgroup sets no limit.
//
// scratch must have length ScratchSize.
//
// Unlike the CPU limit, this opens, reads and closes in one call. The runtime
// reads the memory limit once at startup, so there is no descriptor worth
// keeping open for the life of the process.
func ReadMemoryLimit(scratch []byte) (uint64, bool, error) {
	checkBufferSize(scratch, ScratchSize)

	base := scratch[:PathSize]
	scratch2 := scratch[PathSize:]

	n, version, err := findMemory(base, scratch2)
	if err != nil {
		return 0, false, err
	}

	var n2 int
	switch version {
	case V1:
		n2 = copy(base[n:], v1MemoryLimitFile)
	case V2:
		n2 = copy(base[n:], v2MemoryMaxFile)
	default:
		throw("impossible cgroup version")
	}
	path := base[:n+n2]

	fd, errno := sysOpenRead(&path[0])
	if errno != 0 {
		// This may fail if this process was migrated out of the cgroup
		// found above and that cgroup has been deleted.
		return 0, false, errSyscallFailed
	}

	limit, ok, err := readMemoryLimit(fd)
	sysClose(fd)
	return limit, ok, err
}

// readMemoryLimit reads the limit from an open memory.max (v2) or
// memory.limit_in_bytes (v1) file.
func readMemoryLimit(fd int) (uint64, bool, error) {
	// The file holds "<bytes>\n", or "max\n" on cgroup v2 when there is no
	// limit. MaxUint64 requires 20 bytes to display in base 10, so 64 bytes
	// is plenty.
	var b [64]byte
	n, errno := sysPread(fd, b[:], 0)
	if errno != 0 {
		return 0, false, errSyscallFailed
	}
	if n == len(b) {
		return 0, false, errMalformedFile
	}

	return parseMemoryLimit(b[:n])
}

// findMemory finds the path to the memory cgroup that this process is a member
// of and places it in out. scratch is a scratch buffer for internal use.
//
// out must have length PathSize. scratch must have length ParseSize.
//
// Returns the number of bytes written to out and the cgroup version (1 or 2).
//
// Returns ErrNoCgroup if the process is not in a memory cgroup.
func findMemory(out []byte, scratch []byte) (int, Version, error) {
	checkBufferSize(out, PathSize)
	checkBufferSize(scratch, ParseSize)

	// The cgroup path is <cgroup mount point> + <relative path>.
	n, version, err := findMemoryCgroup(out, scratch)
	if err != nil {
		return 0, 0, err
	}

	n, err = findMemoryMountPoint(out, out[:n], version, scratch)
	return n, version, err
}

// findMemoryCgroup reads the memory cgroup this process is a member of from
// /proc/self/cgroup.
func findMemoryCgroup(out []byte, scratch []byte) (int, Version, error) {
	path := []byte("/proc/self/cgroup\x00")
	fd, errno := sysOpenRead(&path[0])
	if sysNotExist(errno) {
		return 0, 0, ErrNoCgroup
	} else if errno != 0 {
		return 0, 0, errSyscallFailed
	}

	n, version, err := parseCgroup(fd, sysRead, out, scratch, "memory")
	sysClose(fd)
	if err != nil {
		return 0, 0, err
	}
	return n, version, nil
}

// findMemoryMountPoint finds the mount point holding the memory controller for
// the specified cgroup and composes the full path to the cgroup in out.
//
// out must have length PathSize, may overlap with cgroup.
// scratch must have length ParseSize.
func findMemoryMountPoint(out, cgroup []byte, version Version, scratch []byte) (int, error) {
	path := []byte("/proc/self/mountinfo\x00")
	fd, errno := sysOpenRead(&path[0])
	if sysNotExist(errno) {
		return 0, ErrNoCgroup
	} else if errno != 0 {
		return 0, errSyscallFailed
	}

	n, err := parseMount(fd, sysRead, out, cgroup, version, scratch, "memory")
	sysClose(fd)
	if err != nil {
		return 0, err
	}
	return n, nil
}
