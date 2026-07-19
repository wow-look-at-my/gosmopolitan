// Socketpair checks: the syscall.Socketpair surface (native on Linux
// hosts, emulated over Apple's socketpair(2) on macOS hosts and over
// a connected loopback TCP pair dressed as AF_UNIX on Windows hosts)
// and the os.NewFile + net.FileConn integration that gives pair fds
// netpoller readiness and deadlines. Mandatory on every host - no
// skip legs.

package main

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"
	"time"
)

func checkSockpair() {
	checkSockpairRaw()
	checkSockpairPoll()
}

// checkSockpairRaw covers the raw syscall surface: create the pair,
// move bytes in both directions with plain write/read on the fds, and
// require both ends to report the Linux socketpair identity - an
// UNNAMED AF_UNIX sockaddr (empty Name) - from getsockname. On
// Windows the names are synthesized (the backing sockets are loopback
// TCP); a leak of the real 127.0.0.1 name fails the type assertion
// here.
func checkSockpairRaw() {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fail("socketpair", "%v", err)
		return
	}
	defer syscall.Close(fds[0])
	defer syscall.Close(fds[1])

	xfer := func(from, to int, msg string) string {
		if n, err := syscall.Write(from, []byte(msg)); err != nil || n != len(msg) {
			return fmt.Sprintf("write fd[%d]: n=%d err=%v", from, n, err)
		}
		buf := make([]byte, 64)
		got := 0
		for got < len(msg) {
			n, err := syscall.Read(to, buf[got:])
			if err != nil {
				return fmt.Sprintf("read fd[%d]: %v", to, err)
			}
			if n == 0 {
				return fmt.Sprintf("read fd[%d]: unexpected EOF after %d bytes", to, got)
			}
			got += n
		}
		if string(buf[:got]) != msg {
			return fmt.Sprintf("fd[%d]->fd[%d]: got %q, want %q", from, to, buf[:got], msg)
		}
		return ""
	}
	if d := xfer(fds[0], fds[1], "pair-ping-0to1"); d != "" {
		fail("socketpair", "%s", d)
		return
	}
	if d := xfer(fds[1], fds[0], "pair-ping-1to0"); d != "" {
		fail("socketpair", "%s", d)
		return
	}

	for i, fd := range fds {
		sa, err := syscall.Getsockname(fd)
		if err != nil {
			fail("socketpair", "getsockname fd[%d]: %v", i, err)
			return
		}
		ua, isUnix := sa.(*syscall.SockaddrUnix)
		if !isUnix || ua.Name != "" {
			fail("socketpair", "getsockname fd[%d] = %#v (%T), want unnamed *SockaddrUnix", i, sa, sa)
			return
		}
	}
	ok("socketpair")
}

// checkSockpairPoll wraps both ends of a fresh pair with os.NewFile +
// net.FileConn and drives them as ordinary net.Conns: the conns must
// identify as unnamed unix-domain endpoints, a reader parked BEFORE
// the write must be woken by netpoller readiness (EAGAIN -> wait ->
// wake, not a buffered fast path), the reverse direction must echo,
// and a read deadline already in the past must surface as a timeout -
// proving netpollopen registration, readiness and the pollDesc
// deadline machinery on pair fds.
func checkSockpairPoll() {
	fds, err := syscall.Socketpair(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fail("sockpairpoll", "socketpair: %v", err)
		return
	}
	// Diagnostic detail, never a verdict: net.FileConn prefers fcntl
	// F_DUPFD_CLOEXEC and falls back to plain dup(2) when that errors
	// with EINVAL/ENOSYS. On the macOS CI runner the fast path DID
	// error (2026-07-19, wave-3 item-1 followup) while dup(2) was
	// still undispatched there, killing FileConn outright; dup is
	// emulated now, and this detail names the fast path's errno on
	// every host so the fallback's cause stays attributable in CI
	// logs.
	dupNote := "dupcloexec=ok"
	if r, _, errno := syscall.Syscall(syscall.SYS_FCNTL, uintptr(fds[0]), syscall.F_DUPFD_CLOEXEC, 0); errno != 0 {
		dupNote = fmt.Sprintf("dupcloexec=%v", errno)
	} else {
		syscall.Close(int(r))
	}
	f0 := os.NewFile(uintptr(fds[0]), "sockpair0")
	f1 := os.NewFile(uintptr(fds[1]), "sockpair1")
	defer f0.Close()
	defer f1.Close()

	c0, err := net.FileConn(f0)
	if err != nil {
		fail("sockpairpoll", "FileConn(end 0): %v", err)
		return
	}
	defer c0.Close()
	c1, err := net.FileConn(f1)
	if err != nil {
		fail("sockpairpoll", "FileConn(end 1): %v", err)
		return
	}
	defer c1.Close()

	la, isUnix := c0.LocalAddr().(*net.UnixAddr)
	if !isUnix || la.Name != "" {
		fail("sockpairpoll", "LocalAddr = %#v (%T), want unnamed *net.UnixAddr", c0.LocalAddr(), c0.LocalAddr())
		return
	}

	c0.SetDeadline(time.Now().Add(10 * time.Second))
	c1.SetDeadline(time.Now().Add(10 * time.Second))

	// Park the reader FIRST, write after a delay: the read must go
	// through an EAGAIN -> netpoller wait -> readiness wake round
	// trip (write-then-read could be satisfied straight from the
	// socket buffer without ever consulting the poller).
	const msg0 = "sockpair-poll-ping"
	type readResult struct {
		s   string
		err error
	}
	rc := make(chan readResult, 1)
	go func() {
		buf := make([]byte, 64)
		n, err := io.ReadAtLeast(c1, buf, len(msg0))
		rc <- readResult{string(buf[:n]), err}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, err := c0.Write([]byte(msg0)); err != nil {
		fail("sockpairpoll", "write end 0: %v", err)
		return
	}
	select {
	case r := <-rc:
		if r.err != nil || r.s != msg0 {
			fail("sockpairpoll", "parked read: got %q err %v, want %q", r.s, r.err, msg0)
			return
		}
	case <-time.After(5 * time.Second):
		fail("sockpairpoll", "parked read not woken within 5s")
		return
	}

	// Reverse direction.
	const msg1 = "sockpair-poll-pong"
	if _, err := c1.Write([]byte(msg1)); err != nil {
		fail("sockpairpoll", "write end 1: %v", err)
		return
	}
	buf := make([]byte, 64)
	n, err := io.ReadAtLeast(c0, buf, len(msg1))
	if err != nil || string(buf[:n]) != msg1 {
		fail("sockpairpoll", "read end 0: got %q err %v, want %q", buf[:n], err, msg1)
		return
	}

	// A read deadline already in the past must surface as a timeout.
	c0.SetReadDeadline(time.Now().Add(-time.Second))
	_, err = c0.Read(buf)
	var ne net.Error
	switch {
	case err == nil:
		fail("sockpairpoll", "read succeeded despite past deadline")
	case !errors.As(err, &ne) || !ne.Timeout():
		fail("sockpairpoll", "read error %v, want timeout", err)
	default:
		ok("sockpairpoll", dupNote)
	}
}
