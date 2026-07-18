// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Windows NT syscall emulation hook (wave 1: write/exit; wave 2: the
// general Emulate dispatcher).
//
// On NT hosts no raw SYSCALL may execute: the assembly Syscall6
// dispatcher returns ENOSYS for everything when __hostos is Windows
// (the safety net), and the syscall package routes emulated calls
// through this table instead. Mirrors the DarwinFns / SetDarwinFns
// pattern (syscall_cosmo_arm64.go), except the emulation is a Go
// function installed by the runtime rather than raw C pointers.
//
// Unlike the darwin path, the NT emulation runs OUTSIDE the
// entersyscall/exitsyscall window: syscall.Syscall/Syscall6 skip
// entersyscall when this table is installed (see
// src/syscall/syscall_cosmo.go), so Emulate is ordinary Go code - it
// may allocate, grow the stack, and block - and it brackets the
// individual potentially-blocking Win32 calls with entersyscall
// itself (the cgocall model). That is what makes path translation
// (UTF-8 -> UTF-16) and Linux-struct synthesis (stat, dirent)
// implementable at all. The darwin emulation has no equivalent
// problem: XNU syscalls consume the caller's C strings and buffers
// directly, so its fixed-size translations fit the nosplit budget.

// WindowsFns is the NT emulation table. A nil table (any host but NT)
// means no routing: callers fall through to the assembly dispatcher.
type WindowsFns struct {
	// Emulate performs the given Linux-NUMBERED syscall using Win32
	// primitives, returning (r1, r2, errno) with a positive LINUX
	// errno. Installed by runtime.ntSetSyscallFns; the dispatcher and
	// the catalog of emulated calls live in
	// runtime/os_cosmo_nt_sys.go.
	//
	// Contract: the dispatcher entry is nosplit and converts every
	// pointer-carrying uintptr argument to a real pointer type before
	// any stack growth can occur (the caller chain from
	// syscall.Syscall down to the dispatcher is nosplit), because
	// arguments may point into the calling goroutine's stack and raw
	// uintptrs are not adjusted when the stack moves.
	Emulate func(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)

	// Spawn launches a child process via CreateProcessW
	// (runtime.ntSpawn) - the posix_spawn-shaped seam the syscall
	// package's forkAndExecInChild NT branch calls instead of
	// fork+execve, which cannot be emulated syscall-by-syscall.
	// argv0 and dir are linux-shaped paths (the runtime translates
	// them); cmdline and env are ready-made NUL-terminated /
	// double-NUL-terminated UTF-16 blocks (the syscall layer owns
	// the Windows quoting and env-sorting algebra, ported from
	// upstream exec_windows.go); stdio are parent fds for the
	// child's std handles, -1 = none. Returns the child pid or a
	// positive Linux errno. Ordinary Go code: may allocate and
	// block (no nosplit chain - the caller is not in syscall state).
	Spawn func(argv0, dir string, cmdline, env []uint16, stdio [3]int32) (pid int32, errno uintptr)
}

// windowsFns has static storage (no allocation at install time:
// SetWindowsFns runs before mallocinit).
var windowsFns WindowsFns
var haveWindowsFns bool

// SetWindowsFns installs the emulation table. Called once from
// runtime.osArchInit on NT hosts, before any user code runs. The
// table is copied, so the caller's struct may live on the stack.
func SetWindowsFns(f *WindowsFns) {
	windowsFns = *f
	haveWindowsFns = true
}

// Windows returns the installed table, or nil when not on an NT host.
//
//go:nosplit
func Windows() *WindowsFns {
	if !haveWindowsFns {
		return nil
	}
	return &windowsFns
}

// Syscall6 emulates the given Linux-numbered syscall via the table.
// Results follow the package convention: (r1, r2, errno) with a
// positive Linux errno.
//
// nosplit: callers hand over uintptr arguments that may point into
// their goroutine's stack; the chain must not grow the stack until
// the dispatcher has re-typed them as pointers (see Emulate).
//
//go:nosplit
func (f *WindowsFns) Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	if f.Emulate == nil {
		return ^uintptr(0), 0, 38 // ENOSYS
	}
	return f.Emulate(num, a1, a2, a3, a4, a5, a6)
}
