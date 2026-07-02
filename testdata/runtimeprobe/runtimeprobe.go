// Runtimeprobe exercises the runtime and os-package surface that must
// work on every APE host today: file I/O, process identity, CPU count,
// the monotonic clock, timers (time.Sleep/Ticker/After and context
// timeouts, which need a working netpoller), TCP/UDP loopback sockets
// with deadlines, os.Executable, argv/env, and working-directory
// syscalls. It deliberately uses NO signals - those are
// known-unimplemented on macOS hosts until the signal wave lands.
//
// Output contract (consumed by testdata/ape/apetest/runtimeprobe_test.go):
// every check prints exactly one line starting with "ok <name>" or
// "FAIL <name>: <detail>", and the process exits 0 iff no check failed.
//
// Usage: RUNTIMEPROBE_MARK=<value> runtimeprobe <value>
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

var failed bool

func ok(name string, detail ...any) {
	if len(detail) > 0 {
		fmt.Printf("ok %s %v\n", name, detail[0])
		return
	}
	fmt.Printf("ok %s\n", name)
}

func fail(name string, f string, args ...any) {
	failed = true
	fmt.Printf("FAIL %s: %s\n", name, fmt.Sprintf(f, args...))
}

// sink defeats dead-code elimination of the busy loop.
var sink uint64

//go:noinline
func spin(x uint64) uint64 {
	// A real (non-inlined) call so the busy loop always contains
	// preemption points even without async preemption.
	return x*2862933555777941757 + 3037000493
}

func main() {
	if os.Getenv("RUNTIMEPROBE_CHILD") == "1" {
		// Child mode for checkExec: print the marker and exit clean.
		fmt.Println("child-ok", os.Getpid())
		return
	}
	startWatchdog()
	checkArgsEnv()
	checkIdentity()
	checkNumCPU()
	checkMonotonic()
	checkTimers()
	checkSockets()
	checkExec()
	checkExecutable()
	checkFiles()
	if failed {
		os.Exit(1)
	}
	fmt.Println("ok all")
}

// startWatchdog aborts the probe if it wedges, so a hang fails CI in
// seconds instead of eating the job timeout. It must not depend on
// anything under test - in particular not on timers - so it burns a spin
// loop against the wall clock instead of sleeping.
func startWatchdog() {
	go func() {
		deadline := time.Now().Add(90 * time.Second)
		x := uint64(1)
		for time.Now().Before(deadline) {
			for i := 0; i < 1_000_000; i++ {
				x = spin(x)
			}
		}
		sink = x
		fmt.Println("FAIL watchdog: probe did not finish within 90s")
		os.Exit(2)
	}()
}

func checkTimers() {
	t0 := time.Now()
	time.Sleep(50 * time.Millisecond)
	d := time.Since(t0)
	switch {
	case d < 45*time.Millisecond:
		fail("sleep", "time.Sleep(50ms) returned after only %v", d)
	case d > 5*time.Second:
		fail("sleep", "time.Sleep(50ms) took %v", d)
	default:
		ok("sleep", d)
	}

	tk := time.NewTicker(10 * time.Millisecond)
	select {
	case <-tk.C:
		ok("ticker")
	case <-time.After(5 * time.Second):
		fail("ticker", "no tick within 5s")
	}
	tk.Stop()

	// A bare time.After select: distinct from the sleep check because it
	// goes through a channel wait rather than timeSleep.
	select {
	case <-time.After(20 * time.Millisecond):
		ok("after")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	select {
	case <-ctx.Done():
		if err := ctx.Err(); err != context.DeadlineExceeded {
			fail("ctxtimeout", "ctx.Err() = %v, want DeadlineExceeded", err)
		} else {
			ok("ctxtimeout")
		}
	case <-time.After(5 * time.Second):
		fail("ctxtimeout", "context not done within 5s")
	}
	cancel()
}

func checkArgsEnv() {
	if len(os.Args) != 2 {
		fail("args", "want exactly 1 argument, got %d: %q", len(os.Args)-1, os.Args)
	} else {
		ok("args", os.Args[1])
	}
	if len(os.Environ()) == 0 {
		fail("environ", "os.Environ() is empty")
	} else {
		ok("environ")
	}
	mark := os.Getenv("RUNTIMEPROBE_MARK")
	switch {
	case mark == "":
		fail("mark", "RUNTIMEPROBE_MARK is unset or empty")
	case len(os.Args) == 2 && mark != os.Args[1]:
		fail("mark", "RUNTIMEPROBE_MARK=%q does not match argv[1]=%q", mark, os.Args[1])
	default:
		ok("mark")
	}
}

func checkIdentity() {
	pid := os.Getpid()
	pid2 := os.Getpid()
	switch {
	case pid <= 0:
		fail("getpid", "os.Getpid() = %d, want > 0", pid)
	case pid != pid2:
		fail("getpid", "unstable: first %d, second %d", pid, pid2)
	default:
		ok("getpid", pid)
	}
	if ppid := os.Getppid(); ppid < 0 {
		fail("getppid", "os.Getppid() = %d, want >= 0", ppid)
	} else {
		ok("getppid", ppid)
	}
}

func checkNumCPU() {
	// Assert only >= 1 so single-core hosts stay green, but print the
	// value: on the macOS CI runners it should exceed 1 now that
	// GOMAXPROCS comes from sysctl hw.ncpu.
	if n := runtime.NumCPU(); n < 1 {
		fail("numcpu", "runtime.NumCPU() = %d, want >= 1", n)
	} else {
		ok("numcpu", n)
	}
}

func checkMonotonic() {
	t0 := time.Now()
	// A fixed amount of real work (no clock-based loop bound, so a
	// broken clock cannot hang the probe). Roughly tens of
	// milliseconds on current hardware.
	x := uint64(1)
	for i := 0; i < 20_000_000; i++ {
		x = spin(x)
	}
	sink = x
	d1 := time.Since(t0)
	d2 := time.Since(t0)
	switch {
	case d1 <= 0:
		fail("monotonic", "time.Since after busy work = %v, want > 0", d1)
	case d1 >= 10*time.Second:
		fail("monotonic", "time.Since after busy work = %v, want < 10s", d1)
	case d2 < d1:
		fail("monotonic", "clock went backwards: first %v, then %v", d1, d2)
	default:
		ok("monotonic", d1)
	}
}

func checkSockets() {
	// TCP loopback: listen, dial, one echo round trip, then a read
	// deadline already in the past on a second connection. The server
	// goroutine holds the second connection open until the deadline
	// check finished, so the expected failure is unambiguously the
	// deadline (not EOF from a closing peer).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		fail("tcplisten", "%v", err)
		return
	}
	ok("tcplisten", ln.Addr())

	srvDone := make(chan string, 1)
	holdOpen := make(chan struct{})
	go func() {
		defer close(srvDone)
		// Connection 1: echo one message back.
		c, err := ln.Accept()
		if err != nil {
			srvDone <- fmt.Sprintf("accept 1: %v", err)
			return
		}
		buf := make([]byte, 256)
		n, err := c.Read(buf)
		if err != nil {
			srvDone <- fmt.Sprintf("server read: %v", err)
			c.Close()
			return
		}
		if _, err := c.Write(buf[:n]); err != nil {
			srvDone <- fmt.Sprintf("server write: %v", err)
			c.Close()
			return
		}
		c.Close()
		// Connection 2: accept, send nothing, hold open until the
		// client's deadline check is done.
		c2, err := ln.Accept()
		if err != nil {
			srvDone <- fmt.Sprintf("accept 2: %v", err)
			return
		}
		<-holdOpen
		c2.Close()
		srvDone <- ""
	}()

	c, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		fail("tcpecho", "dial: %v", err)
		ln.Close()
		close(holdOpen)
		return
	}
	msg := "ping " + os.Getenv("RUNTIMEPROBE_MARK")
	if _, err := c.Write([]byte(msg)); err != nil {
		fail("tcpecho", "write: %v", err)
	} else {
		buf := make([]byte, 256)
		n, err := io.ReadAtLeast(c, buf, len(msg))
		switch {
		case err != nil:
			fail("tcpecho", "read: %v", err)
		case string(buf[:n]) != msg:
			fail("tcpecho", "got %q, want %q", buf[:n], msg)
		default:
			ok("tcpecho")
		}
	}
	c.Close()

	c2, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		fail("deadline", "dial: %v", err)
	} else {
		c2.SetReadDeadline(time.Now().Add(-time.Second))
		_, err := c2.Read(make([]byte, 1))
		var ne net.Error
		switch {
		case err == nil:
			fail("deadline", "read succeeded despite past deadline")
		case !errors.As(err, &ne) || !ne.Timeout():
			fail("deadline", "read error %v, want timeout", err)
		default:
			ok("deadline")
		}
		c2.Close()
	}
	close(holdOpen)
	if msg := <-srvDone; msg != "" {
		fail("tcpserver", "%s", msg)
	} else {
		ok("tcpserver")
	}
	ln.Close()

	// UDP loopback: one packet.
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		fail("udp", "listen: %v", err)
		return
	}
	uc, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		fail("udp", "dial: %v", err)
		pc.Close()
		return
	}
	const datagram = "probe-datagram"
	if _, err := uc.Write([]byte(datagram)); err != nil {
		fail("udp", "write: %v", err)
	} else {
		pc.SetReadDeadline(time.Now().Add(5 * time.Second))
		buf := make([]byte, 64)
		n, _, err := pc.ReadFrom(buf)
		switch {
		case err != nil:
			fail("udp", "read: %v", err)
		case string(buf[:n]) != datagram:
			fail("udp", "got %q, want %q", buf[:n], datagram)
		default:
			ok("udp")
		}
	}
	uc.Close()
	pc.Close()
}

// checkExec re-executes this binary in child mode (fork/exec, the
// status pipe, stdout pipe and wait4 - the whole os/exec stack). How the
// child must be launched depends on what is on disk at os.Executable():
// an assimilated ELF (Linux after first run) or Mach-O executes
// directly, a pristine APE (still starting with "MZ...") needs the shell
// bootstrap on unix hosts, and on Windows the PE always executes
// directly.
func checkExec() {
	exe, err := os.Executable()
	if err != nil {
		fail("execchild", "os.Executable: %v", err)
		return
	}
	f, err := os.Open(exe)
	if err != nil {
		fail("execchild", "open %q: %v", exe, err)
		return
	}
	var magic [4]byte
	_, err = io.ReadFull(f, magic[:])
	f.Close()
	if err != nil {
		fail("execchild", "read magic: %v", err)
		return
	}

	direct := runtime.GOOS == "windows" ||
		(magic == [4]byte{0x7f, 'E', 'L', 'F'}) || // assimilated ELF
		(magic == [4]byte{0xcf, 0xfa, 0xed, 0xfe}) || // assimilated Mach-O 64
		(magic == [4]byte{0xca, 0xfe, 0xba, 0xbe}) // fat Mach-O
	var cmd *exec.Cmd
	if direct {
		cmd = exec.Command(exe)
	} else {
		// Pristine APE on a unix host: bootstrap through the shell,
		// exactly how the probe itself was started.
		cmd = exec.Command("/bin/sh", exe)
	}
	cmd.Env = append(os.Environ(), "RUNTIMEPROBE_CHILD=1")

	// Bound the child ourselves instead of using cmd.Output() alone: if
	// the child ever wedges, an unbounded wait would leave an orphan
	// holding this process's pipes after the watchdog fires, which can
	// wedge the CALLING test harness in turn. Process.Kill works on
	// macOS hosts (kill(2) is emulated; SIGKILL delivery is entirely
	// kernel-side, needing no signal handling in the target).
	var stdout, stderrBuf strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		fail("execchild", "start self (direct=%v): %v", direct, err)
		return
	}
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	var err2 error
	select {
	case err2 = <-waitErr:
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		fail("execchild", "child did not finish within 30s (kill result: %v)", <-waitErr)
		return
	}
	out := stdout.String()
	switch {
	case err2 != nil:
		detail := ""
		if stderrBuf.Len() > 0 {
			detail = fmt.Sprintf(" (stderr: %q)", stderrBuf.String())
		}
		fail("execchild", "run self (direct=%v): %v%s", direct, err2, detail)
	case !strings.HasPrefix(out, "child-ok"):
		fail("execchild", "child output %q, want child-ok prefix", out)
	default:
		ok("execchild")
	}
}

func checkExecutable() {
	exe, err := os.Executable()
	if err != nil {
		fail("executable", "os.Executable(): %v", err)
		return
	}
	if fi, err := os.Stat(exe); err != nil {
		fail("executable", "os.Stat(%q): %v", exe, err)
	} else if !fi.Mode().IsRegular() {
		fail("executable", "%q is not a regular file (%v)", exe, fi.Mode())
	} else {
		ok("executable")
	}
}

func checkFiles() {
	dir, err := os.MkdirTemp("", "runtimeprobe")
	if err != nil {
		fail("mkdirtemp", "%v", err)
		return
	}
	ok("mkdirtemp")

	// A directory must stat as a directory. This is the regression
	// canary for the per-arch Stat_t layout: with the wrong layout the
	// kernel's mode bits land in other fields and IsDir turns false
	// (it cannot be caught with a regular file, whose S_IFREG type can
	// disappear into a zero Mode unnoticed).
	if fi, err := os.Stat(dir); err != nil {
		fail("statdir", "%v", err)
	} else if !fi.IsDir() {
		fail("statdir", "Mode() = %v, want directory", fi.Mode())
	} else {
		ok("statdir")
	}

	const content = "hello from runtimeprobe\n"
	name := dir + string(os.PathSeparator) + "probe.txt"
	f, err := os.Create(name)
	if err != nil {
		fail("create", "%v", err)
		return
	}
	if _, err := f.WriteString(content); err != nil {
		fail("create", "write: %v", err)
		f.Close()
		return
	}
	if err := f.Close(); err != nil {
		fail("create", "close: %v", err)
		return
	}
	ok("create")

	if got, err := os.ReadFile(name); err != nil {
		fail("readback", "%v", err)
	} else if string(got) != content {
		fail("readback", "got %q, want %q", got, content)
	} else {
		ok("readback")
	}

	name2 := dir + string(os.PathSeparator) + "probe-renamed.txt"
	if err := os.Rename(name, name2); err != nil {
		fail("rename", "%v", err)
		// keep going against the original name
		name2 = name
	} else {
		ok("rename")
	}

	if fi, err := os.Stat(name2); err != nil {
		fail("statsize", "%v", err)
	} else if fi.Size() != int64(len(content)) {
		fail("statsize", "size = %d, want %d", fi.Size(), len(content))
	} else {
		ok("statsize")
	}

	checkWd(dir)

	if err := os.Remove(name2); err != nil {
		fail("remove", "%v", err)
	} else {
		ok("remove")
	}
	if err := os.Remove(dir); err != nil {
		fail("rmdir", "%v", err)
	} else {
		ok("rmdir")
	}
}

func checkWd(dir string) {
	wd0, err := os.Getwd()
	if err != nil {
		fail("getwd", "%v", err)
		return
	}
	ok("getwd")
	if err := os.Chdir(dir); err != nil {
		fail("chdir", "%v", err)
		return
	}
	wd, err := os.Getwd()
	if err != nil {
		fail("chdir", "Getwd after Chdir: %v", err)
		os.Chdir(wd0)
		return
	}
	// Compare via SameFile: on macOS the temp dir is reached through
	// the /var -> /private/var symlink, so the strings may differ
	// while naming the same directory.
	fi1, err1 := os.Stat(wd)
	fi2, err2 := os.Stat(dir)
	if err1 != nil || err2 != nil {
		fail("chdir", "stat round-trip: %v / %v", err1, err2)
	} else if !os.SameFile(fi1, fi2) {
		fail("chdir", "Getwd = %q, not the temp dir %q", wd, dir)
	} else {
		ok("chdir")
	}
	if err := os.Chdir(wd0); err != nil {
		fail("wdrestore", "%v", err)
	} else {
		ok("wdrestore")
	}
}
