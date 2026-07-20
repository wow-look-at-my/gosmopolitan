// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// wasip1sockhost is the reference host for this tree's
// GOWASI=wasmedgesock wasip1 binaries, and the vehicle for their
// end-to-end tests.
//
// Stock WASI runtimes (wasmtime, wazero) do not implement the WasmEdge
// socket extension to WASI preview 1, so a module built with
// GOWASI=wasmedgesock cannot even instantiate on them. This program
// embeds wazero and registers its own complete wasi_snapshot_preview1
// host module: the WasmEdge sock_* surface (in the ABI generation of
// second-state/wasmedge_wasi_socket v0.4.3, the same one the syscall
// package targets - see syscall/net_wasip1_wasmedge.go) backed by real
// Go net sockets, plus the baseline preview 1 functions a Go wasip1
// binary needs (args/environ, clocks, stdio, random_get, proc_exit,
// and a poll_oneoff that understands both clock subscriptions and
// fd_read/fd_write readiness on socket fds).
//
// Implementing the whole namespace ourselves - rather than layering on
// wazero's builtin WASI module - is deliberate: wazero's WASI keeps
// its own file descriptor table and cannot serve fds created by a
// foreign extension, while here sockets and stdio live in one table,
// which is exactly the property the WasmEdge extension has.
//
// Semantics notes:
//   - Sockets are nonblocking when the guest sets FDFLAG_NONBLOCK
//     (Go always does). sock_connect on a nonblocking socket starts
//     the dial in a goroutine and returns EINPROGRESS; completion is
//     reported as poll_oneoff write readiness and the result is read
//     with sock_getsockopt(SOL_SOCKET, SO_ERROR), mirroring the
//     kernel dance Go's netFD.connect expects.
//   - Read readiness is tracked by a per-connection pump goroutine
//     that drains the host socket into a buffer; fd_read/sock_recv
//     serve from that buffer and return EAGAIN when it is empty.
//   - Writes go directly to the host socket with a short write
//     deadline; a full TCP buffer surfaces as a short write or EAGAIN
//     rather than wedging the single-threaded module.
//   - Errors are returned as WASI errno values (the numbering in
//     syscall/tables_wasip1.go, which is also what WasmEdge returns).
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/tetratelabs/wazero/api"
)

// WASI errno values (see syscall/tables_wasip1.go).
const (
	wasiSuccess         = 0
	wasiEACCES          = 2
	wasiEADDRINUSE      = 3
	wasiEADDRNOTAVAIL   = 4
	wasiEAFNOSUPPORT    = 5
	wasiEAGAIN          = 6
	wasiEALREADY        = 7
	wasiEBADF           = 8
	wasiECONNABORTED    = 13
	wasiECONNREFUSED    = 14
	wasiECONNRESET      = 15
	wasiEFAULT          = 21
	wasiEHOSTUNREACH    = 23
	wasiEINPROGRESS     = 26
	wasiEINTR           = 27
	wasiEINVAL          = 28
	wasiEIO             = 29
	wasiEISCONN         = 30
	wasiENETUNREACH     = 40
	wasiENOENT          = 44
	wasiENOSYS          = 52
	wasiENOTCONN        = 53
	wasiENOTSOCK        = 57
	wasiENOTSUP         = 58
	wasiEPERM           = 63
	wasiEPIPE           = 64
	wasiEPROTONOSUPPORT = 66
	wasiETIMEDOUT       = 73
)

// WASI fdflags and filetypes.
const (
	fdflagNonblock = 0x0004

	filetypeCharacterDevice = 2
	filetypeSocketDgram     = 5
	filetypeSocketStream    = 6
)

// WasmEdge enums (see syscall/net_wasip1_wasmedge.go).
const (
	wasmedgeAFUnspec = 0
	wasmedgeAFInet4  = 1
	wasmedgeAFInet6  = 2

	wasmedgeSockAny    = 0
	wasmedgeSockDgram  = 1
	wasmedgeSockStream = 2

	wasmedgeSolSocket = 0

	wasmedgeSoReuseaddr = 0
	wasmedgeSoType      = 1
	wasmedgeSoError     = 2
	wasmedgeSoKeepalive = 7
)

// poll_oneoff event types.
const (
	eventtypeClock   = 0
	eventtypeFdRead  = 1
	eventtypeFdWrite = 2
)

const (
	clockRealtime  = 0
	clockMonotonic = 1
)

// readBufSoftCap pauses the read pump when this much unread data is
// buffered for one socket, providing backpressure without ever
// deadlocking the guest (the pump resumes as soon as the guest reads).
const readBufSoftCap = 64 << 20

// packetSoftCap pauses a datagram pump when this many undelivered
// datagrams are queued for one socket; overflow beyond it then falls
// to the host kernel's receive buffer (which drops, as UDP may).
const packetSoftCap = 4096

// packet is one received datagram: payload and source address, with
// message boundaries preserved.
type packet struct {
	data []byte
	addr *net.UDPAddr
}

// sockFD is one socket in the host's fd table. A stream socket goes
// through up to three stages: fresh from sock_open (neither conn nor
// ln set), then either a listener (ln set, acceptor goroutine feeding
// pending) or a connection (conn set, pump goroutine feeding rbuf).
// A datagram socket instead gets udp set - at sock_bind for the
// unconnected shape, at sock_connect for the connected one (which
// also sets conn, so the plain fd_write path sends datagrams) - with
// a packet pump feeding packets.
type sockFD struct {
	family   int32
	sotype   int32
	nonblock bool

	// bind state, before listen/connect.
	boundIP   net.IP
	boundPort uint32

	// listener state.
	ln      net.Listener
	pending []net.Conn
	lnErr   error

	// connection state.
	conn       net.Conn
	connecting bool
	soError    uint16 // async connect result, 0 = success
	wclosed    bool

	rbuf bytes.Buffer
	rerr error // sticky read error; io.EOF means clean shutdown

	// datagram state.
	udp     *net.UDPConn
	packets []packet
	pumpGen int // invalidates a replaced pump (bind-then-connect)
}

type fdEntry struct {
	kind int // 0 stdin, 1 stdout, 2 stderr, 3 socket
	sock *sockFD
}

const (
	fdKindStdin = iota
	fdKindStdout
	fdKindStderr
	fdKindSock
)

// wasiHost implements the wasi_snapshot_preview1 module.
type wasiHost struct {
	args []string
	env  []string

	stdout io.Writer
	stderr io.Writer

	monoStart time.Time

	mu     sync.Mutex
	gen    chan struct{} // closed and replaced on every readiness change
	fds    map[int32]*fdEntry
	nextFD int32

	trace bool
}

func newWASIHost(args, env []string, stdout, stderr io.Writer, trace bool) *wasiHost {
	h := &wasiHost{
		args:      args,
		env:       env,
		stdout:    stdout,
		stderr:    stderr,
		monoStart: time.Now(),
		gen:       make(chan struct{}),
		fds: map[int32]*fdEntry{
			0: {kind: fdKindStdin},
			1: {kind: fdKindStdout},
			2: {kind: fdKindStderr},
		},
		nextFD: 4,
		trace:  trace,
	}
	return h
}

func (h *wasiHost) tracef(format string, args ...any) {
	if h.trace {
		fmt.Fprintf(os.Stderr, "wasip1sockhost: "+format+"\n", args...)
	}
}

// notify wakes every poll_oneoff waiting for a state change.
// h.mu must be held.
func (h *wasiHost) notify() {
	close(h.gen)
	h.gen = make(chan struct{})
}

// h.mu must be held.
func (h *wasiHost) newFD(e *fdEntry) int32 {
	fd := h.nextFD
	h.nextFD++
	h.fds[fd] = e
	return fd
}

// h.mu must be held.
func (h *wasiHost) sock(fd int32) (*sockFD, uint16) {
	e, ok := h.fds[fd]
	if !ok {
		return nil, wasiEBADF
	}
	if e.kind != fdKindSock {
		return nil, wasiENOTSOCK
	}
	return e.sock, 0
}

// startReadPump drains conn into s.rbuf, tracking readiness.
func (h *wasiHost) startReadPump(s *sockFD, conn net.Conn) {
	go func() {
		buf := make([]byte, 32<<10)
		for {
			h.mu.Lock()
			for s.rbuf.Len() > readBufSoftCap && s.rerr == nil {
				ch := h.gen
				h.mu.Unlock()
				<-ch
				h.mu.Lock()
			}
			h.mu.Unlock()

			n, err := conn.Read(buf)
			h.mu.Lock()
			if n > 0 {
				s.rbuf.Write(buf[:n])
			}
			if err != nil {
				if s.rerr == nil {
					s.rerr = err
				}
				h.notify()
				h.mu.Unlock()
				return
			}
			h.notify()
			h.mu.Unlock()
		}
	}()
}

// startPacketPump drains a datagram socket into s.packets, keeping
// message boundaries and source addresses. h.mu must be held. The
// pump belongs to one generation of the socket: when sock_connect
// replaces a bound socket with a connected one, the superseded pump's
// close error must not poison the fresh socket's state.
func (h *wasiHost) startPacketPump(s *sockFD, uc *net.UDPConn) {
	s.pumpGen++
	gen := s.pumpGen
	go func() {
		buf := make([]byte, 64<<10)
		for {
			h.mu.Lock()
			for gen == s.pumpGen && len(s.packets) > packetSoftCap && s.rerr == nil {
				ch := h.gen
				h.mu.Unlock()
				<-ch
				h.mu.Lock()
			}
			h.mu.Unlock()

			n, addr, err := uc.ReadFromUDP(buf)
			h.mu.Lock()
			if gen != s.pumpGen {
				h.mu.Unlock()
				return
			}
			if err == nil || n > 0 {
				// n == 0 with a nil error is a legitimate
				// zero-length datagram.
				s.packets = append(s.packets, packet{
					data: append([]byte(nil), buf[:n]...),
					addr: addr,
				})
			}
			if err != nil {
				if s.rerr == nil {
					s.rerr = err
				}
				h.notify()
				h.mu.Unlock()
				return
			}
			h.notify()
			h.mu.Unlock()
		}
	}()
}

// waitPacketLocked waits (honoring the socket's nonblock flag) for a
// queued datagram and dequeues it. Called with h.mu held; returns
// with it held.
func (h *wasiHost) waitPacketLocked(sk *sockFD) (packet, uint16) {
	if sk.udp == nil {
		return packet{}, wasiENOTCONN
	}
	for len(sk.packets) == 0 {
		if sk.rerr != nil {
			errno := mapNetErrno(sk.rerr)
			if errno == wasiSuccess {
				errno = wasiEIO
			}
			return packet{}, errno
		}
		if sk.nonblock {
			return packet{}, wasiEAGAIN
		}
		ch := h.gen
		h.mu.Unlock()
		<-ch
		h.mu.Lock()
	}
	p := sk.packets[0]
	sk.packets = sk.packets[1:]
	h.notify() // the pump may be paused on the soft cap
	return p, wasiSuccess
}

// startAcceptPump accepts connections into s.pending.
func (h *wasiHost) startAcceptPump(s *sockFD, ln net.Listener) {
	go func() {
		for {
			c, err := ln.Accept()
			h.mu.Lock()
			if err != nil {
				if s.lnErr == nil {
					s.lnErr = err
				}
				h.notify()
				h.mu.Unlock()
				return
			}
			s.pending = append(s.pending, c)
			h.notify()
			h.mu.Unlock()
		}
	}()
}

// mapNetErrno converts a Go net error into a WASI errno.
func mapNetErrno(err error) uint16 {
	if err == nil {
		return wasiSuccess
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return wasiETIMEDOUT
	}
	switch {
	case errors.Is(err, syscall.ECONNREFUSED):
		return wasiECONNREFUSED
	case errors.Is(err, syscall.ECONNRESET):
		return wasiECONNRESET
	case errors.Is(err, syscall.ECONNABORTED):
		return wasiECONNABORTED
	case errors.Is(err, syscall.EPIPE):
		return wasiEPIPE
	case errors.Is(err, syscall.EADDRINUSE):
		return wasiEADDRINUSE
	case errors.Is(err, syscall.EADDRNOTAVAIL):
		return wasiEADDRNOTAVAIL
	case errors.Is(err, syscall.EHOSTUNREACH):
		return wasiEHOSTUNREACH
	case errors.Is(err, syscall.ENETUNREACH):
		return wasiENETUNREACH
	case errors.Is(err, syscall.ETIMEDOUT):
		return wasiETIMEDOUT
	case errors.Is(err, net.ErrClosed):
		return wasiEBADF
	case errors.Is(err, io.EOF):
		return wasiSuccess
	}
	return wasiEIO
}

// readiness. h.mu must be held.

func (s *sockFD) readable() bool {
	if s.sotype == wasmedgeSockDgram {
		if s.udp == nil {
			return false
		}
		return len(s.packets) > 0 || s.rerr != nil
	}
	if s.ln != nil {
		return len(s.pending) > 0 || s.lnErr != nil
	}
	if s.connecting {
		return false
	}
	if s.conn == nil {
		return s.soError != 0
	}
	return s.rbuf.Len() > 0 || s.rerr != nil
}

func (s *sockFD) writable() bool {
	if s.sotype == wasmedgeSockDgram {
		// Datagram sends never wait for a peer, and an unbound
		// socket binds itself on the first sock_send_to.
		return true
	}
	if s.ln != nil {
		return false
	}
	if s.connecting {
		return false
	}
	// A decided (successful or failed) connect is "writable": the
	// guest wakes from WaitWrite and reads SO_ERROR.
	return s.conn != nil || s.soError != 0
}

// memory helpers

func readIOVecs(mem api.Memory, iovs, iovsLen uint32) ([][2]uint32, bool) {
	vecs := make([][2]uint32, 0, iovsLen)
	for i := uint32(0); i < iovsLen; i++ {
		ptr, ok1 := mem.ReadUint32Le(iovs + 8*i)
		length, ok2 := mem.ReadUint32Le(iovs + 8*i + 4)
		if !ok1 || !ok2 {
			return nil, false
		}
		vecs = append(vecs, [2]uint32{ptr, length})
	}
	return vecs, true
}

// gatherIOVecs copies the iovec contents out of guest memory.
func gatherIOVecs(mem api.Memory, iovs, iovsLen uint32) ([]byte, bool) {
	vecs, ok := readIOVecs(mem, iovs, iovsLen)
	if !ok {
		return nil, false
	}
	var buf bytes.Buffer
	for _, v := range vecs {
		if v[1] == 0 {
			continue
		}
		b, ok := mem.Read(v[0], v[1])
		if !ok {
			return nil, false
		}
		buf.Write(b)
	}
	return buf.Bytes(), true
}

// scatterIOVecs copies data into the iovec targets, returning the
// number of bytes placed.
func scatterIOVecs(mem api.Memory, iovs, iovsLen uint32, data []byte) (uint32, bool) {
	vecs, ok := readIOVecs(mem, iovs, iovsLen)
	if !ok {
		return 0, false
	}
	var n uint32
	for _, v := range vecs {
		if len(data) == 0 {
			break
		}
		chunk := data
		if uint32(len(chunk)) > v[1] {
			chunk = chunk[:v[1]]
		}
		if !mem.Write(v[0], chunk) {
			return 0, false
		}
		n += uint32(len(chunk))
		data = data[len(chunk):]
	}
	return n, true
}

// iovecsCap returns the total capacity of the iovecs.
func iovecsCap(mem api.Memory, iovs, iovsLen uint32) (uint32, bool) {
	vecs, ok := readIOVecs(mem, iovs, iovsLen)
	if !ok {
		return 0, false
	}
	var n uint32
	for _, v := range vecs {
		n += v[1]
	}
	return n, true
}

// wasiAddress reads WasmEdge's {buf, buf_len} address struct and the
// IP bytes it points to.
func readWasiAddressIP(mem api.Memory, addrPtr uint32) (net.IP, uint32, uint32, bool) {
	bufPtr, ok1 := mem.ReadUint32Le(addrPtr)
	bufLen, ok2 := mem.ReadUint32Le(addrPtr + 4)
	if !ok1 || !ok2 {
		return nil, 0, 0, false
	}
	if bufLen != 4 && bufLen != 16 {
		// Tolerate over-sized buffers (the SDK passes 16 for v4 in
		// some calls); the family decides how much we read.
		if bufLen < 4 {
			return nil, 0, 0, false
		}
	}
	n := bufLen
	if n > 16 {
		n = 16
	}
	b, ok := mem.Read(bufPtr, n)
	if !ok {
		return nil, 0, 0, false
	}
	ip := make(net.IP, len(b))
	copy(ip, b)
	return ip, bufPtr, bufLen, true
}

func ipFor(family int32, ip net.IP) net.IP {
	switch family {
	case wasmedgeAFInet4:
		if len(ip) >= 4 {
			return net.IP(ip[:4])
		}
	case wasmedgeAFInet6:
		if len(ip) >= 16 {
			return net.IP(ip[:16])
		}
	}
	return nil
}

// udpNet names the host network for a guest address family.
func udpNet(family int32) string {
	if family == wasmedgeAFInet6 {
		return "udp6"
	}
	return "udp4"
}

func hostAddr(ip net.IP, port uint32) string {
	if ip == nil || ip.IsUnspecified() {
		return fmt.Sprintf(":%d", port)
	}
	return net.JoinHostPort(ip.String(), fmt.Sprint(port))
}

// splitIPPort turns a net.Addr into raw IP bytes, an address kind
// (4 or 6, per the SDK contract) and a port.
func splitIPPort(a net.Addr) (ip net.IP, kind uint32, port uint32) {
	var aIP net.IP
	var aPort int
	switch ta := a.(type) {
	case *net.TCPAddr:
		if ta == nil {
			return nil, 0, 0
		}
		aIP, aPort = ta.IP, ta.Port
	case *net.UDPAddr:
		if ta == nil {
			return nil, 0, 0
		}
		aIP, aPort = ta.IP, ta.Port
	default:
		return nil, 0, 0
	}
	if v4 := aIP.To4(); v4 != nil {
		return v4, 4, uint32(aPort)
	}
	ip16 := aIP.To16()
	if ip16 == nil {
		ip16 = net.IPv6zero
	}
	return ip16, 6, uint32(aPort)
}
