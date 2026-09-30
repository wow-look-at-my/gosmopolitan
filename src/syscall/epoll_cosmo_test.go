// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package syscall_test

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"
	"unsafe"
)

// These run against the kernel's epoll on a Linux host and against this
// package's emulation on a macOS one, so both answer to the same
// expectations.

func epollPipe(t *testing.T) (r, w int) {
	t.Helper()
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		syscall.Close(p[0])
		syscall.Close(p[1])
	})
	return p[0], p[1]
}

func epollNew(t *testing.T) int {
	t.Helper()
	epfd, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		t.Fatalf("EpollCreate1: %v", err)
	}
	t.Cleanup(func() { syscall.Close(epfd) })
	return epfd
}

func epollAdd(t *testing.T, epfd, fd int, events uint32) {
	t.Helper()
	ev := syscall.EpollEvent{Events: events, Fd: int32(fd)}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, fd, &ev); err != nil {
		t.Fatalf("EpollCtl ADD %d: %v", fd, err)
	}
}

func epollWaitOK(t *testing.T, epfd int, msec int) []syscall.EpollEvent {
	t.Helper()
	events := make([]syscall.EpollEvent, 8)
	for {
		n, err := syscall.EpollWait(epfd, events, msec)
		if err == syscall.EINTR {
			continue
		}
		if err != nil {
			t.Fatalf("EpollWait: %v", err)
		}
		return events[:n]
	}
}

func TestEpollReportsAReadableDescriptor(t *testing.T) {
	epfd := epollNew(t)
	r, w := epollPipe(t)
	epollAdd(t, epfd, r, syscall.EPOLLIN)

	if got := epollWaitOK(t, epfd, 0); len(got) != 0 {
		t.Fatalf("an empty pipe reported %+v", got)
	}
	if _, err := syscall.Write(w, []byte{1}); err != nil {
		t.Fatal(err)
	}
	got := epollWaitOK(t, epfd, 1000)
	if len(got) != 1 || got[0].Fd != int32(r) || got[0].Events&syscall.EPOLLIN == 0 {
		t.Fatalf("a pipe holding a byte reported %+v, want EPOLLIN for fd %d", got, r)
	}
	// Level-triggered: still readable, so reported again.
	if got := epollWaitOK(t, epfd, 0); len(got) != 1 {
		t.Fatalf("a pipe still holding a byte reported %+v on the second wait", got)
	}
}

func TestEpollCtlErrors(t *testing.T) {
	epfd := epollNew(t)
	r, _ := epollPipe(t)
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(r)}

	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_MOD, r, &ev); err != syscall.ENOENT {
		t.Errorf("MOD of an unregistered fd = %v, want ENOENT", err)
	}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_DEL, r, &ev); err != syscall.ENOENT {
		t.Errorf("DEL of an unregistered fd = %v, want ENOENT", err)
	}
	epollAdd(t, epfd, r, syscall.EPOLLIN)
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, r, &ev); err != syscall.EEXIST {
		t.Errorf("a second ADD = %v, want EEXIST", err)
	}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_DEL, r, &ev); err != nil {
		t.Errorf("DEL of a registered fd = %v", err)
	}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, epfd, &ev); err != syscall.EINVAL {
		t.Errorf("adding the instance to itself = %v, want EINVAL", err)
	}
	if err := syscall.EpollCtl(r, syscall.EPOLL_CTL_ADD, r, &ev); err != syscall.EINVAL {
		t.Errorf("EpollCtl on a pipe = %v, want EINVAL", err)
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "regular"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_ADD, int(f.Fd()), &ev); err != syscall.EPERM {
		t.Errorf("adding a regular file = %v, want EPERM", err)
	}
}

func TestEpollOneshotFiresOnceUntilRearmed(t *testing.T) {
	epfd := epollNew(t)
	r, w := epollPipe(t)
	epollAdd(t, epfd, r, syscall.EPOLLIN|syscall.EPOLLONESHOT)
	syscall.Write(w, []byte{1})

	if got := epollWaitOK(t, epfd, 1000); len(got) != 1 {
		t.Fatalf("first wait reported %+v, want one event", got)
	}
	if got := epollWaitOK(t, epfd, 0); len(got) != 0 {
		t.Fatalf("a fired oneshot reported %+v again", got)
	}
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN | syscall.EPOLLONESHOT, Fd: int32(r)}
	if err := syscall.EpollCtl(epfd, syscall.EPOLL_CTL_MOD, r, &ev); err != nil {
		t.Fatal(err)
	}
	if got := epollWaitOK(t, epfd, 1000); len(got) != 1 {
		t.Fatalf("a rearmed oneshot reported %+v, want one event", got)
	}
}

// An interest added while another thread waits reaches that wait.
func TestEpollWaitSeesAnAddMadeWhileWaiting(t *testing.T) {
	epfd := epollNew(t)
	r, w := epollPipe(t)
	syscall.Write(w, []byte{1})

	type result struct {
		events []syscall.EpollEvent
		err    error
	}
	done := make(chan result, 1)
	go func() {
		events := make([]syscall.EpollEvent, 8)
		for {
			n, err := syscall.EpollWait(epfd, events, 10000)
			if err == syscall.EINTR {
				continue
			}
			if n < 0 {
				n = 0
			}
			done <- result{events[:n], err}
			return
		}
	}()
	time.Sleep(50 * time.Millisecond)
	epollAdd(t, epfd, r, syscall.EPOLLIN)

	select {
	case got := <-done:
		if got.err != nil || len(got.events) != 1 || got.events[0].Fd != int32(r) {
			t.Fatalf("the waiter reported %+v, %v; want fd %d", got.events, got.err, r)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a wait in progress never saw a descriptor added to its instance")
	}
}

func TestEpollWaitTimesOut(t *testing.T) {
	epfd := epollNew(t)
	r, _ := epollPipe(t)
	epollAdd(t, epfd, r, syscall.EPOLLIN)

	start := time.Now()
	if got := epollWaitOK(t, epfd, 100); len(got) != 0 {
		t.Fatalf("an empty pipe reported %+v", got)
	}
	if d := time.Since(start); d < 100*time.Millisecond {
		t.Fatalf("a 100ms wait returned after %v", d)
	}
}

// A registered descriptor that is closed leaves the interest list, as it
// does on Linux, rather than failing every later wait.
func TestEpollDropsAClosedDescriptor(t *testing.T) {
	epfd := epollNew(t)
	var p [2]int
	if err := syscall.Pipe2(p[:], syscall.O_CLOEXEC); err != nil {
		t.Fatal(err)
	}
	epollAdd(t, epfd, p[0], syscall.EPOLLIN)
	syscall.Close(p[0])
	syscall.Close(p[1])
	if got := epollWaitOK(t, epfd, 0); len(got) != 0 {
		t.Fatalf("a closed descriptor reported %+v", got)
	}
}

// golang.org/x/sys/unix reaches epoll_create1 and epoll_ctl through
// RawSyscall, not Syscall.
func TestEpollThroughRawSyscall(t *testing.T) {
	r1, _, errno := syscall.RawSyscall(syscall.SYS_EPOLL_CREATE1, syscall.EPOLL_CLOEXEC, 0, 0)
	if errno != 0 {
		t.Fatalf("raw epoll_create1: %v", errno)
	}
	epfd := int(r1)
	defer syscall.Close(epfd)
	r, w := epollPipe(t)
	ev := syscall.EpollEvent{Events: syscall.EPOLLIN, Fd: int32(r)}
	_, _, errno = syscall.RawSyscall6(syscall.SYS_EPOLL_CTL, uintptr(epfd), syscall.EPOLL_CTL_ADD, uintptr(r), uintptr(unsafe.Pointer(&ev)), 0, 0)
	if errno != 0 {
		t.Fatalf("raw epoll_ctl: %v", errno)
	}
	syscall.Write(w, []byte{1})
	if got := epollWaitOK(t, epfd, 1000); len(got) != 1 || got[0].Fd != int32(r) {
		t.Fatalf("reported %+v, want fd %d", got, r)
	}
}
