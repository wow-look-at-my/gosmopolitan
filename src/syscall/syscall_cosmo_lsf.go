// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall

import "unsafe"

// The Linux socket filter group. GOOS=cosmo presents the Linux ABI, so a
// program written against the linux port names these, and the linux port
// declares them in lsf_linux.go, which cosmo does not build.
//
// The Linux kernel serves the socket filter, the packet socket and the
// interface flag ioctls. No darwin dispatch and no NT dispatch names these
// numbers, so both answer ENOSYS on a non-Linux host.

// The interface flag and filter constants the group needs. cosmo's zerrors
// tables do not carry them. The values are the linux port's, from
// zerrors_linux_amd64.go and zerrors_linux_arm64.go, which agree.
//
// AF_PACKET and SockaddrLinklayer come from syscall_cosmo_sockaddr.go.
const (
	IFF_PROMISC      = 0x100
	IFNAMSIZ         = 0x10
	SIOCGIFFLAGS     = 0x8913
	SIOCSIFFLAGS     = 0x8914
	SO_ATTACH_FILTER = 0x1a
	SO_DETACH_FILTER = 0x1b
)

// Deprecated: Use golang.org/x/net/bpf instead.
func LsfStmt(code, k int) *SockFilter {
	return &SockFilter{Code: uint16(code), K: uint32(k)}
}

// Deprecated: Use golang.org/x/net/bpf instead.
func LsfJump(code, k, jt, jf int) *SockFilter {
	return &SockFilter{Code: uint16(code), Jt: uint8(jt), Jf: uint8(jf), K: uint32(k)}
}

// Deprecated: Use golang.org/x/net/bpf instead.
func LsfSocket(ifindex, proto int) (int, error) {
	var lsall SockaddrLinklayer
	// This is missing SOCK_CLOEXEC, but adding the flag
	// could break callers.
	s, e := Socket(AF_PACKET, SOCK_RAW, proto)
	if e != nil {
		return 0, e
	}
	p := (*[2]byte)(unsafe.Pointer(&lsall.Protocol))
	p[0] = byte(proto >> 8)
	p[1] = byte(proto)
	lsall.Ifindex = ifindex
	e = Bind(s, &lsall)
	if e != nil {
		Close(s)
		return 0, e
	}
	return s, nil
}

type iflags struct {
	name  [IFNAMSIZ]byte
	flags uint16
}

// Deprecated: Use golang.org/x/net/bpf instead.
func SetLsfPromisc(name string, m bool) error {
	s, e := Socket(AF_INET, SOCK_DGRAM|SOCK_CLOEXEC, 0)
	if e != nil {
		return e
	}
	defer Close(s)
	var ifl iflags
	copy(ifl.name[:], []byte(name))
	_, _, ep := Syscall(SYS_IOCTL, uintptr(s), SIOCGIFFLAGS, uintptr(unsafe.Pointer(&ifl)))
	if ep != 0 {
		return Errno(ep)
	}
	if m {
		ifl.flags |= uint16(IFF_PROMISC)
	} else {
		ifl.flags &^= uint16(IFF_PROMISC)
	}
	_, _, ep = Syscall(SYS_IOCTL, uintptr(s), SIOCSIFFLAGS, uintptr(unsafe.Pointer(&ifl)))
	if ep != 0 {
		return Errno(ep)
	}
	return nil
}

// Deprecated: Use golang.org/x/net/bpf instead.
func AttachLsf(fd int, i []SockFilter) error {
	var p SockFprog
	p.Len = uint16(len(i))
	p.Filter = (*SockFilter)(unsafe.Pointer(&i[0]))
	return setsockopt(fd, SOL_SOCKET, SO_ATTACH_FILTER, unsafe.Pointer(&p), unsafe.Sizeof(p))
}

// Deprecated: Use golang.org/x/net/bpf instead.
func DetachLsf(fd int) error {
	var dummy int
	return setsockopt(fd, SOL_SOCKET, SO_DETACH_FILTER, unsafe.Pointer(&dummy), unsafe.Sizeof(dummy))
}
