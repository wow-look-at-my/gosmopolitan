// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Windows NT syscall emulation hook. No raw SYSCALL may execute on an
// NT host: the assembly Syscall6 dispatcher answers ENOSYS for
// everything when __hostos is Windows, and the syscall package routes
// emulated calls through this table. Mirrors the DarwinFns pattern,
// except the emulation is a Go function rather than raw C pointers.
// Unlike the darwin path, the NT emulation runs OUTSIDE the
// entersyscall window: Syscall and Syscall6 skip entersyscall when this
// table is installed, so Emulate is ordinary Go that may allocate, grow
// the stack and block, and it brackets its own blocking Win32 calls.
// That is what makes path translation and struct synthesis possible.
// Darwin has no such problem: XNU consumes the caller's strings and
// buffers directly, so its translations fit nosplit.

// WindowsFns is the NT emulation table. A nil table (any host but NT)
// means no routing: callers fall through to the assembly dispatcher.
type WindowsFns struct {
	// Emulate performs the given Linux-NUMBERED syscall using Win32
	// primitives, answering (r1, r2, errno) with a positive LINUX
	// errno. runtime.ntSetSyscallFns installs it and
	// runtime/os_cosmo_nt_sys.go holds the dispatcher.
	//
	// Contract: the dispatcher entry is nosplit, as is the caller chain
	// from syscall.Syscall down to it, and it converts every
	// pointer-carrying uintptr argument to a real pointer type before
	// any stack growth can occur. An argument may point into the
	// calling goroutine's stack, and a raw uintptr is not adjusted when
	// that stack moves.
	Emulate func(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)

	// Spawn launches a child through CreateProcessW: the
	// posix_spawn-shaped seam forkAndExecInChild's NT branch calls
	// instead of fork+execve, which cannot be emulated
	// syscall-by-syscall. argv0 and dir are linux-shaped paths the
	// runtime translates. cmdline and env are ready-made UTF-16 blocks,
	// because the syscall layer owns the Windows quoting and env-sorting
	// algebra. stdio are parent fds for the child's std handles, -1 for
	// none. It answers the child pid or a positive Linux errno, and it
	// is ordinary Go: the caller is not in syscall state.
	Spawn func(argv0, dir string, cmdline, env []uint16, stdio [3]int32, flags uint32) (pid int32, errno uintptr)
}

// Spawn flag bits. An abstraction over the CreateProcessW creation
// flags so the Win32 constants stay inside the runtime.
const (
	// SpawnNewProcessGroup makes the child the leader of a NEW NT
	// process group (CreateProcessW's CREATE_NEW_PROCESS_GROUP) - the
	// NT projection of SysProcAttr{Setpgid: true, Pgid: 0}. The new
	// group's id equals the child's pid, so the emulated
	// kill(-pid, sig) can target it (GenerateConsoleCtrlEvent).
	SpawnNewProcessGroup uint32 = 1 << 0
)

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
