// Runtimeprobe exercises the runtime and os-package surface that must
// work on every APE host today: file I/O, process identity, CPU count,
// the monotonic clock, os.Executable, argv/env, and working-directory
// syscalls. It deliberately uses NO network, NO timers (time.Sleep), NO
// os/exec and NO signals - those are known-unimplemented on macOS hosts
// until the darwin netpoll and signal waves land.
//
// Output contract (consumed by testdata/ape/apetest/runtimeprobe_test.go):
// every check prints exactly one line starting with "ok <name>" or
// "FAIL <name>: <detail>", and the process exits 0 iff no check failed.
//
// Usage: RUNTIMEPROBE_MARK=<value> runtimeprobe <value>
package main

import (
	"fmt"
	"os"
	"runtime"
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
	checkArgsEnv()
	checkIdentity()
	checkNumCPU()
	checkMonotonic()
	checkExecutable()
	checkFiles()
	if failed {
		os.Exit(1)
	}
	fmt.Println("ok all")
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
