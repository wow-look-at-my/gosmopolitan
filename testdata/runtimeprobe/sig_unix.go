// Unix-host halves of the signal checks. The windows payload of a fat
// APE is a plain GOOS=windows build, where syscall.Kill and SIGUSR1/2
// do not exist - sig_windows.go supplies skip stubs.

//go:build !windows

package main

import (
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

// checkSignalNotify exercises the os/signal Notify path end to end:
// install (sigaction), send (kill), receive (sigtramp -> sigsend ->
// channel). SIGTERM is the classic case; SIGUSR2 diverges numerically
// (Linux 12, Apple 31), so its round trip proves the install-side AND
// receive-side number translation agree - a one-sided bug would
// deliver the wrong signal or none at all.
func checkSignalNotify() {
	for _, tc := range []struct {
		name string
		sig  syscall.Signal
	}{
		{"sigterm", syscall.SIGTERM},
		{"sigusr2", syscall.SIGUSR2},
	} {
		c := make(chan os.Signal, 1)
		signal.Notify(c, tc.sig)
		if err := syscall.Kill(os.Getpid(), tc.sig); err != nil {
			fail(tc.name, "kill(self, %v): %v", tc.sig, err)
			signal.Stop(c)
			continue
		}
		select {
		case got := <-c:
			if got != tc.sig {
				fail(tc.name, "received %v, want %v", got, tc.sig)
			} else {
				ok(tc.name)
			}
		case <-time.After(5 * time.Second):
			fail(tc.name, "signal not delivered within 5s")
		}
		signal.Stop(c)
	}
}

// raiseFatalChild is the child half of checkWaitSig: die by SIGKILL.
// SIGKILL is uncatchable - the kernel enforces it regardless of any
// runtime handler state - so the death is deterministic on every unix
// host. (A catchable signal cannot serve here: Go catches all of them
// and either discards un-notified ones or converges the fatal paths to
// SIGABRT.) The kill still exercises send-side translation on macOS,
// and the parent reads the death back through the wait4 status
// translation; statuses with number-diverging signals are pinned by
// TestDarwinXlatWaitStatus, and receive-side divergence by the sigusr2
// notify check.
func raiseFatalChild() {
	syscall.Kill(os.Getpid(), syscall.SIGKILL)
	// Give delivery a moment, then flag the failure via a normal exit.
	time.Sleep(5 * time.Second)
	os.Exit(3)
}

// checkWaitSigStatus asserts that the child died by SIGKILL and that
// syscall.WaitStatus decodes it with Linux semantics on every host:
// Signaled() true, Signal() == SIGKILL, and not Exited().
func checkWaitSigStatus(err error) {
	var ee *exec.ExitError
	if !errors.As(err, &ee) {
		fail("waitsig", "child error %v, want ExitError", err)
		return
	}
	ws, isWait := ee.Sys().(syscall.WaitStatus)
	switch {
	case !isWait:
		fail("waitsig", "Sys() is %T, want syscall.WaitStatus", ee.Sys())
	case !ws.Signaled():
		fail("waitsig", "child not signaled: status %#x", uint32(ws))
	case ws.Signal() != syscall.SIGKILL:
		fail("waitsig", "child died by %d (%v), want SIGKILL (%d)",
			int(ws.Signal()), ws.Signal(), int(syscall.SIGKILL))
	default:
		ok("waitsig", ws.Signal())
	}
}
