// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The Windows NT netpoller (wave 2 chunk C): WSAPoll readiness over
// the netpoll_aix.go two-lock, level-triggered design. This replaces
// chunk A's timer-only WaitOnAddress stub wholesale.
//
// Why WSAPoll and not upstream's IOCP netpoll: IOCP reports the
// COMPLETION of a previously submitted OVERLAPPED operation, which
// only works with internal/poll's windows-specific execIO machinery.
// The fork's internal/poll is linux-shaped - nonblocking fds, EAGAIN,
// wait-for-readiness, retry - and WSAPoll is the one winsock call
// that answers the readiness question directly.
//
// Shape (netpoll_aix.go, kept line-for-line where possible):
//
//   - Fixed arrays of WSAPOLLFD / *pollDesc / emulated-fd numbers.
//     Fixed, not slices: netpollinit and netpollopen can run under
//     runtime locks (netpollGenericInit fires from the first timer's
//     addHeap), so nothing here allocates. pd.user holds the fd's
//     slot index, maintained across swap-deletes.
//   - Two-lock protocol: mutators (open/close/arm) take ntMtxpoll,
//     poke the poller off its blocking WSAPoll (one byte to the wake
//     socket, deduplicated by ntPollPendingUpdates), then take
//     ntMtxset - which the poller holds across WSAPoll - and release
//     ntMtxpoll. The poller re-takes both at the top of each cycle.
//   - Level-triggered: delivery CLEARS the direction bit from
//     events; pollWait re-arms each wait via netpollarm
//     (netpollLevelTriggered, set here at init). netpoll(0) returns
//     empty like AIX - a nonblocking check would contend ntMtxset
//     with the blocked poller - which sysmon and findrunnable
//     tolerate.
//   - The wakeup channel is a connected loopback TCP pair: sends on
//     ntWakeSock (the client end) surface as readability on
//     ntWakeRecv (the accepted end), which sits at slot 0. WSAPoll
//     has no pipe/eventfd concept, and the transport MUST be
//     lossless: chunk C shipped a self-connected UDP socket here,
//     and windows-latest disproved it - UDP, loopback included, may
//     drop datagrams on real NT (wine's in-process loopback never
//     does), and one dropped wake byte leaves the poller asleep for
//     its full WSAPoll timeout while every mutator (netpollopen/arm/
//     close block on ntMtxset below) and every new-earlier timer
//     (wakeNetPoller -> netpollBreak) waits it out - the observed
//     ~5s windows-latest probe stalls, rescued only by whatever
//     stale deadline the poller already held. TCP retransmits, so a
//     wake byte cannot be lost; upstream's netpollBreak transports
//     are lossless for the same reason (windows uses
//     PostQueuedCompletionStatus, aix a pipe). netpollBreak sends
//     one byte, guarded by the shared netpollWakeSig dedup; the
//     poller drains with nonblocking recvs when it blocks.
//
// WSAPOLLFD is NOT the Linux pollfd: the fd is a SOCKET (8 bytes on
// win64), so the struct is 16 bytes, and the POLL* constants have
// different values. Only POLLRDNORM/POLLWRNORM may be REQUESTED
// (WSAPoll rejects POLLERR/POLLHUP/POLLPRI in events with WSAEINVAL);
// ERR/HUP/NVAL are revents-only and always reported.
//
// The wave-9 forensic counters (xnuPoll*, netpoll_cosmo_xnu.go) are
// fed from this poller too - exactly one host poller is live per
// process - so a stall names itself through cosmoNetpollDiag in the
// runtimeprobe watchdog output on NT as well.

package runtime

import "unsafe"

// ntWSAPollFD mirrors WSAPOLLFD on win64: SOCKET, then two SHORTs,
// padded to 16 bytes (Go and MSVC agree on this layout).
type ntWSAPollFD struct {
	fd      uintptr
	events  int16
	revents int16
}

// Winsock POLL* values (NOT the Linux ones).
const (
	_NT_POLLRDNORM = 0x0100
	_NT_POLLRDBAND = 0x0200
	_NT_POLLWRNORM = 0x0010
	_NT_POLLERR    = 0x0001
	_NT_POLLHUP    = 0x0002
	_NT_POLLNVAL   = 0x0004
)

// One slot per possible socket fd plus the wake socket: the fd table
// caps live sockets at ntFDMax, so registration can never overflow.
const ntPollMax = ntFDMax + 1

var (
	ntPollFds  [ntPollMax]ntWSAPollFD
	ntPollPds  [ntPollMax]*pollDesc
	ntPollNums [ntPollMax]int32 // emulated fd numbers (netpollclose is keyed by fd)
	ntPollLen  int32            // live slots, including slot 0

	ntMtxpoll            mutex
	ntMtxset             mutex
	ntPollPendingUpdates int32

	ntWakeSock uintptr // loopback TCP wake pair: send (client) end
	ntWakeRecv uintptr // loopback TCP wake pair: polled (accepted) end, slot 0
	ntWakeByte = [1]byte{'x'}
	ntDrainBuf [16]byte
)

func netpollinitNT() {
	if eno := ntWinsockEnsure(); eno != 0 {
		println("runtime: netpollinit: winsock unavailable, errno", eno)
		throw("runtime: netpollinit failed")
	}
	// Build the lossless TCP wake pair (see the file comment) with
	// the shared recipe - ntLoopbackTCPPair, os_cosmo_nt_sock.go,
	// which the wave-3 socketpair emulation reuses. Blocking,
	// TCP_NODELAY, uninheritable, peer-verified.
	a, c, step, werr := ntLoopbackTCPPair()
	if werr != 0 {
		println("runtime: netpollinit: wake pair", step, "failed with", werr)
		throw("runtime: netpollinit failed")
	}
	// Both ends nonblocking: the send side must never wedge a
	// mutator, the recv side is drained opportunistically.
	var one uint32 = 1
	if r, werr := ntcallE(ntWSAIoctlsocketFn, c, _NT_FIONBIO,
		uintptr(unsafe.Pointer(&one)), 0, 0, 0, 0); ntSockErr(r) {
		println("runtime: netpollinit: wake FIONBIO failed with", werr)
		throw("runtime: netpollinit failed")
	}
	one = 1
	if r, werr := ntcallE(ntWSAIoctlsocketFn, a, _NT_FIONBIO,
		uintptr(unsafe.Pointer(&one)), 0, 0, 0, 0); ntSockErr(r) {
		println("runtime: netpollinit: wake recv FIONBIO failed with", werr)
		throw("runtime: netpollinit failed")
	}
	ntWakeSock = c
	ntWakeRecv = a
	ntPollFds[0] = ntWSAPollFD{fd: a, events: _NT_POLLRDNORM}
	ntPollPds[0] = nil
	ntPollNums[0] = -1
	ntPollLen = 1
	// WSAPoll is level-triggered: pollWait must arm the awaited
	// direction on every wait (netpoll.go).
	netpollLevelTriggered = true
}

// ntNetpollwakeup pokes the poller off its blocking WSAPoll before a
// descriptor-set change. Caller holds ntMtxpoll.
func ntNetpollwakeup() {
	if ntPollPendingUpdates == 0 {
		ntPollPendingUpdates = 1
		r, werr := ntcallE(ntWSASendFn, ntWakeSock, uintptr(unsafe.Pointer(&ntWakeByte[0])), 1, 0, 0, 0, 0)
		if ntSockErr(r) {
			// Should be impossible (1-byte send on a healthy connected
			// loopback socket). If it ever fires, the poller may sleep
			// its full timeout - name the cause instead of wedging
			// silently.
			println("runtime: netpoll wakeup send failed with", werr)
		}
	}
}

// netpollopenNT registers fd. ONLY socket-kind fds are accepted:
// pipes and files must keep failing here so internal/poll falls back
// to blocking mode for them - that fallback is load-bearing for exec
// stdio (chunk B) - and WSAPoll would report them POLLNVAL anyway.
func netpollopenNT(fd uintptr, pd *pollDesc) uintptr {
	e, ok := ntFDLookup(int32(fd))
	if !ok || e.kind != ntFDSocket {
		return 38 // ENOSYS -> pd.pollable() == false -> blocking mode
	}
	lock(&ntMtxpoll)
	ntNetpollwakeup()

	lock(&ntMtxset)
	unlock(&ntMtxpoll)

	if ntPollLen >= ntPollMax {
		unlock(&ntMtxset)
		return 24 // EMFILE (unreachable: the fd table is smaller)
	}
	i := ntPollLen
	pd.user = uint32(i)
	ntPollFds[i] = ntWSAPollFD{fd: e.handle} // armed later by netpollarm
	ntPollNums[i] = int32(fd)
	ntPollPds[i] = pd
	ntPollLen++
	unlock(&ntMtxset)
	return 0
}

func netpollcloseNT(fd uintptr) uintptr {
	lock(&ntMtxpoll)
	ntNetpollwakeup()

	lock(&ntMtxset)
	unlock(&ntMtxpoll)

	for i := int32(1); i < ntPollLen; i++ {
		if ntPollNums[i] == int32(fd) {
			last := ntPollLen - 1
			ntPollFds[i] = ntPollFds[last]
			ntPollNums[i] = ntPollNums[last]
			ntPollPds[i] = ntPollPds[last]
			ntPollPds[i].user = uint32(i)
			ntPollPds[last] = nil
			ntPollLen--
			break
		}
	}
	unlock(&ntMtxset)
	return 0
}

func netpollarmNT(pd *pollDesc, mode int) {
	lock(&ntMtxpoll)
	ntNetpollwakeup()

	lock(&ntMtxset)
	unlock(&ntMtxpoll)

	// ntMtxset protects against stale pollDescs, so pd.user is valid
	// here (aix precedent).
	switch mode {
	case 'r':
		ntPollFds[pd.user].events |= _NT_POLLRDNORM
	case 'w':
		ntPollFds[pd.user].events |= _NT_POLLWRNORM
	}
	unlock(&ntMtxset)
}

// netpollBreakNT interrupts a blocking WSAPoll. The netpollWakeSig
// dedup happened in netpollBreak (netpoll_cosmo.go).
func netpollBreakNT() {
	if ntWakeSock == 0 {
		return
	}
	r, werr := ntcallE(ntWSASendFn, ntWakeSock, uintptr(unsafe.Pointer(&ntWakeByte[0])), 1, 0, 0, 0, 0)
	if ntSockErr(r) {
		// A lost break leaves the poller asleep until its current
		// WSAPoll timeout: timers added after the failed send fire
		// late (rescued only by whatever deadline the poller is
		// already waiting on). See ntNetpollwakeup.
		println("runtime: netpollBreak send failed with", werr)
	}
}

// netpollNT is the NT leg of netpoll: one WSAPoll cycle.
//
// delay < 0: blocks indefinitely; delay == 0: returns empty without
// polling (see the file comment); delay > 0: blocks up to that many
// nanoseconds.
func netpollNT(delay int64) (gList, int32) {
	var timeoutMs int32
	if delay < 0 {
		timeoutMs = -1
	} else if delay == 0 {
		return gList{}, 0
	} else if delay < 1e6 {
		timeoutMs = 1
	} else if delay < 1e15 {
		timeoutMs = int32(delay / 1e6)
	} else {
		// An arbitrary cap on how long to wait for a timer.
		// 1e9 ms == ~11.5 days.
		timeoutMs = 1e9
	}

	lock(&ntMtxpoll)
	lock(&ntMtxset)
	ntPollPendingUpdates = 0
	unlock(&ntMtxpoll)

	// Forensic counters (see netpoll_cosmo_xnu.go): noise next to the
	// foreign call, gold when a CI wedge needs a name.
	xnuPollCycles.Add(1)
	xnuPollEnterNs.Store(nanotime())
	r := ntcall(ntWSAPollFn, uintptr(unsafe.Pointer(&ntPollFds[0])),
		uintptr(uint32(ntPollLen)), uintptr(uint32(timeoutMs)), 0, 0, 0)
	n := int32(uint32(r))
	var werr uintptr
	if n < 0 {
		// The ntcall6 trampoline captured this thread's last error
		// into m.ntLastError atomically with the call (chunk D2) -
		// exact even with SuspendThread preemption live.
		werr = uintptr(getg().m.ntLastError)
	}
	xnuPollExitNs.Store(nanotime())
	xnuPollDone.Add(1)
	xnuPollLastN.Store(n)
	xnuPollLastE.Store(int32(werr))
	if n < 0 {
		println("runtime: WSAPoll failed with", werr, "nfds", ntPollLen)
		unlock(&ntMtxset)
		throw("runtime: netpoll failed")
	}
	if n > 0 && ntPollFds[0].revents&(_NT_POLLRDNORM|_NT_POLLERR|_NT_POLLHUP) != 0 {
		ntPollFds[0].revents = 0
		if delay != 0 {
			// A wakeup byte could be picked up by a nonblocking
			// poll; only drain and reset when blocking (aix rule -
			// and netpollNT(0) never reaches here anyway).
			for {
				rr := int32(uint32(ntcall(ntWSARecvFn, ntWakeRecv,
					uintptr(unsafe.Pointer(&ntDrainBuf[0])), uintptr(len(ntDrainBuf)), 0, 0, 0)))
				if rr <= 0 {
					break
				}
			}
			netpollWakeSig.Store(0)
		}
		n--
	}
	var toRun gList
	delta := int32(0)
	for i := int32(1); i < ntPollLen && n > 0; i++ {
		pfd := &ntPollFds[i]
		re := pfd.revents
		if re == 0 {
			continue
		}
		// POLLNVAL means the SOCKET died underneath us (a close
		// racing registration); disarm both directions and wake both
		// sides with the error bit set, or an armed dead slot would
		// spin WSAPoll forever.
		var mode int32
		if re&(_NT_POLLRDNORM|_NT_POLLRDBAND|_NT_POLLHUP|_NT_POLLERR|_NT_POLLNVAL) != 0 {
			mode += 'r'
			pfd.events &^= _NT_POLLRDNORM
		}
		if re&(_NT_POLLWRNORM|_NT_POLLHUP|_NT_POLLERR|_NT_POLLNVAL) != 0 {
			mode += 'w'
			pfd.events &^= _NT_POLLWRNORM
		}
		if mode != 0 {
			ntPollPds[i].setEventErr(re == _NT_POLLERR || re == _NT_POLLNVAL, 0)
			delta += netpollready(&toRun, ntPollPds[i], mode)
		}
		pfd.revents = 0
		n--
	}
	unlock(&ntMtxset)
	return toRun, delta
}
