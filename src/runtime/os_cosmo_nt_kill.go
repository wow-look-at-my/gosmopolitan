// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The kill/tkill/tgkill dispatcher cases: a Linux signal send turned
// into an NT action. These sit with the syscall-emulation layer they
// belong to (spawn table, errno mapping), which is amd64 only. The
// signal machinery itself is architecture-neutral and lives in
// os_cosmo_nt_sig.go.

package runtime

// GenerateConsoleCtrlEvent ctrl-type ids (winbase.h).
const (
	_NT_CTRL_C_EVENT     = 0
	_NT_CTRL_BREAK_EVENT = 1
)

// ntEmuKill implements kill(2) (dispatcher case, os_cosmo_nt_sys.go).
// pid == self delivers on the calling thread; a pid from the chunk-B
// spawn table terminates that child with the encoded status;
// pid < -1 addresses a process GROUP we created (wave 3 item 4,
// ntEmuKillGroup below). Anything else is ESRCH: unrelated processes
// are not addressable (we only hold handles for our own children),
// and pid == 0 (the caller's own group) and pid == -1 (broadcast)
// have no NT projection - this process is not a group we created, so
// both keep the pre-wave-3 ESRCH.
func ntEmuKill(pid, sig int32) (r1, r2, errno uintptr) {
	if sig < 0 || sig >= _NSIG {
		return ntFail3(ntEINVAL)
	}
	self := int32(uint32(ntcall(ntGetCurrentProcessIdFn, 0, 0, 0, 0, 0, 0)))
	if pid == self {
		if eno := ntKillSelf(uint32(sig)); eno != 0 {
			return ntFail3(eno)
		}
		return 0, 0, 0
	}
	if pid < -1 {
		return ntEmuKillGroup(uint32(-pid), sig)
	}
	if pid <= 0 {
		return ntFail3(ntESRCH)
	}
	h, ok := ntProcFind(uint32(pid))
	if !ok {
		return ntFail3(ntESRCH)
	}
	if sig == 0 {
		return 0, 0, 0 // existence probe
	}
	// Terminate the child with the fork's encoded signal status;
	// chunk B's wait4 decodes it into "killed by signal sig". Best
	// effort: TerminateProcess on an already-exited child fails, and
	// Linux kill on a zombie succeeds, so the result is not
	// surfaced. The handle stays in the table - wait4 still reaps.
	ntcall(ntTerminateProcessFn, h, _NT_SIGDEATH_BASE|uintptr(uint32(sig)), 0, 0, 0, 0)
	return 0, 0, 0
}

// ntEmuKillGroup implements kill(-pgid, sig): signal a whole process
// group. Only groups WE created are addressable - pgid must be the
// pid of a spawned child launched with CREATE_NEW_PROCESS_GROUP
// (SysProcAttr{Setpgid: true} through ntForkExec/ntSpawn); everything
// else is ESRCH, mirroring the own-children-only rule of the
// positive-pid arm.
//
//   - SIGQUIT -> GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pgid):
//     THE reliably deliverable group-targeted console event (upstream
//     Go's own TestCtrlBreak uses exactly this pairing). In a cosmo
//     child the injected handler maps CTRL_BREAK back to SIGQUIT,
//     completing the Linux-shaped round trip.
//   - SIGINT -> GenerateConsoleCtrlEvent(CTRL_C_EVENT, pgid),
//     best-effort: NT creates CREATE_NEW_PROCESS_GROUP children with
//     Ctrl-C DISABLED until they opt back in (SetConsoleCtrlHandler
//     (NULL, FALSE)), so delivery to a child that never re-enabled it
//     silently no-ops. Upstream windows Go has the identical hole;
//     callers wanting a reliable group chord send SIGQUIT.
//   - sig 0 -> existence probe.
//   - any other sig -> TerminateProcess(leader, encoded status): no
//     NT API delivers arbitrary signals group-wide, so group-kill
//     degrades to leader-kill - the leader is the group's one member
//     we know of (documented in DEBUGGING.md wave 3 item 4). Same
//     best-effort result discipline as the positive arm (a dead
//     leader is the Linux kill-a-zombie success).
//
// A GenerateConsoleCtrlEvent failure (e.g. no console attached)
// surfaces as the mapped errno from the trampoline-captured last
// error.
func ntEmuKillGroup(pgid uint32, sig int32) (r1, r2, errno uintptr) {
	h, ok := ntProcFindGroup(pgid)
	if !ok {
		return ntFail3(ntESRCH)
	}
	switch sig {
	case 0:
		return 0, 0, 0 // existence probe
	case _SIGINT, _SIGQUIT:
		ev := uintptr(_NT_CTRL_BREAK_EVENT)
		if sig == _SIGINT {
			ev = _NT_CTRL_C_EVENT
		}
		if r, werr := ntcallE(ntGenerateConsoleCtrlEventFn, ev, uintptr(pgid), 0, 0, 0, 0, 0); r == 0 {
			return ntFail3(ntErrno(werr))
		}
		return 0, 0, 0
	}
	ntcall(ntTerminateProcessFn, h, _NT_SIGDEATH_BASE|uintptr(uint32(sig)), 0, 0, 0, 0)
	return 0, 0, 0
}

// ntEmuTkill implements tkill(2). Only the calling thread is
// addressable in chunk D1: cross-thread delivery needs the
// SuspendThread machinery (chunk D2's preemptM), and every
// process-level observable (os/signal, signal deaths) is
// thread-agnostic anyway. The runtime's own signalM stays gated off
// on NT, so nothing in-tree sends cross-thread.
func ntEmuTkill(tid, sig int32) (r1, r2, errno uintptr) {
	if sig < 0 || sig >= _NSIG {
		return ntFail3(ntEINVAL)
	}
	cur := int32(uint32(ntcall(ntGetCurrentThreadIdFn, 0, 0, 0, 0, 0, 0)))
	if tid != cur {
		return ntFail3(ntESRCH)
	}
	if eno := ntKillSelf(uint32(sig)); eno != 0 {
		return ntFail3(eno)
	}
	return 0, 0, 0
}

// ntEmuTgkill implements tgkill(2): tgid must be this process.
func ntEmuTgkill(tgid, tid, sig int32) (r1, r2, errno uintptr) {
	self := int32(uint32(ntcall(ntGetCurrentProcessIdFn, 0, 0, 0, 0, 0, 0)))
	if tgid != self {
		return ntFail3(ntESRCH)
	}
	return ntEmuTkill(tid, sig)
}
