// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build wasip1 && wasip1.wasmedgesock

package net

import (
	"context"
	"os"
	"syscall"
)

// With GOWASI=wasmedgesock, TCP sockets on wasip1 are real: they are
// created through the WasmEdge socket extension to WASI preview 1
// (see syscall/net_wasip1_wasmedge.go) and their fds flow through
// internal/poll and the runtime's poll_oneoff netpoller like the fds
// of inherited pre-opened listeners always have. Everything the
// extension cannot express yet (UDP, unix sockets, raw IP, RawConn
// control functions) still goes to the fake in-memory network, and
// DNS resolution still only works for addresses the fake network can
// answer, so real dials should use IP literals.

// socket returns a network file descriptor that is ready for
// asynchronous I/O using the network poller.
func socket(ctx context.Context, net string, family, sotype, proto int, ipv6only bool, laddr, raddr sockaddr, ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error) (*netFD, error) {
	if sotype != syscall.SOCK_STREAM || (family != syscall.AF_INET && family != syscall.AF_INET6) {
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
