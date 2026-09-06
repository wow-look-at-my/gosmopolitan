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
// ANCILLARY DATA (wave 3 item 2b): SCM_RIGHTS fd passing over
// pathname AF_UNIX SOCK_STREAM sockets, sender-push model. There is
// no NT primitive for attaching handles to socket messages, so the
// emulation defines a WIRE FRAME that rides the ordinary afunix byte
// stream, emitted only by sendmsg calls that actually carry rights
// (plain sends stay unframed, wire-compatible with write/send):
//
//	offset  size  field
//	0       8     magic F5 "SCMRIG" '0' (improbable first byte -
//	              0xF5 is illegal UTF-8 lead and non-ASCII; the
//	              final byte is the protocol VERSION)
//	8       4     nfds (u32, <= ntSCMMaxFD)
//	12      4     sender pid (u32, diagnostic only)
//	16      4     dataLen (u32): payload bytes after the records
//	20      4     reserved (0)
//	24      ...   nfds records, then dataLen data bytes
//
//	record: u32 kind (ntSCMKind*), u32 Linux O_* flags, then
//	  kind file/pipe: u64 receiver-relative HANDLE value
//	  kind sock:      u16 infoLen (= 628) + WSAPROTOCOL_INFOW bytes
//
// All fields little-endian. SENDER-PUSH: the transfer happens at
// sendmsg time - WSADuplicateSocketW(s, peerPid) for sockets,
// OpenProcess(PROCESS_DUP_HANDLE, peerPid) + DuplicateHandle for
// files/pipes - so the sender may close (or exit) immediately after
// sendmsg returns, the Linux invariant. (Receiver-pull was rejected
// for exactly that reason: the source handle could be gone or
// recycled by recvmsg time.) The peer pid comes from the afunix.sys
// SIO_AF_UNIX_GETPEERPID WSAIoctl and is cached on the fd entry (a
// connection's peer can never change). The whole frame + data goes
// out as ONE vectored WSASend (looped to completion on short sends).
// recvmsg-with-a-control-buffer MSG_PEEKs 8 bytes: on magic match it
// consumes the self-delimiting frame, reconstructs the fds (sockets
// via WSASocketW(FROM_PROTOCOL_INFO), files/pipes by inserting the
// already-receiver-relative handle), synthesizes the Linux SCM_RIGHTS
// cmsg, and returns the data; otherwise the plain path runs.
//
// HONEST LIMITS, by design:
//   - Both ends must be cosmo-Go binaries speaking this frame. A
//     foreign peer reads frame bytes as data; a foreign sender's raw
//     bytes could alias the magic with probability ~2^-64 per message
//     boundary (surfaced as EBADMSG/EPROTO, never silent corruption).
//     Likewise the RECEIVER must ask for the rights: a plain read
//     concurrent with a frame arrival sees frame bytes as data (Linux
//     instead quietly discards the fds there).
//   - Same-user only: OpenProcess(PROCESS_DUP_HANDLE) across users
//     needs privileges the emulation does not negotiate.
//   - Pathname AF_UNIX SOCK_STREAM carriers only. socketpair ends are
//     refused EOPNOTSUPP as carriers AND as payload (their peer is by
//     construction in-process - ExtraFiles is ENOSYS, so a pair end
//     can never reach another process - and their backing loopback
//     TCP identity must not leak). dir/stdio fds are refused
//     EOPNOTSUPP as payload; files, pipes and non-pair sockets pass.
//   - A socket whose LAST sender-side handle closes before the
//     receiver imports the WSAPROTOCOL_INFOW is provider-dependent
//     (msafd keeps it importable in practice); receivers should
//     import before signaling the sender to close, which the fdpass
//     probe sequences deliberately.
//   - MSG_PEEK on a frame is refused EOPNOTSUPP (a peek cannot
//     deliver fds nondestructively); MSG_CMSG_CLOEXEC is already
//     refused EINVAL by ntMsgFlags like every untranslatable flag,
//     so received fds have CLOEXEC clear, the Linux default.
//   - Data bytes past the caller's iovecs are consumed and DISCARDED
//     with MSG_TRUNC (the frame is a unit; Linux would leave stream
//     tails readable). Control-buffer truncation follows Linux
//     exactly: deliver fdmax = (controllen-16)/4 fds, close the
//     overflow, raise MSG_CTRUNC.
//
// Errno contract (verified against a live Linux kernel, 2026-07-19):
// SCM_RIGHTS on an INET/INET6 socket is silently DROPPED and the data
// sent (__sock_cmsg_send: "semantically in SOL_UNIX"); non-SOL_SOCKET
// cmsg levels are skipped; an unknown SOL_SOCKET cmsg type is EINVAL;
// a bad payload fd is EBADF before any data moves; SCM_CREDENTIALS is
// EOPNOTSUPP here (Linux validates credentials we cannot). Win32
// failures mid-transfer map through ntErrno/ntWSAToLinux (EPROTO for
// the no-progress degenerate case); a torn or malformed frame - bad
// version byte, absurd nfds/dataLen, EOF mid-frame, wrong infoLen -
// is EBADMSG.

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

// ---- SCM_RIGHTS fd passing (wave 3 item 2b) ----

const (
	// Linux ancillary constants (amd64). Cmsghdr is {Len u64, Level
	// i32, Type i32} with 8-byte CMSG alignment; Len includes the
	// 16-byte header.
	_NT_SOL_SOCKET = 1
	_NT_SCM_RIGHTS = 1
	_NT_MSG_CTRUNC = 0x8

	// ntSCMMaxFD caps the fds one sendmsg may carry. Linux's own cap
	// is SCM_MAX_FD=253 per message; 64 keeps the worst-case frame
	// (~41 KiB of WSAPROTOCOL_INFOW records) safely under afunix's
	// default socket buffer, and nothing real passes more than a
	// handful. Past the cap: EINVAL, the errno Linux uses past ITS
	// cap (scm_fp_copy).
	ntSCMMaxFD = 64

	// SIO_AF_UNIX_GETPEERPID: afunix.sys's _WSAIOR(IOC_VENDOR, 256)
	// ioctl answering the connected peer's pid as a ULONG. Shipped in
	// the SDK's afunix.h and stable since Win10 17063; the moral
	// equivalent of SO_PEERCRED's pid field, which afunix lacks.
	_NT_SIO_AF_UNIX_GETPEERPID = 0x58000100

	_NT_PROCESS_DUP_HANDLE     = 0x0040
	_NT_DUPLICATE_CLOSE_SOURCE = 0x1

	// FROM_PROTOCOL_INFO (-1) for each of WSASocketW's af/type/proto
	// when importing a WSAPROTOCOL_INFOW; win64 int parameters read
	// the low 32 bits, so all-ones works.
	_NT_FROM_PROTOCOL_INFO = ^uintptr(0)

	// WSAPROTOCOL_INFOW is 628 bytes on x64 (five u32s of flags, the
	// provider GUID, the catalog id, a WSAPROTOCOLCHAIN of 4+7*4,
	// nine i32s, two more u32s, szProtocol[256] WCHARs). Carried as
	// an opaque blob; the only field the receiver interprets is
	// iAddressFamily (i32, NT numbering) at offset 76.
	ntWSAProtocolInfoWLen    = 628
	ntWSAProtocolInfoWFamOff = 76

	// Frame-protocol errnos (extending the socket set).
	ntEPROTO  = 71
	ntEBADMSG = 74
)

// ntSCMMagic opens every rights frame; see the file comment. The
// final byte is the protocol version.
var ntSCMMagic = [8]byte{0xF5, 'S', 'C', 'M', 'R', 'I', 'G', '0'}

// Frame record kinds - wire values, deliberately decoupled from the
// internal ntFDKind enum so a table reshuffle can never silently
// change the protocol.
const (
	ntSCMKindFile = 1 // u64 receiver-relative HANDLE follows
	ntSCMKindPipe = 2 // u64 receiver-relative HANDLE follows
	ntSCMKindSock = 3 // u16 infoLen + WSAPROTOCOL_INFOW blob follows
)

// Little-endian wire serialization. The frame never crosses machines
// (both ends share one kernel), but an explicit byte order keeps the
// layout self-describing and unpadded.
func ntAppendU16(b []byte, v uint16) []byte { return append(b, byte(v), byte(v>>8)) }
func ntAppendU32(b []byte, v uint32) []byte {
	return append(b, byte(v), byte(v>>8), byte(v>>16), byte(v>>24))
}
func ntAppendU64(b []byte, v uint64) []byte {
	return ntAppendU32(ntAppendU32(b, uint32(v)), uint32(v>>32))
}
func ntGetU16(b []byte) uint16 { return uint16(b[0]) | uint16(b[1])<<8 }
func ntGetU32(b []byte) uint32 {
	return uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
}
func ntGetU64(b []byte) uint64 { return uint64(ntGetU32(b)) | uint64(ntGetU32(b[4:]))<<32 }
func ntPutU32At(b []byte, v uint32) {
	b[0], b[1], b[2], b[3] = byte(v), byte(v>>8), byte(v>>16), byte(v>>24)
}
func ntPutU64At(b []byte, v uint64) {
	ntPutU32At(b, uint32(v))
	ntPutU32At(b[4:], uint32(v>>32))
}

// ntSCMParse walks a Linux cmsg buffer and collects the SCM_RIGHTS
// payload fds, with the kernel's exact acceptance rules (__scm_send,
// verified live 2026-07-19): headers must satisfy CMSG_OK (Len >= 16
// and within the buffer, else EINVAL); non-SOL_SOCKET levels are
// silently skipped; SOL_SOCKET types other than SCM_RIGHTS are
// EINVAL - except SCM_CREDENTIALS(2), which Linux supports but this
// emulation cannot (EOPNOTSUPP, an honest gap). Multiple SCM_RIGHTS
// cmsgs accumulate into one fd list, like the kernel's scm.fp.
func ntSCMParse(control *byte, controllen uint64) (fds []int32, eno uintptr) {
	buf := unsafe.Slice(control, controllen)
	off := 0
	for len(buf)-off >= 16 {
		clen := ntGetU64(buf[off:])
		if clen < 16 || clen > uint64(len(buf)-off) {
			return nil, ntEINVAL
		}
		level := int32(ntGetU32(buf[off+8:]))
		typ := int32(ntGetU32(buf[off+12:]))
		switch {
		case level != _NT_SOL_SOCKET:
			// Skipped, per __scm_send's continue.
		case typ == _NT_SCM_RIGHTS:
			n := int((clen - 16) / 4)
			if len(fds)+n > ntSCMMaxFD {
				return nil, ntEINVAL
			}
			for i := 0; i < n; i++ {
				fds = append(fds, int32(ntGetU32(buf[off+16+4*i:])))
			}
		case typ == 2: // SCM_CREDENTIALS
			return nil, ntEOPNOTSUPP
		default:
			return nil, ntEINVAL
		}
		off += int((clen + 7) &^ 7)
	}
	// A trailing fragment shorter than a header is ignored, matching
	// for_each_cmsghdr's termination.
	return fds, 0
}

// ntSCMPeerPid resolves (and caches) the pid of the process on the
// other end of a connected AF_UNIX socket via SIO_AF_UNIX_GETPEERPID.
// Failure - unconnected socket, pre-17063 NT, wine - reports
// EOPNOTSUPP: ancillary transfer is impossible, while plain data on
// the same socket keeps working.
func ntSCMPeerPid(fd int32, e *ntFDEntry) (pid uint32, eno uintptr) {
	if e.sockPeerPid != 0 {
		return e.sockPeerPid, 0
	}
	var out, ret uint32
	r := ntcall10x(ntWSAIoctlFn, e.handle, _NT_SIO_AF_UNIX_GETPEERPID,
		0, 0, uintptr(unsafe.Pointer(&out)), 4,
		uintptr(unsafe.Pointer(&ret)), 0, 0, 0)
	if ntSockErr(r) || out == 0 {
		return 0, ntEOPNOTSUPP
	}
	ntFDSetSockPeerPid(fd, out)
	e.sockPeerPid = out
	return out, 0
}

// ntSockSendVAll pushes an entire WSABUF array (total bytes) to the
// socket, resuming after short sends: a nonblocking socket may accept
// only part of a frame, and a partially transmitted frame MUST be
// completed - the receiver consumes frames whole. EAGAIN with zero
// progress is returned to the caller (clean Linux semantics, nothing
// consumed); EAGAIN after partial progress yields and retries, which
// can block a nonblocking caller until the peer drains - the
// documented cost of framing (frames are small; in practice they fit
// the socket buffer and this loop runs once).
func ntSockSendVAll(h uintptr, bufs []ntWSABuf, total int64, wflags uintptr) (eno uintptr) {
	var sent int64
	var scratch []ntWSABuf
	for sent < total {
		cur := bufs
		if sent > 0 {
			if scratch == nil {
				scratch = make([]ntWSABuf, 0, len(bufs))
			}
			cur = scratch[:0]
			skip := sent
			for _, b := range bufs {
				bl := int64(b.len)
				if skip >= bl {
					skip -= bl
					continue
				}
				if skip > 0 {
					b = ntWSABuf{uint32(bl - skip), (*byte)(add(unsafe.Pointer(b.buf), uintptr(skip)))}
					skip = 0
				}
				cur = append(cur, b)
			}
		}
		var got uint32
		r, werr := ntcallSE(ntWSASendVFn, h, uintptr(unsafe.Pointer(&cur[0])),
			uintptr(uint32(len(cur))), uintptr(unsafe.Pointer(&got)), wflags, 0, 0)
		if ntSockErr(r) {
			if werr == _NT_WSAEWOULDBLOCK {
				if sent == 0 {
					return ntEAGAIN
				}
				osyield()
				continue
			}
			return ntWSAToLinux(werr)
		}
		if got == 0 {
			return ntEPROTO // no progress on success: do not spin
		}
		sent += int64(got)
	}
	return 0
}

// ntSCMRecvExact reads exactly len(p) bytes of an already-detected
// frame. The sender emits header+records+data in one send, so
// missing bytes are in flight: EWOULDBLOCK yields and retries. EOF or
// SHUT_RD mid-frame is a torn frame (EBADMSG).
func ntSCMRecvExact(h uintptr, p []byte) uintptr {
	got := 0
	for got < len(p) {
		r, werr := ntcallSE(ntSockRecvFn, h, uintptr(unsafe.Pointer(&p[got])),
			uintptr(uint32(len(p)-got)), 0, 0, 0, 0)
		ri := int32(uint32(r))
		if ri < 0 {
			switch werr {
			case _NT_WSAEWOULDBLOCK:
				osyield()
				continue
			case _NT_WSAEINTR:
				continue
			case _NT_WSAESHUTDOWN:
				return ntEBADMSG
			}
			return ntWSAToLinux(werr)
		}
		if ri == 0 {
			return ntEBADMSG
		}
		got += int(ri)
	}
	return 0
}

// ntSendmsgControl is the send-side ancillary path: ntEmuSendmsg
// routes here whenever the caller supplied a non-empty control
// buffer, BEFORE any data moves. See the file comment for the frame
// protocol and the errno contract.
func ntSendmsgControl(fd int32, e *ntFDEntry, msg *ntLinuxMsghdr, flags int32) (r1, r2, errno uintptr) {
	wflags, eno := ntMsgFlags(flags) // caller validated; recompute for the delegates
	if eno != 0 {
		return ntFail3(eno)
	}
	fds, eno := ntSCMParse(msg.control, msg.controllen)
	if eno != 0 {
		return ntFail3(eno)
	}
	if len(fds) == 0 {
		// Every cmsg was of a level Linux ignores: plain data send.
		return ntSendmsgData(fd, e, msg, flags, wflags)
	}
	if e.sockFam != _NT_AF_UNIX {
		// Linux's __sock_cmsg_send explicitly IGNORES SCM_RIGHTS on
		// inet sockets ("semantically in SOL_UNIX") - verified live:
		// the data goes out, the cmsg evaporates. Match it.
		return ntSendmsgData(fd, e, msg, flags, wflags)
	}
	if e.sockPair {
		// socketpair carriers are refused: their peer is in-process
		// by construction (see the file comment), and the frame would
		// ride a loopback TCP stream no recvmsg treats as AF_UNIX.
		return ntFail3(ntEOPNOTSUPP)
	}
	pid, eno := ntSCMPeerPid(fd, e)
	if eno != 0 {
		return ntFail3(eno)
	}

	// Validate everything BEFORE any cross-process side effect, in
	// Linux's order: payload fds first (EBADF/EOPNOTSUPP), then the
	// data iovecs.
	ents := make([]ntFDEntry, len(fds))
	for i, pfd := range fds {
		pe, ok := ntFDLookup(pfd)
		if !ok {
			return ntFail3(ntEBADF)
		}
		switch pe.kind {
		case ntFDSocket:
			if pe.sockPair {
				// Pair ends as PAYLOAD are refused too: the imported
				// socket would be the backing loopback TCP object,
				// whose synthesized unnamed-AF_UNIX identity
				// (sockPair) cannot cross the frame honestly.
				return ntFail3(ntEOPNOTSUPP)
			}
		case ntFDFile, ntFDPipe:
			// Fine. (Pipes transfer even though same-process dup(2)
			// on them stays ENOSYS - DuplicateHandle works on any
			// kernel handle.)
		default: // dir, stdio
			return ntFail3(ntEOPNOTSUPP)
		}
		ents[i] = pe
	}
	if msg.iovlen > ntMaxIov {
		return ntFail3(ntEMSGSIZE)
	}
	n := int(msg.iovlen)
	if n > 0 && msg.iov == nil {
		return ntFail3(ntEFAULT)
	}
	var dataTotal int64
	var iovs []ntLinuxIovec
	if n > 0 {
		iovs = unsafe.Slice(msg.iov, n)
		dataTotal, eno = ntIovTotal(iovs)
		if eno != 0 {
			return ntFail3(eno)
		}
	}

	// Build the frame header + records, duplicating each fd into the
	// peer as we go (sender-push; see the file comment).
	frame := make([]byte, 0, 24+len(ents)*(8+2+ntWSAProtocolInfoWLen))
	frame = append(frame, ntSCMMagic[:]...)
	frame = ntAppendU32(frame, uint32(len(ents)))
	frame = ntAppendU32(frame, uint32(ntcall(ntGetCurrentProcessIdFn, 0, 0, 0, 0, 0, 0)))
	frame = ntAppendU32(frame, uint32(dataTotal))
	frame = ntAppendU32(frame, 0) // reserved
	if dataTotal > 0x7FFFFFFF-int64(len(frame))-int64(len(ents))*(8+2+ntWSAProtocolInfoWLen) {
		return ntFail3(ntEMSGSIZE)
	}
	var hPeer uintptr    // PROCESS_DUP_HANDLE handle, opened lazily
	var pushed []uintptr // receiver-relative handles pushed so far
	bail := func(eno uintptr) (uintptr, uintptr, uintptr) {
		// Reclaim handles already planted in the peer (best-effort:
		// duplicate-close the source from here), then release the
		// peer process handle. Duplicated socket infos have no peer
		// handle to reclaim - the provider's reference dies with the
		// source socket (documented residual).
		for _, ph := range pushed {
			ntcallE(ntDuplicateHandleFn, hPeer, ph, 0, 0, 0, 0, _NT_DUPLICATE_CLOSE_SOURCE)
		}
		if hPeer != 0 {
			ntcall(ntCloseHandleFn, hPeer, 0, 0, 0, 0, 0)
		}
		return ntFail3(eno)
	}
	var info [ntWSAProtocolInfoWLen]byte
	for i := range ents {
		pe := &ents[i]
		switch pe.kind {
		case ntFDSocket:
			r, werr := ntcallE(ntWSADupSocketWFn, pe.handle, uintptr(pid),
				uintptr(unsafe.Pointer(&info[0])), 0, 0, 0, 0)
			if ntSockErr(r) {
				return bail(ntWSAToLinux(werr))
			}
			frame = ntAppendU32(frame, ntSCMKindSock)
			frame = ntAppendU32(frame, uint32(pe.flags))
			frame = ntAppendU16(frame, ntWSAProtocolInfoWLen)
			frame = append(frame, info[:]...)
		case ntFDFile, ntFDPipe:
			if hPeer == 0 {
				h, werr := ntcallE(ntOpenProcessFn, _NT_PROCESS_DUP_HANDLE, 0,
					uintptr(pid), 0, 0, 0, 0)
				if h == 0 {
					return bail(ntErrno(werr))
				}
				hPeer = h
			}
			var peerRel uintptr
			r, werr := ntcallE(ntDuplicateHandleFn,
				_NT_CURRENT_PROCESS, pe.handle, hPeer,
				uintptr(unsafe.Pointer(&peerRel)),
				0, // dwDesiredAccess (ignored with SAME_ACCESS)
				0, // bInheritHandle = FALSE
				_NT_DUPLICATE_SAME_ACCESS)
			if r == 0 {
				return bail(ntErrno(werr))
			}
			pushed = append(pushed, peerRel)
			k := uint32(ntSCMKindFile)
			if pe.kind == ntFDPipe {
				k = ntSCMKindPipe
			}
			frame = ntAppendU32(frame, k)
			frame = ntAppendU32(frame, uint32(pe.flags))
			frame = ntAppendU64(frame, uint64(peerRel))
		}
	}

	// One vectored send: the frame, then the data iovecs.
	wsa := make([]ntWSABuf, 0, 1+n)
	wsa = append(wsa, ntWSABuf{uint32(len(frame)), &frame[0]})
	for i := range iovs {
		if iovs[i].len == 0 {
			continue
		}
		wsa = append(wsa, ntWSABuf{uint32(iovs[i].len), iovs[i].base})
	}
	if eno := ntSockSendVAll(e.handle, wsa, int64(len(frame))+dataTotal, wflags); eno != 0 {
		return bail(eno)
	}
	if hPeer != 0 {
		ntcall(ntCloseHandleFn, hPeer, 0, 0, 0, 0, 0)
	}
	// Linux sendmsg reports the DATA length; control never counts.
	return uintptr(dataTotal), 0, 0
}

// ntSCMFail is ntFail3 in ntRecvmsgControl's handled-quadruple shape.
func ntSCMFail(eno uintptr) (bool, uintptr, uintptr, uintptr) {
	return true, ^uintptr(0), 0, eno
}

// ntRecvmsgControl is the receive-side ancillary path: ntEmuRecvmsg
// calls it when (and only when) the caller supplied a control buffer,
// BEFORE the plain receive. It MSG_PEEKs for the frame magic; on a
// match it owns the whole receive (handled=true), otherwise the plain
// path runs (which zeroes msg_controllen - no ancillary arrived).
func ntRecvmsgControl(fd int32, e *ntFDEntry, msg *ntLinuxMsghdr, flags int32) (handled bool, r1, r2, errno uintptr) {
	if e.sockFam != _NT_AF_UNIX || e.sockPair {
		// Frames only ever travel on pathname AF_UNIX streams (the
		// sender refuses everything else), so skip the peek.
		return false, 0, 0, 0
	}
	var hdr8 [8]byte
	for {
		r, _ := ntcallSE(ntSockRecvFn, e.handle, uintptr(unsafe.Pointer(&hdr8[0])),
			8, _NT_MSG_PEEK, 0, 0, 0)
		ri := int32(uint32(r))
		if ri <= 0 {
			// Nothing readable (EAGAIN), EOF, or a socket error: the
			// plain path reproduces the exact condition.
			return false, 0, 0, 0
		}
		for i := 0; i < int(ri); i++ {
			if hdr8[i] != ntSCMMagic[i] {
				return false, 0, 0, 0
			}
		}
		if ri == 8 {
			break
		}
		// A true prefix of the magic: the sender emits the whole
		// frame in one send, so the rest is in flight - yield and
		// re-peek. (A foreign sender that emits a magic prefix and
		// stalls forever would park us here; that is the documented
		// aliasing hazard, probability ~2^-8 per prefix byte.)
		osyield()
	}
	if flags&_NT_MSG_PEEK != 0 {
		// A peek cannot deliver fds nondestructively; refuse rather
		// than hand the caller raw frame bytes as data.
		return ntSCMFail(ntEOPNOTSUPP)
	}

	// Validate the caller's iovecs BEFORE consuming the stream, so
	// EMSGSIZE/EFAULT leave the frame intact like a Linux early-out.
	if msg.iovlen > ntMaxIov {
		return ntSCMFail(ntEMSGSIZE)
	}
	niov := int(msg.iovlen)
	if niov > 0 {
		if msg.iov == nil {
			return ntSCMFail(ntEFAULT)
		}
		if _, eno := ntIovTotal(unsafe.Slice(msg.iov, niov)); eno != 0 {
			return ntSCMFail(eno)
		}
	}

	// Consume the header.
	var hdr [24]byte
	if eno := ntSCMRecvExact(e.handle, hdr[:]); eno != 0 {
		return ntSCMFail(eno)
	}
	nfds := int(ntGetU32(hdr[8:]))
	dataLen := int64(ntGetU32(hdr[16:]))
	if nfds > ntSCMMaxFD || dataLen > 0x7FFFFFFF {
		return ntSCMFail(ntEBADMSG)
	}

	// Consume the records.
	type ntSCMRec struct {
		kind   uint32
		flags  int32
		handle uintptr
		info   *[ntWSAProtocolInfoWLen]byte
	}
	// drop releases a parsed-but-undeliverable record's resources:
	// handles close directly (they are already OURS - receiver
	// relative); duplicated sockets import-then-close, which is what
	// releases the provider's duplication reference.
	drop := func(rec *ntSCMRec) {
		switch rec.kind {
		case ntSCMKindSock:
			s, _ := ntcallE(ntWSASocketWFn, _NT_FROM_PROTOCOL_INFO,
				_NT_FROM_PROTOCOL_INFO, _NT_FROM_PROTOCOL_INFO,
				uintptr(unsafe.Pointer(&rec.info[0])), 0,
				_NT_WSA_FLAG_NO_HANDLE_INHERIT, 0)
			if s != _NT_INVALID_SOCKET {
				ntcall(ntWSACloseSocketFn, s, 0, 0, 0, 0, 0)
			}
		case ntSCMKindFile, ntSCMKindPipe:
			ntcall(ntCloseHandleFn, rec.handle, 0, 0, 0, 0, 0)
		}
	}
	recs := make([]ntSCMRec, nfds)
	for i := range recs {
		var rh [8]byte
		eno := ntSCMRecvExact(e.handle, rh[:])
		if eno == 0 {
			recs[i].kind = ntGetU32(rh[0:])
			recs[i].flags = int32(ntGetU32(rh[4:]))
			switch recs[i].kind {
			case ntSCMKindSock:
				var il [2]byte
				if eno = ntSCMRecvExact(e.handle, il[:]); eno == 0 {
					if ntGetU16(il[:]) != ntWSAProtocolInfoWLen {
						eno = ntEBADMSG
					} else {
						recs[i].info = new([ntWSAProtocolInfoWLen]byte)
						eno = ntSCMRecvExact(e.handle, recs[i].info[:])
					}
				}
			case ntSCMKindFile, ntSCMKindPipe:
				var hv [8]byte
				if eno = ntSCMRecvExact(e.handle, hv[:]); eno == 0 {
					recs[i].handle = uintptr(ntGetU64(hv[:]))
				}
			default:
				eno = ntEBADMSG
			}
		}
		if eno != 0 {
			for j := 0; j < i; j++ {
				drop(&recs[j])
			}
			return ntSCMFail(eno)
		}
	}

	// How many fds fit the caller's control buffer: Linux's
	// scm_max_fds formula, (controllen - sizeof(cmsghdr)) / 4 - which
	// deliberately lets CMSG_SPACE's alignment slack carry an extra
	// fd, verified against a live kernel. Overflow fds are closed and
	// MSG_CTRUNC raised.
	fdmax := 0
	if msg.control != nil && msg.controllen >= 16 {
		fdmax = int((msg.controllen - 16) / 4)
	}
	if fdmax > nfds {
		fdmax = nfds
	}
	created := make([]int32, 0, fdmax)
	fail := func(next int, eno uintptr) (bool, uintptr, uintptr, uintptr) {
		// All-or-nothing: close anything already delivered and drop
		// the rest, so an error never half-installs a transfer.
		for _, cfd := range created {
			ntEmuClose(cfd)
		}
		for j := next; j < len(recs); j++ {
			drop(&recs[j])
		}
		return ntSCMFail(eno)
	}
	for i := range recs {
		rec := &recs[i]
		if i >= fdmax {
			drop(rec)
			continue
		}
		switch rec.kind {
		case ntSCMKindSock:
			s, werr := ntcallE(ntWSASocketWFn, _NT_FROM_PROTOCOL_INFO,
				_NT_FROM_PROTOCOL_INFO, _NT_FROM_PROTOCOL_INFO,
				uintptr(unsafe.Pointer(&rec.info[0])), 0,
				_NT_WSA_FLAG_NO_HANDLE_INHERIT, 0)
			if s == _NT_INVALID_SOCKET {
				return fail(i+1, ntWSAToLinux(werr))
			}
			nfd := ntFDAlloc(s, ntFDSocket, rec.flags, false, nil)
			if nfd < 0 {
				ntcall(ntWSACloseSocketFn, s, 0, 0, 0, 0, 0)
				return fail(i+1, uintptr(-nfd))
			}
			// The Linux family comes from the info blob's
			// iAddressFamily (NT numbering; 23 -> 10). The imported
			// socket shares the sender's socket object, so blocking
			// mode and options carry over on their own - like a
			// passed fd's shared open file description on Linux.
			fam := uint16(ntGetU32(rec.info[ntWSAProtocolInfoWFamOff:]))
			if fam == _NT_AF_INET6_NT {
				fam = _NT_AF_INET6
			}
			ntFDSetSockFam(nfd, fam)
			created = append(created, nfd)
		default: // file, pipe (validated at parse)
			k := ntFDKind(ntFDFile)
			if rec.kind == ntSCMKindPipe {
				k = ntFDPipe
			}
			nfd := ntFDAlloc(rec.handle, k, rec.flags, false, nil)
			if nfd < 0 {
				ntcall(ntCloseHandleFn, rec.handle, 0, 0, 0, 0, 0)
				return fail(i+1, uintptr(-nfd))
			}
			created = append(created, nfd)
		}
	}

	// Data bytes: fill the iovecs, then drain-and-discard any excess
	// (MSG_TRUNC; see the file comment - the frame is a unit).
	copied := int64(0)
	remaining := dataLen
	if niov > 0 {
		iovs := unsafe.Slice(msg.iov, niov) // bases pre-validated above
		for i := range iovs {
			if remaining == 0 {
				break
			}
			m := int64(iovs[i].len)
			if m > remaining {
				m = remaining
			}
			if m == 0 {
				continue
			}
			if eno := ntSCMRecvExact(e.handle, unsafe.Slice(iovs[i].base, m)); eno != 0 {
				return fail(len(recs), eno)
			}
			copied += m
			remaining -= m
		}
	}
	var outFlags int32
	if remaining > 0 {
		var sink [512]byte
		for remaining > 0 {
			m := remaining
			if m > int64(len(sink)) {
				m = int64(len(sink))
			}
			if eno := ntSCMRecvExact(e.handle, sink[:m]); eno != 0 {
				return fail(len(recs), eno)
			}
			remaining -= m
		}
		outFlags |= _NT_MSG_TRUNC
	}

	// Synthesize the Linux SCM_RIGHTS cmsg. Reported controllen =
	// min(CMSG_SPACE(4*delivered), supplied controllen) and cmsg Len
	// = CMSG_LEN(4*delivered), both verified against a live kernel.
	delivered := len(created)
	var consumed uint64
	if delivered > 0 {
		ctrl := unsafe.Slice(msg.control, msg.controllen)
		cmsgLen := uint64(16 + 4*delivered)
		ntPutU64At(ctrl, cmsgLen)
		ntPutU32At(ctrl[8:], _NT_SOL_SOCKET)
		ntPutU32At(ctrl[12:], _NT_SCM_RIGHTS)
		for i, cfd := range created {
			ntPutU32At(ctrl[16+4*i:], uint32(cfd))
		}
		consumed = (cmsgLen + 7) &^ 7
		if consumed > msg.controllen {
			consumed = msg.controllen
		}
	}
	if delivered < nfds {
		outFlags |= _NT_MSG_CTRUNC
	}
	msg.controllen = consumed
	msg.flags = outFlags
	return true, uintptr(copied), 0, 0
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
	return ntSendmsgData(fd, &e, msg, flags, wflags)
}

// ntSendmsgData is the plain (no-ancillary) sendmsg path. It is also
// the delegate ntSendmsgControl falls back to when Linux semantics
// say the supplied cmsgs simply evaporate (ignorable levels,
// SCM_RIGHTS on inet sockets).
func ntSendmsgData(fd int32, e *ntFDEntry, msg *ntLinuxMsghdr, flags int32, wflags uintptr) (r1, r2, errno uintptr) {
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
