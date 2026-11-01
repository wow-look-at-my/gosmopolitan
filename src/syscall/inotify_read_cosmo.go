// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// What an instance's descriptor does that a socket's does not: a read returns
// whole records, and refuses a buffer too small for the next one.

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"sync/atomic"
	"unsafe"
)

const inotifyFIONREAD = 0x541b

// inotifySettleNanos is how long a reader waits for the rest of a record a stream socket has shown part of.
const inotifySettleNanos = 1000000

// inotifyFd reports whether fd is a live instance.
//
//go:nosplit
func inotifyFd(fd uintptr) bool {
	list := (*[]int32)(atomic.LoadPointer(&inotifyFds))
	if list == nil {
		return false
	}
	for _, known := range *list {
		if uintptr(known) == fd {
			return true
		}
	}
	return false
}

// inotifyFdTrap reports whether a syscall works on a live instance in a way
// that differs from its socket. FIONREAD differs only on NT, which has no ioctl
// for a socket.
//
//go:nosplit
func inotifyFdTrap(trap, a1, a2 uintptr) bool {
	switch trap {
	case SYS_READ, SYS_CLOSE:
		return inotifyFd(a1)
	case SYS_IOCTL:
		return a2 == inotifyFIONREAD && cosmo.Windows() != nil && inotifyFd(a1)
	}
	return false
}

// inotifyFdSyscall serves a read or a FIONREAD. The pointer may be into the
// caller's stack, and the emulation can grow that stack. It is turned into a
// pointer here, in a nosplit function.
//
//go:nosplit
func inotifyFdSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	if trap == SYS_READ {
		return epollResult(inotifyRead(int(a1), (*byte)(unsafe.Pointer(a2)), int(a3)))
	}
	return epollResult(0, inotifyUnreadTo(int(a1), (*int32)(unsafe.Pointer(a3))))
}

// inotifyDupTrap reports whether a syscall duplicates a live instance.
//
//go:nosplit
func inotifyDupTrap(trap, a1, a2 uintptr) bool {
	if trap == SYS_FCNTL {
		return (a2 == F_DUPFD || a2 == F_DUPFD_CLOEXEC) && inotifyFd(a1)
	}
	return (trap == SYS_DUP || trap == SYS_DUP3 || trap == inotifyDup2Trap) && inotifyFd(a1)
}

// inotifyDup makes a duplicate of an instance's descriptor name the instance as
// well, so that it reads whole records and takes watches.
func inotifyDup(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	named := trap == SYS_DUP3 || trap == inotifyDup2Trap
	if named && a1 == a2 {
		return RawSyscall6(trap, a1, a2, a3, a4, a5, a6)
	}
	if named {
		inotifyForget(int(a2))
	}
	r1, r2, err = RawSyscall6(trap, a1, a2, a3, a4, a5, a6)
	if err != 0 {
		return r1, r2, err
	}
	fd := int(r1)
	if named {
		fd = int(a2)
	}
	inotifies.Lock()
	if ino := inotifies.m[int(a1)]; ino != nil {
		inotifies.m[fd] = ino
		inotifyPublish()
	}
	inotifies.Unlock()
	return r1, r2, 0
}

// inotifyForget stops fd meaning an instance, before the descriptor number can
// be used again. The instance goes away once its goroutines see the close of
// its last descriptor.
func inotifyForget(fd int) {
	inotifies.Lock()
	ino := inotifies.m[fd]
	delete(inotifies.m, fd)
	next := -1
	for other, same := range inotifies.m {
		if same == ino {
			next = other
			break
		}
	}
	inotifyPublish()
	inotifies.Unlock()
	if ino != nil {
		ino.mu.Lock()
		if ino.fd == fd {
			ino.fd = next
		}
		ino.mu.Unlock()
	}
}

// inotifyUnread reports how many bytes wait in an instance.
func inotifyUnread(fd int) (int, Errno) {
	if windows := cosmo.Windows(); windows != nil {
		unread, failure := windows.SocketUnread(int32(fd))
		return unread, Errno(failure)
	}
	var unread int32
	_, _, errno := Syscall(SYS_IOCTL, uintptr(fd), inotifyFIONREAD, uintptr(unsafe.Pointer(&unread)))
	return int(unread), errno
}

func inotifyUnreadTo(fd int, out *int32) Errno {
	unread, errno := inotifyUnread(fd)
	if errno == 0 {
		*out = int32(unread)
	}
	return errno
}

func inotifyPeek(fd int, buf []byte) (int, Errno) {
	count, err := recvfrom(fd, buf, MSG_PEEK, nil, nil)
	if err != nil {
		return 0, err.(Errno)
	}
	return count, 0
}

// inotifyWhole reports how much of buf is whole records.
func inotifyWhole(buf []byte) int {
	total := 0
	for len(buf)-total >= SizeofInotifyEvent {
		event := (*InotifyEvent)(unsafe.Pointer(&buf[total]))
		end := total + SizeofInotifyEvent + int(event.Len)
		if end > len(buf) {
			break
		}
		total = end
	}
	return total
}

// take takes whole records into view. The lock keeps readers from each taking
// what both looked at. A count below zero means a reader must look again.
func (ino *inotifyInstance) take(fd int, view []byte) (int, Errno) {
	ino.reading.Lock()
	defer ino.reading.Unlock()
	var head [SizeofInotifyEvent]byte
	count, errno := inotifyPeek(fd, head[:])
	if errno != 0 || count == 0 {
		return 0, errno
	}
	if count < SizeofInotifyEvent {
		return -1, 0
	}
	event := (*InotifyEvent)(unsafe.Pointer(&head[0]))
	if SizeofInotifyEvent+int(event.Len) > len(view) {
		return 0, EINVAL
	}
	count, errno = inotifyPeek(fd, view)
	if errno != 0 {
		return 0, errno
	}
	whole := inotifyWhole(view[:count])
	if whole == 0 {
		return -1, 0
	}
	taken, err := recvfrom(fd, view[:whole], 0, nil, nil)
	if err != nil {
		return 0, err.(Errno)
	}
	return taken, 0
}

// inotifyRead blocks or answers EAGAIN as the descriptor's mode says, and takes
// only whole records from the socket.
func inotifyRead(fd int, buf *byte, size int) (uintptr, Errno) {
	inotifies.Lock()
	ino := inotifies.m[fd]
	inotifies.Unlock()
	if ino == nil {
		return 0, EBADF
	}
	view := unsafe.Slice(buf, size)
	for {
		taken, errno := ino.take(fd, view)
		if errno != 0 {
			return 0, errno
		}
		if taken >= 0 {
			return uintptr(taken), 0
		}
		inotifySleep(inotifySettleNanos)
	}
}
