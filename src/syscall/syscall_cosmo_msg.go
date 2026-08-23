// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import (
	"internal/runtime/syscall/cosmo"
	"unsafe"
)

// Darwin-host halves of sendmsg/recvmsg. A GOOS=cosmo binary speaks
// the Linux ABI everywhere, but on a macOS host SYS_SENDMSG/
// SYS_RECVMSG reach a dlsym-dispatched Apple libc whose struct msghdr,
// struct sockaddr and struct cmsghdr all differ from Linux's. The
// dispatch side (internal/runtime/syscall/cosmo, socket_cosmo_arm64.go)
// runs inside the _Gsyscall window where every frame is nosplit, so it
// can only re-shape the fixed-size msghdr; everything unbounded - the
// sockaddr translation, the cmsg repack, the MSG_CMSG_CLOEXEC
// emulation, closing fds a truncation cannot deliver - happens HERE,
// as ordinary Go before and after the syscall, in the darwin branches
// recvmsgRaw and sendmsgN take (the single funnel every caller uses:
// Recvmsg, Sendmsg(N), their Inet4/Inet6 variants, and net's
// ReadMsg*/WriteMsg* via internal/poll).
//
// The resulting raw-syscall contract on macOS hosts only: a direct
// syscall.Syscall(SYS_SENDMSG/SYS_RECVMSG) caller's msg_name and
// msg_control buffers cross the boundary with Apple-shaped BYTES
// (widths and flag values are still adapted by the dispatch side).
// Raw msghdr I/O with nil name and nil control - the only raw shape
// anything in the tree issues - behaves identically on every host.

// appleAF_INET6 is Apple's AF_INET6 value (Linux's is 10); the other
// admitted families (AF_UNSPEC/AF_UNIX/AF_INET) coincide. Same table
// as the sendto/recvfrom emulation's darwinSockFamilyToApple.
const appleAF_INET6 = 30

// darwinSockaddrToApple copies the Linux sockaddr at (ptr, salen) into
// buf as an Apple sockaddr ({u8 sa_len, u8 sa_family} in place of the
// u16 family; payloads coincide for every admitted family) and returns
// the Apple namelen. A nil/empty address passes through as 0 - e.g.
// sendmsg on a connected socket. Mirrors the emulation's
// darwinSockaddrOut: abstract AF_UNIX names (leading NUL) are
// Linux-only and refused EINVAL, unknown families EAFNOSUPPORT.
func darwinSockaddrToApple(buf *[SizeofSockaddrAny]byte, ptr unsafe.Pointer, salen int) (alen int, err error) {
	if ptr == nil || salen == 0 {
		return 0, nil
	}
	if salen < 2 || salen > len(buf) {
		return 0, EINVAL
	}
	fam := *(*uint16)(ptr)
	var afam byte
	switch fam {
	case AF_UNSPEC, AF_UNIX, AF_INET:
		afam = byte(fam)
	case AF_INET6:
		afam = appleAF_INET6
	default:
		return 0, EAFNOSUPPORT
	}
	src := unsafe.Slice((*byte)(ptr), salen)
	if fam == AF_UNIX && salen > 2 && src[2] == 0 {
		return 0, EINVAL // abstract socket namespace is Linux-only
	}
	copy(buf[2:salen], src[2:])
	buf[0] = byte(salen) // sa_len
	buf[1] = afam
	return salen, nil
}

// darwinFixRecvSockaddr rewrites, in place, the Apple sockaddr recvmsg
// just delivered into rsa back to the Linux shape: Apple's {sa_len,
// sa_family} bytes become the u16 Linux family (AF_INET6 30 -> 10).
// The payload bytes are already Linux-shaped. Mirrors the emulation's
// darwinSockaddrIn.
func darwinFixRecvSockaddr(rsa *RawSockaddrAny, namelen uint32) {
	if rsa == nil || namelen < 2 {
		return
	}
	afam := (*[2]byte)(unsafe.Pointer(rsa))[1]
	fam := uint16(afam)
	if afam == appleAF_INET6 {
		fam = AF_INET6
	}
	*(*uint16)(unsafe.Pointer(rsa)) = fam
}

// darwinRecvmsgRaw is recvmsgRaw's macOS-host branch: the Linux body
// with the boundary translations bolted on. The caller's oob buffer is
// handed to Apple recvmsg directly (Apple's cmsg shape needs LESS
// space than Linux's for the same payload, so a Linux-provisioned
// buffer always has room), then repacked in place into Linux-shaped
// records by cosmo.CmsgToLinux, which invokes the callbacks: the
// close-on-exec setter for each delivered rights fd when
// MSG_CMSG_CLOEXEC was requested (Apple has no such flag - it is
// stripped before the call and emulated here with fcntl, the same
// post-receive window upstream GOOS=darwin's net layer has), and
// Close for each fd the larger Linux shape cannot fit (MSG_CTRUNC
// raised, fds never leaked - the kernel's own truncation hygiene).
func darwinRecvmsgRaw(fd int, p, oob []byte, flags int, rsa *RawSockaddrAny) (n, oobn int, recvflags int, err error) {
	var msg Msghdr
	msg.Name = (*byte)(unsafe.Pointer(rsa))
	msg.Namelen = uint32(SizeofSockaddrAny)
	var iov Iovec
	if len(p) > 0 {
		iov.Base = &p[0]
		iov.SetLen(len(p))
	}
	var dummy byte
	if len(oob) > 0 {
		if len(p) == 0 {
			var sockType int
			sockType, err = GetsockoptInt(fd, SOL_SOCKET, SO_TYPE)
			if err != nil {
				return
			}
			if sockType != SOCK_DGRAM {
				iov.Base = &dummy
				iov.SetLen(1)
			}
		}
		msg.Control = &oob[0]
		msg.SetControllen(len(oob))
	}
	msg.Iov = &iov
	msg.Iovlen = 1
	cloexec := flags&MSG_CMSG_CLOEXEC != 0
	flags &^= MSG_CMSG_CLOEXEC
	if n, err = recvmsg(fd, &msg, flags); err != nil {
		return
	}
	darwinFixRecvSockaddr(rsa, msg.Namelen)
	recvflags = int(msg.Flags)
	if msg.Controllen > 0 {
		var apply func(int32)
		if cloexec {
			apply = func(rfd int32) { fcntl(int(rfd), F_SETFD, FD_CLOEXEC) }
		}
		llen, ctrunc := cosmo.CmsgToLinux(uintptr(unsafe.Pointer(&oob[0])),
			uintptr(msg.Controllen), uintptr(len(oob)),
			apply, func(rfd int32) { Close(int(rfd)) })
		oobn = int(llen)
		if ctrunc {
			recvflags |= MSG_CTRUNC
		}
	}
	return
}

// darwinSendmsgN is sendmsgN's macOS-host branch: the Linux body with
// the sockaddr translated into an Apple-shaped local and the control
// buffer repacked (never in place - the caller's oob is input and must
// survive, e.g. for a retry loop) into an Apple-shaped allocation by
// cosmo.CmsgToApple. SCM_RIGHTS fd payloads copy through unchanged;
// non-SOL_SOCKET records are skipped exactly like Linux's af_unix send
// path, so callers cannot tell the two hosts apart.
func darwinSendmsgN(fd int, p, oob []byte, ptr unsafe.Pointer, salen _Socklen, flags int) (n int, err error) {
	var msg Msghdr
	var nameBuf [SizeofSockaddrAny]byte
	alen, err := darwinSockaddrToApple(&nameBuf, ptr, int(salen))
	if err != nil {
		return 0, err
	}
	if alen > 0 {
		msg.Name = &nameBuf[0]
		msg.Namelen = uint32(alen)
	}
	var iov Iovec
	if len(p) > 0 {
		iov.Base = &p[0]
		iov.SetLen(len(p))
	}
	var dummy byte
	if len(oob) > 0 {
		if len(p) == 0 {
			var sockType int
			sockType, err = GetsockoptInt(fd, SOL_SOCKET, SO_TYPE)
			if err != nil {
				return 0, err
			}
			if sockType != SOCK_DGRAM {
				iov.Base = &dummy
				iov.SetLen(1)
			}
		}
		actl := make([]byte, len(oob)) // Apple shape is never larger
		dlen, errno := cosmo.CmsgToApple(uintptr(unsafe.Pointer(&oob[0])), uintptr(len(oob)),
			uintptr(unsafe.Pointer(&actl[0])), uintptr(len(actl)))
		if errno != 0 {
			return 0, Errno(errno)
		}
		if dlen > 0 {
			msg.Control = &actl[0]
			msg.SetControllen(int(dlen))
		}
	}
	msg.Iov = &iov
	msg.Iovlen = 1
	if n, err = sendmsg(fd, &msg, flags); err != nil {
		return 0, err
	}
	if len(oob) > 0 && len(p) == 0 {
		n = 0
	}
	return n, nil
}
