// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT sendmsg/recvmsg emulation (wave 3 item 2): the plain
// scatter-gather DATA path behind SYS_SENDMSG/SYS_RECVMSG, dispatched
// by ntSyscallEmulate (os_cosmo_nt_sys.go), plus the socket-only
// SYS_READV/SYS_WRITEV cases built on the same WSABUF machinery
// (ntEmuReadv/ntEmuWritev below - what makes net.Buffers work).
//
// Layouts: callers hand the runtime LINUX amd64 structures
// (syscall.Msghdr/syscall.Iovec, mirrored below as ntLinuxMsghdr/
// ntLinuxIovec); winsock wants WSABUF arrays, whose field order is
// REVERSED relative to iovec ({u32 len, char *buf} vs {base, len}),
// so every call translates through a WSABUF array - stack-backed up
// to ntWSABufStackCap entries, heap-allocated above (the emulation
// layer is ordinary Go; allocation is legal here).
//
// Routing:
//
//   - msg_name == nil: WSASend/WSARecv on the (connected) socket -
//     the scatter-gather cousins of the plain send/recv that
//     ntSockRead/ntSockWrite use. Both are 7-argument calls through
//     ntcallSE (blocking-capable, last-error captured in-trampoline);
//     WSARecv's flags argument is a POINTER (in/out).
//   - msg_name != nil: delegate to ntEmuSendto/ntEmuRecvfrom so the
//     sockaddr translation lives in exactly one place; multiple
//     iovecs coalesce through an allocated buffer first (winsock's
//     sendto/recvfrom take one buffer). AF_UNIX needs no datagram
//     story - afunix.sys is SOCK_STREAM only - and stream SENDS pass
//     a nil name; recvmsg's always-supplied name buffer (the syscall
//     package never omits it) is answered by recvfrom, which ignores
//     it on connected streams and leaves the pre-zeroed buffer as
//     family AF_UNSPEC = "no source address", exactly the signal
//     syscall.Recvmsg keys on. (One knowing divergence: Linux also
//     rewrites msg_namelen to 0 there; the recvfrom delegate reports
//     the family-AF_UNSPEC length instead, which no std caller
//     reads.)
//
// MSG_* flags reuse ntMsgFlags (OOB/PEEK/DONTROUTE pass; anything
// else EINVAL). Error mapping mirrors ntSockRead/ntSockWrite exactly:
// recv-side WSAESHUTDOWN is EOF (0 bytes), send-side WSAESHUTDOWN
// becomes EPIPE via ntWSAToLinux's table, and recv-side WSAEMSGSIZE
// reports a full buffer like the recvfrom emulation (Linux truncates
// datagrams silently) - the msghdr path additionally raises MSG_TRUNC
// in msg_flags, which plain recvfrom has nowhere to report.
//
// ANCILLARY DATA IS NOT IMPLEMENTED YET. ntSendmsgControl and
// ntRecvmsgControl below are the deliberate seam where the SCM_RIGHTS
// item plugs in; until then sendmsg with a control payload is refused
// EOPNOTSUPP and recvmsg always reports an empty control region
// (msg_controllen = 0 - no ancillary ever arrives).

package runtime

import "unsafe"

const (
	// MSG_TRUNC (Linux): recv-side "datagram was truncated", raised
	// in msg_flags OUTPUT only - as an input flag ntMsgFlags refuses
	// it like every other non-passthrough flag.
	_NT_MSG_TRUNC = 0x20

	// Linux's UIO_MAXIOV: iovec counts above this are refused, with
	// the kernel's exact errno split - EMSGSIZE from sendmsg/recvmsg,
	// EINVAL from readv/writev.
	ntMaxIov = 1024

	// WSABUF arrays up to this many entries live on the caller's
	// stack; larger counts allocate. internal/poll batches at most
	// 1024 iovecs, net.Buffers users pass handfuls.
	ntWSABufStackCap = 8
)

// ntLinuxIovec must match syscall.Iovec in syscall/ztypes_cosmo_amd64.go
// (the Linux amd64 iovec, 16 bytes): Base *byte @0, Len uint64 @8.
type ntLinuxIovec struct {
	base *byte
	len  uint64
}

// ntLinuxMsghdr must match syscall.Msghdr in
// syscall/ztypes_cosmo_amd64.go (the Linux amd64 msghdr, 0x38 bytes):
// Name *byte @0, Namelen uint32 @8, 4 pad, Iov *Iovec @16, Iovlen
// uint64 @24, Control *byte @32, Controllen uint64 @40, Flags int32
// @48, 4 pad.
type ntLinuxMsghdr struct {
	name       *byte
	namelen    uint32
	_          [4]byte
	iov        *ntLinuxIovec
	iovlen     uint64
	control    *byte
	controllen uint64
	flags      int32
	_          [4]byte
}

// ntWSABuf is winsock's WSABUF on x64: {ULONG len; CHAR *buf} - 16
// bytes with buf at offset 8 (Go pads after the uint32 exactly like
// MSVC), the REVERSE field order of iovec.
type ntWSABuf struct {
	len uint32
	buf *byte
}

// ntIovecsToWSA translates n iovecs into a WSABUF array (stack-backed
// via stk when n fits) and reports the total byte capacity described.
// The total is clamped to 0x7FFFFFFF - winsock transfer counts are
// 32-bit - by shortening the overflowing buffer and dropping the
// rest: sends then short-write and the caller loops, receives
// short-read; both POSIX-legal. A nil base with a nonzero length is
// EFAULT.
func ntIovecsToWSA(iov *ntLinuxIovec, n int, stk *[ntWSABufStackCap]ntWSABuf) (bufs []ntWSABuf, total int64, eno uintptr) {
	const maxTotal = 0x7FFFFFFF
	if n > ntWSABufStackCap {
		bufs = make([]ntWSABuf, 0, n)
	} else {
		bufs = stk[:0]
	}
	iovs := unsafe.Slice(iov, n)
	for i := range iovs {
		b, l := iovs[i].base, iovs[i].len
		if b == nil && l != 0 {
			return nil, 0, ntEFAULT
		}
		if l > uint64(maxTotal-total) {
			l = uint64(maxTotal - total)
			bufs = append(bufs, ntWSABuf{uint32(l), b})
			total = maxTotal
			break
		}
		bufs = append(bufs, ntWSABuf{uint32(l), b})
		total += int64(l)
	}
	return bufs, total, 0
}

// ntIovTotal sums iovec lengths for the coalescing (datagram
// delegate) paths, where a single flat buffer must hold everything: a
// total past 2 GiB is EMSGSIZE (winsock could not move that datagram
// either). nil base with nonzero length is EFAULT.
func ntIovTotal(iovs []ntLinuxIovec) (int64, uintptr) {
	var t int64
	for i := range iovs {
		if iovs[i].base == nil && iovs[i].len != 0 {
			return 0, ntEFAULT
		}
		if iovs[i].len > 0x7FFFFFFF || t > 0x7FFFFFFF-int64(iovs[i].len) {
			return 0, ntEMSGSIZE
		}
		t += int64(iovs[i].len)
	}
	return t, 0
}

// ntSockSendV writes a WSABUF array to socket h via WSASend (7 args,
// ntcallSE). WSASend reports success as 0 with the count in an out
// parameter, unlike send's count-or-minus-one.
func ntSockSendV(h uintptr, bufs []ntWSABuf, wflags uintptr) (r1, r2, errno uintptr) {
	var sent uint32
	r, werr := ntcallSE(ntWSASendVFn, h, uintptr(unsafe.Pointer(&bufs[0])),
		uintptr(uint32(len(bufs))), uintptr(unsafe.Pointer(&sent)), wflags, 0, 0)
	if ntSockErr(r) {
		// WSAESHUTDOWN -> EPIPE via the table, matching ntSockWrite.
		return ntFail3(ntWSAToLinux(werr))
	}
	return uintptr(sent), 0, 0
}

// ntSockRecvV fills a WSABUF array from socket h via WSARecv. total
// is the array's byte capacity (for the WSAEMSGSIZE full-buffer
// report). Returns the byte count and the OUTPUT msg_flags translated
// to Linux: only MSG_OOB shares a value and passes back; MSG_TRUNC is
// raised on datagram truncation; winsock-only bits (MSG_PARTIAL) are
// dropped.
func ntSockRecvV(h uintptr, bufs []ntWSABuf, wflags uintptr, total int64) (n uintptr, outFlags int32, errno uintptr) {
	var got uint32
	wf := uint32(wflags) // WSARecv's flags argument is in/out
	r, werr := ntcallSE(ntWSARecvVFn, h, uintptr(unsafe.Pointer(&bufs[0])),
		uintptr(uint32(len(bufs))), uintptr(unsafe.Pointer(&got)),
		uintptr(unsafe.Pointer(&wf)), 0, 0)
	if ntSockErr(r) {
		switch werr {
		case _NT_WSAESHUTDOWN:
			return 0, 0, 0 // read after SHUT_RD: EOF, matching ntSockRead
		case _NT_WSAEMSGSIZE:
			// Datagram longer than the buffers: winsock filled them
			// and then failed; Linux truncates silently and raises
			// MSG_TRUNC. Report a full buffer, like ntEmuRecvfrom.
			return uintptr(total), _NT_MSG_TRUNC, 0
		}
		return 0, 0, ntWSAToLinux(werr)
	}
	return uintptr(got), int32(wf) & _NT_MSG_OOB, 0
}

// ntSendmsgControl is the send-side ANCILLARY SEAM: ntEmuSendmsg
// routes here whenever the caller supplied a non-empty control
// buffer, BEFORE any data moves. The SCM_RIGHTS item replaces this
// body with cmsg parsing and its fd-transfer frame; until then every
// ancillary send is refused EOPNOTSUPP (Linux's errno for cmsg types
// a protocol does not support), and plain data sendmsg never enters.
func ntSendmsgControl(fd int32, e *ntFDEntry, msg *ntLinuxMsghdr, flags int32) (r1, r2, errno uintptr) {
	return ntFail3(ntEOPNOTSUPP)
}

// ntRecvmsgControl is the receive-side ANCILLARY SEAM: ntEmuRecvmsg
// calls it when (and only when) the caller supplied a control buffer,
// BEFORE the plain receive, so the SCM_RIGHTS item can MSG_PEEK for
// its wire frame here and take over the whole receive (handled=true
// with its own results). Until then nothing ancillary ever arrives:
// report handled=false and let the plain data path run - it zeroes
// msg_controllen, so a supplied oob buffer simply comes back empty.
func ntRecvmsgControl(fd int32, e *ntFDEntry, msg *ntLinuxMsghdr, flags int32) (handled bool, r1, r2, errno uintptr) {
	return false, 0, 0, 0
}

func ntEmuSendmsg(fd int32, msg *ntLinuxMsghdr, flags int32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd) // EBADF / ENOTSOCK first, the Linux order
	if eno != 0 {
		return ntFail3(eno)
	}
	if msg == nil {
		return ntFail3(ntEFAULT)
	}
	wflags, eno := ntMsgFlags(flags)
	if eno != 0 {
		return ntFail3(eno)
	}
	if msg.control != nil && msg.controllen != 0 {
		return ntSendmsgControl(fd, &e, msg, flags)
	}
	if msg.iovlen > ntMaxIov {
		return ntFail3(ntEMSGSIZE)
	}
	n := int(msg.iovlen)
	if n > 0 && msg.iov == nil {
		return ntFail3(ntEFAULT)
	}
	if msg.name != nil && msg.namelen != 0 {
		// Datagram-style send with a destination: one buffer through
		// the sendto backend (sockaddr translation for free).
		if n == 0 {
			return ntEmuSendto(fd, nil, 0, flags, unsafe.Pointer(msg.name), msg.namelen)
		}
		iovs := unsafe.Slice(msg.iov, n)
		total, eno := ntIovTotal(iovs)
		if eno != 0 {
			return ntFail3(eno)
		}
		if n == 1 {
			return ntEmuSendto(fd, unsafe.Pointer(iovs[0].base), int32(total),
				flags, unsafe.Pointer(msg.name), msg.namelen)
		}
		buf := make([]byte, 0, total)
		for i := range iovs {
			buf = append(buf, unsafe.Slice(iovs[i].base, iovs[i].len)...)
		}
		var p unsafe.Pointer
		if total > 0 {
			p = unsafe.Pointer(&buf[0])
		}
		return ntEmuSendto(fd, p, int32(total), flags, unsafe.Pointer(msg.name), msg.namelen)
	}
	if n == 0 {
		return 0, 0, 0 // zero-length stream send: nothing to do
	}
	var stk [ntWSABufStackCap]ntWSABuf
	bufs, _, eno := ntIovecsToWSA(msg.iov, n, &stk)
	if eno != 0 {
		return ntFail3(eno)
	}
	return ntSockSendV(e.handle, bufs, wflags)
}

// ntIovFD validates the fd for readv/writev: SOCKET-kind fds go
// through the WSABUF path; everything else stays ENOSYS ON PURPOSE.
// Nothing in the standard library issues readv/writev on NT files or
// pipes - internal/poll's only writev consumer is net (net.Buffers
// consolidated writes, netFD-only) and there is no readv consumer at
// all - and exec's stdio pipes must stay on the blocking
// ReadFile/WriteFile path the netpoller refuses to adopt. A visible
// ENOSYS gap beats an untested vectored file path (the same call the
// file/pipe dup refusal makes).
func ntIovFD(fd int32) (ntFDEntry, uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFDEntry{}, ntEBADF
	}
	if e.kind != ntFDSocket {
		return ntFDEntry{}, ntENOSYS
	}
	return e, 0
}

// ntEmuReadv/ntEmuWritev back SYS_READV/SYS_WRITEV for socket-kind
// fds over the same WSABUF machinery as sendmsg/recvmsg (readv is
// recvmsg minus the msghdr, writev is sendmsg minus it). This is what
// makes net.Buffers work on NT. Per Linux, a bad iovec count is
// EINVAL here (the sendmsg/recvmsg spelling is EMSGSIZE).
func ntEmuReadv(fd int32, iov *ntLinuxIovec, iovcnt int32) (r1, r2, errno uintptr) {
	e, eno := ntIovFD(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if iovcnt < 0 || iovcnt > ntMaxIov {
		return ntFail3(ntEINVAL)
	}
	if iovcnt == 0 {
		return 0, 0, 0
	}
	if iov == nil {
		return ntFail3(ntEFAULT)
	}
	var stk [ntWSABufStackCap]ntWSABuf
	bufs, total, eno := ntIovecsToWSA(iov, int(iovcnt), &stk)
	if eno != 0 {
		return ntFail3(eno)
	}
	got, _, eno := ntSockRecvV(e.handle, bufs, 0, total)
	if eno != 0 {
		return ntFail3(eno)
	}
	return got, 0, 0
}

func ntEmuWritev(fd int32, iov *ntLinuxIovec, iovcnt int32) (r1, r2, errno uintptr) {
	e, eno := ntIovFD(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if iovcnt < 0 || iovcnt > ntMaxIov {
		return ntFail3(ntEINVAL)
	}
	if iovcnt == 0 {
		return 0, 0, 0
	}
	if iov == nil {
		return ntFail3(ntEFAULT)
	}
	var stk [ntWSABufStackCap]ntWSABuf
	bufs, _, eno := ntIovecsToWSA(iov, int(iovcnt), &stk)
	if eno != 0 {
		return ntFail3(eno)
	}
	return ntSockSendV(e.handle, bufs, 0)
}

func ntEmuRecvmsg(fd int32, msg *ntLinuxMsghdr, flags int32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if msg == nil {
		return ntFail3(ntEFAULT)
	}
	wflags, eno := ntMsgFlags(flags)
	if eno != 0 {
		return ntFail3(eno)
	}
	if msg.control != nil && msg.controllen != 0 {
		if handled, hr1, hr2, heno := ntRecvmsgControl(fd, &e, msg, flags); handled {
			return hr1, hr2, heno
		}
	}
	// No ancillary data exists on the plain path: the control region
	// comes back empty and msg_flags starts clean.
	msg.controllen = 0
	msg.flags = 0
	if msg.iovlen > ntMaxIov {
		return ntFail3(ntEMSGSIZE)
	}
	n := int(msg.iovlen)
	if n > 0 && msg.iov == nil {
		return ntFail3(ntEFAULT)
	}
	if msg.name != nil && msg.namelen != 0 {
		// recvfrom delegate: fills the source address on unconnected
		// datagram sockets and leaves it AF_UNSPEC on connected
		// streams (see the file comment - std's Recvmsg ALWAYS
		// supplies a name buffer, so stream receives land here too).
		if n == 0 {
			return ntEmuRecvfrom(fd, nil, 0, flags, unsafe.Pointer(msg.name), &msg.namelen)
		}
		iovs := unsafe.Slice(msg.iov, n)
		total, eno := ntIovTotal(iovs)
		if eno != 0 {
			return ntFail3(eno)
		}
		if n == 1 {
			return ntEmuRecvfrom(fd, unsafe.Pointer(iovs[0].base), int32(total),
				flags, unsafe.Pointer(msg.name), &msg.namelen)
		}
		if total == 0 {
			return ntEmuRecvfrom(fd, nil, 0, flags, unsafe.Pointer(msg.name), &msg.namelen)
		}
		tmp := make([]byte, total)
		gr1, gr2, geno := ntEmuRecvfrom(fd, unsafe.Pointer(&tmp[0]), int32(total),
			flags, unsafe.Pointer(msg.name), &msg.namelen)
		if geno != 0 {
			return gr1, gr2, geno
		}
		got := tmp[:int64(gr1)]
		for i := range iovs {
			if len(got) == 0 {
				break
			}
			c := copy(unsafe.Slice(iovs[i].base, iovs[i].len), got)
			got = got[c:]
		}
		return gr1, gr2, 0
	}
	if n == 0 {
		return 0, 0, 0
	}
	var stk [ntWSABufStackCap]ntWSABuf
	bufs, total, eno := ntIovecsToWSA(msg.iov, n, &stk)
	if eno != 0 {
		return ntFail3(eno)
	}
	got, outFlags, eno := ntSockRecvV(e.handle, bufs, wflags, total)
	if eno != 0 {
		return ntFail3(eno)
	}
	msg.flags = outFlags
	return got, 0, 0
}
