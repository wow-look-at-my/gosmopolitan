// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1 && wasip1.wasmedgesock

package syscall

import (
	"runtime"
	"structs"
	"unsafe"
)

// This file replaces net_wasip1.go when GOWASI=wasmedgesock is set. It
// implements the syscall-level socket API on top of the WasmEdge socket
// extension to WASI preview 1: a set of sock_* host functions imported
// from the wasi_snapshot_preview1 module that WasmEdge added because
// preview 1 itself cannot create or connect sockets.
//
// The ABI implemented here is the one spoken by the de-facto guest SDK
// for the extension, github.com/second-state/wasmedge_wasi_socket
// v0.4.3 (its src/socket.rs extern block is the reference):
//
//   - Addresses cross the boundary as a {buf, buf_len} pair pointing at
//     the raw big-endian IP address bytes (4 for IPv4, 16 for IPv6),
//     with the port passed separately as a host-order u32.
//   - Address families are Unspec=0, Inet4=1, Inet6=2 and socket types
//     are Any=0, Datagram=1, Stream=2 (both differ from this package's
//     AF_* and SOCK_* values, which are translated below).
//   - sock_accept takes only (fd, *newfd) - no flags parameter - unlike
//     the sock_accept that later joined the upstream WASI preview 1
//     snapshot (used by net_wasip1.go for inherited listeners).
//   - sock_getlocaladdr/sock_getpeeraddr fill in the address buffer and
//     report the address kind as 4 or 6 (not as the family enum).
//   - Socket option levels/names are WasmEdge's own enums (SOL_SOCKET=0,
//     SO_REUSEADDR=0, SO_ERROR=2, ...), not the Linux values. The SO_*
//     constants exported here use the WasmEdge numbering directly.
//   - All functions return WASI errno values (see tables_wasip1.go).
//
// Note there are two generations of this extension. WasmEdge 0.8.2-0.9
// used a smaller enum space (Inet4=0, Inet6=1; no Unspec/Any values, no
// sock_send_to/sock_recv_from), and WasmEdge 0.12+ additionally accepts
// a 128-byte family-tagged address buffer and a spec-shaped 3-argument
// sock_accept alongside the formats above. This file targets the middle,
// current SDK generation, which WasmEdge continues to accept.
//
// Sockets created here are ordinary WASI file descriptors: fd_read,
// fd_write, fd_close, fd_fdstat_set_flags(FDFLAG_NONBLOCK) and
// poll_oneoff fd_read/fd_write subscriptions all operate on them, which
// is what lets these fds flow through internal/poll and the runtime's
// existing netpoll_wasip1 machinery unchanged.

const (
	SHUT_RD   = 0x1
	SHUT_WR   = 0x2
	SHUT_RDWR = SHUT_RD | SHUT_WR
)

// WasmEdge socket option levels and names. Unlike the constants of the
// same name on other operating systems, these use the WasmEdge enum
// numbering: SetsockoptInt and GetsockoptInt pass them through to the
// host unmodified. SO_ERROR is inherited from net_fake.go and happens
// to match WasmEdge's SoError value.
const (
	SOL_SOCKET = 0

	SO_REUSEADDR = 0
	SO_TYPE      = 1
	// SO_ERROR = 2 is defined in net_fake.go.
	SO_BROADCAST = 4
	SO_SNDBUF    = 5
	SO_RCVBUF    = 6
	SO_KEEPALIVE = 7
)

// WasmEdge address families and socket types.
const (
	wasmedgeAFUnspec = 0
	wasmedgeAFInet4  = 1
	wasmedgeAFInet6  = 2

	wasmedgeSockAny    = 0
	wasmedgeSockDgram  = 1
	wasmedgeSockStream = 2
)

type sdflags = uint32

// wasiAddress is WasmEdge's WasiAddress: a pointer to the raw IP
// address bytes and their length (4 or 16).
type wasiAddress struct {
	_      structs.HostLayout
	buf    uintptr32
	bufLen size
}

//go:wasmimport wasi_snapshot_preview1 sock_open
//go:noescape
func sock_open(family int32, sotype int32, fd *int32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_bind
//go:noescape
func sock_bind(fd int32, addr *wasiAddress, port uint32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_listen
func sock_listen(fd int32, backlog int32) Errno

// The WasmEdge-flavored sock_accept: no fdflags argument, in contrast
// to the sock_accept from the upstream WASI preview 1 snapshot.
//
//go:wasmimport wasi_snapshot_preview1 sock_accept
//go:noescape
func sock_accept(fd int32, newfd *int32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_connect
//go:noescape
func sock_connect(fd int32, addr *wasiAddress, port uint32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_send
//go:noescape
func sock_send(fd int32, iovs *iovec, iovsLen size, flags uint32, nwritten *size) Errno

//go:wasmimport wasi_snapshot_preview1 sock_recv
//go:noescape
func sock_recv(fd int32, iovs *iovec, iovsLen size, flags uint32, nread *size, oflags *size) Errno

//go:wasmimport wasi_snapshot_preview1 sock_shutdown
func sock_shutdown(fd int32, flags sdflags) Errno

//go:wasmimport wasi_snapshot_preview1 sock_getlocaladdr
//go:noescape
func sock_getlocaladdr(fd int32, addr *wasiAddress, addrType *uint32, port *uint32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_getpeeraddr
//go:noescape
func sock_getpeeraddr(fd int32, addr *wasiAddress, addrType *uint32, port *uint32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_setsockopt
//go:noescape
func sock_setsockopt(fd int32, level int32, name int32, flag *int32, flagSize uint32) Errno

//go:wasmimport wasi_snapshot_preview1 sock_getsockopt
//go:noescape
func sock_getsockopt(fd int32, level int32, name int32, flag *int32, flagSize *uint32) Errno

// Socket creates a WasmEdge socket. Only AF_INET and AF_INET6 stream or
// datagram sockets exist on this platform; proto is ignored (WasmEdge
// derives the protocol from the socket type).
func Socket(family, sotype, proto int) (fd int, err error) {
	var af int32
	switch family {
	case AF_INET:
		af = wasmedgeAFInet4
	case AF_INET6:
		af = wasmedgeAFInet6
	default:
		return -1, EAFNOSUPPORT
	}
	var st int32
	switch sotype {
	case SOCK_STREAM:
		st = wasmedgeSockStream
	case SOCK_DGRAM:
		st = wasmedgeSockDgram
	default:
		return -1, EPROTONOSUPPORT
	}
	newfd := int32(-1)
	errno := sock_open(af, st, &newfd)
	if errno != 0 {
		return -1, errnoErr(errno)
	}
	return int(newfd), nil
}

// sockaddrIPAndPort deconstructs a Sockaddr into the raw address bytes
// and port expected by the WasmEdge host functions.
func sockaddrIPAndPort(sa Sockaddr) (ip []byte, port uint32, err error) {
	switch sa := sa.(type) {
	case *SockaddrInet4:
		return sa.Addr[:], uint32(sa.Port), nil
	case *SockaddrInet6:
		return sa.Addr[:], uint32(sa.Port), nil
	case nil:
		return nil, 0, EINVAL
	default:
		return nil, 0, EAFNOSUPPORT
	}
}

// wasiIPAndPortToSockaddr is the inverse of sockaddrIPAndPort, applied
// to the outputs of sock_getlocaladdr/sock_getpeeraddr. The address
// kind is 4 or 6, per the SDK's contract.
func wasiIPAndPortToSockaddr(buf []byte, addrType, port uint32) (Sockaddr, error) {
	switch addrType {
	case 4:
		sa := &SockaddrInet4{Port: int(port)}
		copy(sa.Addr[:], buf)
		return sa, nil
	case 6:
		sa := &SockaddrInet6{Port: int(port)}
		copy(sa.Addr[:], buf)
		return sa, nil
	default:
		return nil, EAFNOSUPPORT
	}
}

func Bind(fd int, sa Sockaddr) error {
	ip, port, err := sockaddrIPAndPort(sa)
	if err != nil {
		return err
	}
	addr := wasiAddress{
		buf:    uintptr32(uintptr(unsafe.Pointer(unsafe.SliceData(ip)))),
		bufLen: size(len(ip)),
	}
	errno := sock_bind(int32(fd), &addr, port)
	runtime.KeepAlive(ip)
	return errnoErr(errno)
}

func StopIO(fd int) error {
	return ENOSYS
}

func Listen(fd int, backlog int) error {
	if backlog < 0 {
		backlog = 0
	}
	return errnoErr(sock_listen(int32(fd), int32(backlog)))
}

func Accept(fd int) (int, Sockaddr, error) {
	newfd := int32(-1)
	errno := sock_accept(int32(fd), &newfd)
	if errno != 0 {
		return -1, nil, errnoErr(errno)
	}
	sa, _ := Getpeername(int(newfd))
	return int(newfd), sa, nil
}

func Connect(fd int, sa Sockaddr) error {
	ip, port, err := sockaddrIPAndPort(sa)
	if err != nil {
		return err
	}
	addr := wasiAddress{
		buf:    uintptr32(uintptr(unsafe.Pointer(unsafe.SliceData(ip)))),
		bufLen: size(len(ip)),
	}
	errno := sock_connect(int32(fd), &addr, port)
	runtime.KeepAlive(ip)
	return errnoErr(errno)
}

func Getsockname(fd int) (Sockaddr, error) {
	var buf [16]byte
	addr := wasiAddress{
		buf:    uintptr32(uintptr(unsafe.Pointer(&buf[0]))),
		bufLen: size(len(buf)),
	}
	var addrType, port uint32
	errno := sock_getlocaladdr(int32(fd), &addr, &addrType, &port)
	runtime.KeepAlive(&buf)
	if errno != 0 {
		return nil, errnoErr(errno)
	}
	return wasiIPAndPortToSockaddr(buf[:], addrType, port)
}

func Getpeername(fd int) (Sockaddr, error) {
	var buf [16]byte
	addr := wasiAddress{
		buf:    uintptr32(uintptr(unsafe.Pointer(&buf[0]))),
		bufLen: size(len(buf)),
	}
	var addrType, port uint32
	errno := sock_getpeeraddr(int32(fd), &addr, &addrType, &port)
	runtime.KeepAlive(&buf)
	if errno != 0 {
		return nil, errnoErr(errno)
	}
	return wasiIPAndPortToSockaddr(buf[:], addrType, port)
}

func Recvfrom(fd int, p []byte, flags int) (n int, from Sockaddr, err error) {
	return 0, nil, ENOSYS
}

func Sendto(fd int, p []byte, flags int, to Sockaddr) error {
	return ENOSYS
}

func Recvmsg(fd int, p, oob []byte, flags int) (n, oobn, recvflags int, from Sockaddr, err error) {
	return 0, 0, 0, nil, ENOSYS
}

func SendmsgN(fd int, p, oob []byte, to Sockaddr, flags int) (n int, err error) {
	return 0, ENOSYS
}

func GetsockoptInt(fd, level, opt int) (value int, err error) {
	var flag int32
	flagSize := uint32(unsafe.Sizeof(flag))
	errno := sock_getsockopt(int32(fd), int32(level), int32(opt), &flag, &flagSize)
	if errno != 0 {
		return 0, errnoErr(errno)
	}
	return int(flag), nil
}

func SetsockoptInt(fd, level, opt int, value int) error {
	flag := int32(value)
	errno := sock_setsockopt(int32(fd), int32(level), int32(opt), &flag, uint32(unsafe.Sizeof(flag)))
	return errnoErr(errno)
}

func SetReadDeadline(fd int, t int64) error {
	return ENOSYS
}

func SetWriteDeadline(fd int, t int64) error {
	return ENOSYS
}

func Shutdown(fd int, how int) error {
	errno := sock_shutdown(int32(fd), sdflags(how))
	return errnoErr(errno)
}
