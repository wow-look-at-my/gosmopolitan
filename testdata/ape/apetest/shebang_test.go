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

// The rest of this suite runs the APE through /bin/sh, because an MZ-headed
// APE is neither ELF nor a script and execve() therefore refuses it. These
// tests cover the -apeshebang binary, which exists so that spawning it needs
// no shell at all: the kernel's script loader accepts it, and after the first
// run it has assimilated into a native ELF and needs nothing.
//
// Every check here spawns the binary BY PATH with no interpreter, because
// that is the whole claim -- and it is what a hook runner, an LSP client, an
// MCP host or `./mytool` does.

// shebangCopy stages a private, executable copy of the shebang APE. A copy
// per test is mandatory, not hygiene: running one assimilates it in place.
func shebangCopy(t *testing.T) string {
	t.Helper()
	src := os.Getenv("FIZZBUZZ_SHEBANG_BIN")
	require.NotEmpty(t, src, "FIZZBUZZ_SHEBANG_BIN must be set (build with GOCOSMOSHEBANG=1)")

	data, err := os.ReadFile(src)
	require.NoError(t, err)
	tmp := filepath.Join(t.TempDir(), "fizzbuzz-shebang.com")
	require.NoError(t, os.WriteFile(tmp, data, 0o755))
	return tmp
}

// runDirect spawns bin by path, with no shell and no interpreter argument.
func runDirect(t *testing.T, bin string, args ...string) (string, string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 30 * time.Second
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return strings.TrimSpace(stdout.String()), strings.TrimSpace(stderr.String()), err
}

func TestShebangHeading(t *testing.T) {
	data, err := os.ReadFile(shebangCopy(t))
	require.NoError(t, err)
	require.Greater(t, len(data), 128)

	assert.Equal(t, "#!/bin/sh\n", string(data[:10]), "must open with a shebang line")
	assert.NotEqual(t, "MZ", string(data[:2]), "the MZ magic is what this mode trades away")

	nl := strings.IndexByte(string(data[:128]), '\n')
	require.Greater(t, nl, 0)
	assert.NotContains(t, string(data[:nl]), "\x00", "first line must survive the kernel's script loader")
}

// The claim, stated as a test: no shell, no launcher, no binfmt registration.
func TestShebangRunsWithoutAShell(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by TestShebangIsNotWindowsLoadable")
	}
	out, stderr, err := runDirect(t, shebangCopy(t), "10", "5")
	require.NoError(t, err, "direct execve failed; stderr=%q", stderr)
	assert.Equal(t, "fizzbuzz", out)
}

// The shell runs once, at most: the first execution rewrites the file's head
// into the host's native format, and every later spawn is an ordinary ELF
// exec. A binary that stayed a script would keep paying for /bin/sh forever.
func TestShebangAssimilatesOnFirstRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("covered by TestShebangIsNotWindowsLoadable")
	}
	bin := shebangCopy(t)

	_, stderr, err := runDirect(t, bin, "1", "2")
	require.NoError(t, err, "stderr=%q", stderr)

	data, err := os.ReadFile(bin)
	require.NoError(t, err)
	switch runtime.GOOS {
	case "darwin":
		// ARM64 macOS boots through the compiled APE loader instead of
		// assimilating; Intel macOS rewrites a Mach-O head.
		assert.NotEqual(t, "#!", string(data[:2]),
			"macOS binary neither assimilated nor kept a loadable head")
	default:
		assert.Equal(t, "\x7fELF", string(data[:4]), "first run must assimilate to native ELF")
	}

	out, stderr, err := runDirect(t, bin, "10", "5")
	require.NoError(t, err, "second (assimilated) run failed; stderr=%q", stderr)
	assert.Equal(t, "fizzbuzz", out)
}

// The negative control, and the reason the mode exists at all: the default
// heading cannot be spawned by path on unix. If this ever starts passing,
// -apeshebang has become unnecessary and should go.
func TestDefaultHeadingCannotBeSpawnedDirectly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows loads the same file through its PE header")
	}
	src := os.Getenv("FIZZBUZZ_BIN")
	require.NotEmpty(t, src)
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.Equal(t, "MZqFpD='", string(data[:8]), "FIZZBUZZ_BIN is not a pristine MZ APE")

	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	_, _, err = runDirect(t, bin)
	require.Error(t, err, "an MZ APE was spawned directly; the shell workaround is no longer needed")
	assert.Contains(t, strings.ToLower(err.Error()), "exec format error")
}

// The trade, checked rather than merely documented: no MZ means no PE image,
// so Windows cannot load this binary. A build for Windows must not use the
// mode.
func TestShebangIsNotWindowsLoadable(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-only")
	}
	_, _, err := runDirect(t, shebangCopy(t), "10", "5")
	require.Error(t, err, "a shebang APE ran on Windows; the documented trade is wrong")
}
