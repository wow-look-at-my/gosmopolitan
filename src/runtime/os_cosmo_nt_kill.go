// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

// The kill/tkill/tgkill dispatcher cases: a Linux signal send turned into an
// NT action. These sit with the syscall-emulation layer they belong to (spawn
// table, errno mapping), which is amd64 only. The signal machinery itself is
// architecture-neutral and lives in os_cosmo_nt_sig.go.

package runtime

// GenerateConsoleCtrlEvent ctrl-type ids (winbase.h).
const (
	_NT_CTRL_C_EVENT     = 0
	_NT_CTRL_BREAK_EVENT = 1
)

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
	switch sig {
	case _SIGSTOP, _SIGTSTP, _SIGTTIN, _SIGTTOU:
		// A stop signal stops the process; it does not end it.
		return ntEmuSuspendProcess(h)
	case _SIGCONT:
		return ntEmuResumeProcess(h)
	}
	// Terminate the child with the fork's encoded signal status; chunk B's wait4 decodes it into "killed by signal sig".
	ntcall(ntTerminateProcessFn, h, _NT_SIGDEATH_BASE|uintptr(uint32(sig)), 0, 0, 0, 0)
	return 0, 0, 0
}

// ntEmuKillGroup implements kill(-pgid, sig). Only a group WE created is
// addressable: pgid must be a child spawned with CREATE_NEW_PROCESS_GROUP.
// Anything else is ESRCH, mirroring the own-children-only rule of the
// positive-pid arm. Each signal is handled on the case that maps it. SIGQUIT
// is the reliable group chord, because NT creates such a child with Ctrl-C
// DISABLED until it opts back in, so a SIGINT to one that never did silently
// no-ops - upstream windows Go has the identical hole.
func ntEmuKillGroup(pgid uint32, sig int32) (r1, r2, errno uintptr) {
	h, ok := ntProcFindGroup(pgid)
	if !ok {
		return ntFail3(ntESRCH)
	}
	switch sig {
	case 0:
		return 0, 0, 0 // existence probe
	case _SIGINT, _SIGQUIT:
		// A cosmo child's injected handler maps CTRL_BREAK back to SIGQUIT, which completes the Linux-shaped round trip.
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

// ntEmuTkill implements tkill(2). Only the calling thread is addressable in
// chunk D1: cross-thread delivery needs the SuspendThread machinery (chunk
// D2's preemptM), and every process-level observable (os/signal, signal
// deaths) is thread-agnostic anyway.
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
