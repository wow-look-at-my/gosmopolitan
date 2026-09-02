// Copyright 2011 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Netlink sockets and messages

//go:build cosmo

package syscall

import (
	"sync"
	"unsafe"
)

// Netlink constants. The cosmo zerrors files do not carry them. Every
// Linux architecture agrees on each value.
const (
	NETLINK_ROUTE = 0x0

	NLMSG_ALIGNTO  = 0x4
	NLMSG_DONE     = 0x3
	NLMSG_ERROR    = 0x2
	NLMSG_HDRLEN   = 0x10
	NLMSG_MIN_TYPE = 0x10
	NLMSG_NOOP     = 0x1
	NLMSG_OVERRUN  = 0x4

	NLM_F_ACK     = 0x4
	NLM_F_APPEND  = 0x800
	NLM_F_ATOMIC  = 0x400
	NLM_F_CREATE  = 0x400
	NLM_F_DUMP    = 0x300
	NLM_F_ECHO    = 0x8
	NLM_F_EXCL    = 0x200
	NLM_F_MATCH   = 0x200
	NLM_F_MULTI   = 0x2
	NLM_F_REPLACE = 0x100
	NLM_F_REQUEST = 0x1
	NLM_F_ROOT    = 0x100

	RTA_ALIGNTO = 0x4

	RTM_BASE         = 0x10
	RTM_DELACTION    = 0x31
	RTM_DELADDR      = 0x15
	RTM_DELADDRLABEL = 0x49
	RTM_DELLINK      = 0x11
	RTM_DELNEIGH     = 0x1d
	RTM_DELQDISC     = 0x25
	RTM_DELROUTE     = 0x19
	RTM_DELRULE      = 0x21
	RTM_DELTCLASS    = 0x29
	RTM_DELTFILTER   = 0x2d
	RTM_F_CLONED     = 0x200
	RTM_F_EQUALIZE   = 0x400
	RTM_F_NOTIFY     = 0x100
	RTM_F_PREFIX     = 0x800
	RTM_GETACTION    = 0x32
	RTM_GETADDR      = 0x16
	RTM_GETADDRLABEL = 0x4a
	RTM_GETANYCAST   = 0x3e
	RTM_GETDCB       = 0x4e
	RTM_GETLINK      = 0x12
	RTM_GETMULTICAST = 0x3a
	RTM_GETNEIGH     = 0x1e
	RTM_GETNEIGHTBL  = 0x42
	RTM_GETQDISC     = 0x26
	RTM_GETROUTE     = 0x1a
	RTM_GETRULE      = 0x22
	RTM_GETTCLASS    = 0x2a
	RTM_GETTFILTER   = 0x2e
	RTM_MAX          = 0x4f
	RTM_NEWACTION    = 0x30
	RTM_NEWADDR      = 0x14
	RTM_NEWADDRLABEL = 0x48
	RTM_NEWLINK      = 0x10
	RTM_NEWNDUSEROPT = 0x44
	RTM_NEWNEIGH     = 0x1c
	RTM_NEWNEIGHTBL  = 0x40
	RTM_NEWPREFIX    = 0x34
	RTM_NEWQDISC     = 0x24
	RTM_NEWROUTE     = 0x18
	RTM_NEWRULE      = 0x20
	RTM_NEWTCLASS    = 0x28
	RTM_NEWTFILTER   = 0x2c
	RTM_NR_FAMILIES  = 0x10
	RTM_NR_MSGTYPES  = 0x40
	RTM_SETDCB       = 0x4f
	RTM_SETLINK      = 0x13
	RTM_SETNEIGHTBL  = 0x43
)

// Round the length of a netlink message up to align it properly.
func nlmAlignOf(msglen int) int {
	return (msglen + NLMSG_ALIGNTO - 1) & ^(NLMSG_ALIGNTO - 1)
}

// Round the length of a netlink route attribute up to align it
// properly.
func rtaAlignOf(attrlen int) int {
	return (attrlen + RTA_ALIGNTO - 1) & ^(RTA_ALIGNTO - 1)
}

// NetlinkRouteRequest represents a request message to receive routing
// and link states from the kernel.
type NetlinkRouteRequest struct {
	Header NlMsghdr
	Data   RtGenmsg
}

func (rr *NetlinkRouteRequest) toWireFormat() []byte {
	b := make([]byte, rr.Header.Len)
	*(*uint32)(unsafe.Pointer(&b[0:4][0])) = rr.Header.Len
	*(*uint16)(unsafe.Pointer(&b[4:6][0])) = rr.Header.Type
	*(*uint16)(unsafe.Pointer(&b[6:8][0])) = rr.Header.Flags
	*(*uint32)(unsafe.Pointer(&b[8:12][0])) = rr.Header.Seq
	*(*uint32)(unsafe.Pointer(&b[12:16][0])) = rr.Header.Pid
	b[16] = rr.Data.Family
	return b
}

func newNetlinkRouteRequest(proto, seq, family int) []byte {
	rr := &NetlinkRouteRequest{}
	rr.Header.Len = uint32(NLMSG_HDRLEN + SizeofRtGenmsg)
	rr.Header.Type = uint16(proto)
	rr.Header.Flags = NLM_F_DUMP | NLM_F_REQUEST
	rr.Header.Seq = uint32(seq)
	rr.Data.Family = uint8(family)
	return rr.toWireFormat()
}

var pageBufPool = &sync.Pool{New: func() any {
	b := make([]byte, Getpagesize())
	return &b
}}

// NetlinkRIB returns routing information base, as known as RIB, which
// consists of network facility information, states and parameters.
func NetlinkRIB(proto, family int) ([]byte, error) {
	// Only a Linux kernel provides netlink. The macOS arm64 and the
	// Windows socket emulations reject the AF_NETLINK domain, so this
	// call returns EAFNOSUPPORT on those hosts.
	s, err := Socket(AF_NETLINK, SOCK_RAW|SOCK_CLOEXEC, NETLINK_ROUTE)
	if err != nil {
		return nil, err
	}
	defer Close(s)
	sa := &SockaddrNetlink{Family: AF_NETLINK}
	if err := Bind(s, sa); err != nil {
		return nil, err
	}
	wb := newNetlinkRouteRequest(proto, 1, family)
	if err := Sendto(s, wb, 0, sa); err != nil {
		return nil, err
	}
	lsa, err := Getsockname(s)
	if err != nil {
		return nil, err
	}
	lsanl, ok := lsa.(*SockaddrNetlink)
	if !ok {
		return nil, EINVAL
	}
	var tab []byte

	rbNew := pageBufPool.Get().(*[]byte)
	defer pageBufPool.Put(rbNew)
done:
	for {
		rb := *rbNew
		nr, _, err := Recvfrom(s, rb, 0)
		if err != nil {
			return nil, err
		}
		if nr < NLMSG_HDRLEN {
			return nil, EINVAL
		}
		rb = rb[:nr]
		tab = append(tab, rb...)
		msgs, err := ParseNetlinkMessage(rb)
		if err != nil {
			return nil, err
		}
		for _, m := range msgs {
			if m.Header.Seq != 1 || m.Header.Pid != lsanl.Pid {
				return nil, EINVAL
			}
			if m.Header.Type == NLMSG_DONE {
				break done
			}
			if m.Header.Type == NLMSG_ERROR {
				return nil, EINVAL
			}
		}
	}
	return tab, nil
}

// NetlinkMessage represents a netlink message.
type NetlinkMessage struct {
	Header NlMsghdr
	Data   []byte
}

// ParseNetlinkMessage parses b as an array of netlink messages and
// returns the slice containing the NetlinkMessage structures.
func ParseNetlinkMessage(b []byte) ([]NetlinkMessage, error) {
	var msgs []NetlinkMessage
	for len(b) >= NLMSG_HDRLEN {
		h, dbuf, dlen, err := netlinkMessageHeaderAndData(b)
		if err != nil {
			return nil, err
		}
		m := NetlinkMessage{Header: *h, Data: dbuf[:int(h.Len)-NLMSG_HDRLEN]}
		msgs = append(msgs, m)
		b = b[dlen:]
	}
	return msgs, nil
}

func netlinkMessageHeaderAndData(b []byte) (*NlMsghdr, []byte, int, error) {
	h := (*NlMsghdr)(unsafe.Pointer(&b[0]))
	l := nlmAlignOf(int(h.Len))
	if int(h.Len) < NLMSG_HDRLEN || l > len(b) {
		return nil, nil, 0, EINVAL
	}
	return h, b[NLMSG_HDRLEN:], l, nil
}

// NetlinkRouteAttr represents a netlink route attribute.
type NetlinkRouteAttr struct {
	Attr  RtAttr
	Value []byte
}

// ParseNetlinkRouteAttr parses m's payload as an array of netlink
// route attributes and returns the slice containing the
// NetlinkRouteAttr structures.
func ParseNetlinkRouteAttr(m *NetlinkMessage) ([]NetlinkRouteAttr, error) {
	var b []byte
	switch m.Header.Type {
	case RTM_NEWLINK, RTM_DELLINK:
		b = m.Data[SizeofIfInfomsg:]
	case RTM_NEWADDR, RTM_DELADDR:
		b = m.Data[SizeofIfAddrmsg:]
	case RTM_NEWROUTE, RTM_DELROUTE:
		b = m.Data[SizeofRtMsg:]
	default:
		return nil, EINVAL
	}
	var attrs []NetlinkRouteAttr
	for len(b) >= SizeofRtAttr {
		a, vbuf, alen, err := netlinkRouteAttrAndValue(b)
		if err != nil {
			return nil, err
		}
		ra := NetlinkRouteAttr{Attr: *a, Value: vbuf[:int(a.Len)-SizeofRtAttr]}
		attrs = append(attrs, ra)
		b = b[alen:]
	}
	return attrs, nil
}

func netlinkRouteAttrAndValue(b []byte) (*RtAttr, []byte, int, error) {
	a := (*RtAttr)(unsafe.Pointer(&b[0]))
	if int(a.Len) < SizeofRtAttr || int(a.Len) > len(b) {
		return nil, nil, 0, EINVAL
	}
	return a, b[SizeofRtAttr:], rtaAlignOf(int(a.Len)), nil
}
