// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build js || (wasip1 && !wasip1.wasmedgesock)

package net

import (
	"context"
	"syscall"
)

// socket returns a network file descriptor that is ready for
// I/O using the fake network. On wasip1 with GOWASI=wasmedgesock this
// is replaced by the WasmEdge socket version in sock_wasip1_wasmedge.go.
func socket(ctx context.Context, net string, family, sotype, proto int, ipv6only bool, laddr, raddr sockaddr, ctrlCtxFn func(context.Context, string, string, syscall.RawConn) error) (*netFD, error) {
	return fakeSocket(ctx, net, family, sotype, proto, ipv6only, laddr, raddr, ctrlCtxFn)
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
	netfd = newFD("tcp", d)
	if err = netfd.init(); err != nil {
		netfd.Close()
		return nil, err
	}
	return netfd, nil
}
