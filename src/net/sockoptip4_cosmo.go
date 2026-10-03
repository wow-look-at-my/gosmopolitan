// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package net

import (
	"internal/bytealg"
	"runtime"
	"syscall"
)

func joinIPv4Group(fd *netFD, ifi *Interface, ip IP) error {
	mreq := &syscall.IPMreq{Multiaddr: [4]byte{ip[0], ip[1], ip[2], ip[3]}}
	if err := setIPv4MreqToInterface(mreq, ifi); err != nil {
		return err
	}
	err := fd.pfd.SetsockoptIPMreq(syscall.IPPROTO_IP, syscall.IP_ADD_MEMBERSHIP, mreq)
	runtime.KeepAlive(fd)
	return wrapSyscallError("setsockopt", err)
}

func setIPv4MreqToInterface(mreq *syscall.IPMreq, ifi *Interface) error {
	if ifi == nil {
		return nil
	}
	ifat, err := ifi.Addrs()
	if err != nil {
		return err
	}
	for _, ifa := range ifat {
		switch v := ifa.(type) {
		case *IPAddr:
			if a := v.IP.To4(); a != nil {
				copy(mreq.Interface[:], a)
				goto done
			}
		case *IPNet:
			if a := v.IP.To4(); a != nil {
				copy(mreq.Interface[:], a)
				goto done
			}
		}
	}
done:
	if bytealg.Equal(mreq.Interface[:], IPv4zero.To4()) {
		return errNoSuchMulticastInterface
	}
	return nil
}

func setIPv4MulticastInterface(fd *netFD, ifi *Interface) error {
	ip, err := interfaceToIPv4Addr(ifi)
	if err != nil {
		return wrapSyscallError("setsockopt", err)
	}
	var a [4]byte
	copy(a[:], ip.To4())
	err = fd.pfd.SetsockoptInet4Addr(syscall.IPPROTO_IP, syscall.IP_MULTICAST_IF, a)
	runtime.KeepAlive(fd)
	return wrapSyscallError("setsockopt", err)
}

func setIPv4MulticastLoopback(fd *netFD, v bool) error {
	err := fd.pfd.SetsockoptInt(syscall.IPPROTO_IP, syscall.IP_MULTICAST_LOOP, boolint(v))
	runtime.KeepAlive(fd)
	return wrapSyscallError("setsockopt", err)
}
