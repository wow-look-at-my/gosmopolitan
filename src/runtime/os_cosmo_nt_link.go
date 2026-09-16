// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

package runtime

import (
	"internal/abi"
	"unsafe"
)

// Symbolic links, descriptor duplication and the read-only attribute on
// an NT host. See os_cosmo_nt_sys.go for the ntEmu* conventions.
//
// A symlink here is an NT symbolic link: a reparse point carrying the
// name-surrogate bit. NT distinguishes file and directory links at
// creation and a link of the wrong kind does not open, so symlinkat
// reads the target's kind as it exists now. A junction (mount point)
// carries the same bit and reads back as a symlink too, which is what
// upstream's windows port answers.

const (
	ntSysDup2      = 33
	ntSysSymlinkat = 266
	ntSysDup3      = 292

	ntEPERM = 1

	_NT_FILE_ATTRIBUTE_REPARSE_POINT = 0x400
	_NT_FILE_FLAG_OPEN_REPARSE_POINT = 0x00200000

	_NT_SYMBOLIC_LINK_FLAG_DIRECTORY                 = 0x1
	_NT_SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE = 0x2

	_NT_IO_REPARSE_TAG_MOUNT_POINT = 0xA0000003
	_NT_IO_REPARSE_TAG_SYMLINK     = 0xA000000C
	// A reparse point with this bit stands for another name. Symlinks
	// and junctions carry it; placeholders and deduplicated files do not.
	_NT_REPARSE_TAG_NAME_SURROGATE = 0x20000000
	_NT_SYMLINK_FLAG_RELATIVE      = 0x1

	_NT_FSCTL_GET_REPARSE_POINT          = 0x900A8
	_NT_MAXIMUM_REPARSE_DATA_BUFFER_SIZE = 16 * 1024
	_NT_FileAttributeTagInfo             = 9

	_NT_ERROR_ACCESS_DENIED       = 5
	_NT_ERROR_INVALID_PARAMETER   = 87
	_NT_ERROR_PRIVILEGE_NOT_HELD  = 1314
	_NT_ERROR_NOT_A_REPARSE_POINT = 4390

	_NT_DT_LNK = 10
)

// ntcallE8 is ntcallE for an eight-argument function (DeviceIoControl).
//
//go:nosplit
func ntcallE8(fn, a1, a2, a3, a4, a5, a6, a7, a8 uintptr) (r, lastErr uintptr) {
	args := ntcallArgs10{fn: fn, a1: a1, a2: a2, a3: a3, a4: a4, a5: a5, a6: a6, a7: a7, a8: a8}
	asmcgocall(unsafe.Pointer(abi.FuncPCABI0(ntcall10)), unsafe.Pointer(&args))
	return args.ret, uintptr(getg().m.ntLastError)
}

// ntFileAttributeTagInfo is FILE_ATTRIBUTE_TAG_INFO.
type ntFileAttributeTagInfo struct {
	attrs uint32
	tag   uint32
}

// ntHandleIsNameSurrogate reports whether h, opened without following
// reparse points, is a symlink or a junction.
func ntHandleIsNameSurrogate(h uintptr) bool {
	var info ntFileAttributeTagInfo
	r, _ := ntcallE(ntGetFileInformationByHandleExFn, h, _NT_FileAttributeTagInfo,
		uintptr(unsafe.Pointer(&info)), unsafe.Sizeof(info), 0, 0, 0)
	return r != 0 && ntIsNameSurrogate(info.attrs, info.tag)
}

// ntIsNameSurrogate is the attribute-and-tag test behind
// ntHandleIsNameSurrogate, for a caller that already holds both.
func ntIsNameSurrogate(attrs, tag uint32) bool {
	return attrs&_NT_FILE_ATTRIBUTE_REPARSE_POINT != 0 && tag&_NT_REPARSE_TAG_NAME_SURROGATE != 0
}

// ntWIsAbs reports whether a NUL-terminated Win32 path names a drive or
// a UNC share, the two spellings NT resolves without a directory.
func ntWIsAbs(w []uint16) bool {
	if len(w) >= 3 && w[1] == ':' {
		return true
	}
	return len(w) >= 3 && w[0] == '\\' && w[1] == '\\'
}

// ntSymlinkTargetIsDir reports whether the target a link is about to be
// given names a directory today. A relative target is read against the
// link's own directory, which is where NT and Linux both resolve it.
func ntSymlinkTargetIsDir(wlink, wtarget []uint16) bool {
	w := wtarget
	if !ntWIsAbs(wtarget) && wtarget[0] != '\\' {
		i := len(wlink) - 1 // the NUL
		for i > 0 && wlink[i-1] != '\\' {
			i--
		}
		w = make([]uint16, 0, i+len(wtarget))
		w = append(w, wlink[:i]...)
		w = append(w, wtarget...)
	}
	attrs, _ := ntcallE(ntGetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	KeepAlive(w)
	return uint32(attrs) != _NT_INVALID_FILE_ATTRIBUTES && uint32(attrs)&_NT_FILE_ATTRIBUTE_DIRECTORY != 0
}

// ntEmuSymlinkat creates newpath as a symbolic link to target.
//
// The link body is stored in NT spelling: an absolute target goes
// through ntPathW like every other path, so "/c/x" and "/tmp/x" name
// the file they name here, and a relative one only has its slashes
// flipped, because NT resolves it against the link's directory the way
// Linux does. ntReadlinkW undoes exactly this translation.
//
// CreateSymbolicLinkW refuses an unprivileged process unless developer
// mode is on, which ALLOW_UNPRIVILEGED_CREATE asks for. A host older
// than that flag answers ERROR_INVALID_PARAMETER and gets the call
// again without it. The call returns a BOOLEAN, so only the low byte
// of the result is the answer.
func ntEmuSymlinkat(oldp *byte, newdirfd int32, newp *byte) (r1, r2, errno uintptr) {
	if ntCreateSymbolicLinkWFn == 0 {
		return ntFail3(ntENOSYS)
	}
	target := ntCPath(oldp)
	if target == "" {
		return ntFail3(ntENOENT)
	}
	wlink, eno := ntAtPathW(newdirfd, ntCPath(newp))
	if eno != 0 {
		return ntFail3(eno)
	}
	var wtarget []uint16
	if target[0] == '/' || (len(target) >= 2 && ntIsAlpha(target[0]) && target[1] == ':') {
		wtarget = ntPathW(target)
	} else {
		wtarget = append(ntUTF16Append(make([]uint16, 0, len(target)+1), target, true), 0)
	}
	flags := uintptr(_NT_SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE)
	if ntSymlinkTargetIsDir(wlink, wtarget) {
		flags |= _NT_SYMBOLIC_LINK_FLAG_DIRECTORY
	}
	r, werr := ntcallE(ntCreateSymbolicLinkWFn, uintptr(unsafe.Pointer(&wlink[0])),
		uintptr(unsafe.Pointer(&wtarget[0])), flags, 0, 0, 0, 0)
	if uint8(r) == 0 && werr == _NT_ERROR_INVALID_PARAMETER {
		flags &^= _NT_SYMBOLIC_LINK_FLAG_ALLOW_UNPRIVILEGED_CREATE
		r, werr = ntcallE(ntCreateSymbolicLinkWFn, uintptr(unsafe.Pointer(&wlink[0])),
			uintptr(unsafe.Pointer(&wtarget[0])), flags, 0, 0, 0, 0)
	}
	KeepAlive(wlink)
	KeepAlive(wtarget)
	if uint8(r) == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ntReadlinkW reads the name the link at w stands for, in the Linux
// spelling symlinkat took it in. The substitute name is the one NT
// resolves, and it carries the \??\ prefix of the object namespace,
// which is stripped here; \??\UNC\server\share becomes \\server\share.
// A path that is not a symlink or a junction answers EINVAL, Linux's
// errno for readlink of an ordinary file.
func ntReadlinkW(w []uint16) (string, uintptr) {
	if ntDeviceIoControlFn == 0 {
		return "", ntENOSYS
	}
	h, werr := ntcallE(ntCreateFileWFn, uintptr(unsafe.Pointer(&w[0])), _NT_FILE_READ_ATTRIBUTES,
		_NT_FILE_SHARE_ALL, 0, _NT_OPEN_EXISTING,
		_NT_FILE_FLAG_BACKUP_SEMANTICS|_NT_FILE_FLAG_OPEN_REPARSE_POINT, 0)
	KeepAlive(w)
	if h == _NT_INVALID_HANDLE_VALUE {
		return "", ntErrno(werr)
	}
	buf := make([]byte, _NT_MAXIMUM_REPARSE_DATA_BUFFER_SIZE)
	var got uint32
	r, werr := ntcallE8(ntDeviceIoControlFn, h, _NT_FSCTL_GET_REPARSE_POINT, 0, 0,
		uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&got)), 0)
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
	if r == 0 {
		if werr == _NT_ERROR_NOT_A_REPARSE_POINT {
			return "", ntEINVAL
		}
		return "", ntErrno(werr)
	}
	if got < 8 {
		return "", ntEINVAL
	}
	// REPARSE_DATA_BUFFER: ReparseTag, ReparseDataLength, Reserved, then
	// the tag's own layout. Both name-carrying layouts open with the
	// substitute and print name offsets and lengths, in bytes, into a
	// path buffer that follows a Flags word for a symlink and nothing for
	// a junction.
	tag := *(*uint32)(unsafe.Pointer(&buf[0]))
	subOff := uintptr(*(*uint16)(unsafe.Pointer(&buf[8])))
	subLen := uintptr(*(*uint16)(unsafe.Pointer(&buf[10])))
	var pathBuf uintptr
	relative := false
	switch tag {
	case _NT_IO_REPARSE_TAG_SYMLINK:
		relative = *(*uint32)(unsafe.Pointer(&buf[16]))&_NT_SYMLINK_FLAG_RELATIVE != 0
		pathBuf = 20
	case _NT_IO_REPARSE_TAG_MOUNT_POINT:
		pathBuf = 16
	default:
		return "", ntEINVAL
	}
	start := pathBuf + subOff
	if start+subLen > uintptr(got) || subLen%2 != 0 {
		return "", ntEINVAL
	}
	name := unsafe.Slice((*uint16)(unsafe.Pointer(&buf[start])), subLen/2)
	if relative {
		return ntUTF16ToString(ntFlipToSlash(name)), 0
	}
	if len(name) >= 4 && name[0] == '\\' && name[1] == '?' && name[2] == '?' && name[3] == '\\' {
		name = name[4:]
		if len(name) >= 4 && name[0] == 'U' && name[1] == 'N' && name[2] == 'C' && name[3] == '\\' {
			name = append([]uint16{'\\'}, name[3:]...)
		}
	}
	return ntPathToLinux(name), 0
}

// ntFlipToSlash returns a copy of w with every backslash a slash.
func ntFlipToSlash(w []uint16) []uint16 {
	out := make([]uint16, len(w))
	for i, c := range w {
		if c == '\\' {
			c = '/'
		}
		out[i] = c
	}
	return out
}

// ntClearReadonly drops the READONLY attribute from w and reports
// whether it did. The attribute is NT's refusal to modify a file, which
// covers deleting it; Linux keeps that decision in the directory.
func ntClearReadonly(w []uint16) bool {
	if ntSetFileAttributesWFn == 0 {
		return false
	}
	attrs, _ := ntcallE(ntGetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	if uint32(attrs) == _NT_INVALID_FILE_ATTRIBUTES || uint32(attrs)&_NT_FILE_ATTRIBUTE_READONLY == 0 {
		KeepAlive(w)
		return false
	}
	r, _ := ntcallE(ntSetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])),
		attrs&^_NT_FILE_ATTRIBUTE_READONLY, 0, 0, 0, 0, 0)
	KeepAlive(w)
	return r != 0
}

// ntChmodW carries the one permission bit NT has: the owner's write
// bit, as the READONLY attribute. Every other bit is synthetic (see
// ntStatFromInfo), so a mode that only changes those is already
// applied.
func ntChmodW(w []uint16, mode uint32) uintptr {
	attrs, werr := ntcallE(ntGetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])), 0, 0, 0, 0, 0, 0)
	if uint32(attrs) == _NT_INVALID_FILE_ATTRIBUTES {
		KeepAlive(w)
		return ntErrno(werr)
	}
	want := attrs
	if mode&0o200 == 0 {
		want |= _NT_FILE_ATTRIBUTE_READONLY
	} else {
		want &^= _NT_FILE_ATTRIBUTE_READONLY
	}
	if want == attrs {
		KeepAlive(w)
		return 0
	}
	if ntSetFileAttributesWFn == 0 {
		return ntENOSYS
	}
	r, werr := ntcallE(ntSetFileAttributesWFn, uintptr(unsafe.Pointer(&w[0])), want, 0, 0, 0, 0, 0)
	KeepAlive(w)
	if r == 0 {
		return ntErrno(werr)
	}
	return 0
}

// ntDupHandle duplicates a slot's handle into this process. A socket is
// a real kernel file handle, and a same-process duplicate names the
// same object with an independent lifetime, which is dup(2)'s
// contract. MSDN's warning against DuplicateHandle on sockets concerns
// non-IFS layered providers, which msafd and afunix are not.
func ntDupHandle(h uintptr) (uintptr, uintptr) {
	var nh uintptr
	r, werr := ntcallE(ntDuplicateHandleFn,
		_NT_CURRENT_PROCESS, h, _NT_CURRENT_PROCESS,
		uintptr(unsafe.Pointer(&nh)),
		0, // dwDesiredAccess (ignored with SAME_ACCESS)
		0, // bInheritHandle = FALSE
		_NT_DUPLICATE_SAME_ACCESS)
	if r == 0 {
		return 0, ntErrno(werr)
	}
	return nh, 0
}

// ntCloseKind closes a handle the way its fd kind requires: closesocket
// for a socket, because CloseHandle would leak the winsock provider
// state behind the SOCKET.
func ntCloseKind(h uintptr, kind ntFDKind) {
	if kind == ntFDSocket {
		ntcall(ntWSACloseSocketFn, h, 0, 0, 0, 0, 0)
		return
	}
	ntcall(ntCloseHandleFn, h, 0, 0, 0, 0, 0)
}

// ntDupEntry is the slot a duplicate of e gets: the same kind, flags,
// path and socket identity over a fresh handle. The nonblocking mode
// travels with the handle, because FIONBIO is socket-object state, and
// the enumeration cursor of a directory travels with it too, the way
// Linux's shared open file description carries the offset. Entries the
// original fd fetched but has not delivered stay with it. Close-on-exec
// is the caller's, per POSIX.
func ntDupEntry(e ntFDEntry, nh uintptr, cloexec bool) ntFDEntry {
	e.handle = nh
	e.cloexec = cloexec
	e.pending = nil
	return e
}

// ntEmuDup implements dup(2) for every fd kind, into the lowest free
// slot.
func ntEmuDup(fd int32) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	nh, eno := ntDupHandle(e.handle)
	if eno != 0 {
		return ntFail3(eno)
	}
	nfd := ntFDAllocEntry(ntDupEntry(e, nh, false))
	if nfd < 0 {
		ntCloseKind(nh, e.kind)
		return ntFail3(uintptr(-nfd))
	}
	return uintptr(nfd), 0, 0
}

// ntEmuDup3 implements dup3(2), and dup2(2) through it: the duplicate
// lands in newfd, closing whatever was there, in one step under the
// table lock so no other opener can take the slot in between.
func ntEmuDup3(oldfd, newfd, flags int32) (r1, r2, errno uintptr) {
	if flags&^int32(_NT_O_CLOEXEC) != 0 || oldfd == newfd {
		return ntFail3(ntEINVAL)
	}
	if newfd < 0 || newfd >= ntFDMax {
		return ntFail3(ntEBADF)
	}
	e, ok := ntFDLookup(oldfd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	nh, eno := ntDupHandle(e.handle)
	if eno != 0 {
		return ntFail3(eno)
	}
	old, oldKind, hadOld := ntFDReplace(newfd, ntDupEntry(e, nh, flags&_NT_O_CLOEXEC != 0))
	if hadOld {
		ntCloseKind(old, oldKind)
	}
	return uintptr(newfd), 0, 0
}

// ntEmuDup2 implements dup2(2). Unlike dup3, the same fd twice is a
// no-op that answers the fd.
func ntEmuDup2(oldfd, newfd int32) (r1, r2, errno uintptr) {
	if oldfd == newfd {
		if _, ok := ntFDLookup(oldfd); !ok {
			return ntFail3(ntEBADF)
		}
		return uintptr(newfd), 0, 0
	}
	return ntEmuDup3(oldfd, newfd, 0)
}
