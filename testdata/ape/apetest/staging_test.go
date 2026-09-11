package apetest

import (
	"context"
	"fmt"
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

// apeRunBaseDir is the fixed, uid-scoped directory every APE stages into:
// staging reads no environment variable at all, so there is no longer a way
// to redirect it per test. os.Getuid() matches the shell script's own
// $(id -u); on windows nothing stages here at all (skipWhereNothingIsStaged
// covers that build's tests, but this helper still needs a caller guard
// since not every test that uses it calls that first).
func apeRunBaseDir(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("no /tmp staging on windows")
	}
	return fmt.Sprintf("/tmp/.ape-run-1-%d", os.Getuid())
}

// runStaged runs bin. The bootstrap script stages the copy it runs under
// apeRunBaseDir(t), a fixed location shared by every test and every run on
// the host -- staging does not read HOME or TMPDIR, so nothing here can
// redirect it into a private directory.
func runStaged(t *testing.T, bin string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	shellArgs := append([]string{bin}, args...)
	cmd := commandForAPE(ctx, bin, shellArgs, args)
	cmd.Env = os.Environ()
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	require.NoError(t, cmd.Run(), "stderr: %s", errOut.String())
	return strings.TrimSpace(out.String())
}

// stagedCopySet snapshots the per-identity directories currently staged
// under apeRunBaseDir(t), as a set. Callers diff a before/after pair (see
// newEntries) rather than trust an absolute count, because the directory is
// shared with every other test and run on the host, not private to one.
func stagedCopySet(t *testing.T) map[string]bool {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(apeRunBaseDir(t), "*"))
	require.NoError(t, err)
	set := make(map[string]bool, len(entries))
	for _, e := range entries {
		set[e] = true
	}
	return set
}

// newEntries returns the paths present in after but not before.
func newEntries(before, after map[string]bool) []string {
	var out []string
	for e := range after {
		if !before[e] {
			out = append(out, e)
		}
	}
	return out
}

// A rebuild that lands in the same second as the last one, in place, at the
// same size, must not run the copy staged for its predecessor. Seconds are not
// enough to tell those two files apart, which is why the key reads the mtime
// to the nanosecond -- and a build loop produces exactly this pattern.
func TestStagedCopyFollowsASameSecondRewrite(t *testing.T) {
	skipWhereNothingIsStaged(t)
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	before := stagedCopySet(t)
	first := runStaged(t, bin, "10", "5")
	afterFirst := stagedCopySet(t)
	require.Len(t, newEntries(before, afterFirst), 1, "the first run stages one copy")

	// Rewrite in place, same bytes and same size, immediately: only the mtime
	// moves, and on a one-second clock it would not move at all.
	require.NoError(t, os.WriteFile(bin, data, 0o755))
	second := runStaged(t, bin, "10", "5")
	afterSecond := stagedCopySet(t)

	assert.Equal(t, first, second, "the rewritten binary still runs")
	assert.Len(t, newEntries(before, afterSecond), 2, "the rewrite must key to a copy of its own, not to the one already staged")
}

// Every run reuses the staged copy: an unchanged binary must not be copied
// again.
func TestStagedCopyIsReused(t *testing.T) {
	skipWhereNothingIsStaged(t)
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	before := stagedCopySet(t)
	runStaged(t, bin, "10", "5")
	staged := newEntries(before, stagedCopySet(t))
	require.Len(t, staged, 1)
	before2, err := os.Stat(filepath.Join(staged[0], "fizzbuzz.com"))
	require.NoError(t, err)

	runStaged(t, bin, "10", "5")

	after, err := os.Stat(filepath.Join(staged[0], "fizzbuzz.com"))
	require.NoError(t, err)
	assert.Len(t, newEntries(before, stagedCopySet(t)), 1, "no second copy")
	assert.Equal(t, before2.ModTime(), after.ModTime(), "the copy was not written again")
}

// The APE itself is never written, on any host. That is the whole point: it is
// often on a read-only path, its checksum is what a consumer verified, and one
// platform's header would cost it every other platform.
func TestRunningLeavesTheAPEAlone(t *testing.T) {
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	runStaged(t, bin, "10", "5")

	after, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, data, after, "the APE must be byte-identical after running")
}

// An APE reached through a symlink keys on what the link POINTS AT. Both stat
// implementations lstat by default, so the identity used to describe the link
// itself: an inode and an mtime that a rebuild of the target never moves. The
// staged copy from the very first run then answered every later one, and a
// tool installed as ~/.local/bin/<name> ran its original build for ever.
func TestStagedCopyFollowsTheSymlinkTarget(t *testing.T) {
	skipWhereNothingIsStaged(t)
	if runtime.GOOS == "windows" {
		t.Skip("no symlink without a privilege on NT")
	}
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	dir := t.TempDir()
	target := filepath.Join(dir, "fizzbuzz.com")
	require.NoError(t, os.WriteFile(target, data, 0o755))

	// The link is what gets run, exactly as a $PATH install would be.
	link := filepath.Join(dir, "fizzbuzz-link.com")
	require.NoError(t, os.Symlink(target, link))

	before := stagedCopySet(t)
	first := runStaged(t, link, "10", "5")
	require.Len(t, newEntries(before, stagedCopySet(t)), 1, "the first run through the link stages one copy")

	linkStat, err := os.Lstat(link)
	require.NoError(t, err)

	// Rebuild what the link points at. The link is untouched, so an identity
	// read off the link cannot tell this apart from the run above.
	require.NoError(t, os.WriteFile(target, data, 0o755))
	second := runStaged(t, link, "10", "5")

	afterStat, err := os.Lstat(link)
	require.NoError(t, err)
	require.Equal(t, linkStat.ModTime(), afterStat.ModTime(), "the link itself never changed, which is the whole trap")

	assert.Equal(t, first, second, "the rebuilt binary still runs through the link")
	assert.Len(t, newEntries(before, stagedCopySet(t)), 2,
		"the rebuild must stage a copy of its own: one copy means the link's own inode keyed it and the first build runs for ever")
}
