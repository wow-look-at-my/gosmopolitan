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
	"execchild",
	"executable",
	"mkdirtemp", "statdir", "create", "readback", "rename", "statsize",
	"getwd", "chdir", "wdrestore",
	"remove", "rmdir",
	"readdir", "walkdir", "removeall",
	"segvrecover", "sigterm", "sigusr2", "preempt", "waitsig",
	"all",
}

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

func TestRuntimeProbe(t *testing.T) {
	skipIfExecUnsupported(t)
	bin := copyProbeBinary(t)

	const mark = "probe-mark-42"
	// Deadline + WaitDelay: a wedged probe (or a leaked grandchild from
	// its exec check holding the output pipes) must become a failing
	// test that still logs the partial output, never a hung CI job.
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// The cosmo NT personality boots the fat APE natively through
		// its PE header; CreateProcess needs no shell. NT bring-up
		// wave 2 grew the runtime surface the probe needs (file I/O,
		// sockets, signals, os/exec, async preemption).
		cmd = exec.CommandContext(ctx, bin, mark)
	default:
		// Unix: invoke through a shell for the APE bootstrap.
		cmd = exec.CommandContext(ctx, "/bin/sh", bin, mark)
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
}
