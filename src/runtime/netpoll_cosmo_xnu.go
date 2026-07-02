// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Darwin (XNU) fallback network poller for GOOS=cosmo.
//
// A cosmo binary runs on Linux, macOS and Windows with one runtime, so
// the poller cannot be chosen with build tags: netpoll_cosmo.go holds the
// epoll implementation used on Linux hosts and dispatches at run time on
// __hostos to the functions in this file when the host is XNU, where the
// epoll/eventfd syscalls behind the Linux path do not exist (before this
// poller, netpollinit threw on macOS and the first timer, socket or
// os/exec pipe killed the process).
//
// The implementation is a port of netpoll_aix.go: a level-triggered
// poll(2) loop over a mutex-protected pollfd array, with a nonblocking
// self-pipe to interrupt a blocked poll when the fd set must change
// (netpollopen/netpollclose/netpollarm) or a timer needs an earlier
// wakeup (netpollBreak). Only one M polls at a time - it holds xnuMtxset
// across the (possibly blocking) poll, and every mutator boots it out
// through the pipe before touching the array. poll itself is Apple
// libc's poll, resolved through the Syslib's dlsym at osinit and called
// via the cosmoLibcCall6 trampoline (see cosmoDarwinPoll in
// os_cosmo_arm64.go; there is no Syslib on amd64, so the amd64 stub
// reports the poller unsupported and netpollinit fails visibly).
//
// Because poll is level-triggered, descriptors are registered with no
// events armed and poll_runtime_pollWait arms the direction it is about
// to wait for (netpollLevelTriggered in netpoll.go); netpoll disarms a
// direction when it delivers readiness, or a ready-but-unwaited fd would
// spin the poller.

import (
	"unsafe"
)

// pollfd is struct pollfd. Apple's sys/poll.h and Linux's
// asm-generic/poll.h agree on the layout: {int fd; short events; short
// revents}, 8 bytes.
type pollfd struct {
	fd      int32
	events  int16
	revents int16
}

// Event bits. Apple sys/poll.h and Linux asm-generic/poll.h assign the
// same values to all of these (POLLIN 0x1, POLLPRI 0x2, POLLOUT 0x4,
// POLLERR 0x8, POLLHUP 0x10, POLLNVAL 0x20); AIX is the odd one out,
// which is why netpoll_aix.go has different constants.
const (
	_POLLIN  = 0x0001
	_POLLOUT = 0x0004
	_POLLERR = 0x0008
	_POLLHUP = 0x0010
)

var (
	xnuPfds           []pollfd
	xnuPds            []*pollDesc
	xnuMtxpoll        mutex
	xnuMtxset         mutex
	xnuRdwake         int32 = -1
	xnuWrwake         int32 = -1
	xnuPendingUpdates int32
)

// xnuMaxPollMs bounds every blocking poll(2): XNU can LOSE the wakeup
// byte written to the self-pipe while poll is entering its wait (the
// long-documented Apple poll-on-pipe race, rdar 37537852 - "poll()
// sometimes doesn't return when a polled pipe becomes readable"; the
// reason most darwin software uses kqueue or select instead). When
// that happens with an unbounded timeout, the mutator that wrote the
// byte waits on xnuMtxset for as long as the poll sleeps: forever.
// The observed CI wedge (goroutines stuck inside net.Listen/Dial's
// netpollopen or pollWait's netpollarm while the poller slept through
// their wakeup) is exactly that shape, and a watchdog traceback
// confirmed the mutator parked in the runtime lock with the wakeup
// protocol correctly executed on the user side. Capping the wait
// converts a lost wakeup from a permanent wedge into a bounded
// (<=100ms) hiccup: on timeout the poller returns empty, releases
// xnuMtxset (letting any queued mutator in), and the scheduler calls
// back in with a recomputed delay. An idle process wakes 10x/s on one
// thread - negligible - and actual readiness is unaffected
// (level-triggered events still return immediately).
const xnuMaxPollMs = 100

func netpollinitDarwin() {
	if !cosmoDarwinPollSupported() {
		println("runtime: netpollinit: Apple libc poll is unavailable on this host")
		throw("runtime: netpollinit failed")
	}

	// Create the pipe we use to wake up a blocked poll. The darwin pipe2
	// emulation (Syslib pipe + fcntl) applies the flags for real or
	// fails; it never returns descriptors without them.
	r, w, errno := nonblockingPipe()
	if errno != 0 {
		println("runtime: netpollinit: pipe failed with", -errno)
		throw("runtime: netpollinit failed")
	}
	xnuRdwake = r
	xnuWrwake = w

	// Pre-allocate array of pollfd structures for poll.
	xnuPfds = make([]pollfd, 1, 128)
	// Poll the read side of the pipe.
	xnuPfds[0] = pollfd{fd: xnuRdwake, events: _POLLIN}

	xnuPds = make([]*pollDesc, 1, 128)
	xnuPds[0] = nil

	// Level-triggered readiness: poll_runtime_pollWait must arm the
	// awaited direction (netpollarm) before each wait.
	netpollLevelTriggered = true
}

func netpollIsPollDescriptorDarwin(fd uintptr) bool {
	return fd == uintptr(xnuRdwake) || fd == uintptr(xnuWrwake)
}

// netpollwakeupDarwin writes on xnuWrwake to wake up a poll blocked in
// netpollDarwin before any changes to the pollfd array. Callers hold
// xnuMtxpoll; the byte is coalesced until the poller observes it.
//
// The write must not be fire-and-forget: xnuPendingUpdates suppresses
// every later wakeup attempt until the poller resets it, so a silently
// lost byte here would leave the poller asleep (for its full timeout,
// or forever with delay < 0) while the mutator queues on xnuMtxset.
// EINTR is real once signal delivery is live on macOS - async
// preemption's SIGURG interrupts libc calls - so retry it; EAGAIN
// means 64KiB of wakeup bytes are already queued and the poller
// cannot miss them.
func netpollwakeupDarwin() {
	if xnuPendingUpdates == 0 {
		xnuPendingUpdates = 1
		b := [1]byte{0}
		for {
			n := write(uintptr(xnuWrwake), unsafe.Pointer(&b[0]), 1)
			if n == 1 {
				break
			}
			if n == -_EINTR {
				continue
			}
			if n == -_EAGAIN {
				break
			}
			println("runtime: netpollwakeup write failed with", -n)
			throw("runtime: netpollwakeup write failed")
		}
	}
}

func netpollopenDarwin(fd uintptr, pd *pollDesc) uintptr {
	lock(&xnuMtxpoll)
	netpollwakeupDarwin()

	lock(&xnuMtxset)
	unlock(&xnuMtxpoll)

	// We don't worry about pd.fdseq here,
	// as xnuMtxset protects us from stale pollDescs.

	pd.user = uint32(len(xnuPfds))
	xnuPfds = append(xnuPfds, pollfd{fd: int32(fd)})
	xnuPds = append(xnuPds, pd)
	unlock(&xnuMtxset)
	return 0
}

func netpollcloseDarwin(fd uintptr) uintptr {
	lock(&xnuMtxpoll)
	netpollwakeupDarwin()

	lock(&xnuMtxset)
	unlock(&xnuMtxpoll)

	for i := 0; i < len(xnuPfds); i++ {
		if xnuPfds[i].fd == int32(fd) {
			xnuPfds[i] = xnuPfds[len(xnuPfds)-1]
			xnuPfds = xnuPfds[:len(xnuPfds)-1]

			xnuPds[i] = xnuPds[len(xnuPds)-1]
			xnuPds[i].user = uint32(i)
			xnuPds = xnuPds[:len(xnuPds)-1]
			break
		}
	}
	unlock(&xnuMtxset)
	return 0
}

func netpollarmDarwin(pd *pollDesc, mode int) {
	lock(&xnuMtxpoll)
	netpollwakeupDarwin()

	lock(&xnuMtxset)
	unlock(&xnuMtxpoll)

	switch mode {
	case 'r':
		xnuPfds[pd.user].events |= _POLLIN
	case 'w':
		xnuPfds[pd.user].events |= _POLLOUT
	}
	unlock(&xnuMtxset)
}

// netpollBreakDarwin interrupts a poll. The netpollWakeSig deduplication
// happens in the shared netpollBreak (netpoll_cosmo.go) before dispatch.
//
// Like the epoll path's eventfd write, this write must retry on EINTR:
// the caller already won the netpollWakeSig CAS, so a lost byte would
// suppress every subsequent netpollBreak (they all CAS-fail) while the
// poller sleeps through its timeout - timer wakeups would stall until
// the poll's own deadline. EAGAIN (pipe full) is success: bytes are
// already pending.
func netpollBreakDarwin() {
	b := [1]byte{0}
	for {
		n := write(uintptr(xnuWrwake), unsafe.Pointer(&b[0]), 1)
		if n == 1 {
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

// netpollDarwin checks for ready network connections.
// Returns a list of goroutines that become runnable,
// and a delta to add to netpollWaiters.
// This must never return an empty list with a non-zero delta.
//
// delay < 0: blocks indefinitely
// delay == 0: does not block, just polls
// delay > 0: block for up to that many nanoseconds
//
//go:nowritebarrierrec
func netpollDarwin(delay int64) (gList, int32) {
	if xnuRdwake == -1 {
		// Not initialized (mirrors the epfd == -1 guard on the epoll path).
		return gList{}, 0
	}
	var timeout int32
	if delay < 0 {
		timeout = xnuMaxPollMs
	} else if delay == 0 {
		// A nonblocking check would still need xnuMtxset, which the M
		// blocked in poll holds - waiting for it could block the caller
		// (sysmon, spinning Ms) for the remainder of that poll. Like
		// netpoll_aix.go, report no readiness instead.
		return gList{}, 0
	} else if delay < 1e6 {
		timeout = 1
	} else if delay < 1e15 {
		timeout = int32(delay / 1e6)
	} else {
		timeout = xnuMaxPollMs
	}
	if timeout > xnuMaxPollMs {
		// Never sleep unbounded on XNU: see xnuMaxPollMs. Waking with
		// nothing ready returns an empty list; the scheduler
		// recomputes the timer delay and calls back in.
		timeout = xnuMaxPollMs
	}
retry:
	lock(&xnuMtxpoll)
	lock(&xnuMtxset)
	xnuPendingUpdates = 0
	unlock(&xnuMtxpoll)

	n, e := cosmoDarwinPoll(&xnuPfds[0], int32(len(xnuPfds)), timeout)
	if n < 0 {
		if e != _EINTR {
			println("runtime: poll failed with", e, "len(xnuPfds)=", len(xnuPfds))
			unlock(&xnuMtxset)
			throw("runtime: netpoll failed")
		}
		unlock(&xnuMtxset)
		// If a timed sleep was interrupted, just return to
		// recalculate how long we should sleep now.
		if timeout > 0 {
			return gList{}, 0
		}
		goto retry
	}
	// Check if some descriptors need to be changed
	if n != 0 && xnuPfds[0].revents&(_POLLIN|_POLLHUP|_POLLERR) != 0 {
		if delay != 0 {
			// A netpollwakeup could be picked up by a
			// non-blocking poll. Only clear the wakeup
			// if blocking.
			//
			// An EINTR mid-drain (!= 1) exits the loop with bytes
			// possibly left in the pipe; that is self-healing - the
			// next poll wakes immediately and drains again - so no
			// retry is needed here, unlike the write side.
			var b [1]byte
			for read(xnuRdwake, noescape(unsafe.Pointer(&b[0])), 1) == 1 {
			}
			netpollWakeSig.Store(0)
		}
		// Still look at the other fds even if the mode may have
		// changed, as netpollBreak might have been called.
		n--
	}
	var toRun gList
	delta := int32(0)
	for i := 1; i < len(xnuPfds) && n > 0; i++ {
		pfd := &xnuPfds[i]

		var mode int32
		if pfd.revents&(_POLLIN|_POLLHUP|_POLLERR) != 0 {
			mode += 'r'
			pfd.events &= ^int16(_POLLIN)
		}
		if pfd.revents&(_POLLOUT|_POLLHUP|_POLLERR) != 0 {
			mode += 'w'
			pfd.events &= ^int16(_POLLOUT)
		}
		if mode != 0 {
			xnuPds[i].setEventErr(pfd.revents == _POLLERR, 0)
			delta += netpollready(&toRun, xnuPds[i], mode)
			n--
		}
	}
	unlock(&xnuMtxset)
	return toRun, delta
}
