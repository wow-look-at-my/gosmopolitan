// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo && amd64

// Windows NT socket emulation (wave 2 chunk C): the winsock backends
// for the Linux-numbered socket syscalls dispatched by
// ntSyscallEmulate (os_cosmo_nt_sys.go).
//
// Model: linux-shaped nonblocking BSD sockets over the classic
// synchronous ws2_32 surface - WSASocketW WITHOUT WSA_FLAG_OVERLAPPED
// (plus WSA_FLAG_NO_HANDLE_INHERIT), ioctlsocket(FIONBIO) for
// O_NONBLOCK, plain recv/send/recvfrom/sendto/accept/connect, and
// readiness from the WSAPoll netpoller (netpoll_cosmo_nt.go). None of
// upstream's IOCP/OVERLAPPED machinery is involved. (windows-latest's
// AF_UNIX capability probe confirms afunix.sys binds fine on exactly
// this creation shape - non-overlapped, family 1, UTF-8 sun_path,
// namelen 2+len+1 - so no per-family creation deltas exist.)
//
// Translation happens at exactly this boundary, in both directions:
//
//   - Address families: AF_UNSPEC/AF_UNIX/AF_INET match Linux;
//     AF_INET6 is 23 on NT vs 10 on Linux and is rewritten inside
//     every sockaddr crossing the boundary (the layouts are otherwise
//     byte-identical - NT sockaddrs have no sa_len, unlike darwin).
//   - Errors: winsock failures land in the same TEB last-error slot
//     every Win32 call uses (WSAGetLastError reads the same word), so
//     ntcallE/ntcallSE already capture them; ntWSAToLinux maps the
//     WSAE* range onto Linux errnos. connect's WSAEWOULDBLOCK
//     specifically becomes EINPROGRESS so internal/poll's nonblocking
//     connect loop (wait-writable, then SO_ERROR) works unchanged.
//   - Options: a curated (level,optname) value map, darwin-style;
//     unknown combinations report ENOPROTOOPT. SO_ERROR additionally
//     translates the returned VALUE, and SO_LINGER converts between
//     Linux's {i32,i32} and winsock's {u16,u16} linger structs.
//   - AF_UNIX: pathname stream sockets over afunix.sys (Win10 17063+).
//     sun_path is translated through the chunk-A path layer (afunix
//     takes UTF-8), and the Linux-spelling name is RECORDED in the fd
//     table: getsockname/getpeername report the recorded bytes, like
//     the Linux kernel returns exactly what was bound, because
//     translating winsock's stored Windows path back would surface
//     the /c/... alias. Abstract-namespace names (leading NUL) are
//     refused EINVAL exactly like the darwin leg; autobind (empty
//     path) likewise. Socket files are reparse points that are NOT
//     auto-deleted on close; unlink(2) removes them like any file.
//
// UDP: SIO_UDP_CONNRESET and SIO_UDP_NETRESET are disabled at
// socket() time (best-effort), so an ICMP unreachable latched by an
// earlier send cannot fail unrelated recvs with WSAECONNRESET - the
// same fix upstream net applies on Windows. A datagram longer than
// the recv buffer fails WSAEMSGSIZE on winsock; Linux silently
// truncates, so the emulation reports a full buffer instead.
//
// sendmsg/recvmsg (fd passing, ReadMsg*) stay ENOSYS for now - the
// darwin leg lacks them too - and are wave 3's next item. socketpair
// is emulated since wave 3 item 1 (ntEmuSocketpair below): a
// connected loopback TCP pair dressed as unnamed AF_UNIX, built by
// the same recipe as the netpoller's wake channel (ntLoopbackTCPPair,
// shared with netpollinitNT). dup(2) is emulated for socket-kind fds
// only (ntEmuDup) - net.FileConn needs it.

package runtime

import (
	"internal/runtime/atomic"
	"unsafe"
)

// Linux errno values used by the socket emulation (extending the file
// I/O set in os_cosmo_nt_sys.go).
const (
	ntEINTR           = 4
	ntEFAULT          = 14
	ntENOTSOCK        = 88
	ntEMSGSIZE        = 90
	ntENOPROTOOPT     = 92
	ntEPROTONOSUPPORT = 93
	ntEOPNOTSUPP      = 95
	ntEAFNOSUPPORT    = 97
	ntEADDRINUSE      = 98
	ntEADDRNOTAVAIL   = 99
	ntENETDOWN        = 100
	ntENETUNREACH     = 101
	ntENETRESET       = 102
	ntECONNABORTED    = 103
	ntECONNRESET      = 104
	ntENOBUFS         = 105
	ntEISCONN         = 106
	ntENOTCONN        = 107
	ntETIMEDOUT       = 110
	ntECONNREFUSED    = 111
	ntEHOSTDOWN       = 112
	ntEHOSTUNREACH    = 113
	ntEALREADY        = 114
	ntEINPROGRESS     = 115
)

// Linux socket constants (amd64).
const (
	_NT_AF_UNIX  = 1
	_NT_AF_INET  = 2
	_NT_AF_INET6 = 10 // Linux value; NT uses 23

	_NT_SOCK_STREAM   = 1
	_NT_SOCK_DGRAM    = 2
	_NT_SOCK_NONBLOCK = 0x800
	_NT_SOCK_CLOEXEC  = 0x80000

	_NT_MSG_OOB       = 0x1
	_NT_MSG_PEEK      = 0x2
	_NT_MSG_DONTROUTE = 0x4
)

// Winsock constants.
const (
	_NT_AF_INET6_NT = 23

	_NT_WSA_FLAG_NO_HANDLE_INHERIT = 0x80

	_NT_INVALID_SOCKET = ^uintptr(0)

	_NT_FIONBIO = 0x8004667E

	_NT_SIO_UDP_CONNRESET = 0x9800000C // IOC_IN|IOC_VENDOR|12
	_NT_SIO_UDP_NETRESET  = 0x9800000F // IOC_IN|IOC_VENDOR|15

	_NT_HANDLE_FLAG_INHERIT = 0x1

	_NT_WSAEINTR           = 10004
	_NT_WSAEBADF           = 10009
	_NT_WSAEACCES          = 10013
	_NT_WSAEFAULT          = 10014
	_NT_WSAEINVAL          = 10022
	_NT_WSAEMFILE          = 10024
	_NT_WSAEWOULDBLOCK     = 10035
	_NT_WSAEINPROGRESS     = 10036
	_NT_WSAEALREADY        = 10037
	_NT_WSAENOTSOCK        = 10038
	_NT_WSAEMSGSIZE        = 10040
	_NT_WSAENOPROTOOPT     = 10042
	_NT_WSAEPROTONOSUPPORT = 10043
	_NT_WSAESOCKTNOSUPPORT = 10044
	_NT_WSAEOPNOTSUPP      = 10045
	_NT_WSAEPFNOSUPPORT    = 10046
	_NT_WSAEAFNOSUPPORT    = 10047
	_NT_WSAEADDRINUSE      = 10048
	_NT_WSAEADDRNOTAVAIL   = 10049
	_NT_WSAENETDOWN        = 10050
	_NT_WSAENETUNREACH     = 10051
	_NT_WSAENETRESET       = 10052
	_NT_WSAECONNABORTED    = 10053
	_NT_WSAECONNRESET      = 10054
	_NT_WSAENOBUFS         = 10055
	_NT_WSAEISCONN         = 10056
	_NT_WSAENOTCONN        = 10057
	_NT_WSAESHUTDOWN       = 10058
	_NT_WSAETIMEDOUT       = 10060
	_NT_WSAECONNREFUSED    = 10061
	_NT_WSAEHOSTDOWN       = 10064
	_NT_WSAEHOSTUNREACH    = 10065
)

// ws2_32 function pointers, resolved lazily by ntWinsockEnsure (a
// non-network program must never load winsock). All are classic
// synchronous BSD-shaped entry points; the only OVERLAPPED-capable
// call, WSAIoctl, is always passed a nil OVERLAPPED.
var (
	ntWSAStartupFn      uintptr
	ntWSASocketWFn      uintptr
	ntWSABindFn         uintptr
	ntWSAListenFn       uintptr
	ntWSAConnectFn      uintptr
	ntWSAAcceptFn       uintptr
	ntWSAGetsocknameFn  uintptr
	ntWSAGetpeernameFn  uintptr
	ntWSASetsockoptFn   uintptr
	ntWSAGetsockoptFn   uintptr
	ntWSAShutdownFn     uintptr
	ntWSACloseSocketFn  uintptr
	ntWSAIoctlsocketFn  uintptr
	ntWSAIoctlFn        uintptr
	ntWSAPollFn         uintptr
	ntWSARecvFn         uintptr
	ntWSASendFn         uintptr
	ntWSARecvfromFn     uintptr
	ntWSASendtoFn       uintptr
	ntWSASetHandleInfFn uintptr // kernel32 SetHandleInformation (accepted sockets)
)

var (
	ntNameWs232        = []byte("ws2_32.dll\x00")
	ntNameWSAStartup   = []byte("WSAStartup\x00")
	ntNameWSASocketW   = []byte("WSASocketW\x00")
	ntNameSockBind     = []byte("bind\x00")
	ntNameSockListen   = []byte("listen\x00")
	ntNameSockConnect  = []byte("connect\x00")
	ntNameSockAccept   = []byte("accept\x00")
	ntNameGetsockname  = []byte("getsockname\x00")
	ntNameGetpeername  = []byte("getpeername\x00")
	ntNameSetsockopt   = []byte("setsockopt\x00")
	ntNameGetsockopt   = []byte("getsockopt\x00")
	ntNameSockShutdown = []byte("shutdown\x00")
	ntNameClosesocket  = []byte("closesocket\x00")
	ntNameIoctlsocket  = []byte("ioctlsocket\x00")
	ntNameWSAIoctl     = []byte("WSAIoctl\x00")
	ntNameWSAPoll      = []byte("WSAPoll\x00")
	ntNameSockRecv     = []byte("recv\x00")
	ntNameSockSend     = []byte("send\x00")
	ntNameSockRecvfrom = []byte("recvfrom\x00")
	ntNameSockSendto   = []byte("sendto\x00")
	ntNameSetHandleInf = []byte("SetHandleInformation\x00")
	ntWSAData          [408]byte // WSADATA (amd64 layout is 400 bytes; padded)
)

// ntWSAReady: 0 = untried, 1 = ready, 2 = failed (sticky).
var (
	ntWSAReady uint32
	ntWSALock  mutex
)

// ntWinsockEnsure loads ws2_32.dll and calls WSAStartup(2.2) once,
// lazily, at the first socket-family syscall or at netpollinit
// (whichever runs first - the two race, hence the lock). Returns 0
// when winsock is ready, or the Linux errno to report. Never
// allocates: it can run under netpollGenericInit's lock from the
// first timer creation.
func ntWinsockEnsure() uintptr {
	if atomic.Load(&ntWSAReady) == 1 {
		return 0
	}
	lock(&ntWSALock)
	if ntWSAReady != 0 {
		st := ntWSAReady
		unlock(&ntWSALock)
		if st == 1 {
			return 0
		}
		return ntENOSYS
	}
	gpa := ntiat[0] // &GetProcAddress
	lla := ntiat[1] // &LoadLibraryA

	ws := ntcall(lla, uintptr(unsafe.Pointer(&ntNameWs232[0])), 0, 0, 0, 0, 0)
	ok := ws != 0
	sym := func(name *byte) uintptr {
		if !ok {
			return 0
		}
		fn := ntcall(gpa, ws, uintptr(unsafe.Pointer(name)), 0, 0, 0, 0)
		if fn == 0 {
			ok = false
		}
		return fn
	}
	ntWSAStartupFn = sym(&ntNameWSAStartup[0])
	ntWSASocketWFn = sym(&ntNameWSASocketW[0])
	ntWSABindFn = sym(&ntNameSockBind[0])
	ntWSAListenFn = sym(&ntNameSockListen[0])
	ntWSAConnectFn = sym(&ntNameSockConnect[0])
	ntWSAAcceptFn = sym(&ntNameSockAccept[0])
	ntWSAGetsocknameFn = sym(&ntNameGetsockname[0])
	ntWSAGetpeernameFn = sym(&ntNameGetpeername[0])
	ntWSASetsockoptFn = sym(&ntNameSetsockopt[0])
	ntWSAGetsockoptFn = sym(&ntNameGetsockopt[0])
	ntWSAShutdownFn = sym(&ntNameSockShutdown[0])
	ntWSACloseSocketFn = sym(&ntNameClosesocket[0])
	ntWSAIoctlsocketFn = sym(&ntNameIoctlsocket[0])
	ntWSAIoctlFn = sym(&ntNameWSAIoctl[0])
	ntWSAPollFn = sym(&ntNameWSAPoll[0])
	ntWSARecvFn = sym(&ntNameSockRecv[0])
	ntWSASendFn = sym(&ntNameSockSend[0])
	ntWSARecvfromFn = sym(&ntNameSockRecvfrom[0])
	ntWSASendtoFn = sym(&ntNameSockSendto[0])
	if ok {
		// WSAStartup returns the error code directly (not via the
		// last-error slot); 0 means winsock 2.2 is up.
		ok = ntcall(ntWSAStartupFn, 0x202,
			uintptr(unsafe.Pointer(&ntWSAData[0])), 0, 0, 0, 0) == 0
	}
	// SetHandleInformation lives in kernel32 (already loaded; the
	// LoadLibraryA just bumps a refcount). Graceful: accepted sockets
	// merely stay inheritable if it is missing.
	if k32 := ntcall(lla, uintptr(unsafe.Pointer(&ntNameKernel32[0])), 0, 0, 0, 0, 0); k32 != 0 {
		ntWSASetHandleInfFn = ntcall(gpa, k32, uintptr(unsafe.Pointer(&ntNameSetHandleInf[0])), 0, 0, 0, 0)
	}
	if ok {
		atomic.Store(&ntWSAReady, 1)
	} else {
		atomic.Store(&ntWSAReady, 2)
	}
	unlock(&ntWSALock)
	if !ok {
		return ntENOSYS
	}
	return 0
}

// ntWSAToLinux maps a winsock (WSAE*) last-error value to the Linux
// errno the unix-shaped standard library expects. Non-winsock codes
// fall through to the general Win32 table.
func ntWSAToLinux(werr uintptr) uintptr {
	switch werr {
	case 0:
		return 0
	case _NT_WSAEINTR:
		return ntEINTR
	case _NT_WSAEBADF:
		return ntEBADF
	case _NT_WSAEACCES:
		return ntEACCES
	case _NT_WSAEFAULT:
		return ntEFAULT
	case _NT_WSAEINVAL:
		return ntEINVAL
	case _NT_WSAEMFILE:
		return ntEMFILE
	case _NT_WSAEWOULDBLOCK:
		return ntEAGAIN
	case _NT_WSAEINPROGRESS:
		// Winsock-1.1 blocking-hook artifact, not the connect-pending
		// condition (that is WSAEWOULDBLOCK, mapped by the connect
		// path); EINPROGRESS is still the least-wrong translation.
		return ntEINPROGRESS
	case _NT_WSAEALREADY:
		return ntEALREADY
	case _NT_WSAENOTSOCK:
		return ntENOTSOCK
	case _NT_WSAEMSGSIZE:
		return ntEMSGSIZE
	case _NT_WSAENOPROTOOPT:
		return ntENOPROTOOPT
	case _NT_WSAEPROTONOSUPPORT:
		return ntEPROTONOSUPPORT
	case _NT_WSAESOCKTNOSUPPORT, _NT_WSAEOPNOTSUPP:
		return ntEOPNOTSUPP
	case _NT_WSAEPFNOSUPPORT, _NT_WSAEAFNOSUPPORT:
		return ntEAFNOSUPPORT
	case _NT_WSAEADDRINUSE:
		return ntEADDRINUSE
	case _NT_WSAEADDRNOTAVAIL:
		return ntEADDRNOTAVAIL
	case _NT_WSAENETDOWN:
		return ntENETDOWN
	case _NT_WSAENETUNREACH:
		return ntENETUNREACH
	case _NT_WSAENETRESET:
		return ntENETRESET
	case _NT_WSAECONNABORTED:
		return ntECONNABORTED
	case _NT_WSAECONNRESET:
		return ntECONNRESET
	case _NT_WSAENOBUFS:
		return ntENOBUFS
	case _NT_WSAEISCONN:
		return ntEISCONN
	case _NT_WSAENOTCONN:
		return ntENOTCONN
	case _NT_WSAESHUTDOWN:
		// Send after SHUT_WR; Linux reports EPIPE (reads special-case
		// this to EOF before consulting the table).
		return ntEPIPE
	case _NT_WSAETIMEDOUT:
		return ntETIMEDOUT
	case _NT_WSAECONNREFUSED:
		return ntECONNREFUSED
	case _NT_WSAEHOSTDOWN:
		return ntEHOSTDOWN
	case _NT_WSAEHOSTUNREACH:
		return ntEHOSTUNREACH
	}
	if werr >= 10000 && werr < 11000 {
		return ntEIO
	}
	return ntErrno(werr)
}

// ---- sockaddr translation ----

const ntSockaddrBufMax = 112 // sizeof(syscall.RawSockaddrAny)

// ntSockaddrToNT converts a caller-supplied Linux sockaddr into the
// winsock form in out: AF_INET copies through, AF_INET6 rewrites the
// family 10 -> 23, and AF_UNIX pathnames are translated through the
// chunk-A path layer to a Windows UTF-8 path (what afunix.sys
// expects). Returns the winsock namelen, the LINUX family, and - for
// AF_UNIX - the original Linux-spelling path for the fd-table record.
func ntSockaddrToNT(sa unsafe.Pointer, salen uint32, out *[ntSockaddrBufMax]byte) (outlen int32, fam uint16, unixPath string, eno uintptr) {
	if sa == nil || salen < 2 || salen > ntSockaddrBufMax {
		return 0, 0, "", ntEINVAL
	}
	src := unsafe.Slice((*byte)(sa), salen)
	fam = uint16(src[0]) | uint16(src[1])<<8
	switch fam {
	case _NT_AF_INET:
		if salen < 16 {
			return 0, 0, "", ntEINVAL
		}
		copy(out[:16], src[:16])
		return 16, fam, "", 0
	case _NT_AF_INET6:
		if salen < 28 {
			return 0, 0, "", ntEINVAL
		}
		copy(out[:28], src[:28])
		out[0], out[1] = _NT_AF_INET6_NT, 0
		return 28, fam, "", 0
	case _NT_AF_UNIX:
		// Path bytes follow the 2-byte family, NUL-terminated within
		// salen. An empty path is either a Linux autobind request or
		// an abstract-namespace name (leading NUL): afunix.sys is
		// pathname-only, so both are refused EINVAL, exactly like the
		// darwin leg.
		n := 0
		for 2+n < int(salen) && src[2+n] != 0 {
			n++
		}
		if n == 0 {
			return 0, 0, "", ntEINVAL
		}
		path := string(src[2 : 2+n])
		w := ntPathW(path)
		if w == nil {
			return 0, 0, "", ntEINVAL
		}
		wp := ntUTF16ToString(w[:len(w)-1]) // drop the NUL; afunix takes UTF-8
		if len(wp) > 107 {
			return 0, 0, "", ntEINVAL
		}
		out[0], out[1] = _NT_AF_UNIX, 0
		copy(out[2:], wp)
		out[2+len(wp)] = 0
		return int32(2 + len(wp) + 1), fam, path, 0
	}
	return 0, 0, "", ntEAFNOSUPPORT
}

// ntSockaddrFromNT writes a Linux-shaped sockaddr into the caller's
// (dst, *dstLen) out-parameters from a winsock sockaddr: the AF_INET6
// family is rewritten 23 -> 10, and AF_UNIX names are SYNTHESIZED
// from unixName (the fd table's recorded Linux spelling; empty =
// unnamed, addrlen 2). The destination is zeroed up to the caller's
// buffer length first - the sockaddr decoder scans the whole sun_path
// array - and *dstLen reports the full length even when the copy was
// truncated (kernel semantics).
func ntSockaddrFromNT(dst unsafe.Pointer, dstLen *uint32, src *[ntSockaddrBufMax]byte, srcLen int32, unixName string) {
	if dst == nil || dstLen == nil {
		return
	}
	inCap := *dstLen
	if inCap > ntSockaddrBufMax {
		inCap = ntSockaddrBufMax
	}
	if srcLen < 2 {
		srcLen = 2
	}
	if srcLen > ntSockaddrBufMax {
		srcLen = ntSockaddrBufMax
	}
	fam := uint16(src[0]) | uint16(src[1])<<8
	var tmp [ntSockaddrBufMax]byte
	var n uint32
	switch fam {
	case _NT_AF_INET6_NT:
		n = uint32(srcLen)
		copy(tmp[:n], src[:n])
		tmp[0], tmp[1] = _NT_AF_INET6, 0
	case _NT_AF_UNIX:
		tmp[0], tmp[1] = _NT_AF_UNIX, 0
		n = 2
		if unixName != "" {
			m := len(unixName)
			if m > 107 {
				m = 107
			}
			copy(tmp[2:], unixName[:m])
			tmp[2+m] = 0
			n = uint32(2 + m + 1)
		}
	default:
		n = uint32(srcLen)
		copy(tmp[:n], src[:n])
	}
	memclrNoHeapPointers(dst, uintptr(inCap))
	c := n
	if c > inCap {
		c = inCap
	}
	copy(unsafe.Slice((*byte)(dst), c), tmp[:c])
	*dstLen = n
}

// ntMsgFlags translates Linux MSG_* send/recv flags. OOB, PEEK and
// DONTROUTE share values with winsock; anything else (MSG_WAITALL,
// MSG_TRUNC, ...) differs or does not exist and is refused.
func ntMsgFlags(flags int32) (uintptr, uintptr) {
	if flags&^(_NT_MSG_OOB|_NT_MSG_PEEK|_NT_MSG_DONTROUTE) != 0 {
		return 0, ntEINVAL
	}
	return uintptr(uint32(flags)), 0
}

// ntSockErr distills the "int-returning winsock call failed" check:
// winsock's int results are 32-bit, so only the low word is
// meaningful.
func ntSockErr(r uintptr) bool {
	return int32(uint32(r)) == -1
}

// ntSockLookup fetches fd's table entry and insists it is a socket.
func ntSockLookup(fd int32) (ntFDEntry, uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFDEntry{}, ntEBADF
	}
	if e.kind != ntFDSocket {
		return ntFDEntry{}, ntENOTSOCK
	}
	return e, 0
}

// ---- syscall backends ----

func ntEmuSocket(domain, typ, proto int32) (r1, r2, errno uintptr) {
	if eno := ntWinsockEnsure(); eno != 0 {
		return ntFail3(eno)
	}
	flags := typ & (_NT_SOCK_NONBLOCK | _NT_SOCK_CLOEXEC)
	st := typ &^ (_NT_SOCK_NONBLOCK | _NT_SOCK_CLOEXEC)
	var af uintptr
	switch domain {
	case _NT_AF_UNIX:
		af = _NT_AF_UNIX
	case _NT_AF_INET:
		af = _NT_AF_INET
	case _NT_AF_INET6:
		af = _NT_AF_INET6_NT
	default:
		return ntFail3(ntEAFNOSUPPORT)
	}
	// Non-overlapped socket, never inheritable: children only ever
	// receive the three explicitly duplicated stdio handles.
	s, werr := ntcallE(ntWSASocketWFn, af, uintptr(uint32(st)), uintptr(uint32(proto)),
		0, 0, _NT_WSA_FLAG_NO_HANDLE_INHERIT, 0)
	if s == _NT_INVALID_SOCKET {
		return ntFail3(ntWSAToLinux(werr))
	}
	if st == _NT_SOCK_DGRAM && domain != _NT_AF_UNIX {
		// Keep latched ICMP unreachable reports from failing
		// unrelated recvs (see the file comment). Best-effort: wine
		// does not implement these ioctls.
		var off, ret uint32
		ntcall10x(ntWSAIoctlFn, s, _NT_SIO_UDP_CONNRESET,
			uintptr(unsafe.Pointer(&off)), 4, 0, 0,
			uintptr(unsafe.Pointer(&ret)), 0, 0, 0)
		ntcall10x(ntWSAIoctlFn, s, _NT_SIO_UDP_NETRESET,
			uintptr(unsafe.Pointer(&off)), 4, 0, 0,
			uintptr(unsafe.Pointer(&ret)), 0, 0, 0)
	}
	sflags := int32(_NT_O_RDWR)
	if flags&_NT_SOCK_NONBLOCK != 0 {
		var one uint32 = 1
		r, werr2 := ntcallE(ntWSAIoctlsocketFn, s, _NT_FIONBIO,
			uintptr(unsafe.Pointer(&one)), 0, 0, 0, 0)
		if ntSockErr(r) {
			ntcall(ntWSACloseSocketFn, s, 0, 0, 0, 0, 0)
			return ntFail3(ntWSAToLinux(werr2))
		}
		sflags |= _NT_O_NONBLOCK
	}
	fd := ntFDAlloc(s, ntFDSocket, sflags, flags&_NT_SOCK_CLOEXEC != 0, nil)
	if fd < 0 {
		ntcall(ntWSACloseSocketFn, s, 0, 0, 0, 0, 0)
		return ntFail3(uintptr(-fd))
	}
	ntFDSetSockFam(fd, uint16(domain))
	return uintptr(fd), 0, 0
}

// ntLoopbackTCPPair builds a connected loopback TCP pair - the
// netpoller wake-channel recipe of wave 2, factored out here in wave
// 3 so the socketpair emulation shares it: loopback listener, bind
// 127.0.0.1:0, blocking connect against the one-slot backlog, accept,
// close the listener. All in-kernel and immediate. Returns the
// accepted and client SOCKETs - blocking, TCP_NODELAY both ways,
// uninheritable - or, on failure, the failing step's name (a static
// string) plus the Win32/WSA error, with every socket it opened
// already closed. Allocation-free: also called from netpollinitNT,
// which can run under runtime locks (the first timer's
// netpollGenericInit) where entersyscall is off-limits too - hence
// plain ntcallE throughout, like the wave-2 inline original.
//
// Hardening both callers gain over the inline original: after the
// accept, the client's getsockname must equal the accepted end's
// getpeername (family, port AND address) - otherwise some OTHER
// local process won the connect race against the one-slot backlog
// and the two ends would not be connected to each other. The window
// is tiny and 127.0.0.1-only, but the failure mode (a "pair" whose
// halves talk to a stranger) is worth the two name queries; a
// mismatch reports WSAECONNABORTED.
func ntLoopbackTCPPair() (a, c uintptr, step string, werr uintptr) {
	l, lerr := ntcallE(ntWSASocketWFn, _NT_AF_INET, _NT_SOCK_STREAM, 0,
		0, 0, _NT_WSA_FLAG_NO_HANDLE_INHERIT, 0)
	if l == _NT_INVALID_SOCKET {
		return 0, 0, "listener socket", lerr
	}
	var sin [16]byte
	sin[0] = _NT_AF_INET
	sin[4], sin[5], sin[6], sin[7] = 127, 0, 0, 1
	if r, e := ntcallE(ntWSABindFn, l, uintptr(unsafe.Pointer(&sin[0])), 16, 0, 0, 0, 0); ntSockErr(r) {
		ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
		return 0, 0, "bind", e
	}
	var slen int32 = 16
	if r, e := ntcallE(ntWSAGetsocknameFn, l, uintptr(unsafe.Pointer(&sin[0])),
		uintptr(unsafe.Pointer(&slen)), 0, 0, 0, 0); ntSockErr(r) {
		ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
		return 0, 0, "getsockname", e
	}
	if r, e := ntcallE(ntWSAListenFn, l, 1, 0, 0, 0, 0, 0); ntSockErr(r) {
		ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
		return 0, 0, "listen", e
	}
	c, werr = ntcallE(ntWSASocketWFn, _NT_AF_INET, _NT_SOCK_STREAM, 0,
		0, 0, _NT_WSA_FLAG_NO_HANDLE_INHERIT, 0)
	if c == _NT_INVALID_SOCKET {
		ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
		return 0, 0, "client socket", werr
	}
	if r, e := ntcallE(ntWSAConnectFn, c, uintptr(unsafe.Pointer(&sin[0])), 16, 0, 0, 0, 0); ntSockErr(r) {
		ntcall(ntWSACloseSocketFn, c, 0, 0, 0, 0, 0)
		ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
		return 0, 0, "connect", e
	}
	a, werr = ntcallE(ntWSAAcceptFn, l, 0, 0, 0, 0, 0, 0)
	if a == _NT_INVALID_SOCKET {
		ntcall(ntWSACloseSocketFn, c, 0, 0, 0, 0, 0)
		ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
		return 0, 0, "accept", werr
	}
	ntcall(ntWSACloseSocketFn, l, 0, 0, 0, 0, 0)
	// Connect-race verification (see above). Compare only the
	// family+port+address head (8 bytes): providers do not promise a
	// zeroed sin_zero tail.
	var cname, aname [16]byte
	clen, alen := int32(16), int32(16)
	if r, e := ntcallE(ntWSAGetsocknameFn, c, uintptr(unsafe.Pointer(&cname[0])),
		uintptr(unsafe.Pointer(&clen)), 0, 0, 0, 0); ntSockErr(r) {
		ntcall(ntWSACloseSocketFn, a, 0, 0, 0, 0, 0)
		ntcall(ntWSACloseSocketFn, c, 0, 0, 0, 0, 0)
		return 0, 0, "verify getsockname", e
	}
	if r, e := ntcallE(ntWSAGetpeernameFn, a, uintptr(unsafe.Pointer(&aname[0])),
		uintptr(unsafe.Pointer(&alen)), 0, 0, 0, 0); ntSockErr(r) {
		ntcall(ntWSACloseSocketFn, a, 0, 0, 0, 0, 0)
		ntcall(ntWSACloseSocketFn, c, 0, 0, 0, 0, 0)
		return 0, 0, "verify getpeername", e
	}
	mismatch := clen < 8 || alen < 8
	for i := 0; i < 8 && !mismatch; i++ {
		mismatch = cname[i] != aname[i]
	}
	if mismatch {
		ntcall(ntWSACloseSocketFn, a, 0, 0, 0, 0, 0)
		ntcall(ntWSACloseSocketFn, c, 0, 0, 0, 0, 0)
		return 0, 0, "peer verify", _NT_WSAECONNABORTED
	}
	// The accepted end never crosses a CreateProcess boundary
	// explicitly, but keep it uninheritable like every created socket
	// (graceful: it merely stays inheritable if the resolve failed).
	if ntWSASetHandleInfFn != 0 {
		ntcall(ntWSASetHandleInfFn, a, _NT_HANDLE_FLAG_INHERIT, 0, 0, 0, 0)
	}
	// Pair traffic - wake bytes and socketpair payloads alike - must
	// hit the wire immediately, not sit in a Nagle buffer behind a
	// delayed ACK. Best-effort, both directions.
	var one uint32 = 1
	ntcall(ntWSASetsockoptFn, c, 6 /* IPPROTO_TCP */, 1, /* TCP_NODELAY */
		uintptr(unsafe.Pointer(&one)), 4, 0)
	one = 1
	ntcall(ntWSASetsockoptFn, a, 6, 1, uintptr(unsafe.Pointer(&one)), 4, 0)
	return a, c, "", 0
}

// ntEmuSocketpair emulates socketpair(2) with a connected loopback
// TCP pair dressed as AF_UNIX (ntLoopbackTCPPair above). Accepted
// shape: AF_UNIX (=AF_LOCAL) SOCK_STREAM with protocol 0, plus the
// SOCK_NONBLOCK/SOCK_CLOEXEC creation flags (stripped exactly like
// ntEmuSocket). Refusals, each deliberate:
//   - SOCK_DGRAM -> EOPNOTSUPP: a datagram pair would have to ride
//     loopback UDP, which legally DROPS datagrams on real NT - the
//     wave-2 netpoller lesson, where one lost wake datagram wedged
//     the poller for its full timeout - and afunix.sys has no DGRAM
//     support to fall back on; there is no lossless NT transport
//     with datagram semantics. (Other non-stream types likewise.)
//   - other domains -> EOPNOTSUPP (Linux refuses AF_INET socketpair
//     with the same errno).
//   - protocols other than 0 -> EPROTONOSUPPORT.
//
// The ends are real TCP sockets under the covers, so data flow,
// shutdown(2), FIONBIO and WSAPoll readiness all work through the
// existing socket-kind machinery; the fd entries carry sockPair so
// name queries synthesize the Linux truth (unnamed AF_UNIX, see
// ntEmuGetsockname) and never leak the 127.0.0.1 backing address.
// os/exec interaction is nil by construction: both ends are born
// uninheritable and ntForkExec rejects ExtraFiles (>3 attr.Files,
// ENOSYS), so a pair end cannot cross into a child process on NT.
func ntEmuSocketpair(domain, typ, proto int32, sv *[2]int32) (r1, r2, errno uintptr) {
	if sv == nil {
		return ntFail3(ntEFAULT)
	}
	if eno := ntWinsockEnsure(); eno != 0 {
		return ntFail3(eno)
	}
	flags := typ & (_NT_SOCK_NONBLOCK | _NT_SOCK_CLOEXEC)
	st := typ &^ (_NT_SOCK_NONBLOCK | _NT_SOCK_CLOEXEC)
	if domain != _NT_AF_UNIX {
		return ntFail3(ntEOPNOTSUPP)
	}
	if st != _NT_SOCK_STREAM {
		return ntFail3(ntEOPNOTSUPP)
	}
	if proto != 0 {
		return ntFail3(ntEPROTONOSUPPORT)
	}
	a, c, _, werr := ntLoopbackTCPPair()
	if werr != 0 {
		return ntFail3(ntWSAToLinux(werr))
	}
	fds := [2]int32{-1, -1}
	bail := func(eno uintptr) (uintptr, uintptr, uintptr) {
		if fds[0] >= 0 {
			ntFDRelease(fds[0])
		}
		ntcall(ntWSACloseSocketFn, a, 0, 0, 0, 0, 0)
		ntcall(ntWSACloseSocketFn, c, 0, 0, 0, 0, 0)
		return ntFail3(eno)
	}
	sflags := int32(_NT_O_RDWR)
	if flags&_NT_SOCK_NONBLOCK != 0 {
		sflags |= _NT_O_NONBLOCK
	}
	for i, s := range [2]uintptr{a, c} {
		if flags&_NT_SOCK_NONBLOCK != 0 {
			var one uint32 = 1
			if r, werr2 := ntcallE(ntWSAIoctlsocketFn, s, _NT_FIONBIO,
				uintptr(unsafe.Pointer(&one)), 0, 0, 0, 0); ntSockErr(r) {
				return bail(ntWSAToLinux(werr2))
			}
		}
		fd := ntFDAlloc(s, ntFDSocket, sflags, flags&_NT_SOCK_CLOEXEC != 0, nil)
		if fd < 0 {
			return bail(uintptr(-fd))
		}
		ntFDSetSockFam(fd, _NT_AF_UNIX)
		ntFDSetSockPair(fd)
		fds[i] = fd
	}
	sv[0], sv[1] = fds[0], fds[1]
	return 0, 0, 0
}

// ntEmuDup implements dup(2) for SOCKET-kind fds via DuplicateHandle
// - the exact call upstream Go's poll.DupCloseOnExec makes on
// windows: msafd sockets are real kernel file handles, and a
// same-process duplicate refers to the same socket object with an
// independent handle lifetime, which is dup(2)'s contract (shared
// socket state, separately closeable; the object lives until the
// last handle closes). Who needs it: net.FileConn/FileListener wrap
// an *os.File by fcntl F_DUPFD_CLOEXEC - ENOSYS from ntFcntl - then
// fall back to plain dup(2), landing here (poll.dupCloseOnExecOld).
// MSDN's warning against DuplicateHandle on sockets concerns non-IFS
// layered providers, which the base msafd/afunix stacks are not (and
// upstream Go has shipped exactly this call for years).
//
// Non-socket kinds stay ENOSYS on purpose: nothing in std needs a
// file/pipe dup on NT yet, and a visible gap beats an untested path
// (the cosmo graceful-stub philosophy).
func ntEmuDup(fd int32) (r1, r2, errno uintptr) {
	e, ok := ntFDLookup(fd)
	if !ok {
		return ntFail3(ntEBADF)
	}
	if e.kind != ntFDSocket {
		return ntFail3(ntENOSYS)
	}
	var nh uintptr
	r, werr := ntcallE(ntDuplicateHandleFn,
		_NT_CURRENT_PROCESS, e.handle, _NT_CURRENT_PROCESS,
		uintptr(unsafe.Pointer(&nh)),
		0, // dwDesiredAccess (ignored with SAME_ACCESS)
		0, // bInheritHandle = FALSE
		_NT_DUPLICATE_SAME_ACCESS)
	if r == 0 {
		return ntFail3(ntErrno(werr))
	}
	// The duplicate shares every socket property (nonblocking mode
	// included - FIONBIO is socket-object state) but starts with
	// CLOEXEC clear, per POSIX. Copy the recorded socket identity so
	// name queries on the dup answer like the original.
	nfd := ntFDAlloc(nh, ntFDSocket, e.flags, false, nil)
	if nfd < 0 {
		ntcall(ntWSACloseSocketFn, nh, 0, 0, 0, 0, 0)
		return ntFail3(uintptr(-nfd))
	}
	ntFDSetSockFam(nfd, e.sockFam)
	if e.sockPair {
		ntFDSetSockPair(nfd)
	}
	if e.unixBound != "" {
		ntFDSetUnixName(nfd, e.unixBound, true)
	}
	if e.unixPeer != "" {
		ntFDSetUnixName(nfd, e.unixPeer, false)
	}
	return uintptr(nfd), 0, 0
}

func ntEmuBind(fd int32, sa unsafe.Pointer, salen uint32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	var buf [ntSockaddrBufMax]byte
	blen, fam, upath, eno := ntSockaddrToNT(sa, salen, &buf)
	if eno != 0 {
		return ntFail3(eno)
	}
	r, werr := ntcallE(ntWSABindFn, e.handle, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(uint32(blen)), 0, 0, 0, 0)
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	if fam == _NT_AF_UNIX {
		ntFDSetUnixName(fd, upath, true)
	}
	return 0, 0, 0
}

func ntEmuConnect(fd int32, sa unsafe.Pointer, salen uint32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	var buf [ntSockaddrBufMax]byte
	blen, fam, upath, eno := ntSockaddrToNT(sa, salen, &buf)
	if eno != 0 {
		return ntFail3(eno)
	}
	r, werr := ntcallSE(ntWSAConnectFn, e.handle, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(uint32(blen)), 0, 0, 0, 0)
	if ntSockErr(r) {
		le := ntWSAToLinux(werr)
		if werr == _NT_WSAEWOULDBLOCK {
			// Nonblocking connect pending. This is winsock's spelling
			// of EINPROGRESS (WSAEINPROGRESS is a winsock-1.1
			// artifact); internal/poll now waits for writability and
			// confirms via SO_ERROR.
			le = ntEINPROGRESS
		}
		if fam == _NT_AF_UNIX && le == ntEINPROGRESS {
			// Record the intended peer now; getpeername only reports
			// it after winsock confirms the connection completed.
			ntFDSetUnixName(fd, upath, false)
		}
		return ntFail3(le)
	}
	if fam == _NT_AF_UNIX {
		ntFDSetUnixName(fd, upath, false)
	}
	return 0, 0, 0
}

func ntEmuListen(fd, backlog int32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	r, werr := ntcallE(ntWSAListenFn, e.handle, uintptr(uint32(backlog)), 0, 0, 0, 0, 0)
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	return 0, 0, 0
}

func ntEmuAccept4(fd int32, rsa unsafe.Pointer, alen *uint32, flags int32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if flags&^(_NT_SOCK_NONBLOCK|_NT_SOCK_CLOEXEC) != 0 {
		return ntFail3(ntEINVAL)
	}
	var buf [ntSockaddrBufMax]byte
	var blen int32 = ntSockaddrBufMax
	ns, werr := ntcallSE(ntWSAAcceptFn, e.handle, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&blen)), 0, 0, 0, 0)
	if ns == _NT_INVALID_SOCKET {
		if werr == _NT_WSAECONNRESET {
			// A reset pending connection surfaces as an accept error
			// on winsock; Linux reports ECONNABORTED, which
			// internal/poll's accept loop swallows and re-polls.
			return ntFail3(ntECONNABORTED)
		}
		return ntFail3(ntWSAToLinux(werr))
	}
	// The accepted handle inherits the listener's properties, but its
	// HANDLE inheritability is not contractually specified: strip it
	// so a concurrent CreateProcessW(bInheritHandles=TRUE) can never
	// capture it.
	if ntWSASetHandleInfFn != 0 {
		ntcall(ntWSASetHandleInfFn, ns, _NT_HANDLE_FLAG_INHERIT, 0, 0, 0, 0)
	}
	sflags := int32(_NT_O_RDWR)
	if flags&_NT_SOCK_NONBLOCK != 0 {
		var one uint32 = 1
		ntcall(ntWSAIoctlsocketFn, ns, _NT_FIONBIO, uintptr(unsafe.Pointer(&one)), 0, 0, 0)
		sflags |= _NT_O_NONBLOCK
	}
	nfd := ntFDAlloc(ns, ntFDSocket, sflags, flags&_NT_SOCK_CLOEXEC != 0, nil)
	if nfd < 0 {
		ntcall(ntWSACloseSocketFn, ns, 0, 0, 0, 0, 0)
		return ntFail3(uintptr(-nfd))
	}
	ntFDSetSockFam(nfd, e.sockFam)
	// Peer address out. A unix peer that never bound is unnamed
	// (Linux behavior for unbound clients); named unix peers are not
	// back-translated (nothing in the library needs them, and the
	// probe's dialers are unbound).
	if rsa != nil && alen != nil {
		ntSockaddrFromNT(rsa, alen, &buf, blen, "")
	}
	return uintptr(nfd), 0, 0
}

// ntEmuGetsockname backs both getsockname (peer=false) and
// getpeername (peer=true).
func ntEmuGetsockname(fd int32, rsa unsafe.Pointer, alen *uint32, peer bool) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if rsa == nil || alen == nil {
		return ntFail3(ntEINVAL)
	}
	if e.sockPair {
		// socketpair fds never consult winsock here: it would answer
		// with the backing 127.0.0.1 TCP names. Linux reports both
		// ends of a socketpair as UNNAMED AF_UNIX - the 2-byte family
		// and nothing else - for getsockname and getpeername alike.
		var buf [ntSockaddrBufMax]byte
		buf[0] = _NT_AF_UNIX
		ntSockaddrFromNT(rsa, alen, &buf, 2, "")
		return 0, 0, 0
	}
	fn := ntWSAGetsocknameFn
	if peer {
		fn = ntWSAGetpeernameFn
	}
	var buf [ntSockaddrBufMax]byte
	var blen int32 = ntSockaddrBufMax
	r, werr := ntcallE(fn, e.handle, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(unsafe.Pointer(&blen)), 0, 0, 0, 0)
	if ntSockErr(r) {
		if !peer {
			// Winsock refuses getsockname on an unbound socket
			// (WSAEINVAL); Linux succeeds with the zero address.
			// Synthesize what the Linux kernel would say.
			switch e.sockFam {
			case _NT_AF_INET:
				buf = [ntSockaddrBufMax]byte{_NT_AF_INET}
				blen = 16
			case _NT_AF_INET6:
				buf = [ntSockaddrBufMax]byte{_NT_AF_INET6_NT}
				blen = 28
			case _NT_AF_UNIX:
				buf = [ntSockaddrBufMax]byte{_NT_AF_UNIX}
				blen = 2
			default:
				return ntFail3(ntWSAToLinux(werr))
			}
		} else {
			return ntFail3(ntWSAToLinux(werr))
		}
	}
	name := ""
	if e.sockFam == _NT_AF_UNIX {
		if peer {
			name = e.unixPeer
		} else {
			name = e.unixBound
		}
	}
	ntSockaddrFromNT(rsa, alen, &buf, blen, name)
	return 0, 0, 0
}

// ntSockoptXlat maps a Linux (level, optname) pair onto the winsock
// values. Curated, darwin-style: only what the standard library
// actually issues; unknown pairs report ENOPROTOOPT.
func ntSockoptXlat(level, name int32) (wl, wn int32, ok bool) {
	switch level {
	case 1: // SOL_SOCKET -> 0xffff
		wl = 0xffff
		switch name {
		case 2: // SO_REUSEADDR (NT semantics are looser - REUSEADDR+REUSEPORT-ish - acceptable for listeners)
			wn = 0x0004
		case 3: // SO_TYPE
			wn = 0x1008
		case 4: // SO_ERROR
			wn = 0x1007
		case 6: // SO_BROADCAST
			wn = 0x0020
		case 7: // SO_SNDBUF
			wn = 0x1001
		case 8: // SO_RCVBUF
			wn = 0x1002
		case 9: // SO_KEEPALIVE
			wn = 0x0008
		case 13: // SO_LINGER (struct differs; handled by the callers)
			wn = 0x0080
		default:
			return 0, 0, false
		}
	case 6: // IPPROTO_TCP (same level value)
		wl = 6
		switch name {
		case 1: // TCP_NODELAY
			wn = 1
		case 4: // TCP_KEEPIDLE
			wn = 3
		case 5: // TCP_KEEPINTVL
			wn = 17
		case 6: // TCP_KEEPCNT
			wn = 16
		default:
			return 0, 0, false
		}
	case 41: // IPPROTO_IPV6 (same level value)
		wl = 41
		switch name {
		case 26: // IPV6_V6ONLY -> 27
			wn = 27
		default:
			return 0, 0, false
		}
	default:
		return 0, 0, false
	}
	return wl, wn, true
}

func ntEmuSetsockopt(fd, level, name int32, val unsafe.Pointer, vallen uint32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if e.sockFam == _NT_AF_UNIX && level == 1 && name == 2 {
		// SOL_SOCKET/SO_REUSEADDR on an AF_UNIX socket: Linux accepts
		// it as a no-op (pathname binds never reuse - a taken path is
		// EADDRINUSE regardless), and net's listenStream sets it on
		// every stream listener. Forwarding it to winsock poisons
		// afunix.sys: msafd records the option, and the subsequent
		// bind fails WSAEOPNOTSUPP (windows-latest evidence: the CI
		// AF_UNIX capability probe binds a clean socket fine with our
		// exact creation flags and sockaddr, while the listener path
		// failed at exactly bind). Swallow it - the Linux semantics.
		return 0, 0, 0
	}
	wl, wn, ok := ntSockoptXlat(level, name)
	if !ok {
		return ntFail3(ntENOPROTOOPT)
	}
	if val == nil || vallen == 0 {
		return ntFail3(ntEINVAL)
	}
	p, plen := uintptr(val), uintptr(vallen)
	var wlinger [2]uint16
	if level == 1 && name == 13 {
		// SO_LINGER: Linux {int32 onoff; int32 linger} -> winsock
		// {u_short l_onoff; u_short l_linger}.
		if vallen < 8 {
			return ntFail3(ntEINVAL)
		}
		lv := (*[2]int32)(val)
		wlinger[0], wlinger[1] = uint16(lv[0]), uint16(lv[1])
		p, plen = uintptr(unsafe.Pointer(&wlinger[0])), 4
	}
	r, werr := ntcallE(ntWSASetsockoptFn, e.handle, uintptr(uint32(wl)),
		uintptr(uint32(wn)), p, plen, 0, 0)
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	return 0, 0, 0
}

func ntEmuGetsockopt(fd, level, name int32, val unsafe.Pointer, vallen *uint32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	wl, wn, ok := ntSockoptXlat(level, name)
	if !ok {
		return ntFail3(ntENOPROTOOPT)
	}
	if val == nil || vallen == nil {
		return ntFail3(ntEINVAL)
	}
	switch {
	case level == 1 && name == 4: // SO_ERROR: translate the VALUE too
		if *vallen < 4 {
			return ntFail3(ntEINVAL)
		}
		var werrVal int32
		var wlen int32 = 4
		r, werr := ntcallE(ntWSAGetsockoptFn, e.handle, uintptr(uint32(wl)), uintptr(uint32(wn)),
			uintptr(unsafe.Pointer(&werrVal)), uintptr(unsafe.Pointer(&wlen)), 0, 0)
		if ntSockErr(r) {
			return ntFail3(ntWSAToLinux(werr))
		}
		out := int32(0)
		if werrVal != 0 {
			out = int32(ntWSAToLinux(uintptr(uint32(werrVal))))
		}
		*(*int32)(val) = out
		*vallen = 4
		return 0, 0, 0
	case level == 1 && name == 13: // SO_LINGER: widen {u16,u16} -> {i32,i32}
		if *vallen < 8 {
			return ntFail3(ntEINVAL)
		}
		var wlinger [2]uint16
		var wlen int32 = 4
		r, werr := ntcallE(ntWSAGetsockoptFn, e.handle, uintptr(uint32(wl)), uintptr(uint32(wn)),
			uintptr(unsafe.Pointer(&wlinger[0])), uintptr(unsafe.Pointer(&wlen)), 0, 0)
		if ntSockErr(r) {
			return ntFail3(ntWSAToLinux(werr))
		}
		lv := (*[2]int32)(val)
		lv[0], lv[1] = int32(wlinger[0]), int32(wlinger[1])
		*vallen = 8
		return 0, 0, 0
	}
	// Generic int-valued option: the 4-byte value passes through, and
	// the Linux u32 optlen slot doubles as winsock's int32 one.
	r, werr := ntcallE(ntWSAGetsockoptFn, e.handle, uintptr(uint32(wl)), uintptr(uint32(wn)),
		uintptr(val), uintptr(unsafe.Pointer(vallen)), 0, 0)
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	return 0, 0, 0
}

func ntEmuShutdown(fd, how int32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if how < 0 || how > 2 {
		return ntFail3(ntEINVAL)
	}
	// SHUT_RD/SHUT_WR/SHUT_RDWR and SD_RECEIVE/SD_SEND/SD_BOTH share
	// values.
	r, werr := ntcallE(ntWSAShutdownFn, e.handle, uintptr(uint32(how)), 0, 0, 0, 0, 0)
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	return 0, 0, 0
}

func ntEmuSendto(fd int32, p unsafe.Pointer, n int32, flags int32, to unsafe.Pointer, tolen uint32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if n < 0 {
		return ntFail3(ntEINVAL)
	}
	wflags, eno := ntMsgFlags(flags)
	if eno != 0 {
		return ntFail3(eno)
	}
	var r, werr uintptr
	if to == nil || tolen == 0 {
		r, werr = ntcallSE(ntWSASendFn, e.handle, uintptr(p), uintptr(uint32(n)), wflags, 0, 0, 0)
	} else {
		var buf [ntSockaddrBufMax]byte
		blen, _, _, aeno := ntSockaddrToNT(to, tolen, &buf)
		if aeno != 0 {
			return ntFail3(aeno)
		}
		r, werr = ntcallSE(ntWSASendtoFn, e.handle, uintptr(p), uintptr(uint32(n)), wflags,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(uint32(blen)), 0)
	}
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	return uintptr(uint32(r)), 0, 0
}

func ntEmuRecvfrom(fd int32, p unsafe.Pointer, n int32, flags int32, from unsafe.Pointer, fromlen *uint32) (r1, r2, errno uintptr) {
	e, eno := ntSockLookup(fd)
	if eno != 0 {
		return ntFail3(eno)
	}
	if n < 0 {
		return ntFail3(ntEINVAL)
	}
	wflags, eno := ntMsgFlags(flags)
	if eno != 0 {
		return ntFail3(eno)
	}
	var buf [ntSockaddrBufMax]byte
	var blen int32 = ntSockaddrBufMax
	var r, werr uintptr
	if from == nil {
		r, werr = ntcallSE(ntWSARecvFn, e.handle, uintptr(p), uintptr(uint32(n)), wflags, 0, 0, 0)
	} else {
		r, werr = ntcallSE(ntWSARecvfromFn, e.handle, uintptr(p), uintptr(uint32(n)), wflags,
			uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(&blen)), 0)
	}
	ri := int32(uint32(r))
	if ri == -1 {
		switch werr {
		case _NT_WSAEMSGSIZE:
			// Datagram longer than the buffer: winsock filled the
			// buffer (and the source address) and then failed; Linux
			// silently truncates. Report a full buffer.
			ri = n
		case _NT_WSAESHUTDOWN:
			return 0, 0, 0 // read after SHUT_RD: EOF, like Linux
		default:
			return ntFail3(ntWSAToLinux(werr))
		}
	}
	if from != nil && fromlen != nil {
		ntSockaddrFromNT(from, fromlen, &buf, blen, "")
	}
	return uintptr(uint32(ri)), 0, 0
}

// ntSockRead and ntSockWrite back SYS_READ/SYS_WRITE on socket-kind
// fds (ntEmuRead/ntEmuWrite dispatch by kind): sockets need
// recv/send, not ReadFile/WriteFile.
func ntSockRead(h uintptr, p unsafe.Pointer, n int32) (r1, r2, errno uintptr) {
	r, werr := ntcallSE(ntWSARecvFn, h, uintptr(p), uintptr(uint32(n)), 0, 0, 0, 0)
	ri := int32(uint32(r))
	if ri == -1 {
		if werr == _NT_WSAESHUTDOWN {
			return 0, 0, 0 // EOF
		}
		return ntFail3(ntWSAToLinux(werr))
	}
	return uintptr(uint32(ri)), 0, 0
}

func ntSockWrite(h uintptr, p unsafe.Pointer, n int32) (r1, r2, errno uintptr) {
	r, werr := ntcallSE(ntWSASendFn, h, uintptr(p), uintptr(uint32(n)), 0, 0, 0, 0)
	if ntSockErr(r) {
		return ntFail3(ntWSAToLinux(werr))
	}
	return uintptr(uint32(r)), 0, 0
}
