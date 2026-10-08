// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"sync"
	"unsafe"
)

// epoll on a macOS host, kept here and waited on with select, since kqueue cannot watch a terminal there.

//go:linkname runtime_nanotime runtime.nanotime
func runtime_nanotime() int64

const (
	epollWAKEUP    = 1 << 29
	epollEXCLUSIVE = 1 << 28
	epollET        = 1 << 31

	epollReadable = EPOLLIN | EPOLLRDNORM
	epollWritable = EPOLLOUT | EPOLLWRNORM | EPOLLWRBAND
	epollUrgent   = EPOLLPRI | EPOLLRDBAND

	// epollAccepted is every event bit epoll_ctl takes here. EPOLLERR and
	// EPOLLHUP are accepted and ignored, as Linux does.
	epollAccepted = epollReadable | epollWritable | epollUrgent | EPOLLRDHUP |
		EPOLLERR | EPOLLHUP | EPOLLMSG | EPOLLONESHOT | epollWAKEUP
)

// An epollID names the file a descriptor refers to, so that a descriptor number closed and reused is not taken for the file registered.
type epollID struct{ dev, ino uint64 }

func epollIDOf(fd int) (epollID, uint32, Errno) {
	var st Stat_t
	if err := Fstat(fd, &st); err != nil {
		return epollID{}, 0, err.(Errno)
	}
	return epollID{st.Dev, st.Ino}, st.Mode, 0
}

type epollEntry struct {
	id     epollID
	events uint32     // the interest; zero once a oneshot has fired
	event  EpollEvent // as registered: its data is returned with each event
}

type epollInstance struct {
	id   epollID // of the read end, which is the descriptor callers hold
	wake int     // the write end

	mu  sync.Mutex
	fds map[int]*epollEntry
}

var epolls struct {
	sync.Mutex
	m map[int]*epollInstance
}

// darwinEpollTrap reports whether trap is an epoll syscall this package
// serves, which it does only on a macOS host.
//
//go:nosplit
func darwinEpollTrap(trap uintptr) bool {
	switch trap {
	case SYS_EPOLL_CREATE1, SYS_EPOLL_CTL, SYS_EPOLL_PWAIT,
		darwinSysEpollPwait2, darwinSysEpollCreate, darwinSysEpollWait:
		return cosmo.Darwin()
	}
	return false
}

// darwinEpollSyscall serves an epoll syscall on a macOS host. The pointer
// arguments may point into the caller's stack. The emulation can grow that
// stack. They are turned into pointers here, in a nosplit function, before
// anything can move them. A timeout in milliseconds stays an integer: a
// small integer in a pointer is an invalid pointer to the stack copier.
//
//go:nosplit
func darwinEpollSyscall(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	switch trap {
	case darwinSysEpollCreate:
		if int32(a1) <= 0 {
			return ^uintptr(0), 0, EINVAL
		}
		return epollResult(epollCreate(0))
	case SYS_EPOLL_CREATE1:
		return epollResult(epollCreate(a1))
	case SYS_EPOLL_CTL:
		return epollResult(0, epollCtl(int(a1), int(a2), int(a3), (*EpollEvent)(unsafe.Pointer(a4))))
	case darwinSysEpollWait:
		return epollResult(epollWait(int(a1), (*EpollEvent)(unsafe.Pointer(a2)), int(int32(a3)), epollMsec(a4)))
	case SYS_EPOLL_PWAIT:
		if a5 != 0 {
			return ^uintptr(0), 0, ENOSYS
		}
		return epollResult(epollWait(int(a1), (*EpollEvent)(unsafe.Pointer(a2)), int(int32(a3)), epollMsec(a4)))
	case darwinSysEpollPwait2:
		if a5 != 0 {
			return ^uintptr(0), 0, ENOSYS
		}
		return epollResult(epollWait(int(a1), (*EpollEvent)(unsafe.Pointer(a2)), int(int32(a3)), epollTimespec((*Timespec)(unsafe.Pointer(a4)))))
	}
	return ^uintptr(0), 0, ENOSYS
}

//go:nosplit
func epollResult(r uintptr, err Errno) (uintptr, uintptr, Errno) {
	if err != 0 {
		return ^uintptr(0), 0, err
	}
	return r, 0, 0
}

// epollMsec converts an epoll_wait timeout to nanoseconds. Negative waits
// forever.
//
//go:nosplit
func epollMsec(ms uintptr) int64 {
	if int32(ms) < 0 {
		return -1
	}
	return int64(int32(ms)) * 1e6
}

// epollTimespec converts an epoll_pwait2 timeout to nanoseconds. Nil
// waits forever.
//
//go:nosplit
func epollTimespec(ts *Timespec) int64 {
	if ts == nil {
		return -1
	}
	if ts.Sec < 0 || ts.Nsec < 0 || ts.Nsec >= 1e9 {
		return -2
	}
	return ts.Nano()
}

func epollCreate(flags uintptr) (uintptr, Errno) {
	if flags&^EPOLL_CLOEXEC != 0 {
		return 0, EINVAL
	}
	var p [2]int
	if err := Pipe2(p[:], O_CLOEXEC|O_NONBLOCK); err != nil {
		return 0, err.(Errno)
	}
	if flags&EPOLL_CLOEXEC == 0 {
		if _, err := fcntl(p[0], F_SETFD, 0); err != nil {
			Close(p[0])
			Close(p[1])
			return 0, err.(Errno)
		}
	}
	id, _, e := epollIDOf(p[0])
	if e != 0 {
		Close(p[0])
		Close(p[1])
		return 0, e
	}
	ep := &epollInstance{id: id, wake: p[1], fds: map[int]*epollEntry{}}

	epolls.Lock()
	defer epolls.Unlock()
	if epolls.m == nil {
		epolls.m = map[int]*epollInstance{}
	}
	// An instance whose descriptor was closed is released here, since close is
	// not seen by this package.
	for fd, old := range epolls.m {
		if oid, _, e := epollIDOf(fd); e != 0 || oid != old.id {
			Close(old.wake)
			delete(epolls.m, fd)
		}
	}
	epolls.m[p[0]] = ep
	return uintptr(p[0]), 0
}

// epollLookup returns the instance epfd refers to.
func epollLookup(epfd int) (*epollInstance, Errno) {
	id, _, e := epollIDOf(epfd)
	if e != 0 {
		return nil, e
	}
	epolls.Lock()
	defer epolls.Unlock()
	ep := epolls.m[epfd]
	if ep == nil || ep.id != id {
		return nil, EINVAL
	}
	return ep, 0
}

func epollCtl(epfd, op, fd int, event *EpollEvent) Errno {
	ep, e := epollLookup(epfd)
	if e != 0 {
		return e
	}
	if fd == epfd {
		return EINVAL
	}
	id, mode, e := epollIDOf(fd)
	if e != 0 {
		return e
	}
	// Linux refuses what can never block, which is what these are.
	if t := mode & S_IFMT; t == S_IFREG || t == S_IFDIR {
		return EPERM
	}
	var events uint32
	if op != EPOLL_CTL_DEL {
		if event == nil {
			return EFAULT
		}
		events = event.Events
		if events&(epollET|epollEXCLUSIVE) != 0 || events&^epollAccepted != 0 {
			return EINVAL
		}
	}

	ep.mu.Lock()
	cur := ep.fds[fd]
	if cur != nil && cur.id != id {
		delete(ep.fds, fd)
		cur = nil
	}
	switch op {
	case EPOLL_CTL_ADD:
		if cur != nil {
			ep.mu.Unlock()
			return EEXIST
		}
		ep.fds[fd] = &epollEntry{id: id, events: events, event: *event}
	case EPOLL_CTL_MOD:
		if cur == nil {
			ep.mu.Unlock()
			return ENOENT
		}
		cur.events, cur.event = events, *event
	case EPOLL_CTL_DEL:
		if cur == nil {
			ep.mu.Unlock()
			return ENOENT
		}
		delete(ep.fds, fd)
	default:
		ep.mu.Unlock()
		return EINVAL
	}
	ep.mu.Unlock()

	// A full pipe already holds a wakeup, so EAGAIN is not a failure.
	var b [1]byte
	write(ep.wake, b[:])
	return 0
}

// epollWait waits up to timeout nanoseconds, forever when negative, and
// fills events with what is ready.
func epollWait(epfd int, events *EpollEvent, maxevents int, timeout int64) (uintptr, Errno) {
	if maxevents <= 0 || timeout < -1 {
		return 0, EINVAL
	}
	if events == nil {
		return 0, EFAULT
	}
	ep, e := epollLookup(epfd)
	if e != 0 {
		return 0, e
	}
	out := unsafe.Slice(events, maxevents)
	deadline := runtime_nanotime() + timeout

	for {
		type want struct {
			fd     int
			events uint32
		}
		ep.mu.Lock()
		wants := make([]want, 0, len(ep.fds))
		maxfd := epfd
		for fd, en := range ep.fds {
			if en.events&(epollReadable|epollWritable|epollUrgent) == 0 {
				continue
			}
			wants = append(wants, want{fd, en.events})
			maxfd = max(maxfd, fd)
		}
		ep.mu.Unlock()

		words := maxfd/64 + 1
		sets := make([]uint64, 3*words)
		rd, wr, ex := sets[:words], sets[words:2*words], sets[2*words:]
		rd[epfd/64] |= 1 << (epfd % 64)
		for _, w := range wants {
			bit := uint64(1) << (w.fd % 64)
			if w.events&epollReadable != 0 {
				rd[w.fd/64] |= bit
			}
			if w.events&epollWritable != 0 {
				wr[w.fd/64] |= bit
			}
			if w.events&epollUrgent != 0 {
				ex[w.fd/64] |= bit
			}
		}

		var ts *Timespec
		if timeout >= 0 {
			ts = new(Timespec)
			*ts = NsecToTimespec(max(deadline-runtime_nanotime(), 0))
		}
		n, _, err := Syscall6(SYS_PSELECT6, uintptr(maxfd+1),
			uintptr(unsafe.Pointer(&rd[0])), uintptr(unsafe.Pointer(&wr[0])),
			uintptr(unsafe.Pointer(&ex[0])), uintptr(unsafe.Pointer(ts)), 0)
		if err == EBADF {
			// A registered descriptor was closed. Linux drops it from
			// the interest list, so do the same and wait again.
			if e := ep.prune(epfd); e != 0 {
				return 0, e
			}
			continue
		}
		if err != 0 {
			return 0, err
		}
		if n == 0 {
			return 0, 0
		}
		if rd[epfd/64]&(1<<(epfd%64)) != 0 {
			ep.drain(epfd)
		}

		count := 0
		ep.mu.Lock()
		for _, w := range wants {
			en := ep.fds[w.fd]
			if en == nil || en.events != w.events {
				continue // changed while waiting: the next pass sees it
			}
			bit := uint64(1) << (w.fd % 64)
			var got uint32
			if rd[w.fd/64]&bit != 0 {
				got |= en.events & epollReadable
			}
			if wr[w.fd/64]&bit != 0 {
				got |= en.events & epollWritable
			}
			if ex[w.fd/64]&bit != 0 {
				got |= en.events & epollUrgent
			}
			if got == 0 {
				continue
			}
			out[count] = en.event
			out[count].Events = got
			count++
			if en.events&EPOLLONESHOT != 0 {
				en.events = 0
			}
			if count == maxevents {
				break
			}
		}
		ep.mu.Unlock()
		if count > 0 {
			return uintptr(count), 0
		}
		// Only a wakeup, or an interest that changed: wait again for
		// whatever time is left.
		if timeout >= 0 && runtime_nanotime() >= deadline {
			return 0, 0
		}
	}
}

// prune drops every registration whose descriptor no longer refers to the
// file registered, and reports EBADF when there was none to drop. Then
// the bad descriptor is the instance's own.
func (ep *epollInstance) prune(epfd int) Errno {
	if id, _, e := epollIDOf(epfd); e != 0 || id != ep.id {
		return EBADF
	}
	ep.mu.Lock()
	defer ep.mu.Unlock()
	dropped := false
	for fd, en := range ep.fds {
		if id, _, e := epollIDOf(fd); e != 0 || id != en.id {
			delete(ep.fds, fd)
			dropped = true
		}
	}
	if !dropped {
		return EBADF
	}
	return 0
}

// drain empties the wakeup pipe, which is non-blocking.
func (ep *epollInstance) drain(epfd int) {
	var buf [64]byte
	for {
		n, err := read(epfd, buf[:])
		if err != nil || n < len(buf) {
			return
		}
	}
}
