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

// The ptrace request numbers. The linux port names them PTRACE_PEEKTEXT and
// so on in zerrors_linux_amd64.go and zerrors_linux_arm64.go, which cosmo
// does not build. Both linux tables give each request the same value.
const (
	ptraceReqPeekText    = 0x1
	ptraceReqPeekData    = 0x2
	ptraceReqPokeText    = 0x4
	ptraceReqPokeData    = 0x5
	ptraceReqCont        = 0x7
	ptraceReqSingleStep  = 0x9
	ptraceReqAttach      = 0x10
	ptraceReqDetach      = 0x11
	ptraceReqSyscall     = 0x18
	ptraceReqSetOptions  = 0x4200
	ptraceReqGetEventMsg = 0x4201
	ptraceReqGetRegset   = 0x4204
	ptraceReqSetRegset   = 0x4205

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
	return ptracePeek(ptraceReqPeekText, pid, addr, out)
}

func PtracePeekData(pid int, addr uintptr, out []byte) (count int, err error) {
	return ptracePeek(ptraceReqPeekData, pid, addr, out)
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
	return ptracePoke(ptraceReqPokeText, ptraceReqPeekText, pid, addr, data)
}

func PtracePokeData(pid int, addr uintptr, data []byte) (count int, err error) {
	return ptracePoke(ptraceReqPokeData, ptraceReqPeekData, pid, addr, data)
}

func PtraceGetRegs(pid int, regsout *PtraceRegs) (err error) {
	var iov Iovec
	iov.Base = (*byte)(unsafe.Pointer(regsout))
	iov.SetLen(int(unsafe.Sizeof(*regsout)))
	return ptracePtr(ptraceReqGetRegset, pid, uintptr(_NT_PRSTATUS), unsafe.Pointer(&iov))
}

func PtraceSetRegs(pid int, regs *PtraceRegs) (err error) {
	var iov Iovec
	iov.Base = (*byte)(unsafe.Pointer(regs))
	iov.SetLen(int(unsafe.Sizeof(*regs)))
	return ptracePtr(ptraceReqSetRegset, pid, uintptr(_NT_PRSTATUS), unsafe.Pointer(&iov))
}

func PtraceSetOptions(pid int, options int) (err error) {
	return ptrace(ptraceReqSetOptions, pid, 0, uintptr(options))
}

func PtraceGetEventMsg(pid int) (msg uint, err error) {
	var data _C_long
	err = ptracePtr(ptraceReqGetEventMsg, pid, 0, unsafe.Pointer(&data))
	msg = uint(data)
	return
}

func PtraceCont(pid int, signal int) (err error) {
	return ptrace(ptraceReqCont, pid, 0, uintptr(signal))
}

func PtraceSyscall(pid int, signal int) (err error) {
	return ptrace(ptraceReqSyscall, pid, 0, uintptr(signal))
}

func PtraceSingleStep(pid int) (err error) { return ptrace(ptraceReqSingleStep, pid, 0, 0) }

func PtraceAttach(pid int) (err error) { return ptrace(ptraceReqAttach, pid, 0, 0) }

func PtraceDetach(pid int) (err error) { return ptrace(ptraceReqDetach, pid, 0, 0) }
