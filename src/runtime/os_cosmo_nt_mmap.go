// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// mmap and its neighbours over the Win32 section API.
//
// An anonymous mapping is VirtualAlloc, which is what the runtime's own
// allocator already uses. A file-backed one is a section: CreateFileMappingW
// over the slot's HANDLE, then MapViewOfFile. The section handle closes
// immediately, because the view keeps it alive.
//
// munmap tells the two apart with VirtualQuery rather than a side table, so
// nothing here has a capacity to run out of. MEM_MAPPED is a view and
// MEM_PRIVATE is an allocation.

package runtime

import "unsafe"

const (
	_NT_PAGE_NOACCESS          = 0x01
	_NT_PAGE_READONLY          = 0x02
	_NT_PAGE_WRITECOPY         = 0x08
	_NT_PAGE_EXECUTE_READ      = 0x20
	_NT_PAGE_EXECUTE_READWRITE = 0x40
	_NT_PAGE_EXECUTE_WRITECOPY = 0x80

	_NT_FILE_MAP_COPY    = 0x0001
	_NT_FILE_MAP_WRITE   = 0x0002
	_NT_FILE_MAP_READ    = 0x0004
	_NT_FILE_MAP_EXECUTE = 0x0020

	_NT_MEM_PRIVATE = 0x20000
	_NT_MEM_MAPPED  = 0x40000

	// MapViewOfFile takes an offset in units of the allocation
	// granularity, which is coarser than the page size mmap asks for.
	_NT_ALLOC_GRANULARITY = 64 << 10
)

// ntMemoryBasicInfo is MEMORY_BASIC_INFORMATION.
type ntMemoryBasicInfo struct {
	baseAddress       uintptr
	allocationBase    uintptr
	allocationProtect uint32
	partitionId       uint16
	_                 uint16
	regionSize        uintptr
	state             uint32
	protect           uint32
	typ               uint32
	_                 uint32
}

// ntMmapProt turns the Linux prot word into the page protection a section
// takes. An unreadable mapping is PAGE_NOACCESS, which is what PROT_NONE
// asks for.
func ntMmapProt(prot uintptr) uint32 {
	exec := prot&_PROT_EXEC != 0
	write := prot&_PROT_WRITE != 0
	switch {
	case exec && write:
		return _NT_PAGE_EXECUTE_READWRITE
	case exec:
		return _NT_PAGE_EXECUTE_READ
	case write:
		return _NT_PAGE_READWRITE
	case prot&_PROT_READ != 0:
		return _NT_PAGE_READONLY
	}
	return _NT_PAGE_NOACCESS
}

// ntEmuMmap backs SYS_MMAP.
func ntEmuMmap(addr, length, prot, flags uintptr, fd int32, offset int64) (r1, r2, errno uintptr) {
	if length == 0 {
		return ntFail3(ntEINVAL)
	}
	if flags&_MAP_ANON != 0 {
		// VirtualAlloc rounds the length up to a page itself, and a
		// zero address lets NT choose. MAP_FIXED is the caller naming
		// the address, which VirtualAlloc also takes.
		p := ntVirtualAlloc(unsafe.Pointer(addr), length,
			_NT_MEM_RESERVE|_NT_MEM_COMMIT, uintptr(ntMmapProt(prot)))
		if p == nil {
			return ntFail3(ntENOMEM)
		}
		return uintptr(p), 0, 0
	}

	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDFile {
		// A section needs a real file behind it: a pipe or a socket
		// has no bytes at an offset.
		return ntFail3(ntEACCES)
	}
	if offset < 0 || offset%_NT_ALLOC_GRANULARITY != 0 {
		// Linux asks for a page-aligned offset and NT asks for a
		// granularity-aligned one. Refusing by name beats mapping the
		// wrong bytes.
		return ntFail3(ntEINVAL)
	}

	// A private mapping is copy-on-write, so the section itself has to
	// allow the write even though the file need not.
	sectionProt := ntMmapProt(prot)
	access := uintptr(0)
	if flags&_MAP_PRIVATE != 0 && prot&_PROT_WRITE != 0 {
		if prot&_PROT_EXEC != 0 {
			sectionProt = _NT_PAGE_EXECUTE_WRITECOPY
		} else {
			sectionProt = _NT_PAGE_WRITECOPY
		}
		access = _NT_FILE_MAP_COPY
	} else {
		if prot&_PROT_READ != 0 {
			access |= _NT_FILE_MAP_READ
		}
		if prot&_PROT_WRITE != 0 {
			access |= _NT_FILE_MAP_WRITE
		}
	}
	if prot&_PROT_EXEC != 0 {
		access |= _NT_FILE_MAP_EXECUTE
	}

	// A zero size maps the whole file from the offset, which is what a
	// caller asking for more than the file holds would get on Linux.
	// Name the end explicitly instead, so a short file fails here.
	end := uint64(offset) + uint64(length)
	h, werr := ntcallE(ntCreateFileMappingWFn, e.handle, 0, uintptr(sectionProt),
		uintptr(end>>32), uintptr(uint32(end)), 0, 0)
	if h == 0 {
		return ntFail3(ntErrno(werr))
	}
	v, werr := ntcallE(ntMapViewOfFileFn, h, access,
		uintptr(uint64(offset)>>32), uintptr(uint32(offset)), length, 0, 0)
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0) // the view holds the section
	if v == 0 {
		return ntFail3(ntErrno(werr))
	}
	return v, 0, 0
}

// ntEmuMunmap backs SYS_MUNMAP.
func ntEmuMunmap(addr, length uintptr) (r1, r2, errno uintptr) {
	if addr == 0 || length == 0 {
		return ntFail3(ntEINVAL)
	}
	var mbi ntMemoryBasicInfo
	if ntcall(ntVirtualQueryFn, addr, uintptr(unsafe.Pointer(&mbi)),
		unsafe.Sizeof(mbi), 0, 0, 0) == 0 {
		return ntFail3(ntEINVAL)
	}
	if mbi.allocationBase != addr {
		// NT releases a whole allocation or nothing, so a partial
		// unmap cannot be served. Say so rather than releasing more
		// than the caller asked to release.
		return ntFail3(ntEINVAL)
	}
	switch mbi.typ {
	case _NT_MEM_MAPPED:
		if ntcall(ntUnmapViewOfFileFn, addr, 0, 0, 0, 0, 0) == 0 {
			return ntFail3(ntEINVAL)
		}
	case _NT_MEM_PRIVATE:
		if ntVirtualFree(unsafe.Pointer(addr), 0, _NT_MEM_RELEASE) == 0 {
			return ntFail3(ntEINVAL)
		}
	default:
		return ntFail3(ntEINVAL)
	}
	return 0, 0, 0
}

// ntEmuMsync backs SYS_MSYNC. FlushViewOfFile writes the dirty pages of a
// view back; an anonymous mapping has nothing to write and succeeds.
func ntEmuMsync(addr, length uintptr) (r1, r2, errno uintptr) {
	if addr == 0 {
		return ntFail3(ntEINVAL)
	}
	if ntFlushViewOfFileFn == 0 {
		return ntFail3(ntENOSYS)
	}
	if r, werr := ntcallE(ntFlushViewOfFileFn, addr, length, 0, 0, 0, 0, 0); r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ntEmuMlock backs SYS_MLOCK and SYS_MUNLOCK.
func ntEmuMlock(addr, length uintptr, lock bool) (r1, r2, errno uintptr) {
	fn := ntVirtualUnlockFn
	if lock {
		fn = ntVirtualLockFn
	}
	if fn == 0 {
		return ntFail3(ntENOSYS)
	}
	if r, werr := ntcallE(fn, addr, length, 0, 0, 0, 0, 0); r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}
