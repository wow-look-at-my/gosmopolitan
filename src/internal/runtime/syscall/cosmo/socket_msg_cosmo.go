// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package cosmo

import "unsafe"

// Darwin (macOS ARM64) sendmsg/recvmsg emulation: the layout-translation
// core. Linux and Apple disagree on struct msghdr field widths (Linux
// iovlen/controllen are size_t=u64 where Apple's are int32/u32) and on
// struct cmsghdr entirely (Linux {Len u64, Level i32, Type i32}, 16-byte
// header, data aligned to 8; Apple {Len u32, Level i32, Type i32},
// 12-byte header, data aligned to 4 - __DARWIN_ALIGN32), so control
// buffers must be repacked cmsg-by-cmsg, never passed through whole.
// struct iovec is {base, len} on both systems, so iovec arrays DO pass
// through untouched.
//
// The work is split across the syscall boundary by cost:
//
//   - The dlsym-dispatch side (darwinSendmsg/darwinRecvmsg in
//     socket_cosmo_arm64.go) runs inside the _Gsyscall window, where
//     every frame is nosplit and the whole chain must fit the linker's
//     792-byte budget - no room for sockaddr scratch or cmsg repack
//     buffers. It performs only the FIXED-SIZE msghdr re-shaping
//     (field widths, iovlen bound, result-flag values) and passes the
//     msg_name and msg_control POINTERS through untouched. Its
//     contract is therefore: those buffers' BYTES are Apple-shaped in
//     both directions.
//   - Package syscall's sendmsgN/recvmsgRaw darwin branches
//     (syscall_cosmo_msg.go there) run as ordinary Go BEFORE and
//     AFTER the window, where allocation is legal: they translate the
//     sockaddr and repack the control buffer via the exported helpers
//     below, emulate MSG_CMSG_CLOEXEC (Apple has no such flag), and
//     close fds a truncation cannot deliver. Raw syscall.Syscall
//     users of SYS_SENDMSG/SYS_RECVMSG on a macOS host get the
//     Apple-shaped-buffer contract - exactly the raw-msghdr shapes
//     the runtimeprobe sendmsg check exercises (nil name, nil
//     control, multi-iovec data).
//
// This file is deliberately arch-independent (//go:build cosmo, not
// cosmo && arm64): everything in it is pure byte manipulation over
// caller-provided buffers, byte-identical on both cosmo architectures,
// which lets the repack logic run under GOOS=cosmo on any host arch -
// socket_msg_cosmo_test.go exercises it on the Linux CI leg. Fd side
// effects (applying close-on-exec, closing fds a truncation drops) are
// taken as function values so the callers choose the environment:
// package syscall passes fcntl/close closures, tests pass recorders.

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
// controllen and 0, or 0 and a Linux errno. Policy mirrors what
// Linux's af_unix send path does with each record (verified against a
// live kernel for the NT emulation, DEBUGGING.md wave 3 item 2b):
//
//   - SOL_SOCKET/SCM_RIGHTS: translated (level 1 -> 0xffff), the fd
//     payload copied unchanged.
//   - non-SOL_SOCKET levels: silently skipped (__scm_send's continue).
//   - SOL_SOCKET/SCM_CREDENTIALS: EOPNOTSUPP (Linux validates
//     credentials this emulation cannot).
//   - other SOL_SOCKET types: EINVAL, and malformed records (cmsg_len
//     shorter than a header or overrunning the buffer) are EINVAL.
//
// A repack that outgrows dstCap fails with ENOBUFS (the shape of
// Linux's own optmem_max overflow); callers size dst >= srcLen, which
// always suffices because the Apple shape of any record is strictly
// smaller than its Linux shape. A buffer that skips down to nothing
// returns dlen 0 and the caller sends with no control at all, which
// is exactly what Linux transmits in that case.
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
// Apple recvmsg just delivered into buf (alen bytes of a buffer whose
// caller-visible capacity is cap - Apple needs less space than Linux
// for the same payload, so the caller's Linux-provisioned buffer
// always had room for the Apple shape) into Linux-shaped records,
// returning the Linux controllen and whether control data was
// truncated to make the larger Linux shape fit.
//
// Record policy: SOL_SOCKET(0xffff)/SCM_RIGHTS records translate;
// everything else is dropped - no other record type can arrive, since
// the sockopt layer cannot enable timestamping and friends on macOS
// hosts (darwinSockoptXlat refuses the options). Truncation semantics
// mirror the Linux kernel's, pinned live for the NT emulation
// (DEBUGGING.md wave 3 item 2b): SCM_RIGHTS truncates at fd
// granularity - (avail-hdr)/4 fds delivered, so CMSG_SPACE alignment
// slack still carries whole fds ("a 24-byte buffer receives TWO fds") -
// with every undelivered fd CLOSED (never leaked into the process) and
// MSG_CTRUNC raised; a record that cannot fit even one fd (or any
// non-rights record that cannot fit whole) is dropped entirely, and
// the final record may sit flush against cap without its alignment
// padding (the kernel's tight-fit rule).
//
// applyFd is invoked for every DELIVERED rights fd (package syscall
// passes its close-on-exec setter when the caller asked for
// MSG_CMSG_CLOEXEC, which Apple lacks); closeFd for every dropped one.
// Either may be nil. All fd values are read before any bytes are
// rewritten.
//
// The in-place rewrite runs in three stages so no write can overrun
// unread input: (1) compact translatable Apple records to the front of
// the buffer (records only shrink or hold position: forward-safe), (2)
// plan Linux offsets against cap, apply fd side effects, and shorten
// the cut record's Apple header in place if the plan truncates
// mid-record, (3) expand records back to front (per record the write
// cursor is at or past the read cursor - Linux records are strictly
// larger - with the payload copied high-to-low and the header written
// only after its payload has moved).
func CmsgToLinux(buf, alen, capacity uintptr, applyFd, closeFd func(int32)) (llen uintptr, ctrunc bool) {
	// Stage 1: compact translatable records to the buffer front.
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

	// Stage 2: plan Linux offsets against cap; apply fd side effects.
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

	// Stage 3: expand back to front.
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
