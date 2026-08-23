//go:build !windows

package apetest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// How the kernel comes to load an APE.
//
// Every other execution in this suite goes through /bin/sh, so nothing here
// asserted that the kernel will load the binary at all -- the suite would pass
// against an artifact execve(2) refuses outright. It refuses this one: byte 0
// is 'M', and execve accepts only \x7fELF, a native Mach-O, or a #! line.
//
// The prologue closes that gap two different ways, and which one runs is a
// property of the platform, so both are pinned here:
//
//   - amd64 (either OS), and arm64 Linux with no loader installed: it rewrites
//     the file's own header in place -- printf '\177ELF...' >&7, plus a dd
//     conv=notrunc that lays a Mach-O header over it under /Applications --
//     then exec "$0". After that first run the file IS a native executable.
//   - arm64 macOS: no assimilation at all. It compiles a loader to
//     $TMPDIR/.ape-<ver> and execs the APE through it, so the file stays
//     MZ-headed and direct execution never becomes possible.
//
// shell_test.go asserts those printf/dd statements exist in the header. What
// is asserted here is that they do what they are for.
//
// Go's os/exec is a raw execve: unlike a POSIX shell (which retries an ENOEXEC
// file as a script) or glibc's execvp (which does the same), it reports the
// failure. That is what makes exec.Command a valid probe here.

const apeMagic = "MZqFpD='"

// assimilatesInPlace reports whether this platform's prologue branch rewrites
// the binary's header rather than routing through a separate loader.
func assimilatesInPlace() bool {
	return runtime.GOARCH == "amd64" || runtime.GOOS == "linux"
}

func headerOf(t *testing.T, path string, n int) string {
	t.Helper()
	f, err := os.Open(path)
	require.NoError(t, err)
	defer f.Close()

	buf := make([]byte, n)
	_, err = f.Read(buf)
	require.NoError(t, err)
	return string(buf)
}

// runDirect execs the binary with no shell anywhere in the path.
func runDirect(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.WaitDelay = 30 * time.Second
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

// runThroughShell is the ordinary invocation the rest of the suite uses. The
// path must be qualified: the prologue resolves itself with `command -v "$0"`,
// which yields nothing for a bare filename, and the run dies writing to "".
func runThroughShell(t *testing.T, bin string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	require.True(t, filepath.IsAbs(bin), "prologue needs a resolvable $0")
	cmd := exec.CommandContext(ctx, "/bin/sh", append([]string{bin}, args...)...)
	cmd.WaitDelay = 30 * time.Second
	out, err := cmd.Output()
	return strings.TrimSpace(string(out)), err
}

func TestExecveRefusesTheAPEAsShipped(t *testing.T) {
	bin := copyBinary(t)
	require.Equal(t, apeMagic, headerOf(t, bin, len(apeMagic)), "fixture is not a fresh APE")

	_, err := runDirect(t, bin, "10", "5")
	require.Error(t, err, "an MZ-headed file must not be loadable by execve")
	assert.ErrorIs(t, err, syscall.ENOEXEC)

	// A refused exec must not have touched the file: whatever makes it
	// runnable has to be the prologue, not a side effect of trying.
	assert.Equal(t, apeMagic, headerOf(t, bin, len(apeMagic)))
}

func TestAPEBecomesDirectlyExecutable(t *testing.T) {
	if !assimilatesInPlace() {
		t.Skipf("%s/%s runs the APE through a compiled loader; see TestAPEUsesALoaderInsteadOfAssimilating",
			runtime.GOOS, runtime.GOARCH)
	}
	bin := copyBinary(t)

	out, err := runThroughShell(t, bin, "10", "5")
	require.NoError(t, err)
	require.Equal(t, "fizzbuzz", out)

	// The header the prologue wrote over itself: ELF, or Mach-O where the dd
	// relocation ran. Either way it is no longer the APE magic.
	assert.NotEqual(t, apeMagic, headerOf(t, bin, len(apeMagic)),
		"the prologue must have rewritten its own header")

	got, err := runDirect(t, bin, "10", "5")
	require.NoError(t, err, "the kernel must load the assimilated binary directly")
	assert.Equal(t, "fizzbuzz", got, "and it must still be the same program")
}

func TestAPEUsesALoaderInsteadOfAssimilating(t *testing.T) {
	if assimilatesInPlace() {
		t.Skipf("%s/%s assimilates in place; see TestAPEBecomesDirectlyExecutable",
			runtime.GOOS, runtime.GOARCH)
	}
	bin := copyBinary(t)

	out, err := runThroughShell(t, bin, "10", "5")
	require.NoError(t, err)
	require.Equal(t, "fizzbuzz", out)

	// The whole point of the loader branch: the artifact is left alone, so it
	// stays the same portable file after running as it was when it shipped.
	assert.Equal(t, apeMagic, headerOf(t, bin, len(apeMagic)),
		"the loader branch must not rewrite the binary")

	_, err = runDirect(t, bin, "10", "5")
	assert.ErrorIs(t, err, syscall.ENOEXEC, "and it must still need the loader")
}
