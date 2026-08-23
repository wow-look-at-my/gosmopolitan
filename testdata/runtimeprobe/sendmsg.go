// Sendmsg/recvmsg checks: the syscall-level scatter-gather message
// surface - native on Linux hosts, emulated over WSASend/WSARecv on
// Windows hosts since NT wave 3 item 2, and emulated over dlsym'd
// Apple libc sendmsg/recvmsg on macOS hosts since the darwin msghdr
// translation (2026-07-21). Mandatory on ALL THREE hosts - no skip
// legs: a sendmsg failure anywhere is a FAIL. The raw two-iovec leg
// exercises exactly the msghdr shape the darwin fixed-size adapter
// passes through (nil name, nil control, coinciding iovec layouts).

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"time"
	"unsafe"
)

// probeHostIsNT reports whether the probe is executing on a Windows
// host - the same test selfCommand uses (the OS env var is set by
// every Windows since NT; unix hosts lack it).
func probeHostIsNT() bool { return os.Getenv("OS") == "Windows_NT" }

// probeHostIsLinux: only Linux has /proc/version.
func probeHostIsLinux() bool {
	_, err := os.Stat("/proc/version")
	return err == nil
}

// remainingIovecs builds an iovec array covering everything at and
// after byte offset off in the concatenation of parts (at most two
// parts - all this file's transfers use two). Short reads/writes
// rebuild the array from the new offset and continue.
func remainingIovecs(off int, parts ...[]byte) ([2]syscall.Iovec, int) {
	var iovs [2]syscall.Iovec
	n := 0
	for _, p := range parts {
		if off >= len(p) {
			off -= len(p)
			continue
		}
		rest := p[off:]
		iovs[n].Base = &rest[0]
		iovs[n].SetLen(len(rest))
		n++
		off = 0
	}
	return iovs, n
}

// unixSyscallPair builds a connected pathname AF_UNIX stream pair at
// the raw-fd level - the unixsock listener+dial recipe without the
// net package, since the checks below issue raw sendmsg/recvmsg on
// the fds. The dial runs in a goroutine (whether a blocking connect
// completes on backlog-queue or only at accept varies by host), and
// the accept side polls a nonblocking listener with a deadline so no
// error path can deadlock the probe.
func unixSyscallPair(name string) (a, c int, cleanup func(), errDetail string) {
	dir, err := os.MkdirTemp("", "runtimeprobe-msg")
	if err != nil {
		return 0, 0, nil, fmt.Sprintf("MkdirTemp: %v", err)
	}
	rmdir := func() { os.RemoveAll(dir) }
	spath := filepath.Join(dir, name+".sock")

	l, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		rmdir()
		return 0, 0, nil, fmt.Sprintf("listener socket: %v", err)
	}
	if err := syscall.Bind(l, &syscall.SockaddrUnix{Name: spath}); err != nil {
		syscall.Close(l)
		rmdir()
		return 0, 0, nil, fmt.Sprintf("bind %s: %v", spath, err)
	}
	if err := syscall.Listen(l, 1); err != nil {
		syscall.Close(l)
		rmdir()
		return 0, 0, nil, fmt.Sprintf("listen: %v", err)
	}
	if err := syscall.SetNonblock(l, true); err != nil {
		syscall.Close(l)
		rmdir()
		return 0, 0, nil, fmt.Sprintf("listener nonblock: %v", err)
	}

	type dialRes struct {
		fd  int
		err error
	}
	dc := make(chan dialRes, 1)
	go func() {
		fd, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
		if err == nil {
			if err = syscall.Connect(fd, &syscall.SockaddrUnix{Name: spath}); err != nil {
				syscall.Close(fd)
				fd = -1
			}
		}
		dc <- dialRes{fd, err}
	}()

	a = -1
	deadline := time.Now().Add(5 * time.Second)
	for {
		nfd, _, err := syscall.Accept(l)
		if err == nil {
			a = nfd
			break
		}
		if err != syscall.EAGAIN && err != syscall.EWOULDBLOCK {
			syscall.Close(l)
			rmdir()
			d := <-dc
			if d.fd >= 0 {
				syscall.Close(d.fd)
			}
			return 0, 0, nil, fmt.Sprintf("accept: %v (dial: %v)", err, d.err)
		}
		if time.Now().After(deadline) {
			syscall.Close(l)
			rmdir()
			return 0, 0, nil, "accept: no connection within 5s"
		}
		time.Sleep(5 * time.Millisecond)
	}
	syscall.Close(l)
	d := <-dc
	if d.err != nil {
		syscall.Close(a)
		rmdir()
		return 0, 0, nil, fmt.Sprintf("dial: %v", d.err)
	}
	cleanup = func() {
		syscall.Close(a)
		syscall.Close(d.fd)
		rmdir()
	}
	return a, d.fd, cleanup, ""
}

// checkSendmsg drives sendmsg/recvmsg over an in-process pathname
// AF_UNIX pair, in two legs:
//
//  1. the public API (syscall.Sendmsg -> syscall.Recvmsg, single
//     buffer): the receive supplies an oob buffer that must come back
//     EMPTY - no ancillary data was sent, so oobn = 0 and recvflags =
//     0 on every host;
//  2. raw two-iovec msghdrs both directions via syscall.Syscall
//     (nothing in std issues multi-iovec sendmsg, and this is exactly
//     the WSABUF scatter-gather translation on NT), with deliberately
//     misaligned iovec splits on the receive side and a short-read
//     loop that rebuilds the iovecs at the current offset.
func checkSendmsg() {
	a, c, cleanup, detail := unixSyscallPair("sendmsg")
	if detail != "" {
		fail("sendmsg", "%s", detail)
		return
	}
	defer cleanup()

	// Leg 1: public API, client -> accepted end.
	const pubMsg = "sendmsg-public-leg"
	if err := syscall.Sendmsg(c, []byte(pubMsg), nil, nil, 0); err != nil {
		fail("sendmsg", "public Sendmsg: %v", err)
		return
	}
	buf := make([]byte, 64)
	oob := make([]byte, 64)
	got, oobTotal := 0, 0
	for got < len(pubMsg) {
		n, oobn, recvflags, _, err := syscall.Recvmsg(a, buf[got:], oob, 0)
		if err != nil {
			fail("sendmsg", "public Recvmsg: %v", err)
			return
		}
		if n == 0 {
			fail("sendmsg", "public Recvmsg: unexpected EOF after %d bytes", got)
			return
		}
		if recvflags != 0 {
			fail("sendmsg", "public Recvmsg: recvflags = %#x, want 0", recvflags)
			return
		}
		got += n
		oobTotal += oobn
	}
	if string(buf[:got]) != pubMsg {
		fail("sendmsg", "public leg: got %q, want %q", buf[:got], pubMsg)
		return
	}
	if oobTotal != 0 {
		fail("sendmsg", "public leg: oobn = %d, want 0 (no ancillary was sent)", oobTotal)
		return
	}

	// Leg 2: raw two-iovec msghdrs, accepted end -> client.
	partA := []byte("iov-part-A|")
	partB := []byte("iov-part-B")
	want := len(partA) + len(partB)
	siovs, scnt := remainingIovecs(0, partA, partB)
	var smsg syscall.Msghdr
	smsg.Iov = &siovs[0]
	smsg.Iovlen = uint64(scnt)
	r, _, errno := syscall.Syscall(syscall.SYS_SENDMSG, uintptr(a), uintptr(unsafe.Pointer(&smsg)), 0)
	if errno != 0 {
		fail("sendmsg", "raw 2-iovec sendmsg: %v", errno)
		return
	}
	if int(r) != want {
		fail("sendmsg", "raw 2-iovec sendmsg: sent %d, want %d", r, want)
		return
	}
	// Receive into two slices whose split (7) is misaligned with the
	// send split (11), so bytes provably scatter across both iovecs.
	dst1 := make([]byte, 7)
	dst2 := make([]byte, want-7)
	got = 0
	for got < want {
		riovs, rcnt := remainingIovecs(got, dst1, dst2)
		var rmsg syscall.Msghdr
		rmsg.Iov = &riovs[0]
		rmsg.Iovlen = uint64(rcnt)
		r, _, errno := syscall.Syscall(syscall.SYS_RECVMSG, uintptr(c), uintptr(unsafe.Pointer(&rmsg)), 0)
		if errno != 0 {
			fail("sendmsg", "raw 2-iovec recvmsg: %v", errno)
			return
		}
		if r == 0 {
			fail("sendmsg", "raw 2-iovec recvmsg: unexpected EOF after %d bytes", got)
			return
		}
		got += int(r)
	}
	if all := string(dst1) + string(dst2); all != string(partA)+string(partB) {
		fail("sendmsg", "raw 2-iovec leg: got %q, want %q", all, string(partA)+string(partB))
		return
	}
	ok("sendmsg")
}
