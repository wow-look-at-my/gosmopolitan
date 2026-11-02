// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

// perThreadSyscallArgs contains the system call number, arguments.

package runtime

import (
	"internal/goarch"
	"internal/runtime/atomic"
	"internal/runtime/syscall/cosmo"
	_ "unsafe"
)

// perThreadSyscallArgs contains the system call number, arguments.
type perThreadSyscallArgs struct {
	trap uintptr
	a1   uintptr
	a2   uintptr
	a3   uintptr
	a4   uintptr
	a5   uintptr
	a6   uintptr
	r1   uintptr
	r2   uintptr
}

var perThreadSyscall perThreadSyscallArgs

//go:nosplit
func runPerThreadSyscall() {
	gp := getg()
	if atomic.Load(&gp.m.needPerThreadSyscall) == 0 {
		return
	}

	args := perThreadSyscall
	r1, r2, errno := cosmo.Syscall6(args.trap, args.a1, args.a2, args.a3, args.a4, args.a5, args.a6)
	if goarch.IsPpc64 == 1 || goarch.IsPpc64le == 1 {
		r2 = 0
	}
	if errno != 0 || r1 != args.r1 || r2 != args.r2 {
		print("trap:", args.trap, ", a123456=[", args.a1, ",", args.a2, ",", args.a3, ",", args.a4, ",", args.a5, ",", args.a6, "]\n")
		print("results: got {r1=", r1, ",r2=", r2, ",errno=", errno, "}, want {r1=", args.r1, ",r2=", args.r2, ",errno=0}\n")
		fatal("AllThreadsSyscall6 results differ between threads; runtime corrupted")
	}

	atomic.Store(&gp.m.needPerThreadSyscall, 0)
	futexwakeup(&gp.m.needPerThreadSyscall, 1)
}

// syscall_runtime_doAllThreadsSyscall executes a system call on every M.
// It is the linux port's driver over the cosmo syscall entry and rests
// on the same properties os_linux.go documents.
//
// On a darwin or NT host the call runs on the calling thread alone. XNU
// keeps credentials per process, so one call is the process-wide change
// the caller asked for. Neither host can deliver sigPerThreadSyscall to
// another thread - darwinSignalM drops the realtime range and NT has no
// cross-thread signal - so the wait below would. Never end there.
//
//go:linkname syscall_runtime_doAllThreadsSyscall syscall.runtime_doAllThreadsSyscall
//go:uintptrescapes
func syscall_runtime_doAllThreadsSyscall(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, err uintptr) {
	if isdarwin() || iswindows() {
		return cosmo.Syscall6(trap, a1, a2, a3, a4, a5, a6)
	}

	// STW so user goroutines see an atomic change to thread state.
	stw := stopTheWorld(stwAllThreadsSyscall)

	// allocmLock prevents new Ms while this runs, and serializes callers.
	allocmLock.lock()
	acquirem()

	r1, r2, errno := cosmo.Syscall6(trap, a1, a2, a3, a4, a5, a6)
	if errno != 0 {
		releasem(getg().m)
		allocmLock.unlock()
		startTheWorld(stw)
		return r1, r2, errno
	}

	perThreadSyscall = perThreadSyscallArgs{
		trap: trap,
		a1:   a1,
		a2:   a2,
		a3:   a3,
		a4:   a4,
		a5:   a5,
		a6:   a6,
		r1:   r1,
		r2:   r2,
	}

	// Wait for every thread to set procid before any signal goes out.
	atomic.Store(&threadStart.waiting, 1)
	for mp := allm; mp != nil; mp = mp.alllink {
		threadStartWait(mp)
	}
	atomic.Store(&threadStart.waiting, 0)

	gp := getg()
	tid := gp.m.procid
	for mp := allm; mp != nil; mp = mp.alllink {
		if atomic.Load64(&mp.procid) == tid {
			continue
		}
		atomic.Store(&mp.needPerThreadSyscall, 1)
		signalM(mp, sigPerThreadSyscall)
	}

	// runPerThreadSyscall clears each flag and wakes its word.
	for mp := allm; mp != nil; mp = mp.alllink {
		if mp.procid == tid {
			continue
		}
		for atomic.Load(&mp.needPerThreadSyscall) != 0 {
			futexsleep(&mp.needPerThreadSyscall, 1, -1)
		}
	}

	perThreadSyscall = perThreadSyscallArgs{}

	releasem(getg().m)
	allocmLock.unlock()
	startTheWorld(stw)

	return r1, r2, errno
}
