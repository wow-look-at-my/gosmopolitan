// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && arm64

package cosmo

import "unsafe"

// Darwin (macOS ARM64) socket syscall emulation, the socket half of the
// slow path in syscall_cosmo_arm64.go. Socket syscalls arrive with Linux
// numbers, Linux struct sockaddr layouts and Linux option constants;
// Apple libc (resolved via the Syslib's dlsym at startup) wants its own
// versions of all three, so everything is translated here, in one place,
// in both directions:
//
//   - sockaddr: Linux starts with a 16-bit family; Apple replaces it
//     with {uint8 sa_len, uint8 sa_family}. The payload beyond those two
//     bytes (port, address, path) has an identical layout for every
//     family this emulation admits (AF_UNIX, AF_INET, AF_INET6).
//   - family values: AF_UNSPEC/AF_UNIX/AF_INET coincide; AF_INET6 is 10
//     on Linux, 30 on Apple.
//   - options: see darwinSockoptXlat for the (level, optname) table.
//
// Everything here is reached from syscall.Syscall/Syscall6, i.e. inside
// the _Gsyscall window, so every function is nosplit (the linker's
// nosplit check bounds the chains; the sockaddr scratch buffers are the
// biggest frames at 112 bytes, well under the darwinFstatat precedent).
//
// sendmsg/recvmsg are emulated since 2026-07-21: Linux and Apple
// disagree on struct msghdr field widths and on struct cmsghdr
// entirely. darwinSendmsg/darwinRecvmsg below are fixed-size msghdr
// shape adapters (all the nosplit budget allows); the unbounded halves
// - sockaddr translation and the cmsg repack, SCM_RIGHTS fd payloads
// preserved and MSG_CMSG_CLOEXEC emulated via fcntl - run as ordinary
// Go in package syscall's darwin branch, over the exported helpers in
// socket_msg_cosmo.go (see that file's header for the split and the
// resulting raw-syscall contract).

// Linux arm64 socket syscall numbers handled by the slow path.
const (
	sysSOCKET      = 198
	sysSOCKETPAIR  = 199
	sysBIND        = 200
	sysLISTEN      = 201
	sysACCEPT      = 202
	sysCONNECT     = 203
	sysGETSOCKNAME = 204
	sysGETPEERNAME = 205
	sysSENDTO      = 206
	sysRECVFROM    = 207
	sysSETSOCKOPT  = 208
	sysGETSOCKOPT  = 209
	sysSHUTDOWN    = 210
	sysSENDMSG     = 211
	sysRECVMSG     = 212
	sysACCEPT4     = 242
)

const (
	darwinEAFNOSUPPORT = 97 // Linux numbering
	darwinENOPROTOOPT  = 92 // Linux numbering
)

// Address families. AF_UNSPEC (0), AF_UNIX (1) and AF_INET (2) have the
// same values on Linux and Apple; AF_INET6 differs.
const (
	linuxAF_UNIX  = 1
	linuxAF_INET  = 2
	linuxAF_INET6 = 10
	appleAF_INET6 = 30
)

// Linux encodes close-on-exec/nonblocking flags in the socket type
// argument (socket, socketpair, accept4). Apple has no such flags; they
// are emulated with fcntl on the new descriptor.
const (
	linuxSOCK_NONBLOCK = 0x800
	linuxSOCK_CLOEXEC  = 0x80000

	fdCLOEXEC = 1 // FD_CLOEXEC, same on both systems
)

// appleSO_NOSIGPIPE suppresses SIGPIPE on writes to a broken socket
// (Apple's replacement for Linux's per-call MSG_NOSIGNAL). It is set on
// every socket this emulation creates: the Go runtime normally absorbs
// SIGPIPE in its signal handler, but signal handling is still stubbed on
// macOS hosts (signal wave), where an unsuppressed SIGPIPE would kill
// the process.
const appleSO_NOSIGPIPE = 0x1022

// darwinSockFamilyToApple translates a Linux address family for Apple.
//
//go:nosplit
func darwinSockFamilyToApple(f uint16) (byte, bool) {
	switch f {
	case 0, linuxAF_UNIX, linuxAF_INET:
		return byte(f), true
	case linuxAF_INET6:
		return appleAF_INET6, true
	}
	return 0, false
}

// darwinSockaddrOut copies the Linux sockaddr at (addr, addrlen) into
// buf as an Apple sockaddr and returns the Apple (ptr, len) pair to pass
// to libc. A nil/empty address passes through as (0, 0) - e.g. sendto on
// a connected socket.
//
//go:nosplit
func darwinSockaddrOut(buf *[112]byte, addr, addrlen uintptr) (aptr, alen, errno uintptr) {
	if addr == 0 || addrlen == 0 {
		return 0, 0, 0
	}
	if addrlen < 2 || addrlen > uintptr(len(buf)) {
		return 0, 0, darwinEINVAL
	}
	fam := *(*uint16)(unsafe.Pointer(addr))
	afam, famOK := darwinSockFamilyToApple(fam)
	if !famOK {
		return 0, 0, darwinEAFNOSUPPORT
	}
	if fam == linuxAF_UNIX && addrlen > 2 && *(*byte)(unsafe.Pointer(addr + 2)) == 0 {
		// Abstract socket namespace (leading NUL) is Linux-only.
		return 0, 0, darwinEINVAL
	}
	for i := uintptr(2); i < addrlen; i++ {
		buf[i] = *(*byte)(unsafe.Pointer(addr + i))
	}
	buf[0] = byte(addrlen) // sa_len
	buf[1] = afam
	return uintptr(unsafe.Pointer(&buf[0])), addrlen, 0
}

// darwinSockaddrIn rewrites, in place, a sockaddr Apple libc just filled
// in (accept, getsockname, getpeername, recvfrom) into the Linux shape:
// Apple's {sa_len, sa_family} bytes become the 16-bit Linux family. The
// rest of the bytes are already in the Linux layout.
//
//go:nosplit
func darwinSockaddrIn(addr uintptr, alenp uintptr) {
	if addr == 0 || alenp == 0 {
		return
	}
	if *(*uint32)(unsafe.Pointer(alenp)) < 2 {
		return
	}
	afam := *(*byte)(unsafe.Pointer(addr + 1))
	fam := uint16(afam)
	if afam == appleAF_INET6 {
		fam = linuxAF_INET6
	}
	*(*uint16)(unsafe.Pointer(addr)) = fam
}

// darwinSetNoSigpipe sets SO_NOSIGPIPE on a new socket, best effort (a
// failure is not worth refusing the socket over). Kept flat - it calls
// the libc trampoline directly - because it sits on the deepest nosplit
// chains (socket/socketpair/accept4 with flags), where the darwinCall
// helper's frame would not fit.
//
// SOCK_CLOEXEC/SOCK_NONBLOCK application shares darwinApplyFdFlags with
// pipe2 (the SOCK_ and O_ flag bits have the same values on Linux);
// call sites apply both helpers sequentially rather than nested, again
// for nosplit depth.
//
//go:nosplit
func darwinSetNoSigpipe(fd uintptr) {
	if darwinFns.Setsockopt != 0 {
		one := uint32(1)
		darwinLibcCall6(darwinFns.Setsockopt, fd, appleSOL_SOCKET, appleSO_NOSIGPIPE,
			uintptr(unsafe.Pointer(&one)), 4, 0)
	}
}

// darwinCloseFd closes a descriptor during error cleanup.
//
//go:nosplit
func darwinCloseFd(fd uintptr) {
	if darwinFns.Close != 0 {
		darwinLibcCall6(darwinFns.Close, fd, 0, 0, 0, 0, 0)
	}
}

//go:nosplit
func darwinSocket(domain, typ, proto uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Socket == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	afam, famOK := darwinSockFamilyToApple(uint16(domain))
	if !famOK {
		return ^uintptr(0), 0, darwinEAFNOSUPPORT
	}
	flags := typ & (linuxSOCK_CLOEXEC | linuxSOCK_NONBLOCK)
	atyp := typ &^ (linuxSOCK_CLOEXEC | linuxSOCK_NONBLOCK) // SOCK_STREAM/DGRAM/RAW values coincide
	fd, _, e := darwinCall(darwinFns.Socket, uintptr(afam), atyp, proto, 0, 0, 0)
	if e != 0 {
		return ^uintptr(0), 0, e
	}
	if e := darwinApplyFdFlags(fd, flags); e != 0 {
		darwinCloseFd(fd)
		return ^uintptr(0), 0, e
	}
	darwinSetNoSigpipe(fd)
	return fd, 0, 0
}

//go:nosplit
func darwinSocketpair(domain, typ, proto, sv uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Socketpair == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	afam, famOK := darwinSockFamilyToApple(uint16(domain))
	if !famOK {
		return ^uintptr(0), 0, darwinEAFNOSUPPORT
	}
	flags := typ & (linuxSOCK_CLOEXEC | linuxSOCK_NONBLOCK)
	atyp := typ &^ (linuxSOCK_CLOEXEC | linuxSOCK_NONBLOCK)
	if _, _, e := darwinCall(darwinFns.Socketpair, uintptr(afam), atyp, proto, sv, 0, 0); e != 0 {
		return ^uintptr(0), 0, e
	}
	fds := (*[2]int32)(unsafe.Pointer(sv))
	for _, fd := range fds {
		if e := darwinApplyFdFlags(uintptr(fd), flags); e != 0 {
			darwinCloseFd(uintptr(fds[0]))
			darwinCloseFd(uintptr(fds[1]))
			return ^uintptr(0), 0, e
		}
		darwinSetNoSigpipe(uintptr(fd))
	}
	return 0, 0, 0
}

//go:nosplit
func darwinBindConnect(fn, s, addr, addrlen uintptr) (r1, r2, errno uintptr) {
	if fn == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	var buf [112]byte
	aptr, alen, e := darwinSockaddrOut(&buf, addr, addrlen)
	if e != 0 {
		return ^uintptr(0), 0, e
	}
	return darwinCall(fn, s, aptr, alen, 0, 0, 0)
}

//go:nosplit
func darwinAccept4(s, rsa, alenp, flags uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Accept == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if flags&^uintptr(linuxSOCK_CLOEXEC|linuxSOCK_NONBLOCK) != 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	fd, _, e := darwinCall(darwinFns.Accept, s, rsa, alenp, 0, 0, 0)
	if e != 0 {
		return ^uintptr(0), 0, e
	}
	darwinSockaddrIn(rsa, alenp)
	if e := darwinApplyFdFlags(fd, flags); e != 0 {
		darwinCloseFd(fd)
		return ^uintptr(0), 0, e
	}
	darwinSetNoSigpipe(fd)
	return fd, 0, 0
}

// darwinSockname handles getsockname/getpeername.
//
//go:nosplit
func darwinSockname(fn, s, rsa, alenp uintptr) (r1, r2, errno uintptr) {
	if fn == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	r1, r2, errno = darwinCall(fn, s, rsa, alenp, 0, 0, 0)
	if errno == 0 {
		darwinSockaddrIn(rsa, alenp)
	}
	return r1, r2, errno
}

// darwinCheckMsgFlags admits the send/recv flags whose values coincide
// on Linux and Apple (none, MSG_OOB 0x1, MSG_PEEK 0x2, MSG_DONTROUTE
// 0x4) and rejects everything else rather than passing bits Apple would
// misread (e.g. Linux MSG_DONTWAIT 0x40 is Apple MSG_FLUSH).
//
//go:nosplit
func darwinCheckMsgFlags(flags uintptr) uintptr {
	if flags&^uintptr(0x7) != 0 {
		return darwinEINVAL
	}
	return 0
}

//go:nosplit
func darwinSendto(s, p, n, flags, to, tolen uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Sendto == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if e := darwinCheckMsgFlags(flags); e != 0 {
		return ^uintptr(0), 0, e
	}
	var buf [112]byte
	aptr, alen, e := darwinSockaddrOut(&buf, to, tolen)
	if e != 0 {
		return ^uintptr(0), 0, e
	}
	return darwinCall(darwinFns.Sendto, s, p, n, flags, aptr, alen)
}

//go:nosplit
func darwinRecvfrom(s, p, n, flags, from, fromlenp uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Recvfrom == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if e := darwinCheckMsgFlags(flags); e != 0 {
		return ^uintptr(0), 0, e
	}
	r1, r2, errno = darwinCall(darwinFns.Recvfrom, s, p, n, flags, from, fromlenp)
	if errno == 0 {
		darwinSockaddrIn(from, fromlenp)
	}
	return r1, r2, errno
}

// darwinSendmsg emulates the Linux sendmsg syscall as a FIXED-SIZE
// shape adapter: the Linux msghdr's field widths are converted into an
// Apple msghdr (iovlen u64 -> int32 behind the UIO_MAXIOV bound,
// controllen u64 -> u32), the iovec array passes through (layouts
// coincide), and the msg_name/msg_control POINTERS pass through
// untouched - their BYTES must already be Apple-shaped, per the
// contract in socket_msg_cosmo.go's header (package syscall's darwin
// branch performs the sockaddr translation and cmsg repack as
// ordinary Go before entering the window; nothing unbounded fits the
// nosplit budget here). SIGPIPE needs no per-call handling: every
// socket this emulation creates already carries SO_NOSIGPIPE (a
// broken-pipe send fails with EPIPE, Go's expected semantics).
//
//go:nosplit
func darwinSendmsg(s, msgp, flags uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Sendmsg == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if e := darwinCheckMsgFlags(flags); e != 0 {
		return ^uintptr(0), 0, e
	}
	if msgp == 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	lm := (*linuxMsghdr)(unsafe.Pointer(msgp))
	if lm.Iovlen > msgMaxIovlen {
		return ^uintptr(0), 0, cmsgEMSGSIZE
	}
	if lm.Controllen > 0x7fffffff {
		return ^uintptr(0), 0, darwinEINVAL // Apple's controllen is u32
	}
	var amsg appleMsghdr
	amsg.Name = lm.Name
	if lm.Name != 0 {
		amsg.Namelen = lm.Namelen
	}
	amsg.Iov = lm.Iov
	amsg.Iovlen = int32(lm.Iovlen)
	if lm.Control != 0 && lm.Controllen != 0 {
		amsg.Control = lm.Control
		amsg.Controllen = uint32(lm.Controllen)
	}
	return darwinCall(darwinFns.Sendmsg, s, uintptr(unsafe.Pointer(&amsg)), flags, 0, 0, 0)
}

// darwinRecvmsg emulates the Linux recvmsg syscall, the same
// fixed-size shape adapter as darwinSendmsg: msghdr widths in, msghdr
// widths and result-flag VALUES (XlatMsgFlags) out. The msg_name and
// msg_control buffers come back with Apple-shaped BYTES - package
// syscall's darwin branch rewrites the sockaddr family and repacks
// the control records (with the MSG_CMSG_CLOEXEC emulation and
// truncation fd hygiene) after the window; raw SYS_RECVMSG users get
// the Apple-shaped-buffer contract. MSG_CMSG_CLOEXEC itself is
// refused EINVAL here like every untranslatable flag (the NT
// emulation's exact stance): the std path strips it and emulates
// above, so a raw caller's request is refused visibly rather than
// silently ignored.
//
//go:nosplit
func darwinRecvmsg(s, msgp, flags uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Recvmsg == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if e := darwinCheckMsgFlags(flags); e != 0 {
		return ^uintptr(0), 0, e
	}
	if msgp == 0 {
		return ^uintptr(0), 0, darwinEINVAL
	}
	lm := (*linuxMsghdr)(unsafe.Pointer(msgp))
	if lm.Iovlen > msgMaxIovlen {
		return ^uintptr(0), 0, cmsgEMSGSIZE
	}
	if lm.Controllen > 0x7fffffff {
		return ^uintptr(0), 0, darwinEINVAL // Apple's controllen is u32
	}
	var amsg appleMsghdr
	amsg.Name = lm.Name
	if lm.Name != 0 {
		amsg.Namelen = lm.Namelen
	}
	amsg.Iov = lm.Iov
	amsg.Iovlen = int32(lm.Iovlen)
	if lm.Control != 0 && lm.Controllen != 0 {
		amsg.Control = lm.Control
		amsg.Controllen = uint32(lm.Controllen)
	}
	r1, r2, errno = darwinCall(darwinFns.Recvmsg, s, uintptr(unsafe.Pointer(&amsg)), flags, 0, 0, 0)
	if errno != 0 {
		return r1, r2, errno
	}
	lm.Namelen = amsg.Namelen
	lm.Controllen = uint64(amsg.Controllen)
	lm.Flags = XlatMsgFlags(amsg.Flags)
	return r1, r2, 0
}

// darwinSockoptXlat translates a Linux (level, optname) pair to Apple's.
// Only pairs whose option VALUE also has the same meaning on both
// systems are listed; everything else reports ENOPROTOOPT so the gap is
// visible instead of programming a different option than requested.
//
// Levels: Linux SOL_SOCKET is 1, Apple's is 0xffff; the IPPROTO_* levels
// (0 ip, 6 tcp, 17 udp, 41 ipv6) are protocol numbers and coincide.
//
//go:nosplit
func darwinSockoptXlat(level, name uintptr) (alevel, aname uintptr, ok bool) {
	switch level {
	case linuxSOL_SOCKET:
		switch name {
		case 1: // SO_DEBUG
			return appleSOL_SOCKET, 0x0001, true
		case 2: // SO_REUSEADDR
			return appleSOL_SOCKET, 0x0004, true
		case 3: // SO_TYPE (SOCK_* result values coincide)
			return appleSOL_SOCKET, 0x1008, true
		case 4: // SO_ERROR (result translated by the caller)
			return appleSOL_SOCKET, 0x1007, true
		case 5: // SO_DONTROUTE
			return appleSOL_SOCKET, 0x0010, true
		case 6: // SO_BROADCAST
			return appleSOL_SOCKET, 0x0020, true
		case 7: // SO_SNDBUF
			return appleSOL_SOCKET, 0x1001, true
		case 8: // SO_RCVBUF
			return appleSOL_SOCKET, 0x1002, true
		case 9: // SO_KEEPALIVE
			return appleSOL_SOCKET, 0x0008, true
		case 10: // SO_OOBINLINE
			return appleSOL_SOCKET, 0x0100, true
		case 13: // SO_LINGER -> SO_LINGER_SEC: struct linger matches, but
			// Apple's plain SO_LINGER (0x80) counts l_linger in clock
			// ticks; SO_LINGER_SEC uses seconds like Linux.
			return appleSOL_SOCKET, 0x1080, true
		case 15: // SO_REUSEPORT
			return appleSOL_SOCKET, 0x0200, true
		case 30: // SO_ACCEPTCONN
			return appleSOL_SOCKET, 0x0002, true
		}
	case 6: // IPPROTO_TCP
		switch name {
		case 1: // TCP_NODELAY
			return 6, 0x01, true
		case 4: // TCP_KEEPIDLE -> Apple TCP_KEEPALIVE (idle seconds)
			return 6, 0x10, true
		case 5: // TCP_KEEPINTVL
			return 6, 0x101, true
		case 6: // TCP_KEEPCNT
			return 6, 0x102, true
		}
	case 41: // IPPROTO_IPV6
		switch name {
		case 26: // IPV6_V6ONLY
			return 41, 27, true
		}
	}
	return 0, 0, false
}

//go:nosplit
func darwinSetsockopt(s, level, name, val, vallen uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Setsockopt == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	alevel, aname, ok := darwinSockoptXlat(level, name)
	if !ok {
		return ^uintptr(0), 0, darwinENOPROTOOPT
	}
	return darwinCall(darwinFns.Setsockopt, s, alevel, aname, val, vallen, 0)
}

// Apple's SOL_LOCAL options for AF_UNIX peer identity. SOL_LOCAL is
// level 0, which is IPPROTO_IP on Linux - the two option namespaces
// collide outright (Linux IP_TTL is 2, LOCAL_PEERPID is 2), so level 0
// is NOT passed through. Peer identity is reached through the Linux
// spelling instead; see darwinPeercred.
const (
	appleSOL_LOCAL      = 0
	appleLOCAL_PEERCRED = 0x001
	appleLOCAL_PEERPID  = 0x002

	linuxSO_PEERCRED = 17 // level SOL_SOCKET
)

// appleXucredHead is the leading 16 bytes of Apple's struct xucred:
// cr_version, cr_uid, cr_ngroups, then cr_groups[0] at offset 12 (gid_t
// alignment pads the 2-byte cr_ngroups). The rest of cr_groups is the
// supplementary list, which SO_PEERCRED does not report, so only the head
// is read - the full 76-byte struct does not fit the 792-byte nosplit
// budget this dispatch path runs under.
type appleXucredHead struct {
	Version uint32
	Uid     uint32
	Ngroups int16
	_       int16
	Group0  uint32
}

// linuxUcred is struct ucred, what SO_PEERCRED returns on Linux.
type linuxUcred struct {
	Pid int32
	Uid uint32
	Gid uint32
}

// darwinPeercred answers a Linux SO_PEERCRED getsockopt from Apple's two
// SOL_LOCAL options: LOCAL_PEERPID for the pid, LOCAL_PEERCRED for the
// uid and primary gid. Either failing fails the call - a half-filled
// ucred would report a real pid beside an invented uid.
//
//go:nosplit
func darwinPeercred(s, val, vallenp uintptr) (r1, r2, errno uintptr) {
	if val == 0 || vallenp == 0 {
		return ^uintptr(0), 0, darwinEFAULT
	}
	if *(*uint32)(unsafe.Pointer(vallenp)) < 12 {
		return ^uintptr(0), 0, darwinEINVAL
	}

	// Flat calls to the libc trampoline, not darwinCall: this sits under
	// darwinGetsockopt on an already-deep nosplit chain, where that
	// helper's frame does not fit (same reason as darwinSetNoSigpipe).
	var pid int32
	pidLen := uint32(4)
	if int64(darwinLibcCall6(darwinFns.Getsockopt, s, appleSOL_LOCAL, appleLOCAL_PEERPID,
		uintptr(unsafe.Pointer(&pid)), uintptr(unsafe.Pointer(&pidLen)), 0)) == -1 {
		return ^uintptr(0), 0, darwinErrno()
	}

	var xu appleXucredHead
	xuLen := uint32(unsafe.Sizeof(xu))
	if int64(darwinLibcCall6(darwinFns.Getsockopt, s, appleSOL_LOCAL, appleLOCAL_PEERCRED,
		uintptr(unsafe.Pointer(&xu)), uintptr(unsafe.Pointer(&xuLen)), 0)) == -1 {
		return ^uintptr(0), 0, darwinErrno()
	}
	gid := uint32(0)
	if xu.Ngroups > 0 {
		gid = xu.Group0 // Linux reports the primary gid here
	}

	uc := (*linuxUcred)(unsafe.Pointer(val))
	uc.Pid = pid
	uc.Uid = xu.Uid
	uc.Gid = gid
	*(*uint32)(unsafe.Pointer(vallenp)) = 12
	return 0, 0, 0
}

//go:nosplit
func darwinGetsockopt(s, level, name, val, vallenp uintptr) (r1, r2, errno uintptr) {
	if darwinFns.Getsockopt == 0 {
		return ^uintptr(0), 0, darwinENOSYS
	}
	if level == linuxSOL_SOCKET && name == linuxSO_PEERCRED {
		return darwinPeercred(s, val, vallenp)
	}
	alevel, aname, ok := darwinSockoptXlat(level, name)
	if !ok {
		return ^uintptr(0), 0, darwinENOPROTOOPT
	}
	r1, r2, errno = darwinCall(darwinFns.Getsockopt, s, alevel, aname, val, vallenp, 0)
	if errno == 0 && level == 1 && name == 4 && val != 0 && vallenp != 0 &&
		*(*uint32)(unsafe.Pointer(vallenp)) == 4 {
		// SO_ERROR reports a saved errno with APPLE numbering (e.g. a
		// refused nonblocking connect stores 61); Go compares against
		// Linux values, so translate the payload too.
		ep := (*uint32)(unsafe.Pointer(val))
		if *ep != 0 {
			*ep = uint32(xlatErrnoDarwin(uintptr(*ep)))
		}
	}
	return r1, r2, errno
}
