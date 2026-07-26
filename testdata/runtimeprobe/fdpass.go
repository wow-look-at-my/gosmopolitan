// SCM_RIGHTS fd-passing check (fdpass): a parent/child pair over a
// pathname AF_UNIX stream socket, passing a FILE fd and a SOCKET fd
// plus inline data in one sendmsg. Native on Linux hosts - which is
// also what validates the probe's own logic, since the emulations
// must match those semantics unchanged - emulated via the NT wire
// frame (sender-push WSADuplicateSocketW / DuplicateHandle, wave 3
// item 2b) on Windows hosts, and carried by Apple's native AF_UNIX
// SCM_RIGHTS through the darwin msghdr/cmsghdr layout translation
// (2026-07-21) on macOS hosts. Mandatory on ALL THREE hosts - no
// skip legs: a failure anywhere is a FAIL.
//
// Choreography (parent = the probe, child = the probe re-executed
// with RUNTIMEPROBE_CHILD=fdpass):
//
//	parent: unix listener; temp file with known content (reopened
//	        O_RDONLY); a TCP loopback listen+dial+accept triple, the
//	        accepted conn dup'd out via File() (socket-kind dup) -
//	        the socket payload; spawn child; accept; sendmsg(inline
//	        data + rights{file fd, accepted-TCP fd})
//	child:  dial; recvmsg (control sized for both fds); read the
//	        file THROUGH the passed fd; write a payload INTO the
//	        passed socket fd; echo "<file content>|<inline data>"
//	        back over the unix conn; exit 0
//	parent: assert the TCP payload arrives on the dial side, the
//	        echo matches, and the wait status is clean. Everything
//	        the child imports stays open in the parent until the
//	        echo arrives - the frame model duplicates at send time,
//	        but the probe deliberately sequences import-before-close
//	        (the WSAPROTOCOL_INFOW residual, see os_cosmo_nt_msg.go).
package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	fdpassSockEnv     = "RUNTIMEPROBE_FDPASS_SOCK"
	fdpassInline      = "fdpass-inline-data"
	fdpassFileContent = "fdpass-file-content\n"
	fdpassSockPayload = "fdpass-sock-payload"
)

func checkFdpass() {
	// Unix listener at a fresh pathname.
	dir, err := os.MkdirTemp("", "runtimeprobe-fdpass")
	if err != nil {
		fail("fdpass", "MkdirTemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	spath := filepath.Join(dir, "fdpass.sock")
	l, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		fail("fdpass", "listener socket: %v", err)
		return
	}
	defer syscall.Close(l)
	if err := syscall.Bind(l, &syscall.SockaddrUnix{Name: spath}); err != nil {
		fail("fdpass", "bind %s: %v", spath, err)
		return
	}
	if err := syscall.Listen(l, 1); err != nil {
		fail("fdpass", "listen: %v", err)
		return
	}
	if err := syscall.SetNonblock(l, true); err != nil {
		fail("fdpass", "listener nonblock: %v", err)
		return
	}

	// The FILE payload: known content, reopened O_RDONLY so the
	// shared offset starts at 0.
	fpath := filepath.Join(dir, "payload.txt")
	if err := os.WriteFile(fpath, []byte(fdpassFileContent), 0o644); err != nil {
		fail("fdpass", "WriteFile: %v", err)
		return
	}
	fileFd, err := syscall.Open(fpath, syscall.O_RDONLY, 0)
	if err != nil {
		fail("fdpass", "open payload: %v", err)
		return
	}
	defer syscall.Close(fileFd)

	// The SOCKET payload: a TCP loopback triple. The accepted side's
	// fd crosses to the child (File() dups - the item-1 socket-kind
	// dup emulation - and flips the description to blocking, which is
	// fine both sides); the child writes into it and the parent reads
	// the bytes back out of the DIAL side, proving the passed fd is
	// the same underlying socket.
	tln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("fdpass", "tcp listen: %v", err)
		return
	}
	defer tln.Close()
	tdial, err := net.Dial("tcp", tln.Addr().String())
	if err != nil {
		fail("fdpass", "tcp dial: %v", err)
		return
	}
	defer tdial.Close()
	tacc, err := tln.Accept()
	if err != nil {
		fail("fdpass", "tcp accept: %v", err)
		return
	}
	defer tacc.Close()
	taccF, err := tacc.(*net.TCPConn).File()
	if err != nil {
		fail("fdpass", "accepted File(): %v", err)
		return
	}
	defer taccF.Close()

	// Spawn the child before blocking in accept.
	cmd, direct, bad := selfCommand("fdpass", "fdpass")
	if bad {
		return
	}
	cmd.Env = append(cmd.Env, fdpassSockEnv+"="+spath)
	var childOut, childErr strings.Builder
	cmd.Stdout = &childOut
	cmd.Stderr = &childErr
	if err := cmd.Start(); err != nil {
		fail("fdpass", "start self (direct=%v): %v", direct, err)
		return
	}
	childFail := func(f string, args ...any) {
		detail := fmt.Sprintf(f, args...)
		if childErr.Len() > 0 {
			detail += fmt.Sprintf(" (child stderr: %q)", childErr.String())
		}
		cmd.Process.Kill()
		cmd.Wait()
		fail("fdpass", "%s", detail)
	}

	// Accept the child's dial (nonblocking poll with a deadline, the
	// unixSyscallPair recipe).
	a := -1
	deadline := time.Now().Add(15 * time.Second)
	for {
		nfd, _, err := syscall.Accept(l)
		if err == nil {
			a = nfd
			break
		}
		if err != syscall.EAGAIN && err != syscall.EWOULDBLOCK {
			childFail("accept: %v", err)
			return
		}
		if time.Now().After(deadline) {
			childFail("accept: no connection within 15s")
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer func() {
		// a is handed to an os.File below (which then owns the close
		// and sets a = -1); this covers the error paths before that.
		if a >= 0 {
			syscall.Close(a)
		}
	}()

	// THE payload: one sendmsg carrying inline data plus both fds
	// (order: file first, socket second - the child indexes on it).
	rights := syscall.UnixRights(fileFd, int(taccF.Fd()))
	if err := syscall.Sendmsg(a, []byte(fdpassInline), rights, nil, 0); err != nil {
		childFail("sendmsg rights: %v", err)
		return
	}

	// The child's echo confirms every import happened; only then may
	// the parent-side copies close (the deferred closes above).
	af := os.NewFile(uintptr(a), "fdpass-unix")
	a = -1 // af owns the fd now; FileConn below works on a dup
	ac, err := net.FileConn(af)
	af.Close()
	if err != nil {
		childFail("FileConn(accepted): %v", err)
		return
	}
	defer ac.Close()
	ac.SetReadDeadline(time.Now().Add(20 * time.Second))
	echo, err := io.ReadAll(ac)
	if err != nil {
		childFail("read echo: %v", err)
		return
	}
	wantEcho := fdpassFileContent + "|" + fdpassInline
	if string(echo) != wantEcho {
		childFail("echo = %q, want %q", echo, wantEcho)
		return
	}

	// The socket payload must come out of the dial side.
	tdial.SetReadDeadline(time.Now().Add(20 * time.Second))
	sockBuf := make([]byte, 64)
	sn, err := io.ReadAtLeast(tdial, sockBuf, len(fdpassSockPayload))
	if err != nil {
		childFail("read tcp payload: %v", err)
		return
	}
	if string(sockBuf[:sn]) != fdpassSockPayload {
		childFail("tcp payload = %q, want %q", sockBuf[:sn], fdpassSockPayload)
		return
	}

	err2, completed := waitBounded("fdpass", cmd)
	if !completed {
		return
	}
	if err2 != nil {
		fail("fdpass", "child exit: %v (stderr: %q)", err2, childErr.String())
		return
	}
	ok("fdpass")
}

// fdpassChild is the RUNTIMEPROBE_CHILD=fdpass mode: dial, recvmsg
// the rights, exercise both passed fds, echo, exit. Errors go to
// stderr with a nonzero exit; the parent folds them into its FAIL
// line.
func fdpassChild() {
	die := func(f string, args ...any) {
		fmt.Fprintf(os.Stderr, "fdpass child: %s\n", fmt.Sprintf(f, args...))
		os.Exit(3)
	}
	spath := os.Getenv(fdpassSockEnv)
	if spath == "" {
		die("no %s", fdpassSockEnv)
	}
	c, err := syscall.Socket(syscall.AF_UNIX, syscall.SOCK_STREAM, 0)
	if err != nil {
		die("socket: %v", err)
	}
	if err := syscall.Connect(c, &syscall.SockaddrUnix{Name: spath}); err != nil {
		die("connect %s: %v", spath, err)
	}

	// Control buffer sized for the two expected fds (with slack).
	buf := make([]byte, 256)
	oob := make([]byte, 128)
	n, oobn, recvflags, _, err := syscall.Recvmsg(c, buf, oob, 0)
	if err != nil {
		die("recvmsg: %v", err)
	}
	if recvflags != 0 {
		die("recvmsg flags = %#x, want 0 (no truncation)", recvflags)
	}
	msgs, err := syscall.ParseSocketControlMessage(oob[:oobn])
	if err != nil {
		die("parse control (oobn=%d): %v", oobn, err)
	}
	if len(msgs) != 1 {
		die("got %d control messages, want 1", len(msgs))
	}
	fds, err := syscall.ParseUnixRights(&msgs[0])
	if err != nil {
		die("ParseUnixRights: %v", err)
	}
	if len(fds) != 2 {
		die("got %d rights fds, want 2", len(fds))
	}

	// Passed fd 0: the file. Read its full content through it.
	pf := os.NewFile(uintptr(fds[0]), "passed-file")
	content, err := io.ReadAll(pf)
	pf.Close()
	if err != nil {
		die("read passed file: %v", err)
	}

	// Passed fd 1: the socket. Write the payload into it, then close
	// so the parent's dial-side read can also see EOF.
	payload := []byte(fdpassSockPayload)
	for off := 0; off < len(payload); {
		wn, err := syscall.Write(fds[1], payload[off:])
		if err != nil {
			die("write passed socket: %v", err)
		}
		off += wn
	}
	syscall.Close(fds[1])

	// Echo file content + inline data back, then close (EOF ends the
	// parent's ReadAll).
	echo := append(content, '|')
	echo = append(echo, buf[:n]...)
	for off := 0; off < len(echo); {
		wn, err := syscall.Write(c, echo[off:])
		if err != nil {
			die("write echo: %v", err)
		}
		off += wn
	}
	syscall.Close(c)
}
