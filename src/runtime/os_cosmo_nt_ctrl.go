// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Process control for the syscall-emulation layer: sched_setaffinity,
// sched_getaffinity, getpriority, setpriority, and the SIGSTOP/SIGCONT pair.
// The Win32 mapping follows cosmo libc's precedent (libc/proc/sched_*.c and
// setpriority-nt.c), including the reach it gives a caller. A pid is
// addressable when it names a process this runtime started.

package runtime

import "unsafe"

// The priority classes of libc/nt/enum/processcreationflags.h. They are what
// SetPriorityClass takes, which is why the emulation maps a nice value onto a
// class rather than onto a per-thread priority.
const (
	ntIdlePriorityClass        = 0x00000040
	ntBelowNormalPriorityClass = 0x00004000
	ntNormalPriorityClass      = 0x00000020
	ntAboveNormalPriorityClass = 0x00008000
	ntHighPriorityClass        = 0x00000080
	ntRealtimePriorityClass    = 0x00000100

	// _NT_PRIO_PROCESS is PRIO_PROCESS, the only selection getpriority and setpriority take here.
	_NT_PRIO_PROCESS = 0
)

// ntProcHandle resolves a pid to a handle. A zero pid means this process;
// anything else must name a process this runtime started.
func ntProcHandle(pid int32) (uintptr, bool) {
	if pid == 0 {
		return _NT_CURRENT_PROCESS, true
	}
	if pid < 0 {
		return 0, false
	}
	return ntProcFind(uint32(pid))
}

// ntStatusErrno names the errno a failed NTSTATUS call deserves.
func ntStatusErrno(status int32) uintptr {
	switch uint32(status) {
	case 0xC0000022: // STATUS_ACCESS_DENIED
		return ntEPERM
	case 0xC000010A: // STATUS_PROCESS_IS_TERMINATING
		return ntESRCH
	}
	return ntEIO
}

// ntPriorityClass maps a nice value onto a priority class, the way cosmo's
// sys_setpriority_nt does.
func ntPriorityClass(nice int32) uintptr {
	switch {
	case nice <= -15:
		return ntRealtimePriorityClass
	case nice <= -9:
		return ntHighPriorityClass
	case nice <= -3:
		return ntAboveNormalPriorityClass
	case nice <= 3:
		return ntNormalPriorityClass
	case nice <= 12:
		return ntBelowNormalPriorityClass
	}
	return ntIdlePriorityClass
}

// ntNice reads a priority class back as a nice value.
func ntNice(class uintptr) int32 {
	switch class {
	case ntRealtimePriorityClass:
		return -16
	case ntHighPriorityClass:
		return -10
	case ntAboveNormalPriorityClass:
		return -5
	case ntNormalPriorityClass:
		return 0
	case ntBelowNormalPriorityClass:
		return 5
	case ntIdlePriorityClass:
		return 15
	}
	return 0
}

// ntEmuSchedSetaffinity implements sched_setaffinity(2). Linux passes a
// cpu_set_t the size of a page of bits; the host keeps one machine word.
func ntEmuSchedSetaffinity(pid int32, size uintptr, mask *uint64) (r1, r2, errno uintptr) {
	if size < 8 || mask == nil {
		return ntFail3(ntEINVAL)
	}
	h, ok := ntProcHandle(pid)
	if !ok {
		return ntFail3(ntESRCH)
	}
	if r, werr := ntcallE(ntSetProcessAffinityMaskFn, h, uintptr(*mask), 0, 0, 0, 0, 0); r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ntEmuSchedGetaffinity implements sched_getaffinity(2), which reports how
// many bytes it filled.
func ntEmuSchedGetaffinity(pid int32, size uintptr, mask *uint64) (r1, r2, errno uintptr) {
	if size < 8 || mask == nil {
		return ntFail3(ntEINVAL)
	}
	h, ok := ntProcHandle(pid)
	if !ok {
		return ntFail3(ntESRCH)
	}
	var system uint64
	if r, werr := ntcallE(ntGetProcessAffinityMaskFn, h, uintptr(unsafe.Pointer(mask)),
		uintptr(unsafe.Pointer(&system)), 0, 0, 0, 0); r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 8, 0, 0
}

// ntEmuGetpriority implements getpriority(2). Only PRIO_PROCESS names a
// process, which is what this host reaches.
func ntEmuGetpriority(which, who int32) (r1, r2, errno uintptr) {
	if which != _NT_PRIO_PROCESS {
		return ntFail3(ntEINVAL)
	}
	h, ok := ntProcHandle(who)
	if !ok {
		return ntFail3(ntESRCH)
	}
	class, werr := ntcallE(ntGetPriorityClassFn, h, 0, 0, 0, 0, 0, 0)
	if class == 0 {
		return ntFail3(ntErrno(werr))
	}
	return uintptr(uint32(ntNice(class))), 0, 0
}

// ntEmuSetpriority implements setpriority(2), the same way.
func ntEmuSetpriority(which, who, nice int32) (r1, r2, errno uintptr) {
	if which != _NT_PRIO_PROCESS {
		return ntFail3(ntEINVAL)
	}
	h, ok := ntProcHandle(who)
	if !ok {
		return ntFail3(ntESRCH)
	}
	if r, werr := ntcallE(ntSetPriorityClassFn, h, ntPriorityClass(nice), 0, 0, 0, 0, 0); r == 0 {
		return ntFail3(ntErrno(werr))
	}
	return 0, 0, 0
}

// ntEmuSuspendProcess stops every thread a process has, which is what SIGSTOP
// asks for. A host whose ntdll does not carry the call answers ENOSYS, so a
// caller can tell "not supported" from "done".
func ntEmuSuspendProcess(h uintptr) (r1, r2, errno uintptr) {
	if ntNtSuspendProcessFn == 0 {
		return ntFail3(ntENOSYS)
	}
	if status := int32(uint32(ntcall(ntNtSuspendProcessFn, h, 0, 0, 0, 0, 0))); status < 0 {
		return ntFail3(ntStatusErrno(status))
	}
	return 0, 0, 0
}

// ntEmuResumeProcess lets those threads run again, which is what SIGCONT asks
// for.
func ntEmuResumeProcess(h uintptr) (r1, r2, errno uintptr) {
	if ntNtResumeProcessFn == 0 {
		return ntFail3(ntENOSYS)
	}
	if status := int32(uint32(ntcall(ntNtResumeProcessFn, h, 0, 0, 0, 0, 0))); status < 0 {
		return ntFail3(ntStatusErrno(status))
	}
	return 0, 0, 0
}
