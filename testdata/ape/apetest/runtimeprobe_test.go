package apetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The runtime probe (testdata/runtimeprobe) exercises the runtime/os
// surface that must work on every host: file I/O, pid/ppid, NumCPU, the
// monotonic clock, timers, os.Executable, argv/env and working-directory
// calls. It prints one "ok <name>" line per check and exits non-zero if
// any check printed "FAIL".

// probeOkChecks are the check names runtimeprobe.go emits; keep in sync.
var probeOkChecks = []string{
	"args", "environ", "mark",
	"getpid", "getppid",
	"numcpu",
	"monotonic",
	"sleep", "ticker", "after", "ctxtimeout",
	"tcplisten", "tcpecho", "deadline", "tcpserver", "udp",
	"unixsock", "unixecho",
	"socketpair", "sockpairpoll",
	"sendmsg", "netbuffers", "fdpass",
	"execchild", "lookpath", "execstress", "pipeeof", "cloexec",
	"hostos", "auxv", "procauxv", "hwcap", "fdpath", "peercred", "dupfile",
	"executable",
	"mkdirtemp", "statdir", "create", "readback", "rename", "statsize",
	"getwd", "chdir", "wdrestore",
	"remove", "rmdir",
	"readdir", "walkdir", "removeall",
	"seekreadat",
	"fsmeta", "fsmetaunix", "sysinfo", "flock", "durable", "rusage", "ioctl",
	"nanosleep", "sendfile",
	"segvrecover", "sigterm", "sigusr2", "preempt", "cpuprof", "ctrlbreak", "waitsig",
	"all",
}

// probeFlagArgs is the fixed flag-shaped argv tail the probe demands
// after the mark (runtimeprobe.go probeWantArgs; keep in sync). It
// pins that "-"/"--"-prefixed tokens and a mixed positional+flag
// vector reach os.Args byte-intact on every host - on Windows that
// exercises the NT personality's GetCommandLineW parse.
var probeFlagArgs = []string{"--help", "-x", "--key=value", "trailing-arg"}

func copyProbeBinary(t *testing.T) string {
	t.Helper()
	src := os.Getenv("RUNTIMEPROBE_BIN")
	if src == "" {
		t.Skip("RUNTIMEPROBE_BIN not set; skipping runtime probe execution test")
	}

	// Run a pristine copy: executing an APE self-assimilates it in
	// place, and we must not modify the original artifact.
	tmp := filepath.Join(t.TempDir(), "runtimeprobe.com")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(tmp, data, 0755))
	return tmp
}

// TestRuntimeProbeDirectExecAuxv runs the probe with no shell in front of
// it, which is how a consumer's CI step starts an APE. That matters on
// macOS specifically: the shell bootstrap hands control to the APE loader,
// and the loader builds a System V stack with an auxv on it, so the probe
// under TestRuntimeProbe sees a full vector. A kernel that accepts the
// embedded Mach-O header instead skips the loader, and a Mach-O stack
// carries no auxv at all.
//
// An empty auxv is what sends golang.org/x/sys/cpu to read the ARM64 ID
// registers, which XNU traps: that killed go-toolchain's published APE at
// package init. Every macOS fix downstream of it - fixAuxv's AT_HWCAP,
// syscall's /proc/self/auxv - assumes the loader ran, so this is the one
// place that can catch a kernel that stops requiring it.
//
// A kernel that refuses the file outright is a fact about this host, not a
// failure: the probe never starts, so it reports nothing to judge. Say so
// and skip rather than paint the leg red.
func TestRuntimeProbeDirectExecAuxv(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("the shell bootstrap and a direct exec differ only on macOS")
	}
	skipIfExecUnsupported(t)
	bin := copyProbeBinary(t)

	const mark = "probe-mark-direct"
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, append([]string{mark}, probeFlagArgs...)...)
	cmd.WaitDelay = 30 * time.Second
	cmd.Env = append(os.Environ(), "RUNTIMEPROBE_MARK="+mark)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	t.Logf("direct-exec runtimeprobe output:\n%s", out)
	if stderr.Len() > 0 {
		t.Logf("direct-exec runtimeprobe stderr:\n%s", stderr.String())
	}
	if err != nil && out == "" {
		t.Skipf("this kernel does not exec the APE without a shell: %v", err)
	}

	require.NoError(t, err, "runtimeprobe must exit 0 when it starts at all")
	assert.Contains(t, out, "ok auxv",
		"a directly exec'd APE must still publish an auxv, or x/sys/cpu reads the ID registers and XNU traps it")
}

func TestRuntimeProbe(t *testing.T) {
	skipIfExecUnsupported(t)
	bin := copyProbeBinary(t)

	const mark = "probe-mark-42"
	// Deadline + WaitDelay: a wedged probe (or a leaked grandchild from
	// its exec check holding the output pipes) must become a failing
	// test that still logs the partial output, never a hung CI job.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	args := append([]string{mark}, probeFlagArgs...)
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// The cosmo NT personality boots the fat APE natively through
		// its PE header; CreateProcess needs no shell. NT bring-up
		// wave 2 grew the runtime surface the probe needs (file I/O,
		// sockets, signals, os/exec, async preemption).
		cmd = exec.CommandContext(ctx, bin, args...)
	default:
		// Unix: invoke through a shell for the APE bootstrap.
		cmd = exec.CommandContext(ctx, "/bin/sh", append([]string{bin}, args...)...)
	}
	cmd.WaitDelay = 30 * time.Second
	cmd.Env = append(os.Environ(), "RUNTIMEPROBE_MARK="+mark)

	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	out := stdout.String()
	t.Logf("runtimeprobe output:\n%s", out)
	if stderr.Len() > 0 {
		t.Logf("runtimeprobe stderr:\n%s", stderr.String())
	}
	require.NoError(t, err, "runtimeprobe must exit 0")

	assert.NotContains(t, out, "FAIL", "no check may fail")
	for _, name := range probeOkChecks {
		assert.Contains(t, out, "ok "+name, "check %q must pass", name)
	}

	// The host the probe reports must be the host running it. The probe
	// can only check its own answer is one of the three; this test knows
	// which machine it is on, so it is the only place the value can be
	// held against reality - and getting it wrong is silent everywhere
	// else, because a cosmo binary reports GOOS "cosmo" on every host.
	assert.Contains(t, out, "ok hostos "+runtime.GOOS,
		"probe must report the host it is running on (%s)", runtime.GOOS)
}
