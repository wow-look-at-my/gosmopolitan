// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cosmo_test

import (
	"bytes"
	"encoding/binary"
	"internal/runtime/syscall/cosmo"
	"runtime"
	"testing"
	"unsafe"
)

// The darwin sendmsg/recvmsg emulation's control-buffer repack
// (socket_msg_cosmo.go) is pure byte manipulation, byte-identical on
// both cosmo architectures, so these tests pin its behavior on any
// host - the Linux CI leg runs them; macOS proves the dlsym wiring.
//
// Layout ground truth (see that file's header): Linux cmsghdr is
// {Len u64, Level i32, Type i32} (16 bytes, data aligned to 8); Apple's
// is {Len u32, Level i32, Type i32} (12 bytes, data aligned to 4).
// SOL_SOCKET is 1 on Linux and 0xffff on Apple; SCM_RIGHTS is 1 on
// both.

const (
	tLinuxHdr  = 16
	tAppleHdr  = 12
	tSOLLinux  = 1
	tSOLApple  = 0xffff
	tSCMRights = 1

	tEINVAL     = 22
	tEMSGSIZE   = 90
	tEOPNOTSUPP = 95
	tENOBUFS    = 105
)

// linuxCmsg appends one Linux-shaped cmsg (header + data + alignment
// padding to 8, unless last is set, in which case the final record is
// left unpadded - CMSG_LEN-tight, which the walker must accept).
func linuxCmsg(b []byte, level, typ int32, data []byte, last bool) []byte {
	var hdr [tLinuxHdr]byte
	binary.LittleEndian.PutUint64(hdr[0:], uint64(tLinuxHdr+len(data)))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(level))
	binary.LittleEndian.PutUint32(hdr[12:], uint32(typ))
	b = append(b, hdr[:]...)
	b = append(b, data...)
	if !last {
		for len(b)%8 != 0 {
			b = append(b, 0)
		}
	}
	return b
}

// appleCmsg appends one Apple-shaped cmsg (padding to 4 unless last).
func appleCmsg(b []byte, level, typ int32, data []byte, last bool) []byte {
	var hdr [tAppleHdr]byte
	binary.LittleEndian.PutUint32(hdr[0:], uint32(tAppleHdr+len(data)))
	binary.LittleEndian.PutUint32(hdr[4:], uint32(level))
	binary.LittleEndian.PutUint32(hdr[8:], uint32(typ))
	b = append(b, hdr[:]...)
	b = append(b, data...)
	if !last {
		for len(b)%4 != 0 {
			b = append(b, 0)
		}
	}
	return b
}

func fdBytes(fds ...int32) []byte {
	b := make([]byte, 4*len(fds))
	for i, fd := range fds {
		binary.LittleEndian.PutUint32(b[4*i:], uint32(fd))
	}
	return b
}

func toApple(t *testing.T, src []byte, dstCap int) (dst []byte, errno uintptr) {
	t.Helper()
	dstBuf := make([]byte, dstCap)
	var srcp, dstp uintptr
	if len(src) > 0 {
		srcp = uintptr(unsafe.Pointer(&src[0]))
	}
	if dstCap > 0 {
		dstp = uintptr(unsafe.Pointer(&dstBuf[0]))
	}
	dlen, errno := cosmo.CmsgToApple(srcp, uintptr(len(src)), dstp, uintptr(dstCap))
	runtime.KeepAlive(src)
	runtime.KeepAlive(dstBuf)
	if errno != 0 {
		return nil, errno
	}
	return dstBuf[:dlen], 0
}

// toLinux runs the in-place receive-side repack: buf's first alen bytes
// hold Apple records, cap(buf) plays the caller's Linux capacity.
func toLinux(t *testing.T, apple []byte, capacity int, applied, closed *[]int32) (out []byte, ctrunc bool) {
	t.Helper()
	if capacity < len(apple) {
		t.Fatalf("test bug: capacity %d < apple len %d (Apple never delivers past the caller's buffer)", capacity, len(apple))
	}
	buf := make([]byte, capacity)
	copy(buf, apple)
	var apply, cls func(int32)
	if applied != nil {
		apply = func(fd int32) { *applied = append(*applied, fd) }
	}
	if closed != nil {
		cls = func(fd int32) { *closed = append(*closed, fd) }
	}
	var bufp uintptr
	if capacity > 0 {
		bufp = uintptr(unsafe.Pointer(&buf[0]))
	}
	llen, ctrunc := cosmo.CmsgToLinux(bufp, uintptr(len(apple)), uintptr(capacity), apply, cls)
	runtime.KeepAlive(buf)
	return buf[:llen], ctrunc
}

func TestMsghdrMirrorLayouts(t *testing.T) {
	var lm cosmo.LinuxMsghdrForTest
	if got := unsafe.Sizeof(lm); got != 56 {
		t.Errorf("linuxMsghdr size = %d, want 56", got)
	}
	checks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Name", unsafe.Offsetof(lm.Name), 0},
		{"Namelen", unsafe.Offsetof(lm.Namelen), 8},
		{"Iov", unsafe.Offsetof(lm.Iov), 16},
		{"Iovlen", unsafe.Offsetof(lm.Iovlen), 24},
		{"Control", unsafe.Offsetof(lm.Control), 32},
		{"Controllen", unsafe.Offsetof(lm.Controllen), 40},
		{"Flags", unsafe.Offsetof(lm.Flags), 48},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("linuxMsghdr.%s offset = %d, want %d", c.name, c.got, c.want)
		}
	}

	var am cosmo.AppleMsghdrForTest
	if got := unsafe.Sizeof(am); got != 48 {
		t.Errorf("appleMsghdr size = %d, want 48", got)
	}
	achecks := []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Name", unsafe.Offsetof(am.Name), 0},
		{"Namelen", unsafe.Offsetof(am.Namelen), 8},
		{"Iov", unsafe.Offsetof(am.Iov), 16},
		{"Iovlen", unsafe.Offsetof(am.Iovlen), 24},
		{"Control", unsafe.Offsetof(am.Control), 32},
		{"Controllen", unsafe.Offsetof(am.Controllen), 40},
		{"Flags", unsafe.Offsetof(am.Flags), 44},
	}
	for _, c := range achecks {
		if c.got != c.want {
			t.Errorf("appleMsghdr.%s offset = %d, want %d", c.name, c.got, c.want)
		}
	}
}

func TestCmsgToAppleSingleRights(t *testing.T) {
	src := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(7, 42), false) // CMSG_SPACE(8) = 24
	got, errno := toApple(t, src, 192)
	if errno != 0 {
		t.Fatalf("errno = %d", errno)
	}
	want := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(7, 42), false) // 20 bytes
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestCmsgToAppleMultiRecordAndTightFinal(t *testing.T) {
	// Two rights records; the second is CMSG_LEN-tight (no trailing
	// alignment padding), which the walker must accept.
	src := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(3), false)
	src = linuxCmsg(src, tSOLLinux, tSCMRights, fdBytes(4, 5, 6), true)
	got, errno := toApple(t, src, 192)
	if errno != 0 {
		t.Fatalf("errno = %d", errno)
	}
	want := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(3), false)
	want = appleCmsg(want, tSOLApple, tSCMRights, fdBytes(4, 5, 6), false)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestCmsgToAppleSkipsForeignLevels(t *testing.T) {
	// A non-SOL_SOCKET record between two rights records is silently
	// skipped, exactly like Linux's af_unix send path.
	src := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(1), false)
	src = linuxCmsg(src, 0 /* IPPROTO_IP */, 8 /* IP_PKTINFO */, []byte{1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}, false)
	src = linuxCmsg(src, tSOLLinux, tSCMRights, fdBytes(2), false)
	got, errno := toApple(t, src, 192)
	if errno != 0 {
		t.Fatalf("errno = %d", errno)
	}
	want := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(1), false)
	want = appleCmsg(want, tSOLApple, tSCMRights, fdBytes(2), false)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestCmsgToAppleOddPayloadPads(t *testing.T) {
	// A 6-byte payload is not a real rights payload, but it exercises
	// the Apple 4-alignment: len 18, stored space 20, padding zeroed.
	src := linuxCmsg(nil, tSOLLinux, tSCMRights, []byte{9, 8, 7, 6, 5, 4}, false)
	got, errno := toApple(t, src, 192)
	if errno != 0 {
		t.Fatalf("errno = %d", errno)
	}
	if len(got) != 20 {
		t.Fatalf("dlen = %d, want 20", len(got))
	}
	if l := binary.LittleEndian.Uint32(got[0:]); l != 18 {
		t.Errorf("apple cmsg_len = %d, want 18", l)
	}
	if got[18] != 0 || got[19] != 0 {
		t.Errorf("alignment padding not zeroed: %x", got[16:])
	}
}

func TestCmsgToAppleErrors(t *testing.T) {
	cases := []struct {
		name string
		src  []byte
		cap  int
		want uintptr
	}{
		{"short buffer", make([]byte, 8), 192, tEINVAL},
		{"credentials", linuxCmsg(nil, tSOLLinux, 2 /* SCM_CREDENTIALS */, make([]byte, 12), false), 192, tEOPNOTSUPP},
		{"unknown sol type", linuxCmsg(nil, tSOLLinux, 29, make([]byte, 4), false), 192, tEINVAL},
		{"len under header", func() []byte {
			b := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(1), false)
			binary.LittleEndian.PutUint64(b[0:], 8) // cmsg_len < 16
			return b
		}(), 192, tEINVAL},
		{"len overruns buffer", func() []byte {
			b := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(1), false)
			binary.LittleEndian.PutUint64(b[0:], 64)
			return b
		}(), 192, tEINVAL},
		{"scratch overflow", linuxCmsg(nil, tSOLLinux, tSCMRights, make([]byte, 100), false), 64, tENOBUFS},
	}
	for _, c := range cases {
		if _, errno := toApple(t, c.src, c.cap); errno != c.want {
			t.Errorf("%s: errno = %d, want %d", c.name, errno, c.want)
		}
	}
}

func TestCmsgToLinuxSingleRights(t *testing.T) {
	var applied []int32
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(7, 42), false)
	got, ctrunc := toLinux(t, apple, 64, &applied, nil)
	if ctrunc {
		t.Fatal("unexpected ctrunc")
	}
	want := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(7, 42), false)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if len(applied) != 2 || applied[0] != 7 || applied[1] != 42 {
		t.Fatalf("applied = %v, want [7 42]", applied)
	}
}

func TestCmsgToLinuxMultiRecordExpansion(t *testing.T) {
	// rights(1 fd) + a foreign record (dropped silently: nothing on a
	// macOS host can enable it) + rights(3 fds): the in-place rewrite
	// must compact past the dropped record and expand both kept ones.
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(11), false)
	apple = appleCmsg(apple, 0 /* IPPROTO_IP */, 26, []byte{1, 2, 3, 4, 5, 6, 7, 8}, false)
	apple = appleCmsg(apple, tSOLApple, tSCMRights, fdBytes(21, 22, 23), false)
	var applied []int32
	got, ctrunc := toLinux(t, apple, 128, &applied, nil)
	if ctrunc {
		t.Fatal("unexpected ctrunc")
	}
	want := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(11), false)
	want = linuxCmsg(want, tSOLLinux, tSCMRights, fdBytes(21, 22, 23), false)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if len(applied) != 4 {
		t.Fatalf("applied = %v, want 4 fds", applied)
	}
}

func TestCmsgToLinuxTruncationFdGranularity(t *testing.T) {
	// The NT-session-verified kernel behavior: a 24-byte control buffer
	// receives TWO of three fds (CMSG_SPACE alignment slack carries a
	// whole fd), the third is closed, MSG_CTRUNC raised.
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(5, 6, 7), false) // 24 apple bytes
	var applied, closed []int32
	got, ctrunc := toLinux(t, apple, 24, &applied, &closed)
	if !ctrunc {
		t.Fatal("want ctrunc")
	}
	want := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(5, 6), true) // CMSG_LEN(8)=24, no pad room
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if len(applied) != 2 || applied[0] != 5 || applied[1] != 6 {
		t.Errorf("applied = %v, want [5 6]", applied)
	}
	if len(closed) != 1 || closed[0] != 7 {
		t.Errorf("closed = %v, want [7]", closed)
	}
}

func TestCmsgToLinuxWholeRecordDrop(t *testing.T) {
	// A 16-byte capacity holds Apple's one-fd record (12+4) but not
	// even the bare Linux header plus one fd (20): the record is
	// dropped whole, its fd closed, nothing delivered.
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(8), false)
	var closed []int32
	got, ctrunc := toLinux(t, apple, 16, nil, &closed)
	if !ctrunc {
		t.Fatal("want ctrunc")
	}
	if len(got) != 0 {
		t.Fatalf("llen = %d, want 0", len(got))
	}
	if len(closed) != 1 || closed[0] != 8 {
		t.Fatalf("closed = %v, want [8]", closed)
	}
}

func TestCmsgToLinuxSecondRecordDropped(t *testing.T) {
	// Both Apple records (16 + 20 = 36 bytes) fit the 40-byte buffer,
	// but after the first record's Linux expansion consumes 24 bytes,
	// the 16 remaining cannot hold the second record's Linux shape
	// (24): delivered fds cloexec'd, dropped fds closed, ctrunc.
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(1), false)
	apple = appleCmsg(apple, tSOLApple, tSCMRights, fdBytes(2, 3), false)
	var applied, closed []int32
	got, ctrunc := toLinux(t, apple, 40, &applied, &closed)
	if !ctrunc {
		t.Fatal("want ctrunc")
	}
	want := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(1), false)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
	if len(applied) != 1 || applied[0] != 1 {
		t.Errorf("applied = %v, want [1]", applied)
	}
	if len(closed) != 2 || closed[0] != 2 || closed[1] != 3 {
		t.Errorf("closed = %v, want [2 3]", closed)
	}
}

func TestCmsgToLinuxTightFit(t *testing.T) {
	// Capacity exactly CMSG_LEN(4) = 20: the record fits without its
	// alignment padding (the kernel's tight-fit rule), no truncation.
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(77), false)
	got, ctrunc := toLinux(t, apple, 20, nil, nil)
	if ctrunc {
		t.Fatal("unexpected ctrunc")
	}
	want := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(77), true)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestCmsgToLinuxMalformedTail(t *testing.T) {
	// A valid record followed by a garbage header: the walker stops at
	// the malformed record and delivers what precedes it.
	apple := appleCmsg(nil, tSOLApple, tSCMRights, fdBytes(4), false)
	apple = append(apple, 3, 0, 0, 0, 1, 2, 3, 4, 5, 6, 7, 8) // cmsg_len 3 < header
	got, ctrunc := toLinux(t, apple, 64, nil, nil)
	if ctrunc {
		t.Fatal("unexpected ctrunc")
	}
	want := linuxCmsg(nil, tSOLLinux, tSCMRights, fdBytes(4), false)
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x, want %x", got, want)
	}
}

func TestCmsgToLinuxEmpty(t *testing.T) {
	got, ctrunc := toLinux(t, nil, 64, nil, nil)
	if len(got) != 0 || ctrunc {
		t.Fatalf("got llen=%d ctrunc=%v, want 0,false", len(got), ctrunc)
	}
}

func TestXlatMsgFlags(t *testing.T) {
	cases := []struct {
		apple, linux int32
	}{
		{0, 0},
		{0x1, 0x1},    // MSG_OOB
		{0x8, 0x80},   // Apple MSG_EOR -> Linux MSG_EOR
		{0x10, 0x20},  // Apple MSG_TRUNC -> Linux MSG_TRUNC
		{0x20, 0x8},   // Apple MSG_CTRUNC -> Linux MSG_CTRUNC
		{0x39, 0xa9},  // all four together
		{0x4000, 0x0}, // Apple-only bits (e.g. MSG_DONTWAIT) dropped
	}
	for _, c := range cases {
		if got := cosmo.XlatMsgFlags(c.apple); got != c.linux {
			t.Errorf("xlat(%#x) = %#x, want %#x", c.apple, got, c.linux)
		}
	}
}
