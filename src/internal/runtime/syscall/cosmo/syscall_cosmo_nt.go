// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

// Windows NT syscall emulation hook.

// WindowsFns is the NT emulation table. A nil table (any host but NT)
// means no routing: callers fall through to the assembly dispatcher.
type WindowsFns struct {
	// Emulate performs the given Linux-NUMBERED syscall using Win32 primitives.
	Emulate func(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr)

	// Spawn launches a child through CreateProcessW.
	Spawn func(argv0, dir string, cmdline, env []uint16, stdio [3]int32, flags uint32) (pid int32, errno uintptr)
}

// Spawn flag bits. An abstraction over the CreateProcessW creation
// flags so the Win32 constants stay inside the runtime.
const (
	// SpawnNewProcessGroup makes the child the leader of a NEW NT process group (CreateProcessW's CREATE_NEW_PROCESS_GROUP).
	SpawnNewProcessGroup uint32 = 1 << 0
)

// windowsFns has static storage (no allocation at install time: SetWindowsFns runs before mallocinit).
var windowsFns WindowsFns
var haveWindowsFns bool

// SetWindowsFns installs the emulation table. Called once from
// runtime.osArchInit on NT hosts, before any user code runs.
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

// Syscall6 emulates the given Linux-numbered syscall via the table. Results
// follow the package convention: (r1, r2, errno) with a positive Linux errno.
//
//go:nosplit
func (f *WindowsFns) Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	if f.Emulate == nil {
		return ^uintptr(0), 0, 38 // ENOSYS
	}
	return f.Emulate(num, a1, a2, a3, a4, a5, a6)
}
