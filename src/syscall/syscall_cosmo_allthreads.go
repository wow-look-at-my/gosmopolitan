// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

// The all-threads syscall pair. GOOS=cosmo satisfies the linux build tag, so
// a program written against the linux port names these, and the linux port
// declares them in syscall_linux.go, which cosmo does not build.
//
// Both answer ENOTSUP here. The linux port runs the syscall on every thread
// through runtime_doAllThreadsSyscall, and that entry point does not exist
// for cosmo: the runtime side lives in runtime/os_linux.go, which cosmo does
// not build either. ENOTSUP is the answer the linux port itself gives when it
// cannot reach every thread, so a caller that handles the cgo case already
// handles this one.
//
// A caller that needs the effect must apply it per thread itself, with
// runtime.LockOSThread. Do not paper over this by running the syscall on the
// calling thread alone: that reports success for a process-wide change that
// did not happen.

// minus1 is the result the linux port reports alongside a refusal.
const minus1 = ^uintptr(0)

// AllThreadsSyscall would run a syscall on every OS thread of the Go runtime.
// cosmo has no all-threads mechanism, so it always fails with ENOTSUP and
// changes nothing.
//
//go:uintptrescapes
func AllThreadsSyscall(trap, a1, a2, a3 uintptr) (r1, r2 uintptr, err Errno) {
	return minus1, minus1, ENOTSUP
}

// AllThreadsSyscall6 is like [AllThreadsSyscall], but extended to six
// arguments. It fails the same way and for the same reason.
//
//go:uintptrescapes
func AllThreadsSyscall6(trap, a1, a2, a3, a4, a5, a6 uintptr) (r1, r2 uintptr, err Errno) {
	return minus1, minus1, ENOTSUP
}
