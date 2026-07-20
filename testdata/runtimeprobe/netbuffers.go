// Readv/writev checks: raw two-iovec transfers plus net.Buffers'
// consolidated-write path (internal/poll.Writev -> SYS_WRITEV).
// Unlike sendmsg, EVERY host has this surface - Linux natively,
// macOS through the darwin dispatcher's readv/writev cases, Windows
// through the NT WSABUF emulation since wave 3 item 2 - so there are
// NO skip legs; this check is mandatory everywhere.

package main

import (
	"io"
	"net"
	"os"
	"syscall"
	"time"
	"unsafe"
)

func checkNetBuffers() {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fail("netbuffers", "socketpair: %v", err)
		return
	}
	bail := func(f string, args ...any) {
		fail("netbuffers", f, args...)
		syscall.Close(fds[0])
		syscall.Close(fds[1])
	}

	// Phase 1: raw writev/readv with two iovecs each way, misaligned
	// splits (13 vs 9), short-read loop on the receive side.
	wA := []byte("writev-first|")
	wB := []byte("writev-second")
	want := len(wA) + len(wB)
	wiovs, wcnt := remainingIovecs(0, wA, wB)
	r, _, errno := syscall.Syscall(syscall.SYS_WRITEV, uintptr(fds[0]),
		uintptr(unsafe.Pointer(&wiovs[0])), uintptr(wcnt))
	if errno != 0 {
		bail("raw writev: %v", errno)
		return
	}
	if int(r) != want {
		bail("raw writev: wrote %d, want %d", r, want)
		return
	}
	rd1 := make([]byte, 9)
	rd2 := make([]byte, want-9)
	got := 0
	for got < want {
		riovs, rcnt := remainingIovecs(got, rd1, rd2)
		r, _, errno := syscall.Syscall(syscall.SYS_READV, uintptr(fds[1]),
			uintptr(unsafe.Pointer(&riovs[0])), uintptr(rcnt))
		if errno != 0 {
			bail("raw readv: %v", errno)
			return
		}
		if r == 0 {
			bail("raw readv: unexpected EOF after %d bytes", got)
			return
		}
		got += int(r)
	}
	if all := string(rd1) + string(rd2); all != string(wA)+string(wB) {
		bail("raw readv: got %q, want %q", all, string(wA)+string(wB))
		return
	}

	// Phase 2: net.Buffers consolidated write through net.FileConn -
	// the whole internal/poll.Writev stack down to SYS_WRITEV. From
	// here the fds are owned by the *os.Files.
	f0 := os.NewFile(uintptr(fds[0]), "netbuffers0")
	f1 := os.NewFile(uintptr(fds[1]), "netbuffers1")
	defer f0.Close()
	defer f1.Close()
	c0, err := net.FileConn(f0)
	if err != nil {
		fail("netbuffers", "FileConn(end 0): %v", err)
		return
	}
	defer c0.Close()
	c1, err := net.FileConn(f1)
	if err != nil {
		fail("netbuffers", "FileConn(end 1): %v", err)
		return
	}
	defer c1.Close()
	c0.SetDeadline(time.Now().Add(10 * time.Second))
	c1.SetDeadline(time.Now().Add(10 * time.Second))

	const wantS = "net-buffers-consolidated"
	type readResult struct {
		s   string
		err error
	}
	rc := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := io.ReadAtLeast(c1, buf, len(wantS))
		rc <- readResult{string(buf[:n]), err}
	}()
	bufs := net.Buffers{[]byte("net-"), []byte("buffers-"), []byte("consolidated")}
	n, err := bufs.WriteTo(c0)
	if err != nil || n != int64(len(wantS)) {
		fail("netbuffers", "Buffers.WriteTo: n=%d err=%v, want %d", n, err, len(wantS))
		return
	}
	select {
	case res := <-rc:
		if res.err != nil || res.s != wantS {
			fail("netbuffers", "read side: got %q err %v, want %q", res.s, res.err, wantS)
			return
		}
	case <-time.After(15 * time.Second):
		fail("netbuffers", "read side not done within 15s")
		return
	}
	ok("netbuffers")
}
