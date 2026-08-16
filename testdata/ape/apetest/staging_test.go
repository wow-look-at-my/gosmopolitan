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

// skipWhereNothingIsStaged skips a test on the hosts that boot an APE some
// other way. Windows maps the PE header, and macOS ARM64 compiles the embedded
// loader and hands it the original file. Neither stages anything. A Linux
// ARM64 host with the `ape` loader installed takes that same route.
func skipWhereNothingIsStaged(t *testing.T) {
	t.Helper()
	switch {
	case runtime.GOOS == "windows":
		t.Skip("the NT personality boots the APE through its PE header")
	case runtime.GOOS == "darwin" && runtime.GOARCH == "arm64":
		t.Skip("macOS ARM64 compiles the embedded loader and runs the APE through it")
	case runtime.GOARCH == "arm64":
		if _, err := exec.LookPath("ape"); err == nil {
			t.Skip("an installed ape loader runs the APE as it stands")
		}
	}
}

// runStaged runs bin with the run directory pointed at home, which is where
// the bootstrap script stages the copy it runs.
func runStaged(t *testing.T, home, bin string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	shellArgs := append([]string{bin}, args...)
	cmd := commandForAPE(ctx, bin, shellArgs, args)
	// Both, and both real: the script reads TMPDIR first, and a compiler the
	// script may shell out to reads it as well.
	cmd.Env = append(os.Environ(), "HOME="+home, "TMPDIR="+home)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	require.NoError(t, cmd.Run(), "stderr: %s", errOut.String())
	return strings.TrimSpace(out.String())
}

// stagedCopies lists the per-identity directories the script has staged under
// home.
func stagedCopies(t *testing.T, home string) []string {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(home, ".ape-run-1", "*"))
	require.NoError(t, err)
	return entries
}

// A rebuild that lands in the same second as the last one, in place, at the
// same size, must not run the copy staged for its predecessor. Seconds are not
// enough to tell those two files apart, which is why the key reads the mtime
// to the nanosecond -- and a build loop produces exactly this pattern.
func TestStagedCopyFollowsASameSecondRewrite(t *testing.T) {
	skipWhereNothingIsStaged(t)
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	first := runStaged(t, home, bin, "10", "5")
	require.Len(t, stagedCopies(t, home), 1, "the first run stages one copy")

	// Rewrite in place, same bytes and same size, immediately: only the mtime
	// moves, and on a one-second clock it would not move at all.
	require.NoError(t, os.WriteFile(bin, data, 0o755))
	second := runStaged(t, home, bin, "10", "5")

	assert.Equal(t, first, second, "the rewritten binary still runs")
	assert.Len(t, stagedCopies(t, home), 2, "the rewrite must key to a copy of its own, not to the one already staged")
}

// Every run reuses the staged copy: an unchanged binary must not be copied
// again.
func TestStagedCopyIsReused(t *testing.T) {
	skipWhereNothingIsStaged(t)
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	runStaged(t, home, bin, "10", "5")
	staged := stagedCopies(t, home)
	require.Len(t, staged, 1)
	before, err := os.Stat(filepath.Join(staged[0], "fizzbuzz.com"))
	require.NoError(t, err)

	runStaged(t, home, bin, "10", "5")

	after, err := os.Stat(filepath.Join(staged[0], "fizzbuzz.com"))
	require.NoError(t, err)
	assert.Len(t, stagedCopies(t, home), 1, "no second copy")
	assert.Equal(t, before.ModTime(), after.ModTime(), "the copy was not written again")
}

// The APE itself is never written, on any host. That is the whole point: it is
// often on a read-only path, its checksum is what a consumer verified, and one
// platform's header would cost it every other platform.
func TestRunningLeavesTheAPEAlone(t *testing.T) {
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	home := t.TempDir()
	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	runStaged(t, home, bin, "10", "5")

	after, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, data, after, "the APE must be byte-identical after running")
}
