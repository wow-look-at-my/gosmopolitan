// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

import (
	"internal/runtime/atomic"
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// This file holds the epoll poller used on Linux hosts. Every entry
// point dispatches at run time on the host OS: macOS has no epoll, so
// XNU hosts use the kqueue poller in netpoll_cosmo_xnu.go instead,
// and Windows NT hosts use the WSAPoll poller in netpoll_cosmo_nt.go
// (level-triggered, aix-shaped; it also serves the timer heap's
// unconditional netpollGenericInit). Exactly one of the three is ever
// initialized in a given process.

var (
	epfd           int32         = -1 // epoll descriptor
	netpollEventFd uintptr            // eventfd for netpollBreak
	netpollWakeSig atomic.Uint32      // used to avoid duplicate calls of netpollBreak
)

func netpollinit() {
	if iswindows() {
		netpollinitNT()
		return
	}
	if isdarwin() {
		netpollinitDarwin()
		return
	}
	var errno uintptr
	epfd, errno = cosmo.EpollCreate1(cosmo.EPOLL_CLOEXEC)
	if errno != 0 {
		println("runtime: epollcreate failed with", errno)
		throw("runtime: netpollinit failed")
	}
	efd, errno := cosmo.Eventfd(0, cosmo.EFD_CLOEXEC|cosmo.EFD_NONBLOCK)
	if errno != 0 {
		println("runtime: eventfd failed with", errno)
		throw("runtime: eventfd failed")
	}
	ev := cosmo.EpollEvent{
		Events: cosmo.EPOLLIN,
	}
	*(**uintptr)(unsafe.Pointer(&ev.Data)) = &netpollEventFd
	errno = cosmo.EpollCtl(epfd, cosmo.EPOLL_CTL_ADD, efd, &ev)
	if errno != 0 {
		println("runtime: epollctl failed with", errno)
		throw("runtime: epollctl failed")
	}
	netpollEventFd = uintptr(efd)
}

func netpollIsPollDescriptor(fd uintptr) bool {
	if iswindows() {
		// The NT wake socket is a raw SOCKET with no emulated fd
		// number, so no fd can ever name it.
		return false
	}
	if isdarwin() {
		return netpollIsPollDescriptorDarwin(fd)
	}
	return fd == uintptr(epfd) || fd == netpollEventFd
}

func netpollopen(fd uintptr, pd *pollDesc) uintptr {
	if iswindows() {
		// Sockets only; pipes and files keep failing so
		// internal/poll falls back to blocking mode for them.
		return netpollopenNT(fd, pd)
	}
	if isdarwin() {
		return netpollopenDarwin(fd, pd)
	}
	var ev cosmo.EpollEvent
	ev.Events = cosmo.EPOLLIN | cosmo.EPOLLOUT | cosmo.EPOLLRDHUP | cosmo.EPOLLET
	tp := taggedPointerPack(unsafe.Pointer(pd), pd.fdseq.Load())
	*(*taggedPointer)(unsafe.Pointer(&ev.Data)) = tp
	return cosmo.EpollCtl(epfd, cosmo.EPOLL_CTL_ADD, int32(fd), &ev)
}

func netpollclose(fd uintptr) uintptr {
	if iswindows() {
		return netpollcloseNT(fd)
	}
	if isdarwin() {
		return netpollcloseDarwin(fd)
	}
	var ev cosmo.EpollEvent
	return cosmo.EpollCtl(epfd, cosmo.EPOLL_CTL_DEL, int32(fd), &ev)
}

// netpollarm re-arms one direction before a wait. Only the NT WSAPoll
// poller is level-triggered (it sets netpollLevelTriggered at init);
// the epoll and kqueue pollers are edge-triggered, arm once at
// netpollopen, and must never come here.
func netpollarm(pd *pollDesc, mode int) {
	if iswindows() {
		netpollarmNT(pd, mode)
		return
	}
	if isdarwin() {
		netpollarmDarwin(pd, mode)
		return
	}
	throw("runtime: unused")
}

// netpollBreak interrupts an epollwait (or, on XNU hosts, a kevent).
func netpollBreak() {
	// Failing to cas indicates there is an in-flight wakeup, so we're done here.
	if !netpollWakeSig.CompareAndSwap(0, 1) {
		return
	}

	if iswindows() {
		// One byte to the wake socket boots the poller out of its
		// blocking WSAPoll; the poller drains and resets
		// netpollWakeSig when it wakes blocking.
		netpollBreakNT()
		return
	}

	if isdarwin() {
		netpollBreakDarwin()
		return
	}

	var one uint64 = 1
	oneSize := int32(unsafe.Sizeof(one))
	for {
		n := write(netpollEventFd, noescape(unsafe.Pointer(&one)), oneSize)
		if n == oneSize {
			break
		}
		if n == -_EINTR {
			continue
		}
		if n == -_EAGAIN {
			return
		}
		println("runtime: netpollBreak write failed with", -n)
		throw("runtime: netpollBreak write failed")
	}
}

// netpoll checks for ready network connections.
// Returns a list of goroutines that become runnable,
// and a delta to add to netpollWaiters.
// This must never return an empty list with a non-zero delta.
//
// delay < 0: blocks indefinitely
// delay == 0: does not block, just polls
// delay > 0: block for up to that many nanoseconds
func netpoll(delay int64) (gList, int32) {
	if iswindows() {
		return netpollNT(delay)
	}
	if isdarwin() {
		return netpollDarwin(delay)
	}
	if epfd == -1 {
		return gList{}, 0
	}
	var waitms int32
	if delay < 0 {
		waitms = -1
	} else if delay == 0 {
		waitms = 0
	} else if delay < 1e6 {
		waitms = 1
	} else if delay < 1e15 {
		waitms = int32(delay / 1e6)
	} else {
		// An arbitrary cap on how long to wait for a timer.
		// 1e9 ms == ~11.5 days.
		waitms = 1e9
	}
	var events [128]cosmo.EpollEvent
retry:
	n, errno := cosmo.EpollWait(epfd, events[:], int32(len(events)), waitms)
	if errno != 0 {
		if errno != _EINTR {
			println("runtime: epollwait on fd", epfd, "failed with", errno)
			throw("runtime: netpoll failed")
		}
		// If a timed sleep was interrupted, just return to
		// recalculate how long we should sleep now.
		if waitms > 0 {
			return gList{}, 0
		}
		goto retry
	}
	var toRun gList
	delta := int32(0)
	for i := int32(0); i < n; i++ {
		ev := events[i]
		if ev.Events == 0 {
			continue
		}

		if *(**uintptr)(unsafe.Pointer(&ev.Data)) == &netpollEventFd {
			if ev.Events != cosmo.EPOLLIN {
				println("runtime: netpoll: eventfd ready for", ev.Events)
				throw("runtime: netpoll: eventfd ready for something unexpected")
			}
			if delay != 0 {
				// netpollBreak could be picked up by a
				// nonblocking poll. Only read the 8-byte
				// integer if blocking.
				// Since EFD_SEMAPHORE was not specified,
				// the eventfd counter will be reset to 0.
				var one uint64
				read(int32(netpollEventFd), noescape(unsafe.Pointer(&one)), int32(unsafe.Sizeof(one)))
				netpollWakeSig.Store(0)
			}
			continue
		}

		var mode int32
		if ev.Events&(cosmo.EPOLLIN|cosmo.EPOLLRDHUP|cosmo.EPOLLHUP|cosmo.EPOLLERR) != 0 {
			mode += 'r'
		}
		if ev.Events&(cosmo.EPOLLOUT|cosmo.EPOLLHUP|cosmo.EPOLLERR) != 0 {
			mode += 'w'
		}
		if mode != 0 {
			tp := *(*taggedPointer)(unsafe.Pointer(&ev.Data))
			pd := (*pollDesc)(tp.pointer())
			tag := tp.tag()
			if pd.fdseq.Load() == tag {
				pd.setEventErr(ev.Events == cosmo.EPOLLERR, tag)
				delta += netpollready(&toRun, pd, mode)
			}
		}
	}
	return toRun, delta
}
