// Process-group signal check (ctrlbreak): spawn SELF as a new
// process-group leader (SysProcAttr{Setpgid: true}) and deliver
// SIGQUIT to the whole group with kill(-pid). The child Notifies
// SIGQUIT, reports readiness on stdout, and reports delivery.
//
// On Linux hosts this is native setpgid in the fork child plus the
// kernel's group kill - which is also what validates the probe's own
// logic, since the child runs the SAME code on every host. On Windows
// hosts it is the whole new wave-3 item-4 chain, and the FIRST CI
// coverage of the OS-injected console-ctrl path:
//
//	CREATE_NEW_PROCESS_GROUP spawn -> kill(-pgid, SIGQUIT) ->
//	GenerateConsoleCtrlEvent(CTRL_BREAK_EVENT, pgid) -> conhost
//	injects a foreign handler thread in the CHILD -> asm ntCtrlTramp
//	-> ntCtrlMask -> relay M -> ntKillSelf -> sigtrampgo ->
//	os/signal Notify
//
// Every earlier signal check reached the relay only via SELF-kill;
// this one finally fires the handler on a genuinely OS-injected
// thread. macOS hosts dispatch setpgid and kill (darwinCall), and the
// negative-pid group kill is expected to pass through to XNU -
// attempt-first: only if the SPAWN or the GROUP-KILL syscall itself
// errors, and only on darwin, the check prints an honest skip;
// failures on NT or Linux (and delivery failures anywhere) stay FAIL.
package main

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
)

// ctrlwaitChild is the RUNTIMEPROBE_CHILD=ctrlwait mode: Notify
// SIGQUIT, say ready, await the group-targeted signal. "ctrl-ready"
// is printed only AFTER signal.Notify returned, so the parent cannot
// fire before the handler is installed.
func ctrlwaitChild() {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGQUIT)
	fmt.Println("ctrl-ready")
	select {
	case <-ch:
		fmt.Println("ctrl-got")
		os.Exit(0)
	case <-time.After(20 * time.Second):
		fmt.Println("ctrl-timeout")
		os.Exit(1)
	}
}

// checkCtrlBreak is the parent half: group-leader spawn, readiness
// handshake, kill(-pid, SIGQUIT), delivery handshake, clean wait.
func checkCtrlBreak() {
	// The only host allowed to skip, and only on the two syscall
	// sites named in the file comment.
	darwin := !probeHostIsNT() && !probeHostIsLinux()

	cmd, direct, bad := selfCommand("ctrlbreak", "ctrlwait")
	if bad {
		return
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		fail("ctrlbreak", "StdoutPipe: %v", err)
		return
	}
	var childErr strings.Builder
	cmd.Stderr = &childErr
	if err := cmd.Start(); err != nil {
		if darwin {
			ok("ctrlbreak", fmt.Sprintf("skipped (darwin: %v)", err))
			return
		}
		fail("ctrlbreak", "start self (direct=%v, setpgid): %v", direct, err)
		return
	}

	// Child stdout lines arrive on a channel so every wait below is
	// bounded (the pipe read itself has no deadline).
	lines := make(chan string, 4)
	go func() {
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines <- sc.Text()
		}
		close(lines)
	}()
	childFail := func(f string, args ...any) {
		detail := fmt.Sprintf(f, args...)
		if childErr.Len() > 0 {
			detail += fmt.Sprintf(" (child stderr: %q)", childErr.String())
		}
		cmd.Process.Kill()
		cmd.Wait()
		fail("ctrlbreak", "%s", detail)
	}
	awaitLine := func(want string, timeout time.Duration) bool {
		select {
		case ln, open := <-lines:
			if !open {
				childFail("child stdout closed before %q", want)
				return false
			}
			if ln != want {
				childFail("child printed %q, want %q", ln, want)
				return false
			}
			return true
		case <-time.After(timeout):
			childFail("no %q from child within %v", want, timeout)
			return false
		}
	}

	if !awaitLine("ctrl-ready", 15*time.Second) {
		return
	}
	// The group kill: pgid == the leader child's pid on every host.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGQUIT); err != nil {
		if darwin {
			cmd.Process.Kill()
			cmd.Wait()
			ok("ctrlbreak", fmt.Sprintf("skipped (darwin: kill(-pgid): %v)", err))
			return
		}
		childFail("kill(-%d, SIGQUIT): %v", cmd.Process.Pid, err)
		return
	}
	// 25s > the child's own 20s give-up, so an undelivered signal
	// surfaces as the child's honest "ctrl-timeout" line, not ours.
	if !awaitLine("ctrl-got", 25*time.Second) {
		return
	}
	err2, completed := waitBounded("ctrlbreak", cmd)
	if !completed {
		return
	}
	if err2 != nil {
		fail("ctrlbreak", "child exit: %v (stderr: %q)", err2, childErr.String())
		return
	}
	ok("ctrlbreak")
}
