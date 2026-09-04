// Runtimeprobe exercises the runtime and os-package surface that must
// work on every APE host today: file I/O, directory listing
// (os.ReadDir/filepath.WalkDir, i.e. getdents64), process identity,
// the boot auxv every host has to publish, CPU count, the monotonic
// clock, timers (time.Sleep/Ticker/After and
// context timeouts, which need a working netpoller), TCP/UDP loopback
// sockets with deadlines, socketpair (raw fds and net.FileConn),
// sendmsg/recvmsg and SCM_RIGHTS fd passing to a child process,
// readv/writev + net.Buffers, EOF on a dead child's stdout pipe,
// os.Executable, argv/env, working-directory
// syscalls, exec.LookPath/exec.Command name resolution over the
// host-format PATH (';'-separated drive-letter entries with PATHEXT
// suffix probing on NT hosts), and - since the wave-8 signal work -
// SIGSEGV recovery
// (sigpanic), os/signal delivery, async preemption, wait-status
// signal decoding, CPU profiling (real samples required on all
// hosts), and process-group signaling (Setpgid
// spawn + kill(-pgid), the console-ctrl chain on Windows hosts).
//
// Output contract (consumed by testdata/ape/apetest/runtimeprobe_test.go):
// every check prints exactly one line starting with "ok <name>" or
// "FAIL <name>: <detail>", and the process exits 0 iff no check failed.
//
// Usage: RUNTIMEPROBE_MARK=<value> runtimeprobe <value> --help -x --key=value trailing-arg
//
// The fixed flag-shaped tail after <value> is part of the argv
// contract: probeWantArgs asserts that "--"/"-"-prefixed tokens and a
// mixed positional+flag vector reach os.Args byte-intact on every
// host. On NT that pins the GetCommandLineW -> ntCommandLineToArgv
// path for flag-shaped tokens specifically (a go-toolchain windows
// smoke failure was initially misdiagnosed as flags being dropped
// there; the parse was in fact intact - keep it provably so).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
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
	switch os.Getenv("RUNTIMEPROBE_CHILD") {
	case "1":
		// Child mode for checkExec: print the marker and exit clean.
		fmt.Println("child-ok", os.Getpid())
		return
	case "raise":
		// Child mode for checkWaitSig: die by SIGUSR1.
		raiseFatalChild()
		return
	case "fdpass":
		// Child mode for checkFdpass: receive fds over SCM_RIGHTS.
		fdpassChild()
		return
	case "execstress":
		// Child mode for checkExecStress: stamp the marker proving the
		// exec completed, then exit.
		execStressChild()
		return
	case "pipeecho":
		// Child mode for checkPipeEOF: write one marker line and exit,
		// so the exit is what closes the stdout pipe.
		pipeEOFChild()
		return
	case "ctrlwait":
		// Child mode for checkCtrlBreak: await a group-targeted SIGQUIT.
		ctrlwaitChild()
		return
	}
	startWatchdog()
	// timed localizes latency stalls without weakening any verdict:
	// every healthy check block completes well under a second on every
	// host, so a block that takes 2s+ prints a slow: line plus one
	// poller-counter sample (the wave-9 forensic counters) naming
	// where the time went. CI logs then show WHICH block stalled and
	// whether the poller was wedged inside one long WSAPoll/kevent
	// (sinceenter large) or cycling while work starved.
	timed := func(label string, fn func()) {
		t0 := time.Now()
		fn()
		if d := time.Since(t0); d > 2*time.Second {
			fmt.Printf("slow: %s took %v\n", label, d)
			printNetpollDiag("slow-" + label)
		}
	}
	timed("argsenv", checkArgsEnv)
	timed("identity", checkIdentity)
	timed("numcpu", checkNumCPU)
	timed("monotonic", checkMonotonic)
	timed("timers", checkTimers)
	timed("sockets", checkSockets)
	timed("dns", checkDNS)
	timed("tls", checkTLS)
	timed("sockpair", checkSockpair)
	timed("sendmsg", checkSendmsg)
	timed("netbuffers", checkNetBuffers)
	timed("cloexec", checkCloexec)
	timed("hostos", checkHostOS)
	timed("auxv", checkAuxv)
	timed("procauxv", checkProcAuxv)
	timed("fdpath", checkFdPath)
	timed("peercred", checkPeercred)
	timed("dupfile", checkDupFile)
	timed("executable", checkExecutable)
	timed("files", checkFiles)
	timed("seekreadat", checkSeekReadAt)
	timed("readdir", checkReadDir)
	timed("fsmeta", checkFsMeta)
	timed("fsmetaunix", checkFsMetaUnix)
	timed("sysinfo", checkSysInfo)
	timed("sendfile", checkSendfile)
	timed("nanosleep", checkNanosleep)
	// Exec and signal checks run at the END on purpose, in that order.
	// Exec: if a forked child ever wedges (a nondeterministic macOS CI
	// incident produced kernel-stuck processes), every other check has
	// already printed its verdict, so the partial output localizes the
	// failure precisely. The signal-dependent block (segvrecover,
	// notify, preempt, waitsig) comes dead last: pre-VEH Windows hosts
	// crash at the segv check, and this order maximizes the coverage
	// that still prints before that crash.
	timed("exec", checkExec)
	timed("lookpath", checkLookPath)
	timed("fdpass", checkFdpass)
	// Deliberately adjacent to the other exec checks: this one is the
	// deterministic version of the wedge they hit by chance.
	timed("execstress", checkExecStress)
	// Same family, other end of the exec: execstress watches the status
	// pipe the parent reads before Start returns, this one watches the
	// stdout pipe the parent reads inside Wait.
	timed("pipeeof", checkPipeEOF)
	timed("segvrecover", checkSegvRecover)
	timed("signalnotify", checkSignalNotify)
	timed("preempt", checkPreempt)
	timed("cpuprof", checkCPUProf)
	timed("ctrlbreak", checkCtrlBreak)
	timed("waitsig", checkWaitSig)
	if failed {
		os.Exit(1)
	}
	fmt.Println("ok all")
}

// startWatchdog aborts the probe if it wedges, so a hang fails CI in
// seconds instead of eating the job timeout. It must not depend on
// anything under test - in particular not on timers - so it burns a spin
// loop against the wall clock instead of sleeping.
//
// On firing it panics with traceback "all" instead of calling os.Exit:
// the wedge this exists to diagnose leaves an M stuck inside a runtime
// mutex, which blocks any stop-the-world forever - so runtime.Stack
// (STW) would hang, and once async preemption works, NO user code can
// keep running under a pending STW to time it out (a CI run proved
// both: the dump hung for the step's remaining budget). The panic path
// uses freezetheworld, which is best-effort and dumps every goroutine
// - including ones wedged in locks - without their cooperation. The
// FAIL verdict prints to stdout first; the traceback goes to stderr,
// which the apetest harness logs. A panic exits with status 2, same as
// the old os.Exit.
func startWatchdog() {
	debug.SetTraceback("all")
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
		// Two poller-counter samples a spin apart (no timers - they may
		// be wedged) tell the wedge forensics WHERE the loss is; see
		// printNetpollDiag.
		printNetpollDiag("t0")
		spinUntil := time.Now().Add(300 * time.Millisecond)
		for time.Now().Before(spinUntil) {
			for i := 0; i < 100_000; i++ {
				x = spin(x)
			}
		}
		sink = x
		printNetpollDiag("t0+300ms")
		panic("watchdog: probe wedged; all-goroutine traceback follows")
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

// probeWantArgs is the fixed argv tail the harness passes after the
// mark (see the usage comment): flag-shaped tokens must survive the
// host's argv materialization byte-intact, exactly like positionals.
var probeWantArgs = []string{"--help", "-x", "--key=value", "trailing-arg"}

func checkArgsEnv() {
	// argv[1] is the mark (its VALUE is the "mark" check's contract);
	// argv[2:] must be probeWantArgs byte-for-byte.
	got := os.Args[1:]
	switch {
	case len(got) != 1+len(probeWantArgs):
		fail("args", "want %d arguments (mark + %q), got %d: %q", 1+len(probeWantArgs), probeWantArgs, len(got), got)
	case got[0] == "":
		fail("args", "argv[1] (the mark) is empty: %q", os.Args)
	default:
		mismatch := -1
		for i, want := range probeWantArgs {
			if got[1+i] != want {
				mismatch = i
				break
			}
		}
		if mismatch >= 0 {
			fail("args", "argv[%d] = %q, want %q (full argv %q)", mismatch+2, got[1+mismatch], probeWantArgs[mismatch], os.Args)
		} else {
			ok("args", fmt.Sprintf("%q", got))
		}
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
	case len(os.Args) >= 2 && mark != os.Args[1]:
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

	// Unix-domain stream socket bound to a filesystem path: sockaddr
	// round trip in both directions plus one echo. The dialed end stays
	// unbound on purpose - an unnamed unix socket must surface an EMPTY
	// local name; this is the regression canary for unnamed-vs-abstract
	// confusion in the sockaddr parse. (Windows's stack reports unnamed
	// as "@"; net's own unixsock tests encode that.)
	udir, err := os.MkdirTemp("", "runtimeprobe-unix")
	if err != nil {
		fail("unixsock", "MkdirTemp: %v", err)
		return
	}
	defer os.RemoveAll(udir)
	spath := filepath.Join(udir, "probe.sock")
	uln, err := net.Listen("unix", spath)
	if err != nil {
		fail("unixsock", "listen: %v", err)
		return
	}
	defer uln.Close()

	uDone := make(chan string, 1)
	go func() {
		c, err := uln.Accept()
		if err != nil {
			uDone <- fmt.Sprintf("accept: %v", err)
			return
		}
		defer c.Close()
		buf := make([]byte, 64)
		n, err := c.Read(buf)
		if err != nil {
			uDone <- fmt.Sprintf("server read: %v", err)
			return
		}
		if _, err := c.Write(buf[:n]); err != nil {
			uDone <- fmt.Sprintf("server write: %v", err)
			return
		}
		uDone <- ""
	}()

	usock, err := net.Dial("unix", spath)
	if err != nil {
		fail("unixsock", "dial: %v", err)
		return
	}
	defer usock.Close()

	la, laOK := usock.LocalAddr().(*net.UnixAddr)
	ra, raOK := usock.RemoteAddr().(*net.UnixAddr)
	switch {
	case uln.Addr().String() != spath:
		fail("unixsock", "listener addr %q, want %q", uln.Addr(), spath)
	case !laOK || la.Name != "":
		fail("unixsock", "dialed local addr %#v, want empty name", usock.LocalAddr())
	case !raOK || ra.Name != spath:
		fail("unixsock", "dialed remote addr %#v, want name %q", usock.RemoteAddr(), spath)
	default:
		ok("unixsock")
	}

	const uMsg = "unix-ping"
	usock.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := usock.Write([]byte(uMsg)); err != nil {
		fail("unixecho", "write: %v", err)
		return
	}
	ubuf := make([]byte, 64)
	un, err := io.ReadAtLeast(usock, ubuf, len(uMsg))
	switch {
	case err != nil:
		fail("unixecho", "read: %v", err)
	case string(ubuf[:un]) != uMsg:
		fail("unixecho", "got %q, want %q", ubuf[:un], uMsg)
	default:
		if msg := <-uDone; msg != "" {
			fail("unixecho", "%s", msg)
		} else {
			ok("unixecho")
		}
	}
}

// selfCommand builds an exec.Cmd that re-executes this binary in the
// given RUNTIMEPROBE_CHILD mode. How the child must be launched
// depends on what is on disk at os.Executable(): an assimilated ELF
// (Linux after first run) or Mach-O executes directly, while a
// pristine APE (still starting with "MZ...") needs the shell
// bootstrap.
func selfCommand(name, childMode string) (cmd *exec.Cmd, direct bool, bad bool) {
	exe, err := os.Executable()
	if err != nil {
		fail(name, "os.Executable: %v", err)
		return nil, false, true
	}
	f, err := os.Open(exe)
	if err != nil {
		fail(name, "open %q: %v", exe, err)
		return nil, false, true
	}
	var magic [4]byte
	_, err = io.ReadFull(f, magic[:])
	f.Close()
	if err != nil {
		fail(name, "read magic: %v", err)
		return nil, false, true
	}

	direct = (magic == [4]byte{0x7f, 'E', 'L', 'F'}) || // assimilated ELF
		(magic == [4]byte{0xcf, 0xfa, 0xed, 0xfe}) || // assimilated Mach-O 64
		(magic == [4]byte{0xca, 0xfe, 0xba, 0xbe}) // fat Mach-O
	if !direct && os.Getenv("OS") == "Windows_NT" {
		// On an NT host the binary stays a pristine APE (no
		// self-assimilation there) whose MZ header IS a valid PE, and
		// there is no /bin/sh: exec it directly. The OS env var is
		// set by every Windows since NT (and by wine); on unix hosts
		// it is absent, keeping their behavior untouched.
		direct = true
	}
	if direct {
		cmd = exec.Command(exe)
	} else {
		// Pristine APE: bootstrap through the shell, exactly how the
		// probe itself was started.
		cmd = exec.Command("/bin/sh", exe)
	}
	cmd.Env = append(os.Environ(), "RUNTIMEPROBE_CHILD="+childMode)
	return cmd, direct, false
}

// waitBounded waits for a started child with a 30s bound. Bounding
// ourselves instead of using cmd.Output() alone matters: if the child
// ever wedges, an unbounded wait would leave an orphan holding this
// process's pipes after the watchdog fires, which can wedge the
// CALLING test harness in turn. Process.Kill works on macOS hosts
// (kill(2) is emulated; SIGKILL delivery is entirely kernel-side).
func waitBounded(name string, cmd *exec.Cmd) (error, bool) {
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case err := <-waitErr:
		return err, true
	case <-time.After(30 * time.Second):
		cmd.Process.Kill()
		fail(name, "child did not finish within 30s (kill result: %v)", <-waitErr)
		return nil, false
	}
}

// checkExec re-executes this binary in child mode (fork/exec, the
// status pipe, stdout pipe and wait4 - the whole os/exec stack).
func checkExec() {
	cmd, direct, bad := selfCommand("execchild", "1")
	if bad {
		return
	}
	var stdout, stderrBuf strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderrBuf
	if err := cmd.Start(); err != nil {
		fail("execchild", "start self (direct=%v): %v", direct, err)
		return
	}
	err2, completed := waitBounded("execchild", cmd)
	if !completed {
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

// checkWaitSig spawns a child that kills itself with SIGUSR1 and
// asserts the parent-visible wait status: Signaled(), and Signal() ==
// syscall.SIGUSR1 with LINUX numbering (10; Apple's kernel reports 30,
// so this proves the wait4 boundary translates). On the child side it
// exercises delivery of a fatal un-notified signal: runtime handler ->
// dieFromSignal -> SIG_DFL reinstall -> re-raise.
func checkWaitSig() {
	cmd, direct, bad := selfCommand("waitsig", "raise")
	if bad {
		return
	}
	if err := cmd.Start(); err != nil {
		fail("waitsig", "start self (direct=%v): %v", direct, err)
		return
	}
	err, completed := waitBounded("waitsig", cmd)
	if !completed {
		return
	}
	if err == nil {
		fail("waitsig", "child exited cleanly, want death by SIGUSR1")
		return
	}
	checkWaitSigStatus(err)
}

// nilp is read through a global so the compiler cannot prove the
// dereference in checkSegvRecover faults and rewrite it statically:
// the point is a REAL hardware fault -> SIGSEGV -> sigpanic.
var nilp *int

//go:noinline
func derefNil() int { return *nilp }

// checkSegvRecover asserts that a hardware nil-pointer dereference
// becomes a recoverable runtime error: SIGSEGV -> sigtramp -> sigpanic
// injection via set_pc/set_sp in the signal context. On macOS this is
// the canary for the host-aware sigctxt writing Apple's mcontext.
func checkSegvRecover() {
	defer func() {
		r := recover()
		if r == nil {
			fail("segvrecover", "nil dereference did not panic")
			return
		}
		err, isErr := r.(error)
		if !isErr || !strings.Contains(err.Error(), "invalid memory address") {
			fail("segvrecover", "recovered %v (%T), want runtime nil-deref error", r, r)
			return
		}
		ok("segvrecover")
	}()
	sink = uint64(derefNil())
}

// checkPreempt proves asynchronous preemption: saturate every P with
// call-free spin loops (their only loop-body operation is an inlined
// atomic load, which is not a preemption point), then require a full
// GC cycle - stop-the-world included - to complete promptly. Without
// working preemption signals the GC hangs until the loops' iteration
// bound drains (tens of seconds), turning this into a duration
// failure rather than a probe hang.
func checkPreempt() {
	var stop atomic.Uint32
	var spun atomic.Uint64
	var wg sync.WaitGroup
	n := runtime.GOMAXPROCS(0)
	t0 := time.Now()
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(seed uint64) {
			defer wg.Done()
			x := seed
			for i := uint64(0); i < 20e9 && stop.Load() == 0; i++ {
				x = x*2862933555777941757 + 3037000493
			}
			spun.Add(x)
		}(uint64(i + 1))
	}
	// Let the spinners occupy every P; the sleep also forces main off
	// its P so wake-up itself needs a preemption.
	time.Sleep(100 * time.Millisecond)
	runtime.GC()
	d := time.Since(t0)
	stop.Store(1)
	wg.Wait()
	sink = spun.Load()
	if d > 10*time.Second {
		fail("preempt", "GC took %v with %d spinning goroutines", d, n)
		return
	}
	ok("preempt", d)
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

// checkSeekReadAt exercises os.File.Seek (all three whence values) and
// os.File.ReadAt (mid-file offsets, and past-EOF returning io.EOF) on
// the shipped os.File API. This is the regression canary for the darwin
// pread/pwrite/lseek emulation: before it, macOS returned ENOSYS
// ("function not implemented") for both Seek and ReadAt while Linux and
// Windows dispatched them natively. It runs on every APE host via the
// apetest suite.
func checkSeekReadAt() {
	dir, err := os.MkdirTemp("", "runtimeprobe-seek")
	if err != nil {
		fail("seekreadat", "MkdirTemp: %v", err)
		return
	}
	defer os.RemoveAll(dir)
	name := filepath.Join(dir, "seek.bin")
	// 36 bytes, indices 0..35.
	data := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
	if err := os.WriteFile(name, data, 0o644); err != nil {
		fail("seekreadat", "WriteFile: %v", err)
		return
	}
	f, err := os.Open(name)
	if err != nil {
		fail("seekreadat", "Open: %v", err)
		return
	}
	defer f.Close()

	// Seek from start.
	if off, err := f.Seek(10, io.SeekStart); err != nil || off != 10 {
		fail("seekreadat", "Seek(10, Start) = %d, %v; want 10, nil", off, err)
		return
	}
	// Seek relative to current (10 + 5 = 15).
	if off, err := f.Seek(5, io.SeekCurrent); err != nil || off != 15 {
		fail("seekreadat", "Seek(5, Current) = %d, %v; want 15, nil", off, err)
		return
	}
	// Seek relative to end (36 - 3 = 33).
	if off, err := f.Seek(-3, io.SeekEnd); err != nil || off != 33 {
		fail("seekreadat", "Seek(-3, End) = %d, %v; want 33, nil", off, err)
		return
	}
	ok("seekreadat", "seek")

	// ReadAt mid-file: indices 2..6 are "23456".
	buf := make([]byte, 5)
	if n, err := f.ReadAt(buf, 2); err != nil || n != 5 || string(buf) != "23456" {
		fail("seekreadat", "ReadAt(2) = %d, %v, %q; want 5, nil, \"23456\"", n, err, buf)
		return
	}
	// ReadAt past EOF must return io.EOF.
	if n, err := f.ReadAt(buf, int64(len(data))+1); err != io.EOF || n != 0 {
		fail("seekreadat", "ReadAt(past EOF) = %d, %v; want 0, io.EOF", n, err)
		return
	}
	// ReadAt exactly at EOF must return io.EOF with zero bytes.
	if n, err := f.ReadAt(buf, int64(len(data))); err != io.EOF || n != 0 {
		fail("seekreadat", "ReadAt(at EOF) = %d, %v; want 0, io.EOF", n, err)
		return
	}
	ok("seekreadat", "readat")
}

// checkReadDir exercises directory listing - the getdents64 syscall on
// Linux hosts, emulated via __getdirentries64 on macOS: os.ReadDir
// names, order and IsDir bits, a filepath.WalkDir traversal, and
// os.RemoveAll (which itself lists directories to find what to delete).
func checkReadDir() {
	dir, err := os.MkdirTemp("", "runtimeprobe-readdir")
	if err != nil {
		fail("readdir", "MkdirTemp: %v", err)
		return
	}
	for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o644); err != nil {
			fail("readdir", "WriteFile %s: %v", name, err)
			return
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		fail("readdir", "Mkdir sub: %v", err)
		return
	}

	// os.ReadDir sorts by name, so the result is fully deterministic.
	want := []struct {
		name string
		dir  bool
	}{{"a.txt", false}, {"b.txt", false}, {"c.txt", false}, {"sub", true}}
	ents, err := os.ReadDir(dir)
	if err != nil {
		fail("readdir", "%v", err)
	} else {
		bad := len(ents) != len(want)
		for i := 0; !bad && i < len(want); i++ {
			bad = ents[i].Name() != want[i].name || ents[i].IsDir() != want[i].dir
		}
		if bad {
			var got []string
			for _, e := range ents {
				got = append(got, fmt.Sprintf("%s(dir=%v)", e.Name(), e.IsDir()))
			}
			fail("readdir", "entries %v, want a.txt b.txt c.txt sub(dir)", got)
		} else {
			ok("readdir")
		}
	}

	var files, dirs int
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs++ // the root and sub
		} else {
			files++
		}
		return nil
	})
	switch {
	case err != nil:
		fail("walkdir", "%v", err)
	case dirs != 2 || files != 3:
		fail("walkdir", "walked %d dirs and %d files, want 2 and 3", dirs, files)
	default:
		ok("walkdir")
	}

	switch err := os.RemoveAll(dir); {
	case err != nil:
		fail("removeall", "%v", err)
	default:
		if _, err := os.Stat(dir); err == nil {
			fail("removeall", "%q still exists", dir)
		} else {
			ok("removeall")
		}
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
