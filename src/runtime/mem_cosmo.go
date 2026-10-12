// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

const (
	_EACCES = 13
	_EINVAL = 22
)

// Don't split the stack as this method may be invoked without a valid G, which
// prevents us from allocating more stack.
//
//go:nosplit
func sysAllocOS(n uintptr, vmaName string) unsafe.Pointer {
	if iswindows() {
		// Reserve+commit in one call, like upstream mem_windows.go. Returns nil on failure; the caller handles out-of-memory.
		return ntVirtualAlloc(nil, n, _NT_MEM_RESERVE|_NT_MEM_COMMIT, _NT_PAGE_READWRITE)
	}
	p, err := mmap(nil, n, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_PRIVATE, -1, 0)
	if err != 0 {
		if err == _EACCES {
			print("runtime: mmap: access denied\n")
			exit(2)
		}
		if err == _EAGAIN {
			print("runtime: mmap: too much locked memory (check 'ulimit -l').\n")
			exit(2)
		}
		return nil
	}
	return p
}

var adviseUnused = uint32(_MADV_FREE)

const madviseUnsupported = 0

// ntCommitPages and ntDecommitPages are the NT commit and decommit
// primitives,.go's halving loops.

func ntCommitPages(v unsafe.Pointer, n uintptr) {
	if p := ntVirtualAlloc(v, n, _NT_MEM_COMMIT, _NT_PAGE_READWRITE); p != nil {
		return
	}
	for n > 0 {
		small := n
		for small >= 4096 && ntVirtualAlloc(v, small, _NT_MEM_COMMIT, _NT_PAGE_READWRITE) == nil {
			small /= 2
			small &^= 4096 - 1
		}
		if small < 4096 {
			print("runtime: VirtualAlloc MEM_COMMIT of ", small, " bytes at ", v, " failed\n")
			throw("runtime: cannot commit pages")
		}
		v = add(v, small)
		n -= small
	}
}

func ntDecommitPages(v unsafe.Pointer, n uintptr) {
	if ntVirtualFree(v, n, _NT_MEM_DECOMMIT) != 0 {
		return
	}
	for n > 0 {
		small := n
		for small >= 4096 && ntVirtualFree(v, small, _NT_MEM_DECOMMIT) == 0 {
			small /= 2
			small &^= 4096 - 1
		}
		if small < 4096 {
			print("runtime: VirtualFree MEM_DECOMMIT of ", small, " bytes at ", v, " failed\n")
			throw("runtime: failed to decommit pages")
		}
		v = add(v, small)
		n -= small
	}
}

func sysUnusedOS(v unsafe.Pointer, n uintptr) {
	if uintptr(v)&(physPageSize-1) != 0 || n&(physPageSize-1) != 0 {
		// madvise will round this to any physical page *covered* by this range.
		throw("unaligned sysUnused")
	}

	if iswindows() {
		// NT: decommit (page-granular within a reservation is allowed.
		ntDecommitPages(v, n)
		return
	}

	advise := atomic.Load(&adviseUnused)
	if debug.madvdontneed != 0 && advise != madviseUnsupported {
		advise = _MADV_DONTNEED
	}
	switch advise {
	case _MADV_FREE:
		if madvise(v, n, _MADV_FREE) == 0 {
			break
		}
		atomic.Store(&adviseUnused, _MADV_DONTNEED)
		fallthrough
	case _MADV_DONTNEED:
		if madvise(v, n, _MADV_DONTNEED) == 0 {
			break
		}
		atomic.Store(&adviseUnused, madviseUnsupported)
		fallthrough
	case madviseUnsupported:
		// Fall back on mmap if madvise is not supported.
		p, err := mmap(v, n, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_FIXED|_MAP_PRIVATE, -1, 0)
		if err == 0 && p != nil {
		}
	}

	if debug.harddecommit > 0 {
		p, err := mmap(v, n, _PROT_NONE, _MAP_ANON|_MAP_FIXED|_MAP_PRIVATE, -1, 0)
		if p != v || err != 0 {
			throw("runtime: cannot disable permissions in address space")
		}
	}
}

func sysUsedOS(v unsafe.Pointer, n uintptr) {
	if iswindows() {
		// NT decommits in sysUnusedOS, so committing here is mandatory (upstream mem_windows.go semantics).
		ntCommitPages(v, n)
		return
	}
	if debug.harddecommit > 0 {
		p, err := mmap(v, n, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_FIXED|_MAP_PRIVATE, -1, 0)
		if err == _ENOMEM {
			throw("runtime: out of memory")
		}
		if p != v || err != 0 {
			throw("runtime: cannot remap pages in address space")
		}
		return
	}
}

func sysHugePageOS(v unsafe.Pointer, n uintptr) {
	if physHugePageSize != 0 {
		// Round v up to a huge page boundary.
		beg := alignUp(uintptr(v), physHugePageSize)
		// Round v+n down to a huge page boundary.
		end := alignDown(uintptr(v)+n, physHugePageSize)

		if beg < end {
			madvise(unsafe.Pointer(beg), end-beg, _MADV_HUGEPAGE)
		}
	}
}

func sysNoHugePageOS(v unsafe.Pointer, n uintptr) {
	if uintptr(v)&(physPageSize-1) != 0 {
		throw("unaligned sysNoHugePageOS")
	}
	if iswindows() {
		// No transparent huge pages on NT; nothing to do.
		return
	}
	madvise(v, n, _MADV_NOHUGEPAGE)
}

func sysHugePageCollapseOS(v unsafe.Pointer, n uintptr) {
	if uintptr(v)&(physPageSize-1) != 0 {
		throw("unaligned sysHugePageCollapseOS")
	}
	if physHugePageSize == 0 {
		return
	}
	madvise(v, n, _MADV_COLLAPSE)
}

// Don't split the stack as this function may be invoked without a valid G,
// which prevents us from allocating more stack.
//
//go:nosplit
func sysFreeOS(v unsafe.Pointer, n uintptr) {
	if iswindows() {
		if ntVirtualFree(v, 0, _NT_MEM_RELEASE) == 0 {
			*(*uintptr)(unsafe.Pointer(uintptr(0xf6))) = 0xf6
		}
		return
	}
	munmap(v, n)
}

func sysFaultOS(v unsafe.Pointer, n uintptr) {
	if iswindows() {
		// Decommit so any touch faults, like upstream mem_windows.go's sysFaultOS (which is sysUnusedOS there).
		ntDecommitPages(v, n)
		return
	}
	mprotect(v, n, _PROT_NONE)
	madvise(v, n, _MADV_DONTNEED)
}

func sysReserveOS(v unsafe.Pointer, n uintptr, vmaName string) unsafe.Pointer {
	if iswindows() {
		// v is a hint (arenaHints) or nil.
		if v != nil {
			if p := ntVirtualAlloc(v, n, _NT_MEM_RESERVE, _NT_PAGE_READWRITE); p != nil {
				return p
			}
		}
		return ntVirtualAlloc(nil, n, _NT_MEM_RESERVE, _NT_PAGE_READWRITE)
	}
	p, err := mmap(v, n, _PROT_NONE, _MAP_ANON|_MAP_PRIVATE, -1, 0)
	if err != 0 {
		return nil
	}
	return p
}

func sysMapOS(v unsafe.Pointer, n uintptr, vmaName string) {
	if iswindows() {
		// Commit the reservation in place.
		ntCommitPages(v, n)
		return
	}
	p, err := mmap(v, n, _PROT_READ|_PROT_WRITE, _MAP_ANON|_MAP_FIXED|_MAP_PRIVATE, -1, 0)
	if err == _ENOMEM {
		throw("runtime: out of memory")
	}
	if p != v || err != 0 {
		print("runtime: mmap(", v, ", ", n, ") returned ", p, ", ", err, "\n")
		throw("runtime: cannot map pages in arena address space")
	}

	// Disable huge pages if the GODEBUG for it is set.
	if debug.disablethp != 0 {
		sysNoHugePageOS(v, n)
	}
}

func needZeroAfterSysUnusedOS() bool {
	return debug.madvdontneed == 0
}
