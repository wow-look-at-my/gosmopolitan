// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package runtime

// Darwin (XNU) network poller for GOOS=cosmo, built on kqueue. One
// runtime serves Linux, macOS and Windows, so the poller cannot be
// chosen by build tag: netpoll_cosmo.go holds the epoll implementation
// and dispatches on __hostos to this file when the host is XNU. kqueue
// and kevent are Apple libc wrappers, resolved through the Syslib's
// dlsym at osinit. amd64 has no Syslib, so its stubs report the poller
// unsupported and netpollinit fails visibly.
//
// This ports upstream netpoll_kqueue.go: a descriptor is registered
// once at netpollopen with EV_ADD|EV_CLEAR, netpollBreak is an
// EVFILT_USER self-event, and kevent registration is thread-safe in
// the kernel. So there are NO runtime locks and no wakeup pipe here.

import (
	"internal/runtime/atomic"
	"unsafe"
)

// keventt is Apple's struct kevent (64-bit), from upstream
// defs_darwin_arm64.go.
type keventt struct {
	ident  uint64
	filter int16
	flags  uint16
	fflags uint32
	data   int64
	udata  *byte
}

// Apple kqueue constants (upstream defs_darwin_arm64.go). The _xnu
// suffix marks them as host-Apple values living in a runtime-dispatched
// file, like the rest of the darwin emulation's translated constants.
const (
	_EVFILT_READ_xnu  = -0x1
	_EVFILT_WRITE_xnu = -0x2
	_EVFILT_USER_xnu  = -0xa

	_EV_ADD_xnu       = 0x1
	_EV_CLEAR_xnu     = 0x20
	_EV_ERROR_xnu     = 0x4000
	_EV_EOF_xnu       = 0x8000
	_NOTE_TRIGGER_xnu = 0x1000000

	// Magic identifier for the EVFILT_USER wakeup event; same value as
	// upstream netpoll_kqueue_event.go so a stray printout of it leads
	// searchers to the same place.
	xnuKqIdent = 0xee1eb9f4

	// Linux ETIMEDOUT: kevent errnos arrive here already translated to
	// Linux numbering by cosmoDarwinErrno.
	_ETIMEDOUT_linux = 110
)

// xnuKq is the kqueue descriptor, valid only on XNU hosts.
var xnuKq int32 = -1

// Poller forensics: three atomic stores per kevent cycle, noise next
// to the syscall. The runtimeprobe watchdog samples them twice through
// cosmoNetpollDiag when it fires, so a poller stall names itself in
// the CI log. A frozen cycle counter with exit older than enter means
// stuck inside kevent; done < cycles means stuck between kevent and
// cycle end; the sema counters tell the parking side's story.
var (
	xnuPollCycles  atomic.Uint64 // kevent cycles started
	xnuPollDone    atomic.Uint64 // kevent cycles completed
	xnuPollEnterNs atomic.Int64  // nanotime just before the last kevent wait
	xnuPollExitNs  atomic.Int64  // nanotime just after the last kevent return
	xnuPollLastN   atomic.Int32  // last kevent result
	xnuPollLastE   atomic.Int32  // last kevent errno (Linux numbering)

	// M-parking progress on XNU hosts, incremented by the arm64 sema
	// code (os_cosmo_arm64_sema.go); declared here so the diag function
	// builds on both arches.
	xnuSemaWakeEnter atomic.Uint64 // semawakeup entered (darwin path)
	xnuSemaWakeDone  atomic.Uint64 // semawakeup completed
	xnuSemaAcquired  atomic.Uint64 // semasleep consumed a wakeup (count--)
)

// cosmoNetpollDiag reports the darwin poller's forensic counters plus
// the current nanotime, for the runtimeprobe watchdog (linknamed from
// testdata/runtimeprobe).
//
//go:linkname cosmoNetpollDiag
func cosmoNetpollDiag() (cycles, done uint64, enterNs, exitNs, nowNs int64, lastN, lastE int32, wakeEnter, wakeDone, acquired uint64) {
	return xnuPollCycles.Load(), xnuPollDone.Load(),
		xnuPollEnterNs.Load(), xnuPollExitNs.Load(), nanotime(),
		xnuPollLastN.Load(), xnuPollLastE.Load(),
		xnuSemaWakeEnter.Load(), xnuSemaWakeDone.Load(), xnuSemaAcquired.Load()
}

func netpollinitDarwin() {
	if !cosmoDarwinKqueueSupported() {
		println("runtime: netpollinit: Apple libc kqueue is unavailable on this host")
		throw("runtime: netpollinit failed")
	}
	kq, e := cosmoDarwinKqueue()
	if kq < 0 {
		println("runtime: kqueue failed with", e)
		throw("runtime: netpollinit failed")
	}
	closeonexec(kq)
	xnuKq = kq

	// Register the EVFILT_USER wakeup event (upstream addWakeupEvent).
	ev := keventt{
		ident:  xnuKqIdent,
		filter: _EVFILT_USER_xnu,
		flags:  _EV_ADD_xnu | _EV_CLEAR_xnu,
	}
	for {
		n, e := cosmoDarwinKevent(kq, &ev, 1, nil, 0, nil)
		if n >= 0 {
			break
		}
		if e == _EINTR {
			// All changes contained in the changelist should have been
			// applied before returning EINTR, but retry anyway to make
			// a 100% commitment (matches upstream).
			continue
		}
		println("runtime: kevent for EVFILT_USER failed with", e)
		throw("runtime: kevent failed")
	}
}

func netpollIsPollDescriptorDarwin(fd uintptr) bool {
	return fd == uintptr(xnuKq)
}

func netpollopenDarwin(fd uintptr, pd *pollDesc) uintptr {
	// Arm both EVFILT_READ and EVFILT_WRITE in edge-triggered mode
	// (EV_CLEAR) for the whole fd lifetime. The notifications are
	// automatically unregistered when fd is closed. cosmo is 64-bit
	// only, so the udata field always carries the tagged pd pointer
	// (upstream's 32-bit fallback is not needed).
	var ev [2]keventt
	*(*uintptr)(unsafe.Pointer(&ev[0].ident)) = fd
	ev[0].filter = _EVFILT_READ_xnu
	ev[0].flags = _EV_ADD_xnu | _EV_CLEAR_xnu
	tp := taggedPointerPack(unsafe.Pointer(pd), pd.fdseq.Load())
	ev[0].udata = (*byte)(unsafe.Pointer(uintptr(tp)))
	ev[1] = ev[0]
	ev[1].filter = _EVFILT_WRITE_xnu
	n, e := cosmoDarwinKevent(xnuKq, &ev[0], 2, nil, 0, nil)
	if n < 0 {
		return uintptr(e)
	}
	return 0
}

func netpollcloseDarwin(fd uintptr) uintptr {
	// Don't need to unregister because calling close() on fd will
	// remove any kevents that reference the descriptor.
	return 0
}

func netpollarmDarwin(pd *pollDesc, mode int) {
	// kqueue is edge-triggered and arms once at netpollopen;
	// netpollLevelTriggered is not set, so this is never called.
	throw("runtime: unused")
}

// netpollBreakDarwin interrupts a kevent wait by triggering the
// EVFILT_USER event. The netpollWakeSig deduplication happens in the
// shared netpollBreak (netpoll_cosmo.go) before dispatch.
func netpollBreakDarwin() {
	ev := keventt{
		ident:  xnuKqIdent,
		filter: _EVFILT_USER_xnu,
		fflags: _NOTE_TRIGGER_xnu,
	}
	for {
		n, e := cosmoDarwinKevent(xnuKq, &ev, 1, nil, 0, nil)
		if n >= 0 {
			break
		}
		if e == _EINTR {
			continue
		}
		println("runtime: netpollBreak kevent failed with", e)
		throw("runtime: netpollBreak kevent failed")
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
	if xnuKq == -1 {
		return gList{}, 0
	}
	var tp *timespec
	var ts timespec
	if delay < 0 {
		tp = nil
	} else if delay == 0 {
		tp = &ts
	} else {
		ts.setNsec(delay)
		if ts.tv_sec > 1e6 {
			// Darwin returns EINVAL if the sleep time is too long.
			ts.tv_sec = 1e6
		}
		tp = &ts
	}
	var events [64]keventt
retry:
	xnuPollCycles.Add(1)
	xnuPollEnterNs.Store(nanotime())
	n, e := cosmoDarwinKevent(xnuKq, nil, 0, &events[0], int32(len(events)), tp)
	xnuPollExitNs.Store(nanotime())
	xnuPollLastN.Store(n)
	xnuPollLastE.Store(e)
	if n < 0 {
		// Tolerate ETIMEDOUT like upstream netpoll_kqueue.go
		// (go.dev/issue/59679: macOS kevent has been seen failing with
		// ETIMEDOUT instead of returning 0).
		if e != _EINTR && e != _ETIMEDOUT_linux {
			println("runtime: kevent on fd", xnuKq, "failed with", e)
			throw("runtime: netpoll failed")
		}
		xnuPollDone.Add(1)
		// If a timed sleep was interrupted, just return to
		// recalculate how long we should sleep now.
		if delay > 0 {
			return gList{}, 0
		}
		goto retry
	}
	var toRun gList
	delta := int32(0)
	for i := 0; i < int(n); i++ {
		ev := &events[i]

		if ev.filter == _EVFILT_USER_xnu {
			if ev.ident != xnuKqIdent {
				println("runtime: netpoll: break ident ready for", ev.ident)
				throw("runtime: netpoll: break ident ready for something unexpected")
			}
			if delay != 0 {
				// netpollBreak could be picked up by a nonblocking
				// poll. Only reset the netpollWakeSig if blocking.
				netpollWakeSig.Store(0)
			} else {
				// Got a wrong thread, relay (upstream
				// processWakeupEvent).
				netpollBreakDarwin()
			}
			continue
		}

		var mode int32
		switch ev.filter {
		case _EVFILT_READ_xnu:
			mode += 'r'

			// On some systems when the read end of a pipe is closed
			// the write end will not get a _EVFILT_WRITE event, but
			// will get a _EVFILT_READ event with EV_EOF set. Note
			// that setting 'w' here just means that we will wake up
			// a goroutine waiting to write; that goroutine will try
			// the write again, and the appropriate thing will happen
			// based on what that write returns (success, EPIPE,
			// EAGAIN).
			if ev.flags&_EV_EOF_xnu != 0 {
				mode += 'w'
			}
		case _EVFILT_WRITE_xnu:
			mode += 'w'
		}
		if mode != 0 {
			tp := taggedPointer(uintptr(unsafe.Pointer(ev.udata)))
			pd := (*pollDesc)(tp.pointer())
			tag := tp.tag()
			if pd.fdseq.Load() != tag {
				continue
			}
			pd.setEventErr(ev.flags == _EV_ERROR_xnu, tag)
			delta += netpollready(&toRun, pd, mode)
		}
	}
	xnuPollDone.Add(1)
	return toRun, delta
}
