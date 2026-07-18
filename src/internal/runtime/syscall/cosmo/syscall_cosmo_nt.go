// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

import "unsafe"

// Windows NT syscall emulation hooks (wave 1).
//
// On NT hosts no raw SYSCALL may execute: the assembly Syscall6
// dispatcher returns ENOSYS for everything when __hostos is Windows
// (the safety net), and the syscall package routes the handful of
// emulated calls through this table instead. Mirrors the DarwinFns /
// SetDarwinFns pattern (syscall_cosmo_arm64.go), except the fields
// are Go funcs installed by the runtime rather than raw C pointers.
//
// Wave 1 covers exactly what fizzbuzz needs - Write (fmt output to
// fds 1/2) and Exit - with everything else failing loudly as ENOSYS.
// Later waves grow the table.

// WindowsFns is the NT emulation table. A nil table (any host but NT)
// means no routing: callers fall through to the assembly dispatcher.
type WindowsFns struct {
	Write func(fd int, p unsafe.Pointer, n int32) int32
	Exit  func(code int32)
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
func (f *WindowsFns) Syscall6(num, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2, errno uintptr) {
	switch num {
	case SYS_WRITE:
		n := f.Write(int(a1), unsafe.Pointer(a2), int32(a3))
		if n < 0 {
			return ^uintptr(0), 0, uintptr(-n)
		}
		return uintptr(n), 0, 0
	case SYS_EXIT, SYS_EXIT_GROUP:
		f.Exit(int32(a1))
		// Exit never returns; keep the compiler happy.
		return 0, 0, 0
	default:
		return ^uintptr(0), 0, 38 // ENOSYS
	}
}
