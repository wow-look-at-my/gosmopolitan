// Copyright 2025 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Typed socket option helpers, in the shapes the linux port exports.
//
// A cosmo binary issues the Linux getsockopt and setsockopt syscalls. A
// Linux host runs them directly, so every helper here behaves as it does
// on the linux port. The macOS arm64 and the Windows emulations translate
// a curated table of (level, option) pairs, and report ENOPROTOOPT for a
// pair outside that table. Neither table holds an IPPROTO_IP option, an
// ICMPv6 filter, or an IPv6 multicast or path MTU option.

//go:build cosmo

package syscall

import "unsafe"

const (
	// SO_PEERCRED reads the credentials of a connected AF_UNIX peer.
	// The macOS arm64 emulation answers it from Apple's LOCAL_PEERPID
	// and LOCAL_PEERCRED options. Windows reports ENOPROTOOPT.
	SO_PEERCRED = 0x11

	// SO_BINDTODEVICE binds a socket to one network interface. macOS
	// arm64 and Windows report ENOPROTOOPT.
	SO_BINDTODEVICE = 0x19

	// SO_PASSCRED makes a socket receive the peer credentials as a
	// control message. macOS arm64 and Windows report ENOPROTOOPT.
	SO_PASSCRED = 0x10

	// SCM_CREDENTIALS carries credentials in a socket control message.
	// A sendmsg call that carries such a record reports EOPNOTSUPP on
	// macOS and on Windows.
	SCM_CREDENTIALS = 0x2
)

func GetsockoptInet4Addr(fd, level, opt int) (value [4]byte, err error) {
	vallen := _Socklen(4)
	err = getsockopt(fd, level, opt, unsafe.Pointer(&value[0]), &vallen)
	return value, err
}

func GetsockoptIPMreq(fd, level, opt int) (*IPMreq, error) {
	var value IPMreq
	vallen := _Socklen(SizeofIPMreq)
	err := getsockopt(fd, level, opt, unsafe.Pointer(&value), &vallen)
	return &value, err
}

func GetsockoptIPMreqn(fd, level, opt int) (*IPMreqn, error) {
	var value IPMreqn
	vallen := _Socklen(SizeofIPMreqn)
	err := getsockopt(fd, level, opt, unsafe.Pointer(&value), &vallen)
	return &value, err
}

func GetsockoptIPv6Mreq(fd, level, opt int) (*IPv6Mreq, error) {
	var value IPv6Mreq
	vallen := _Socklen(SizeofIPv6Mreq)
	err := getsockopt(fd, level, opt, unsafe.Pointer(&value), &vallen)
	return &value, err
}

func GetsockoptIPv6MTUInfo(fd, level, opt int) (*IPv6MTUInfo, error) {
	var value IPv6MTUInfo
	vallen := _Socklen(SizeofIPv6MTUInfo)
	err := getsockopt(fd, level, opt, unsafe.Pointer(&value), &vallen)
	return &value, err
}

func GetsockoptICMPv6Filter(fd, level, opt int) (*ICMPv6Filter, error) {
	var value ICMPv6Filter
	vallen := _Socklen(SizeofICMPv6Filter)
	err := getsockopt(fd, level, opt, unsafe.Pointer(&value), &vallen)
	return &value, err
}

func GetsockoptUcred(fd, level, opt int) (*Ucred, error) {
	var value Ucred
	vallen := _Socklen(SizeofUcred)
	err := getsockopt(fd, level, opt, unsafe.Pointer(&value), &vallen)
	return &value, err
}

func SetsockoptIPMreqn(fd, level, opt int, mreq *IPMreqn) (err error) {
	return setsockopt(fd, level, opt, unsafe.Pointer(mreq), unsafe.Sizeof(*mreq))
}

func BindToDevice(fd int, device string) (err error) {
	return SetsockoptString(fd, SOL_SOCKET, SO_BINDTODEVICE, device)
}

// UnixCredentials encodes credentials into a socket control message
// for sending to another process. This can be used for
// authentication.
func UnixCredentials(ucred *Ucred) []byte {
	b := make([]byte, CmsgSpace(SizeofUcred))
	h := (*Cmsghdr)(unsafe.Pointer(&b[0]))
	h.Level = SOL_SOCKET
	h.Type = SCM_CREDENTIALS
	h.SetLen(CmsgLen(SizeofUcred))
	*(*Ucred)(h.data(0)) = *ucred
	return b
}

// ParseUnixCredentials decodes a socket control message that contains
// credentials in a Ucred structure. To receive such a message, the
// SO_PASSCRED option must be enabled on the socket.
func ParseUnixCredentials(m *SocketControlMessage) (*Ucred, error) {
	if m.Header.Level != SOL_SOCKET {
		return nil, EINVAL
	}
	if m.Header.Type != SCM_CREDENTIALS {
		return nil, EINVAL
	}
	if uintptr(len(m.Data)) < unsafe.Sizeof(Ucred{}) {
		return nil, EINVAL
	}
	ucred := *(*Ucred)(unsafe.Pointer(&m.Data[0]))
	return &ucred, nil
}
