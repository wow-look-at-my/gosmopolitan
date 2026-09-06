// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The NT file-descriptor table.
//
// Unix code deals in small integer fds with lowest-free allocation.
// Win32 deals in HANDLEs. This side table maps one to the other for
// everything the syscall emulation opens.
//
// ntFDLock guards slot claim, release, lookup and state updates, and
// nothing allocates while it is held. A lookup returns a COPY, so an
// operation racing a concurrent close of the same fd sees either the
// old handle - the close wins the CloseHandle and the operation fails
// as on Linux - or EBADF. That is the use-after-close semantics unix
// code already lives with.

package runtime

import "unsafe"

// The table is a fixed ntFDMax slots, a few KiB of BSS, so no lookup
// path allocates - the runtime's own fcntl is nosplit - and a full
// table is EMFILE. Slots 0, 1 and 2 are seeded from GetStdHandle at
// boot and are ALWAYS marked open even when the handle is null. A dead
// handle then fails per-operation with EBADF, and keeping the slot
// "open" stops checkfds opening /dev/null through the runtime's raw
// syscall open, which must never run on NT.
const ntFDMax = 512

type ntFDKind uint8

const (
	ntFDFree   ntFDKind = iota
	ntFDFile            // seekable disk file
	ntFDDir             // directory (backup-semantics handle)
	ntFDStdio           // console, inherited pipe, or character device (not seekable)
	ntFDPipe            // anonymous pipe end created by pipe2 (CreatePipe)
	ntFDSocket          // winsock socket; handle holds the SOCKET value
)

// ntDirEnt is one parsed directory entry awaiting delivery to a
// getdents64 buffer.
type ntDirEnt struct {
	ino  uint64
	typ  byte // Linux DT_*
	name string
}

type ntFDEntry struct {
	handle     uintptr
	kind       ntFDKind
	ftype      uint8 // Win32 GetFileType result (stdio fds): pipe vs char
	cloexec    bool
	dirStarted bool       // false selects the RestartInfo class on the next query
	flags      int32      // Linux O_* access mode and status flags
	pathW      []uint16   // absolute Win32 path, for dirfd-relative joins
	pending    []ntDirEnt // parsed getdents64 entries the caller's buffer could not hold

	// Socket-kind state. sockFam holds the LINUX address family.
	// unixBound and unixPeer record the Linux-spelling AF_UNIX pathname
	// the caller bound or connected to, because winsock stores the
	// TRANSLATED Windows path and translating it back would surface the
	// /c/... alias. sockPair marks a socketpair end, which winsock knows
	// as a loopback TCP socket, so a name query synthesizes the Linux
	// truth instead of asking. sockPeerPid caches the
	// SIO_AF_UNIX_GETPEERPID answer, final once taken because a
	// connection's peer can never change; 0 means not queried, and pid
	// 0 is the NT idle process, which can never own a socket.
	sockFam     uint16
	sockPair    bool
	sockPeerPid uint32
	unixBound   string
	unixPeer    string
}

var (
	ntFDLock  mutex
	ntFDTable [ntFDMax]ntFDEntry
)

// ntFDAlloc claims the lowest free slot (unix semantics) for the
// given handle and returns the fd, or -EMFILE when the table is full.
// The caller allocated pathW beforehand; nothing allocates under the
// lock.
func ntFDAlloc(handle uintptr, kind ntFDKind, flags int32, cloexec bool, pathW []uint16) int32 {
	lock(&ntFDLock)
	for fd := int32(0); fd < ntFDMax; fd++ {
		e := &ntFDTable[fd]
		if e.kind == ntFDFree {
			*e = ntFDEntry{
				handle:  handle,
				kind:    kind,
				flags:   flags,
				cloexec: cloexec,
				pathW:   pathW,
			}
			unlock(&ntFDLock)
			return fd
		}
	}
	unlock(&ntFDLock)
	return -24 // EMFILE
}

// ntFDLookup returns a copy of the fd's entry.
//
//go:nosplit
func ntFDLookup(fd int32) (ntFDEntry, bool) {
	if fd < 0 || fd >= ntFDMax {
		return ntFDEntry{}, false
	}
	lock(&ntFDLock)
	e := ntFDTable[fd]
	unlock(&ntFDLock)
	if e.kind == ntFDFree {
		return ntFDEntry{}, false
	}
	return e, true
}

// ntFDRelease frees the slot and returns the handle and its kind for
// the caller to close (outside the lock; sockets need closesocket, not
// CloseHandle).
func ntFDRelease(fd int32) (handle uintptr, kind ntFDKind, ok bool) {
	if fd < 0 || fd >= ntFDMax {
		return 0, ntFDFree, false
	}
	lock(&ntFDLock)
	e := &ntFDTable[fd]
	if e.kind == ntFDFree {
		unlock(&ntFDLock)
		return 0, ntFDFree, false
	}
	handle = e.handle
	kind = e.kind
	*e = ntFDEntry{}
	unlock(&ntFDLock)
	return handle, kind, true
}

// ntFDSetSockFam records the Linux address family of a socket fd.
func ntFDSetSockFam(fd int32, fam uint16) {
	if fd < 0 || fd >= ntFDMax {
		return
	}
	lock(&ntFDLock)
	if ntFDTable[fd].kind == ntFDSocket {
		ntFDTable[fd].sockFam = fam
	}
	unlock(&ntFDLock)
}

// ntFDSetSockPair marks a socket fd as a socketpair end (see the
// sockPair field comment).
func ntFDSetSockPair(fd int32) {
	if fd < 0 || fd >= ntFDMax {
		return
	}
	lock(&ntFDLock)
	if ntFDTable[fd].kind == ntFDSocket {
		ntFDTable[fd].sockPair = true
	}
	unlock(&ntFDLock)
}

// ntFDSetSockPeerPid caches the connected AF_UNIX peer's pid (see the
// sockPeerPid field comment).
func ntFDSetSockPeerPid(fd int32, pid uint32) {
	if fd < 0 || fd >= ntFDMax {
		return
	}
	lock(&ntFDLock)
	if ntFDTable[fd].kind == ntFDSocket {
		ntFDTable[fd].sockPeerPid = pid
	}
	unlock(&ntFDLock)
}

// ntFDSetUnixName records the Linux-spelling pathname an AF_UNIX
// socket was bound (bound=true) or connected (bound=false) to.
func ntFDSetUnixName(fd int32, name string, bound bool) {
	if fd < 0 || fd >= ntFDMax {
		return
	}
	lock(&ntFDLock)
	if ntFDTable[fd].kind == ntFDSocket {
		if bound {
			ntFDTable[fd].unixBound = name
		} else {
			ntFDTable[fd].unixPeer = name
		}
	}
	unlock(&ntFDLock)
}

// ntFDSetFtype records the Win32 GetFileType classification of a
// stdio-kind fd (pipe vs character device), for fstat synthesis.
func ntFDSetFtype(fd int32, ftype uint8) {
	if fd < 0 || fd >= ntFDMax {
		return
	}
	lock(&ntFDLock)
	if ntFDTable[fd].kind != ntFDFree {
		ntFDTable[fd].ftype = ftype
	}
	unlock(&ntFDLock)
}

// ntFDSetDirState stores the getdents64 enumeration state. The
// pending slice was built outside the lock.
func ntFDSetDirState(fd int32, started bool, pending []ntDirEnt) {
	if fd < 0 || fd >= ntFDMax {
		return
	}
	lock(&ntFDLock)
	e := &ntFDTable[fd]
	if e.kind == ntFDDir {
		e.dirStarted = started
		e.pending = pending
	}
	unlock(&ntFDLock)
}

// ntFcntl backs both the runtime's fcntl (fcntl_cosmo_amd64.go,
// nosplit) and the emulated SYS_FCNTL. Only the commands the
// unix-shaped standard library actually issues against files are
// implemented; the rest report ENOSYS loudly.
//
//go:nosplit
func ntFcntl(fd, cmd, arg int32) (ret int32, errno int32) {
	const (
		_F_DUPFD = 0
		_F_GETFD = 1
		_F_SETFD = 2
		_F_GETFL = 3
		_F_SETFL = 4

		_FD_CLOEXEC = 1
		// Linux O_* status bits settable via F_SETFL that we track.
		_O_STATUS = 0x400 | 0x800 // O_APPEND | O_NONBLOCK
	)
	if fd < 0 || fd >= ntFDMax {
		return -1, 9 // EBADF
	}
	// Socket F_SETFL must push the O_NONBLOCK change into winsock
	// (ioctlsocket FIONBIO) after the table update; captured under the
	// lock, issued outside it (use-after-close races have the usual
	// unix semantics: the ioctl fails on a dead SOCKET).
	var nbHandle uintptr
	var nbWord uint32
	syncNB := false
	lock(&ntFDLock)
	e := &ntFDTable[fd]
	if e.kind == ntFDFree {
		unlock(&ntFDLock)
		return -1, 9 // EBADF
	}
	switch cmd {
	case _F_GETFD:
		ret = 0
		if e.cloexec {
			ret = _FD_CLOEXEC
		}
	case _F_SETFD:
		e.cloexec = arg&_FD_CLOEXEC != 0
	case _F_GETFL:
		ret = e.flags
	case _F_SETFL:
		// O_APPEND cannot be turned on retroactively (the handle
		// lacks FILE_APPEND_DATA); O_NONBLOCK on files is a no-op on
		// Linux too. Record the bits so F_GETFL round-trips.
		const _O_NONBLOCK = 0x800
		old := e.flags
		e.flags = e.flags&^int32(_O_STATUS) | arg&int32(_O_STATUS)
		if e.kind == ntFDSocket && (old^e.flags)&_O_NONBLOCK != 0 {
			syncNB = true
			nbHandle = e.handle
			if e.flags&_O_NONBLOCK != 0 {
				nbWord = 1
			}
		}
	default:
		unlock(&ntFDLock)
		return -1, 38 // ENOSYS
	}
	unlock(&ntFDLock)
	if syncNB && ntWSAIoctlsocketFn != 0 {
		ntcall(ntWSAIoctlsocketFn, nbHandle, _NT_FIONBIO, uintptr(unsafe.Pointer(&nbWord)), 0, 0, 0)
	}
	return ret, 0
}
