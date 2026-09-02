// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// Process tracing. GOOS=cosmo presents the Linux ABI, so a program written
// against the linux port names these. The linux port declares them in
// syscall_linux.go, zsyscall_linux_amd64.go and zsyscall_linux_arm64.go,
// which cosmo does not build. The signatures and the bodies are those
// files', unchanged.
//
// A Linux kernel is the only host that serves SYS_PTRACE. The macOS
// emulation (internal/runtime/syscall/cosmo.syscall6SlowDarwin) and the
// Windows emulation (runtime.ntSyscallEmulate) have no case for this
// syscall number, so both answer ENOSYS.

// The ptrace requests, options and events, named as the linux port names
// them in zerrors_linux_amd64.go and zerrors_linux_arm64.go, which cosmo
// does not build. Both linux tables give each name the same value.
const (
	PTRACE_ARCH_PRCTL        = 0x1e
	PTRACE_ATTACH            = 0x10
	PTRACE_CONT              = 0x7
	PTRACE_DETACH            = 0x11
	PTRACE_EVENT_CLONE       = 0x3
	PTRACE_EVENT_EXEC        = 0x4
	PTRACE_EVENT_EXIT        = 0x6
	PTRACE_EVENT_FORK        = 0x1
	PTRACE_EVENT_VFORK       = 0x2
	PTRACE_EVENT_VFORK_DONE  = 0x5
	PTRACE_GETEVENTMSG       = 0x4201
	PTRACE_GETFPREGS         = 0xe
	PTRACE_GETFPXREGS        = 0x12
	PTRACE_GETREGS           = 0xc
	PTRACE_GETREGSET         = 0x4204
	PTRACE_GETSIGINFO        = 0x4202
	PTRACE_GET_THREAD_AREA   = 0x19
	PTRACE_KILL              = 0x8
	PTRACE_OLDSETOPTIONS     = 0x15
	PTRACE_O_MASK            = 0x7f
	PTRACE_O_TRACECLONE      = 0x8
	PTRACE_O_TRACEEXEC       = 0x10
	PTRACE_O_TRACEEXIT       = 0x40
	PTRACE_O_TRACEFORK       = 0x2
	PTRACE_O_TRACESYSGOOD    = 0x1
	PTRACE_O_TRACEVFORK      = 0x4
	PTRACE_O_TRACEVFORKDONE  = 0x20
	PTRACE_PEEKDATA          = 0x2
	PTRACE_PEEKTEXT          = 0x1
	PTRACE_PEEKUSR           = 0x3
	PTRACE_POKEDATA          = 0x5
	PTRACE_POKETEXT          = 0x4
	PTRACE_POKEUSR           = 0x6
	PTRACE_SETFPREGS         = 0xf
	PTRACE_SETFPXREGS        = 0x13
	PTRACE_SETOPTIONS        = 0x4200
	PTRACE_SETREGS           = 0xd
	PTRACE_SETREGSET         = 0x4205
	PTRACE_SETSIGINFO        = 0x4203
	PTRACE_SET_THREAD_AREA   = 0x1a
	PTRACE_SINGLEBLOCK       = 0x21
	PTRACE_SINGLESTEP        = 0x9
	PTRACE_SYSCALL           = 0x18
	PTRACE_SYSEMU            = 0x1f
	PTRACE_SYSEMU_SINGLESTEP = 0x20
	PTRACE_TRACEME           = 0x0

	_NT_PRSTATUS = 1
)

func ptrace(request int, pid int, addr uintptr, data uintptr) (err error) {
	_, _, e1 := Syscall6(SYS_PTRACE, uintptr(request), uintptr(pid), uintptr(addr), uintptr(data), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func ptracePtr(request int, pid int, addr uintptr, data unsafe.Pointer) (err error) {
	_, _, e1 := Syscall6(SYS_PTRACE, uintptr(request), uintptr(pid), uintptr(addr), uintptr(data), 0, 0)
	if e1 != 0 {
		err = errnoErr(e1)
	}
	return
}

func ptracePeek(req int, pid int, addr uintptr, out []byte) (count int, err error) {
	// The peek requests are machine-size oriented, so we wrap it
	// to retrieve arbitrary-length data.

	// The ptrace syscall differs from glibc's ptrace.
	// Peeks returns the word in *data, not as the return value.

	var buf [sizeofPtr]byte

	// Leading edge. PEEKTEXT/PEEKDATA don't require aligned
	// access (PEEKUSER warns that it might), but if we don't
	// align our reads, we might straddle an unmapped page
	// boundary and not get the bytes leading up to the page
	// boundary.
	n := 0
	if addr%sizeofPtr != 0 {
		err = ptracePtr(req, pid, addr-addr%sizeofPtr, unsafe.Pointer(&buf[0]))
		if err != nil {
			return 0, err
		}
		n += copy(out, buf[addr%sizeofPtr:])
		out = out[n:]
	}

	// Remainder.
	for len(out) > 0 {
		// We use an internal buffer to guarantee alignment.
		// It's not documented if this is necessary, but we're paranoid.
		err = ptracePtr(req, pid, addr+uintptr(n), unsafe.Pointer(&buf[0]))
		if err != nil {
			return n, err
		}
		copied := copy(out, buf[0:])
		n += copied
		out = out[copied:]
	}

	return n, nil
}

func PtracePeekText(pid int, addr uintptr, out []byte) (count int, err error) {
	return ptracePeek(PTRACE_PEEKTEXT, pid, addr, out)
}

func PtracePeekData(pid int, addr uintptr, out []byte) (count int, err error) {
	return ptracePeek(PTRACE_PEEKDATA, pid, addr, out)
}

func ptracePoke(pokeReq int, peekReq int, pid int, addr uintptr, data []byte) (count int, err error) {
	// As for ptracePeek, we need to align our accesses to deal
	// with the possibility of straddling an invalid page.

	// Leading edge.
	n := 0
	if addr%sizeofPtr != 0 {
		var buf [sizeofPtr]byte
		err = ptracePtr(peekReq, pid, addr-addr%sizeofPtr, unsafe.Pointer(&buf[0]))
		if err != nil {
			return 0, err
		}
		n += copy(buf[addr%sizeofPtr:], data)
		word := *((*uintptr)(unsafe.Pointer(&buf[0])))
		err = ptrace(pokeReq, pid, addr-addr%sizeofPtr, word)
		if err != nil {
			return 0, err
		}
		data = data[n:]
	}

	// Interior.
	for len(data) > sizeofPtr {
		word := *((*uintptr)(unsafe.Pointer(&data[0])))
		err = ptrace(pokeReq, pid, addr+uintptr(n), word)
		if err != nil {
			return n, err
		}
		n += sizeofPtr
		data = data[sizeofPtr:]
	}

	// Trailing edge.
	if len(data) > 0 {
		var buf [sizeofPtr]byte
		err = ptracePtr(peekReq, pid, addr+uintptr(n), unsafe.Pointer(&buf[0]))
		if err != nil {
			return n, err
		}
		copy(buf[0:], data)
		word := *((*uintptr)(unsafe.Pointer(&buf[0])))
		err = ptrace(pokeReq, pid, addr+uintptr(n), word)
		if err != nil {
			return n, err
		}
		n += len(data)
	}

	return n, nil
}

func PtracePokeText(pid int, addr uintptr, data []byte) (count int, err error) {
	return ptracePoke(PTRACE_POKETEXT, PTRACE_PEEKTEXT, pid, addr, data)
}

func PtracePokeData(pid int, addr uintptr, data []byte) (count int, err error) {
	return ptracePoke(PTRACE_POKEDATA, PTRACE_PEEKDATA, pid, addr, data)
}

func PtraceGetRegs(pid int, regsout *PtraceRegs) (err error) {
	var iov Iovec
	iov.Base = (*byte)(unsafe.Pointer(regsout))
	iov.SetLen(int(unsafe.Sizeof(*regsout)))
	return ptracePtr(PTRACE_GETREGSET, pid, uintptr(_NT_PRSTATUS), unsafe.Pointer(&iov))
}

func PtraceSetRegs(pid int, regs *PtraceRegs) (err error) {
	var iov Iovec
	iov.Base = (*byte)(unsafe.Pointer(regs))
	iov.SetLen(int(unsafe.Sizeof(*regs)))
	return ptracePtr(PTRACE_SETREGSET, pid, uintptr(_NT_PRSTATUS), unsafe.Pointer(&iov))
}

func PtraceSetOptions(pid int, options int) (err error) {
	return ptrace(PTRACE_SETOPTIONS, pid, 0, uintptr(options))
}

func PtraceGetEventMsg(pid int) (msg uint, err error) {
	var data _C_long
	err = ptracePtr(PTRACE_GETEVENTMSG, pid, 0, unsafe.Pointer(&data))
	msg = uint(data)
	return
}

func PtraceCont(pid int, signal int) (err error) {
	return ptrace(PTRACE_CONT, pid, 0, uintptr(signal))
}

func PtraceSyscall(pid int, signal int) (err error) {
	return ptrace(PTRACE_SYSCALL, pid, 0, uintptr(signal))
}

func PtraceSingleStep(pid int) (err error) { return ptrace(PTRACE_SINGLESTEP, pid, 0, 0) }

func PtraceAttach(pid int) (err error) { return ptrace(PTRACE_ATTACH, pid, 0, 0) }

func PtraceDetach(pid int) (err error) { return ptrace(PTRACE_DETACH, pid, 0, 0) }
