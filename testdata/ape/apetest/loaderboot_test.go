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

// skipWhereNoLoaderBoots skips a test on the hosts that boot an APE some
// other way. Windows maps the PE header. darwin/amd64 has no loader and
// stages a copy to put the Mach-O header at offset 0.
func skipWhereNoLoaderBoots(t *testing.T) {
	t.Helper()
	switch {
	case runtime.GOOS == "windows":
		t.Skip("the NT personality boots the APE through its PE header")
	case runtime.GOOS == "darwin" && runtime.GOARCH == "amd64":
		t.Skip("darwin/amd64 has no native loader and stages a copy")
	}
}

// residentLoader returns the loader this host already carries, or "". The
// search matches the bootstrap script's own, so a test that needs the
// unpack path can tell whether the run would ever reach it.
func residentLoader(t *testing.T) string {
	t.Helper()
	name := fmt.Sprintf("apeld-%s-%s", runtime.GOOS, runtime.GOARCH)
	for _, c := range []string{
		os.Getenv("APE_LOADER"),
		"/usr/local/lib/ape/" + name,
		"/usr/lib/ape/" + name,
	} {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return c
		}
	}
	for _, n := range []string{name, "apeld", "ape"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

// apeRunBaseDir is where darwin/amd64 stages its copy. Every other host
// must leave this directory alone.
func apeRunBaseDir(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("/tmp/.ape-run-1-%d", os.Getuid())
}

// runAPE runs bin with env appended to the caller's environment.
func runAPE(t *testing.T, bin string, env []string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	shellArgs := append([]string{bin}, args...)
	cmd := commandForAPE(ctx, bin, shellArgs, args)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	require.NoError(t, cmd.Run(), "stderr: %s", errOut.String())
	return strings.TrimSpace(out.String())
}

// entrySet snapshots the paths directly under dir. Callers diff a
// before/after pair rather than trust an absolute count, because these
// directories are shared with every other test and run on the host.
func entrySet(t *testing.T, dir string) map[string]bool {
	t.Helper()
	entries, err := filepath.Glob(filepath.Join(dir, "*"))
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

// copyAPE puts the binary under test in a directory of this test's own, so
// a run cannot key to whatever another test left behind.
func copyAPE(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)
	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))
	return bin
}

// The APE itself is never written, on any host. That is the whole point: it
// is often on a read-only path, its checksum is what a consumer verified,
// and one platform's header would cost it every other platform.
func TestRunningLeavesTheAPEAlone(t *testing.T) {
	bin := copyAPE(t)
	data, err := os.ReadFile(bin)
	require.NoError(t, err)

	runAPE(t, bin, nil, "10", "5")

	after, err := os.ReadFile(bin)
	require.NoError(t, err)
	assert.Equal(t, data, after, "the APE must be byte-identical after running")
}

// Nothing the size of the program is written anywhere. A loader reads the
// APE where it lies, and a staged copy costs the program's own megabytes.
func TestNoCopyOfTheProgramIsMade(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	bin := copyAPE(t)

	before := entrySet(t, apeRunBaseDir(t))
	runAPE(t, bin, nil, "10", "5")

	assert.Empty(t, newEntries(before, entrySet(t, apeRunBaseDir(t))),
		"a loader boot must stage nothing under the run directory")
}

// The loader is unpacked once per host, not once per program: it is the
// same file for every APE of this architecture, and its name is its own
// content hash. A second run must find it and leave it alone.
func TestTheLoaderIsUnpackedOnceAndReused(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	if p := residentLoader(t); p != "" {
		t.Skipf("%s boots the APE, so nothing is unpacked", p)
	}
	dir := t.TempDir()
	bin := copyAPE(t)

	runAPE(t, bin, []string{"APE_LOADERDIR=" + dir}, "10", "5")
	unpacked := newEntries(nil, entrySet(t, dir))
	require.Len(t, unpacked, 1, "the first run unpacks one loader")
	first, err := os.Stat(unpacked[0])
	require.NoError(t, err)

	// A second program, so only the loader can be what is reused.
	other := copyAPE(t)
	runAPE(t, other, []string{"APE_LOADERDIR=" + dir}, "10", "5")

	assert.Len(t, newEntries(nil, entrySet(t, dir)), 1, "no second loader")
	after, err := os.Stat(unpacked[0])
	require.NoError(t, err)
	assert.Equal(t, first.ModTime(), after.ModTime(), "the loader was not written again")
}

// The property this whole design exists for: with a loader the host already
// carries, an APE starts without writing anything at all. APE_LOADERDIR
// names a directory that cannot be created, so a run that reached the
// unpack path would fail instead of quietly writing.
func TestAResidentLoaderNeedsNothingWritable(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	bin := copyAPE(t)

	loader := residentLoader(t)
	if loader == "" {
		dir := t.TempDir()
		runAPE(t, bin, []string{"APE_LOADERDIR=" + dir}, "10", "5")
		unpacked := newEntries(nil, entrySet(t, dir))
		require.Len(t, unpacked, 1, "the first run unpacks one loader to point the second at")
		loader = unpacked[0]
	}

	// mkdir under /proc fails on every Linux and every macOS this runs on,
	// so a run that reached the unpack path would fail rather than write.
	env := []string{
		"APE_LOADER=" + loader,
		"APE_LOADERDIR=/proc/ape-loaderdir-that-cannot-exist",
	}
	assert.Equal(t, runAPE(t, bin, nil, "10", "5"), runAPE(t, bin, env, "10", "5"),
		"the run through the resident loader must produce the same output")
}

// A rebuilt binary must not run what an earlier build left staged. Only
// darwin/amd64 stages at all, and its key reads the mtime to the
// nanosecond because a build loop rewrites in place inside one second.
func TestStagedCopyFollowsASameSecondRewrite(t *testing.T) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "amd64" {
		t.Skip("only darwin/amd64 stages a copy")
	}
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)

	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	before := entrySet(t, apeRunBaseDir(t))
	first := runAPE(t, bin, nil, "10", "5")
	require.Len(t, newEntries(before, entrySet(t, apeRunBaseDir(t))), 1, "the first run stages one copy")

	// Rewrite in place, same bytes and same size, immediately: only the
	// mtime moves, and on a one-second clock it would not move at all.
	require.NoError(t, os.WriteFile(bin, data, 0o755))
	second := runAPE(t, bin, nil, "10", "5")

	assert.Equal(t, first, second, "the rewritten binary still runs")
	assert.Len(t, newEntries(before, entrySet(t, apeRunBaseDir(t))), 2,
		"the rewrite must key to a copy of its own, not to the one already staged")
}
