// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

import "unsafe"

// Darwin sendmsg/recvmsg emulation: the layout-translation core.
//
// Linux and Apple disagree on msghdr field widths and on cmsghdr
// entirely (see the geometry constants below), so a control buffer is
// repacked cmsg-by-cmsg, NEVER passed through whole. struct iovec is
// {base, len} on both, so an iovec array does pass through untouched.
//
// This file is arch-independent on purpose: it is pure byte
// manipulation over caller-provided buffers, so the repack runs under
// GOOS=cosmo on any host arch and its test runs on the Linux CI leg.
// An fd side effect is taken as a function value, so the caller
// chooses: syscall passes closures and a test passes a recorder.

// Linux (= cosmo ABI) and Apple cmsg geometry, verified against
// syscall/ztypes_cosmo_arm64.go and syscall/ztypes_darwin_arm64.go.
const (
	linuxCmsgHdrLen = 16 // sizeof(struct cmsghdr): Len u64, Level i32, Type i32
	linuxCmsgAlign  = 8  // CMSG_ALIGN = sizeof(size_t)
	appleCmsgHdrLen = 12 // sizeof(struct cmsghdr): Len u32, Level i32, Type i32
	appleCmsgAlign  = 4  // __DARWIN_ALIGN32
)

// Socket levels and control-message types (values verified against
// syscall/zerrors_cosmo_arm64.go and syscall/zerrors_darwin_arm64.go).
const (
	linuxSOL_SOCKET      = 1
	appleSOL_SOCKET      = 0xffff
	scmRights            = 1 // SCM_RIGHTS: same value on both systems
	linuxSCM_CREDENTIALS = 2
)

// msg_flags bits. OOB/PEEK/DONTROUTE coincide (0x1/0x2/0x4, admitted by
// darwinCheckMsgFlags); the result flags below differ and are
// translated by XlatMsgFlags.
const (
	linuxMSG_OOB    = 0x1
	linuxMSG_CTRUNC = 0x8
	linuxMSG_TRUNC  = 0x20
	linuxMSG_EOR    = 0x80

	appleMSG_OOB    = 0x1
	appleMSG_EOR    = 0x8
	appleMSG_TRUNC  = 0x10
	appleMSG_CTRUNC = 0x20
)

// Errno values (Linux numbering) this emulation produces itself.
const (
	cmsgEINVAL     = 22
	cmsgEMSGSIZE   = 90
	cmsgEOPNOTSUPP = 95
	cmsgENOBUFS    = 105
)

// msgMaxIovlen mirrors Linux's UIO_MAXIOV (Apple's IOV_MAX is also
// 1024); larger vectors are refused with sendmsg/recvmsg's exact errno,
// EMSGSIZE. It also guarantees the Linux u64 iovlen fits Apple's int32.
const msgMaxIovlen = 1024

// msgMaxCmsgRecords bounds the receive-side repack's per-call record
// bookkeeping. The kernel coalesces SCM_RIGHTS into a single cmsg per
// message and nothing else survives translation, so >1 delivered
// record is already exotic; records past the bound are dropped like a
// capacity truncation (their fds closed, MSG_CTRUNC raised) instead of
// corrupting anything.
const msgMaxCmsgRecords = 16

// linuxMsghdr mirrors syscall.Msghdr for GOOS=cosmo (both
// architectures; Linux amd64 and arm64 agree): Name@0, Namelen@8,
// Iov@16, Iovlen@24 (u64), Control@32, Controllen@40 (u64), Flags@48.
// Pointers are held as uintptr: the referents are caller-owned buffers
// kept alive across the syscall by the caller, exactly as in the rest
// of this emulation.
type linuxMsghdr struct {
	Name       uintptr
	Namelen    uint32
	_          int32
	Iov        uintptr
	Iovlen     uint64
	Control    uintptr
	Controllen uint64
	Flags      int32
	_          int32
}

// appleMsghdr matches Apple's struct msghdr on arm64: Name@0,
// Namelen@8 (u32), Iov@16, Iovlen@24 (int32), Control@32,
// Controllen@40 (u32), Flags@44.
type appleMsghdr struct {
	Name       uintptr
	Namelen    uint32
	_          int32
	Iov        uintptr
	Iovlen     int32
	_          int32
	Control    uintptr
	Controllen uint32
	Flags      int32
}

// darwinHost records that SetDarwinFns installed the darwin emulation
// table, i.e. this process is running on a macOS host. Constant after
// boot (set before any user code, only on the arm64 XNU path).
var darwinHost bool

// Darwin reports whether this process is running on a macOS host and
// the darwin syscall emulation is active. Package syscall dispatches
// its sendmsg/recvmsg translation on it, the way os/exec dispatches
// NT lookup semantics on Windows().
func Darwin() bool {
	return darwinHost
}

// XlatMsgFlags translates Apple recvmsg result flags (msg_flags) to
// Linux values. Only the four bits a receive can report and Go can
// observe are mapped; anything else Apple-specific is dropped rather
// than aliased onto an unrelated Linux bit. Nosplit: darwinRecvmsg
// calls it inside the _Gsyscall window.
//
//go:nosplit
func XlatMsgFlags(aflags int32) int32 {
	var out int32
	if aflags&appleMSG_OOB != 0 {
		out |= linuxMSG_OOB
	}
	if aflags&appleMSG_EOR != 0 {
		out |= linuxMSG_EOR
	}
	if aflags&appleMSG_TRUNC != 0 {
		out |= linuxMSG_TRUNC
	}
	if aflags&appleMSG_CTRUNC != 0 {
		out |= linuxMSG_CTRUNC
	}
	return out
}

// CmsgToApple repacks a Linux-shaped control buffer at (src, srcLen)
// into an Apple-shaped one at (dst, dstCap), returning the Apple
// controllen and 0, or 0 and a Linux errno. Each record is handled on
// the case that matches it, mirroring Linux's af_unix send path.
//
// A repack that outgrows dstCap fails ENOBUFS, the shape of Linux's own
// optmem_max overflow. A caller sizes dst >= srcLen, which always
// suffices, because the Apple shape of a record is strictly smaller
// than its Linux shape. A buffer that skips down to nothing returns
// dlen 0 and the caller sends no control at all, which is what Linux
// transmits in that case.
func CmsgToApple(src, srcLen, dst, dstCap uintptr) (dlen, errno uintptr) {
	if srcLen < linuxCmsgHdrLen {
		return 0, cmsgEINVAL
	}
	var soff, doff uintptr
	for srcLen-soff >= linuxCmsgHdrLen {
		cl := uintptr(*(*uint64)(unsafe.Pointer(src + soff)))
		level := *(*int32)(unsafe.Pointer(src + soff + 8))
		typ := *(*int32)(unsafe.Pointer(src + soff + 12))
		if cl < linuxCmsgHdrLen || cl > srcLen-soff {
			return 0, cmsgEINVAL
		}
		dataLen := cl - linuxCmsgHdrLen
		next := soff + ((cl + linuxCmsgAlign - 1) &^ (linuxCmsgAlign - 1))
		if next > srcLen {
			next = srcLen // final record: its alignment padding may be absent
		}
		switch {
		case level != linuxSOL_SOCKET:
			// Skipped, like Linux's unix send path.
		case typ == scmRights:
			acl := appleCmsgHdrLen + dataLen
			aspace := (acl + appleCmsgAlign - 1) &^ (appleCmsgAlign - 1)
			if aspace > dstCap-doff {
				return 0, cmsgENOBUFS
			}
			*(*uint32)(unsafe.Pointer(dst + doff)) = uint32(acl)
			*(*int32)(unsafe.Pointer(dst + doff + 4)) = appleSOL_SOCKET
			*(*int32)(unsafe.Pointer(dst + doff + 8)) = scmRights
			for i := uintptr(0); i < dataLen; i++ {
				*(*byte)(unsafe.Pointer(dst + doff + appleCmsgHdrLen + i)) =
					*(*byte)(unsafe.Pointer(src + soff + linuxCmsgHdrLen + i))
			}
			for i := acl; i < aspace; i++ {
				*(*byte)(unsafe.Pointer(dst + doff + i)) = 0
			}
			doff += aspace
		case typ == linuxSCM_CREDENTIALS:
			return 0, cmsgEOPNOTSUPP
		default:
			return 0, cmsgEINVAL
		}
		soff = next
	}
	return doff, 0
}

// CmsgToLinux rewrites, IN PLACE, the Apple-shaped control records
// Apple recvmsg delivered into buf, returning the Linux controllen and
// whether control data was truncated to make the larger Linux shape
// fit. A Linux-provisioned buffer always had room for the Apple shape.
//
// Only SOL_SOCKET/SCM_RIGHTS translates; everything else drops, and
// nothing else can arrive because darwinSockoptXlat refuses the
// options that produce it. Truncation mirrors the Linux kernel:
// SCM_RIGHTS truncates at FD granularity, every undelivered fd is
// CLOSED rather than leaked, and MSG_CTRUNC is raised. applyFd runs for
// each delivered fd and closeFd for each dropped one; either may be
// nil, and every fd is read before any byte is rewritten.
func CmsgToLinux(buf, alen, capacity uintptr, applyFd, closeFd func(int32)) (llen uintptr, ctrunc bool) {
	// Three stages, so no write can overrun unread input.
	//
	// Stage 1: compact translatable records to the buffer front. A
	// record only shrinks or holds position, so this is forward-safe.
	var srcOffs [msgMaxCmsgRecords]uint32
	var n int
	var roff, woff uintptr
	for alen-roff >= appleCmsgHdrLen {
		acl := uintptr(*(*uint32)(unsafe.Pointer(buf + roff)))
		level := *(*int32)(unsafe.Pointer(buf + roff + 4))
		typ := *(*int32)(unsafe.Pointer(buf + roff + 8))
		if acl < appleCmsgHdrLen || acl > alen-roff {
			break // malformed tail: treat the rest as absent
		}
		next := roff + ((acl + appleCmsgAlign - 1) &^ (appleCmsgAlign - 1))
		if next > alen {
			next = alen
		}
		if level == appleSOL_SOCKET && typ == scmRights {
			if n == msgMaxCmsgRecords {
				// Bookkeeping bound: treat like a capacity cut.
				forEachRightsFd(buf+roff+appleCmsgHdrLen, (acl-appleCmsgHdrLen)/4, closeFd)
				ctrunc = true
			} else {
				if woff != roff {
					for i := uintptr(0); i < acl; i++ {
						*(*byte)(unsafe.Pointer(buf + woff + i)) =
							*(*byte)(unsafe.Pointer(buf + roff + i))
					}
				}
				srcOffs[n] = uint32(woff)
				n++
				woff += (acl + appleCmsgAlign - 1) &^ (appleCmsgAlign - 1)
			}
		}
		roff = next
	}

	// Stage 2: plan Linux offsets against cap and apply the fd side
	// effects. A record that cannot fit even one fd is dropped whole,
	// and the final record may sit flush against cap without its
	// alignment padding - the kernel's tight-fit rule. If the plan cuts
	// mid-record, shorten that record's Apple header in place.
	var dstOffs [msgMaxCmsgRecords]uint32
	kept := 0
	var dst uintptr
	for k := 0; k < n; k++ {
		soff := uintptr(srcOffs[k])
		acl := uintptr(*(*uint32)(unsafe.Pointer(buf + soff)))
		dataLen := acl - appleCmsgHdrLen
		nfds := dataLen / 4
		need := linuxCmsgHdrLen + dataLen
		if need > capacity-dst {
			// Cut point: deliver as many whole fds as still fit.
			avail := capacity - dst
			var fit uintptr
			if avail >= linuxCmsgHdrLen+4 {
				fit = (avail - linuxCmsgHdrLen) / 4
			}
			if fit > 0 {
				// Shorten the record's Apple header in place; stage 3
				// then copies it like any other record.
				*(*uint32)(unsafe.Pointer(buf + soff)) = uint32(appleCmsgHdrLen + fit*4)
				forEachRightsFd(buf+soff+appleCmsgHdrLen, fit, applyFd)
				forEachRightsFd(buf+soff+appleCmsgHdrLen+fit*4, nfds-fit, closeFd)
				dstOffs[k] = uint32(dst)
				kept = k + 1
				dst += linuxCmsgHdrLen + fit*4
			} else {
				forEachRightsFd(buf+soff+appleCmsgHdrLen, nfds, closeFd)
			}
			// Every later record is dropped whole.
			for j := k + 1; j < n; j++ {
				joff := uintptr(srcOffs[j])
				jcl := uintptr(*(*uint32)(unsafe.Pointer(buf + joff)))
				forEachRightsFd(buf+joff+appleCmsgHdrLen, (jcl-appleCmsgHdrLen)/4, closeFd)
			}
			ctrunc = true
			break
		}
		forEachRightsFd(buf+soff+appleCmsgHdrLen, nfds, applyFd)
		dstOffs[k] = uint32(dst)
		kept = k + 1
		space := (need + linuxCmsgAlign - 1) &^ (linuxCmsgAlign - 1)
		if space > capacity-dst {
			dst = capacity // tight fit: final record keeps no padding
		} else {
			dst += space
		}
	}
	llen = dst

	// Stage 3: expand back to front. Per record the write cursor is at
	// or past the read cursor, because a Linux record is strictly
	// larger, so copy the payload high to low and write the header only
	// after its payload has moved.
	for k := kept - 1; k >= 0; k-- {
		soff := uintptr(srcOffs[k])
		doff := uintptr(dstOffs[k])
		dataLen := uintptr(*(*uint32)(unsafe.Pointer(buf + soff))) - appleCmsgHdrLen
		for i := dataLen; i > 0; i-- {
			*(*byte)(unsafe.Pointer(buf + doff + linuxCmsgHdrLen + i - 1)) =
				*(*byte)(unsafe.Pointer(buf + soff + appleCmsgHdrLen + i - 1))
		}
		*(*uint64)(unsafe.Pointer(buf + doff)) = uint64(linuxCmsgHdrLen + dataLen)
		*(*int32)(unsafe.Pointer(buf + doff + 8)) = linuxSOL_SOCKET
		*(*int32)(unsafe.Pointer(buf + doff + 12)) = scmRights
		// Zero the record's alignment padding (bounded by llen: the
		// final record may have kept none).
		padEnd := doff + ((linuxCmsgHdrLen + dataLen + linuxCmsgAlign - 1) &^ (linuxCmsgAlign - 1))
		if padEnd > llen {
			padEnd = llen
		}
		for i := doff + linuxCmsgHdrLen + dataLen; i < padEnd; i++ {
			*(*byte)(unsafe.Pointer(buf + i)) = 0
		}
	}
	return llen, ctrunc
}

// forEachRightsFd invokes fn on each of the count int32 fds at p; a nil
// fn is a no-op (tests and the no-side-effect paths pass nil).
func forEachRightsFd(p, count uintptr, fn func(int32)) {
	if fn == nil {
		return
	}
	for i := uintptr(0); i < count; i++ {
		fn(*(*int32)(unsafe.Pointer(p + 4*i)))
	}
}
