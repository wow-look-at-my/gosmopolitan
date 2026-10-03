// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1 && wasip1.wasmedgesock

package net

import (
	"context"
	"os"
	"runtime"
	"syscall"
)

// With GOWASI=wasmedgesock, TCP and UDP sockets on wasip1 are real:
// they are created through the WasmEdge socket extension to WASI
// preview 1 (see syscall/net_wasip1_wasmedge.go) and their fds flow
// through internal/poll and the runtime's poll_oneoff netpoller like
// the fds of inherited pre-opened listeners always have. TCP gets
// Dial/Listen/Accept; UDP gets ListenUDP/ListenPacket with
// ReadFrom/WriteTo and connected-mode Dial with Read/Write, with the
// same deadline machinery as TCP. Everything the extension cannot
// express (unix sockets, raw IP, RawConn control functions, the
// ancillary-data ReadMsgUDP/WriteMsgUDP) still goes to the fake
// in-memory network or errors, and DNS resolution still only works
// for addresses the fake network can answer, so real dials should use
// IP literals.

const (
	readFromSyscallName = "recvfrom"
	writeToSyscallName  = "sendto"
)

// socket returns a network file descriptor that is ready for
// asynchronous I/O using the network poller.
func socket(ctx context.Context, net string, family, sotype, proto int, ipv6only bool, laddr, raddr sockaddr, ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error) (*netFD, error) {
	if (sotype != syscall.SOCK_STREAM && sotype != syscall.SOCK_DGRAM) ||
		(family != syscall.AF_INET && family != syscall.AF_INET6) {
		return fakeSocket(ctx, net, family, sotype, proto, ipv6only, laddr, raddr, ctrlCtxFn)
	}
	if ctrlCtxFn != nil {
		// The WasmEdge extension has no way to expose a raw fd
		// control point before connect/listen; RawConn Control
		// functions cannot be honored.
		return nil, os.NewSyscallError("socket", syscall.ENOTSUP)
	}
	s, err := syscall.Socket(family, sotype, proto)
	if err != nil {
		return nil, os.NewSyscallError("socket", err)
	}
	if err := syscall.SetNonblock(s, true); err != nil {
		syscall.Close(s)
		return nil, os.NewSyscallError("setnonblock", err)
	}
	fd := newFD(net, s)
	fd.family = family
	fd.sotype = sotype

	if raddr == nil {
		if sotype == syscall.SOCK_DGRAM {
			if err := fd.listenDatagram(laddr); err != nil {
				fd.Close()
				return nil, err
			}
			return fd, nil
		}
		if err := fd.listenStream(laddr, listenerBacklog()); err != nil {
			fd.Close()
			return nil, err
		}
		return fd, nil
	}
	if err := fd.dial(ctx, laddr, raddr); err != nil {
		fd.Close()
		return nil, err
	}
	return fd, nil
}

func (fd *netFD) listenStream(laddr sockaddr, backlog int) error {
	// Allow reuse of recently-used addresses. Best effort: not every
	// host implementing the extension accepts this option.
	syscall.SetsockoptInt(fd.pfd.Sysfd, syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	lsa, err := laddr.sockaddr(fd.family)
	if err != nil {
		return err
	}
	if lsa != nil {
		if err := syscall.Bind(fd.pfd.Sysfd, lsa); err != nil {
			return os.NewSyscallError("bind", err)
		}
	}
	if err := syscall.Listen(fd.pfd.Sysfd, backlog); err != nil {
		return os.NewSyscallError("listen", err)
	}
	if err := fd.init(); err != nil {
		return err
	}
	gsa, _ := syscall.Getsockname(fd.pfd.Sysfd)
	fd.setAddr(fd.addrFunc()(gsa), nil)
	return nil
}

// listenDatagram binds a datagram socket and registers it with the
// poller: the UDP half of listenStream (datagram sockets have no
// listen step).
func (fd *netFD) listenDatagram(laddr sockaddr) error {
	lsa, err := laddr.sockaddr(fd.family)
	if err != nil {
		return err
	}
	if lsa == nil {
		// ListenUDP(nil): bind the wildcard so the socket gets a
		// real local port to receive on.
		switch fd.family {
		case syscall.AF_INET:
			lsa = &syscall.SockaddrInet4{}
		default:
			lsa = &syscall.SockaddrInet6{}
		}
	}
	if err := syscall.Bind(fd.pfd.Sysfd, lsa); err != nil {
		return os.NewSyscallError("bind", err)
	}
	if err := fd.init(); err != nil {
		return err
	}
	gsa, _ := syscall.Getsockname(fd.pfd.Sysfd)
	fd.setAddr(fd.addrFunc()(gsa), nil)
	return nil
}

func (fd *netFD) dial(ctx context.Context, laddr, raddr sockaddr) error {
	var lsa syscall.Sockaddr
	var err error
	if laddr != nil {
		if lsa, err = laddr.sockaddr(fd.family); err != nil {
			return err
		}
		if lsa != nil {
			if err := syscall.Bind(fd.pfd.Sysfd, lsa); err != nil {
				return os.NewSyscallError("bind", err)
			}
		}
	}
	rsa, err := raddr.sockaddr(fd.family)
	if err != nil {
		return err
	}
	if err := fd.connect(ctx, rsa); err != nil {
		return err
	}
	fd.isConnected = true

	gla, _ := syscall.Getsockname(fd.pfd.Sysfd)
	gra, _ := syscall.Getpeername(fd.pfd.Sysfd)
	la := fd.addrFunc()(gla)
	ra := fd.addrFunc()(gra)
	if ra == nil {
		// Not every host reports the peer address; fall back to
		// the address we dialed.
		ra = raddr
	}
	fd.setAddr(la, ra)
	return nil
}

// connect issues a nonblocking connect and waits for it to complete,
// honoring the context deadline and cancellation. It is a simplified
// version of the unix netFD.connect.
func (fd *netFD) connect(ctx context.Context, rsa syscall.Sockaddr) error {
	switch err := syscall.Connect(fd.pfd.Sysfd, rsa); err {
	case syscall.EINPROGRESS, syscall.EALREADY, syscall.EINTR:
	case nil:
		return fd.init()
	default:
		return os.NewSyscallError("connect", err)
	}
	if err := fd.init(); err != nil {
		return err
	}
	defer fd.pfd.SetWriteDeadline(noDeadline)
	if deadline, hasDeadline := ctx.Deadline(); hasDeadline {
		fd.pfd.SetWriteDeadline(deadline)
	}
	if ctxDone := ctx.Done(); ctxDone != nil {
		stop := make(chan struct{})
		defer close(stop)
		go func() {
			select {
			case <-ctxDone:
				// Force the WaitWrite below to wake up.
				fd.pfd.SetWriteDeadline(aLongTimeAgo)
			case <-stop:
			}
		}()
	}
	for {
		// Wait for the socket to become writable: the host reports
		// write readiness through poll_oneoff once the connection
		// attempt is decided.
		if err := fd.pfd.WaitWrite(); err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return mapErr(ctxErr)
			}
			return err
		}
		nerr, err := syscall.GetsockoptInt(fd.pfd.Sysfd, syscall.SOL_SOCKET, syscall.SO_ERROR)
		if err != nil {
			return os.NewSyscallError("getsockopt", err)
		}
		switch err := syscall.Errno(nerr); err {
		case syscall.EINPROGRESS, syscall.EALREADY, syscall.EINTR:
		case syscall.Errno(0), syscall.EISCONN:
			if ctxErr := ctx.Err(); ctxErr != nil {
				return mapErr(ctxErr)
			}
			return nil
		default:
			return os.NewSyscallError("connect", err)
		}
	}
}

func (fd *netFD) accept() (netfd *netFD, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.accept(fd.laddr)
	}
	d, _, errcall, err := fd.pfd.Accept()
	if err != nil {
		if errcall != "" {
			err = wrapSyscallError(errcall, err)
		}
		return nil, err
	}
	netfd = newFD(fd.net, d)
	netfd.family = fd.family
	netfd.sotype = fd.sotype
	netfd.isConnected = true
	if err = netfd.init(); err != nil {
		netfd.Close()
		return nil, err
	}
	lsa, _ := syscall.Getsockname(d)
	rsa, _ := syscall.Getpeername(d)
	la := netfd.addrFunc()(lsa)
	ra := netfd.addrFunc()(rsa)
	if la == nil {
		la = fd.laddr
	}
	netfd.setAddr(la, ra)
	return netfd, nil
}

// UDP data path. On js and wasip1 the netFD normally serves the
// per-datagram methods through its embedded *fakeNetFD; with real
// wasmedgesock sockets that embedded pointer is nil, so the methods
// below shadow the promoted ones: fake sockets still delegate to the
// fake network, real sockets go through internal/poll's generic
// ReadFrom/WriteTo (which call syscall.Recvfrom/Sendto, the
// sock_recv_from_v2/sock_send_to wrappers) and share the TCP path's
// poller and deadline machinery.

func (fd *netFD) readFromInet4(p []byte, from *syscall.SockaddrInet4) (n int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.readFromInet4(p, from)
	}
	n, sa, err := fd.pfd.ReadFrom(p)
	if sa4, ok := sa.(*syscall.SockaddrInet4); ok {
		*from = *sa4
	}
	runtime.KeepAlive(fd)
	return n, wrapSyscallError(readFromSyscallName, err)
}

func (fd *netFD) readFromInet6(p []byte, from *syscall.SockaddrInet6) (n int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.readFromInet6(p, from)
	}
	n, sa, err := fd.pfd.ReadFrom(p)
	if sa6, ok := sa.(*syscall.SockaddrInet6); ok {
		*from = *sa6
	}
	runtime.KeepAlive(fd)
	return n, wrapSyscallError(readFromSyscallName, err)
}

func (fd *netFD) writeToInet4(p []byte, sa *syscall.SockaddrInet4) (n int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.writeToInet4(p, sa)
	}
	n, err = fd.pfd.WriteTo(p, sa)
	runtime.KeepAlive(fd)
	return n, wrapSyscallError(writeToSyscallName, err)
}

func (fd *netFD) writeToInet6(p []byte, sa *syscall.SockaddrInet6) (n int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.writeToInet6(p, sa)
	}
	n, err = fd.pfd.WriteTo(p, sa)
	runtime.KeepAlive(fd)
	return n, wrapSyscallError(writeToSyscallName, err)
}

// The msg variants need recvmsg/sendmsg with ancillary data, which the
// WasmEdge extension does not define; on real sockets they fail with
// ENOSYS (surfacing from ReadMsgUDP/WriteMsgUDP) while the plain
// ReadFrom/WriteTo/Read/Write paths above cover UDP.

func (fd *netFD) readMsgInet4(p, oob []byte, flags int, sa *syscall.SockaddrInet4) (n, oobn, retflags int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.readMsgInet4(p, oob, flags, sa)
	}
	return 0, 0, 0, os.NewSyscallError("recvmsg", syscall.ENOSYS)
}

func (fd *netFD) readMsgInet6(p, oob []byte, flags int, sa *syscall.SockaddrInet6) (n, oobn, retflags int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.readMsgInet6(p, oob, flags, sa)
	}
	return 0, 0, 0, os.NewSyscallError("recvmsg", syscall.ENOSYS)
}

func (fd *netFD) writeMsg(p, oob []byte, sa syscall.Sockaddr) (n int, oobn int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.writeMsg(p, oob, sa)
	}
	return 0, 0, os.NewSyscallError("sendmsg", syscall.ENOSYS)
}

func (fd *netFD) writeMsgInet4(p, oob []byte, sa *syscall.SockaddrInet4) (n int, oobn int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.writeMsgInet4(p, oob, sa)
	}
	return 0, 0, os.NewSyscallError("sendmsg", syscall.ENOSYS)
}

func (fd *netFD) writeMsgInet6(p, oob []byte, sa *syscall.SockaddrInet6) (n int, oobn int, err error) {
	if fd.fakeNetFD != nil {
		return fd.fakeNetFD.writeMsgInet6(p, oob, sa)
	}
	return 0, 0, os.NewSyscallError("sendmsg", syscall.ENOSYS)
}
