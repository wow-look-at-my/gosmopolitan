package apetest

import (
	"bytes"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// skipWhereNoLoaderBoots skips a test on the one host that boots an APE
// some other way: Windows maps the payload through the PE header.
func skipWhereNoLoaderBoots(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the NT personality boots the APE through its PE header")
	}
}

// embeddedLoader writes this host's loader out of the APE and returns its
// path. The script reads the same region with dd, at the offset it names. The
// darwin loader is the gzipped one, and the linux loader is not, which is what
// tells them apart in a fat file.
//
// The name is the loader's own, because a darwin ad-hoc signature names the
// file it signed.
func embeddedLoader(t *testing.T) string {
	t.Helper()
	head := first8K(t)
	re := regexp.MustCompile(`dd if="\$o" bs=1 skip=(\d+) count=(\d+) 2>/dev/null (\| gzip -dc )?>`)
	bin := loadBinary(t)
	for _, m := range re.FindAllSubmatch(head, -1) {
		gzipped := len(m[3]) != 0
		if gzipped != (runtime.GOOS == "darwin") {
			continue
		}
		skip, err := strconv.Atoi(string(m[1]))
		require.NoError(t, err)
		count, err := strconv.Atoi(string(m[2]))
		require.NoError(t, err)
		require.LessOrEqual(t, skip+count, len(bin), "the loader region must be inside the file")

		raw := bin[skip : skip+count]
		if gzipped {
			gz, err := gzip.NewReader(bytes.NewReader(raw))
			require.NoError(t, err)
			raw, err = io.ReadAll(gz)
			require.NoError(t, err)
		}
		path := filepath.Join(t.TempDir(), fmt.Sprintf("apeld-%s-%s", runtime.GOOS, runtime.GOARCH))
		require.NoError(t, os.WriteFile(path, raw, 0o755))
		return path
	}
	t.Fatalf("the bootstrap script carries no loader for %s/%s", runtime.GOOS, runtime.GOARCH)
	return ""
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

// apeRunBaseDir is the directory a staged copy of the program would land
// in. Nothing writes there, and TestNoCopyOfTheProgramIsMade says so.
func apeRunBaseDir(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("/tmp/.ape-run-1-%d", os.Getuid())
}

// runAPE runs bin with env appended to the caller's environment.
func runAPE(t *testing.T, bin string, env []string, args ...string) string {
	t.Helper()
	out, err := runAPEErr(t, bin, env, args...)
	require.NoError(t, err, "output: %s", out)
	return out
}

// runAPEErr is runAPE for a case that expects the run to fail.
func runAPEErr(t *testing.T, bin string, env []string, args ...string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	shellArgs := append([]string{bin}, args...)
	cmd := commandForAPE(ctx, bin, shellArgs, args)
	cmd.Env = append(os.Environ(), env...)
	var out, errOut strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errOut
	err := cmd.Run()
	if err != nil {
		return strings.TrimSpace(errOut.String()), err
	}
	return strings.TrimSpace(out.String()), nil
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

// The unpacked loader does not survive its own run. The script passes -u, so
// the loader unlinks its own file before the payload starts. An APE is one
// file, and running it must not leave a second one on the host.
func TestTheUnpackedLoaderDoesNotSurvive(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	if p := residentLoader(t); p != "" {
		t.Skipf("%s boots the APE, so nothing is unpacked", p)
	}
	dir := t.TempDir()
	bin := copyAPE(t)

	// An empty directory proves nothing on its own: a host whose kernel starts
	// the APE by itself never reaches the unpack at all. So first require that
	// taking the unpack away breaks the run. That is what says this host needs
	// it, and that the check below is about the loader deleting itself.
	if _, err := runAPEErr(t, bin, []string{"APE_LOADERDIR=/proc/ape-loaderdir-that-cannot-exist"}, "10", "5"); err == nil {
		t.Skip("this host starts the APE without the unpack path")
	}

	// Twice, because a run that reused a leftover would pass the first check
	// by never unpacking again.
	for i := range 2 {
		runAPE(t, bin, []string{"APE_LOADERDIR=" + dir}, "10", "5")
		assert.Empty(t, newEntries(nil, entrySet(t, dir)),
			"run %d left a loader behind in the unpack directory", i+1)
	}
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
		loader = embeddedLoader(t)
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

// A rebuilt binary runs its own bytes. Staging keyed a copy by the file's
// identity to get this right, and got it wrong whenever a build loop
// rewrote in place inside one second. A loader reads the file every time,
// so the question does not arise.
func TestARebuiltBinaryRunsItsOwnBytes(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	bin := copyAPE(t)

	first := runAPE(t, bin, nil, "10", "5")
	require.Equal(t, "fizzbuzz", first)

	// Rewritten in place, at the same size, inside the same second: the
	// case a staged copy keyed wrong.
	data, err := os.ReadFile(binPath(t))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bin, data, 0o755))

	assert.Equal(t, first, runAPE(t, bin, nil, "10", "5"),
		"the run after an in-place rewrite must still be this file's")
}
