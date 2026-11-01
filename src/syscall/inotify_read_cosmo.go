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

// inotifyForget stops fd meaning an instance, before the descriptor number can
// be used again. The instance goes away once its goroutines see the close.
func inotifyForget(fd int) {
	inotifies.Lock()
	ino := inotifies.m[fd]
	delete(inotifies.m, fd)
	inotifyPublish()
	inotifies.Unlock()
	if ino != nil {
		ino.mu.Lock()
		ino.fd = -1
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

// inotifyAwait waits for records on a blocking NT instance, for the reason
// inotifyHangup gives. It reports whether it waited.
func inotifyAwait(fd int) bool {
	windows := cosmo.Windows()
	if windows == nil {
		return false
	}
	flags, err := fcntl(fd, F_GETFL, 0)
	if err != nil || flags&O_NONBLOCK != 0 {
		return false
	}
	windows.SocketAwait(int32(fd))
	return true
}

// take takes whole records into view. The lock keeps readers from each taking
// what both looked at. A count below zero means a reader must look again.
func (ino *inotifyInstance) take(fd int, view []byte, waited bool) (int, Errno) {
	ino.reading.Lock()
	defer ino.reading.Unlock()
	if waited {
		if unread, errno := inotifyUnread(fd); errno != 0 || unread == 0 {
			return -1, errno
		}
	}
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
		taken, errno := ino.take(fd, view, inotifyAwait(fd))
		if errno != 0 {
			return 0, errno
		}
		if taken >= 0 {
			return uintptr(taken), 0
		}
		inotifySleep(inotifySettleNanos)
	}
}
