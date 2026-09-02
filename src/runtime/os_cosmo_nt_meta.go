// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import "unsafe"

// File-metadata syscalls on an NT host: utimensat, truncate, fchdir and
// linkat. Each backs an os function programs reach constantly -
// os.Chtimes, os.Truncate, os.File.Chdir, os.Link - and each was ENOSYS
// here until this wave. See os_cosmo_nt_sys.go for the ntEmu*
// conventions this file follows.
//
// Every Win32 entry these need is resolved optionally: a missing export
// leaves the pointer zero and the syscall answers ENOSYS, rather than
// bricking the boot over a call most programs never make.

const (
	// FILE_WRITE_ATTRIBUTES: enough to set timestamps, and it does not
	// conflict with another writer the way GENERIC_WRITE does.
	_NT_FILE_WRITE_ATTRIBUTES = 0x0100

	// GetFinalPathNameByHandleW's VOLUME_NAME_DOS form, which returns
	// the path behind a \\?\ prefix.
	_NT_VOLUME_NAME_DOS = 0x0

	// AT_SYMLINK_NOFOLLOW as the syscall package passes it.
	_NT_AT_SYMLINK_NOFOLLOW = 0x100

	// utimensat's nanosecond sentinels, Linux numbering.
	_NT_UTIME_NOW  = 0x3fffffff
	_NT_UTIME_OMIT = 0x3ffffffe

	// 1601-01-01 to 1970-01-01 in 100ns units, the FILETIME epoch shift.
	_NT_FILETIME_EPOCH_DELTA = 116444736000000000
)

// ntFiletime is a FILETIME: 100ns ticks since 1601-01-01, which Win32
// passes as two DWORDs in one 64-bit slot.
type ntFiletime struct {
	lo uint32
	hi uint32
}

// ntTimespecToFiletime converts a unix timespec to a FILETIME. A
// timestamp before the FILETIME epoch cannot be represented; NT clamps
// it to the epoch rather than wrapping into a far-future date.
func ntTimespecToFiletime(ts ntLinuxTimespec) ntFiletime {
	t := ts.sec*1e7 + ts.nsec/100 + _NT_FILETIME_EPOCH_DELTA
	if t < 0 {
		t = 0
	}
	return ntFiletime{lo: uint32(uint64(t)), hi: uint32(uint64(t) >> 32)}
}

// ntNowFiletime reads the current time in FILETIME form, for the
// UTIME_NOW sentinel.
func ntNowFiletime() (ntFiletime, bool) {
	if ntGetSystemTimeAsFileTimeFn == 0 {
		return ntFiletime{}, false
	}
	var ft ntFiletime
	ntcall(ntGetSystemTimeAsFileTimeFn, uintptr(unsafe.Pointer(&ft)), 0, 0, 0, 0, 0)
	return ft, true
}

// ntEmuUtimensat sets a file's access and modification times with
// SetFileTime, which needs a handle rather than a path.
//
// The sentinels translate to Win32's own convention rather than to a
// value: SetFileTime leaves a stamp alone when its pointer is NULL,
// which is exactly UTIME_OMIT, and UTIME_NOW is filled from the system
// clock. A nil times array means "both now" on Linux.
//
// AT_SYMLINK_NOFOLLOW is accepted and cannot change the outcome: this
// port resolves no symlinks (ntEmuReadlinkat answers EINVAL for
// everything but /proc/self/exe), so there is no link to decline to
// follow.
func ntEmuUtimensat(dirfd int32, cpath *byte, times *[2]ntLinuxTimespec, flags int32) (r1, r2, errno uintptr) {
	if ntSetFileTimeFn == 0 {
		return ntFail3(ntENOSYS)
	}
	if flags&^int32(_NT_AT_SYMLINK_NOFOLLOW) != 0 {
		return ntFail3(ntEINVAL)
	}
	w, eno := ntAtPathW(dirfd, ntCPath(cpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), _NT_FILE_WRITE_ATTRIBUTES,
		_NT_FILE_SHARE_ALL, 0, _NT_OPEN_EXISTING, _NT_FILE_FLAG_BACKUP_SEMANTICS, 0)
	KeepAlive(w)
	if h == _NT_INVALID_HANDLE_VALUE {
		return ntFail3(ntErrno(werr))
	}

	var atime, mtime ntFiletime
	aptr, mptr := uintptr(0), uintptr(0)
	set := func(ts ntLinuxTimespec, dst *ntFiletime) uintptr {
		switch ts.nsec {
		case _NT_UTIME_OMIT:
			return 0 // NULL: SetFileTime leaves this stamp alone
		case _NT_UTIME_NOW:
			now, ok := ntNowFiletime()
			if !ok {
				return 0
			}
			*dst = now
		default:
			*dst = ntTimespecToFiletime(ts)
		}
		return uintptr(unsafe.Pointer(dst))
	}
	if times == nil {
		now, ok := ntNowFiletime()
		if !ok {
			ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
			return ntFail3(ntENOSYS)
		}
		atime, mtime = now, now
		aptr = uintptr(unsafe.Pointer(&atime))
		mptr = uintptr(unsafe.Pointer(&mtime))
	} else {
		aptr = set(times[0], &atime)
		mptr = set(times[1], &mtime)
	}

	// The creation time (first pointer) stays NULL: Linux utimensat has
	// no such stamp to carry, and stat reports it as ctime.
	r, werr2 := ntcallE(ntSetFileTimeFn, h, 0, aptr, mptr, 0, 0, 0)
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	if r == 0 {
		return ntFail3(ntErrno(werr2))
	}
	return 0, 0, 0
}

// ntEmuTruncate is the path form of ftruncate. NT can only resize
// through a handle, so this opens one, reuses the same seek-and-
// SetEndOfFile sequence, and closes it.
func ntEmuTruncate(cpath *byte, length int64) (r1, r2, errno uintptr) {
	if length < 0 {
		return ntFail3(ntEINVAL)
	}
	w := ntPathW(ntCPath(cpath))
	if w == nil {
		return ntFail3(ntENOENT)
	}
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), _NT_GENERIC_WRITE,
		_NT_FILE_SHARE_ALL, 0, _NT_OPEN_EXISTING, 0, 0)
	KeepAlive(w)
	if h == _NT_INVALID_HANDLE_VALUE {
		return ntFail3(ntErrno(werr))
	}
	_, werr = ntSeekHandle(h, length, _NT_FILE_BEGIN)
	if werr != 0 {
		ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
		return ntFail3(ntErrno(werr))
	}
	r, werr2 := ntcallE(ntSetEndOfFileFn, h, 0, 0, 0, 0, 0, 0)
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	if r == 0 {
		return ntFail3(ntErrno(werr2))
	}
	return 0, 0, 0
}

// ntHandlePathW recovers a handle's path as a wide string.
// GetFinalPathNameByHandleW answers in \\?\ form, which
// SetCurrentDirectoryW does not accept, so the prefix is dropped when
// it is the plain drive form (a \\?\UNC\ path is left alone - trimming
// it would produce a path that names something else).
func ntHandlePathW(h uintptr) ([]uint16, uintptr) {
	if ntGetFinalPathNameByHandleWFn == 0 {
		return nil, ntENOSYS
	}
	buf := make([]uint16, 4096)
	n, werr := ntcallE(ntGetFinalPathNameByHandleWFn, h, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)-1), _NT_VOLUME_NAME_DOS, 0, 0, 0)
	if n == 0 || n >= uintptr(len(buf)) {
		return nil, ntErrno(werr)
	}
	p := buf[:n]
	if len(p) > 6 && p[0] == '\\' && p[1] == '\\' && p[2] == '?' && p[3] == '\\' &&
		p[5] == ':' {
		p = p[4:]
	}
	// SetCurrentDirectoryW needs a NUL-terminated string, and the slice
	// above stops at the length the call reported.
	out := make([]uint16, len(p)+1)
	copy(out, p)
	return out, 0
}

// ntEmuFchdir changes the working directory to the one a descriptor
// names. NT has no handle-relative equivalent, so the handle is
// resolved back to a path first.
func ntEmuFchdir(fd int32) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDDir {
		return ntFail3(ntENOTDIR)
	}
	w, eno := ntHandlePathW(e.handle)
	if eno != 0 {
		return ntFail3(eno)
	}
	r, werr := ntcallE(ntSetCurrentDirectoryWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ntEmuLinkat creates a hard link. CreateHardLinkW takes the new name
// first, the reverse of linkat.
//
// AT_SYMLINK_FOLLOW is accepted and cannot change the outcome, for the
// same reason as in ntEmuUtimensat: this port resolves no symlinks.
// Hard links need both paths on one NTFS volume; CreateHardLinkW
// reports the cross-volume case itself, and ntErrno maps it to EXDEV.
func ntEmuLinkat(olddirfd int32, oldpath *byte, newdirfd int32, newpath *byte, flags int32) (r1, r2, errno uintptr) {
	if ntCreateHardLinkWFn == 0 {
		return ntFail3(ntENOSYS)
	}
	oldw, eno := ntAtPathW(olddirfd, ntCPath(oldpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	neww, eno := ntAtPathW(newdirfd, ntCPath(newpath))
	if eno != 0 {
		return ntFail3(eno)
	}
	r, werr := ntcallE(ntCreateHardLinkWFn, uintptr(unsafe.Pointer(&neww[0])),
		uintptr(unsafe.Pointer(&oldw[0])), 0, 0, 0, 0, 0)
	KeepAlive(oldw)
	KeepAlive(neww)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}
