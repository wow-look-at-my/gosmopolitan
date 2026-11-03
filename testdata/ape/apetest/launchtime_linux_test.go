package apetest

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// buildLinuxLoader compiles a Linux loader source with the host's cc, with
// the flags cmd/dist gives zig, and returns the binary's path.
func buildLinuxLoader(t *testing.T, cc, source, name string) string {
	t.Helper()
	script, err := filepath.Abs("../../../src/cmd/link/internal/ld/apeld/linux/apeld.ld")
	require.NoError(t, err)
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command(cc, "-Os", "-fno-stack-protector", "-fno-unwind-tables", "-fno-asynchronous-unwind-tables",
		"-nostdlib", "-ffreestanding", "-static", "-fno-pic", "-fno-pie",
		"-Wl,--gc-sections", "-Wl,--build-id=none", "-Wl,-z,norelro", "-Wl,-T,"+script, "-s",
		"-o", out, source)
	built, err := cmd.CombinedOutput()
	require.NoError(t, err, "%s %s: %s", cc, source, built)
	return out
}

// The Linux loader moves the program's argv over its own and names the
// process with prctl, so ps, top, htop and btop show the program. The loader,
// testdata/old-apeld-linux.c, did neither. The same compiler builds both, and
// the rounds alternate them, so a busy host slows each alike.
func TestLinuxLaunchIsNoSlowerThanTheOldLoader(t *testing.T) {
	cc, err := exec.LookPath("cc")
	require.NoError(t, err, "the test builds both loaders with the host's cc")
	current, err := filepath.Abs("../../../src/cmd/link/internal/ld/apeld/linux/apeld.c")
	require.NoError(t, err)
	loaders := []struct {
		name string
		path string
	}{
		{"old loader", buildLinuxLoader(t, cc, "testdata/old-apeld-linux.c", "old")},
		{"current loader", buildLinuxLoader(t, cc, current, "current")},
	}
	ape := copyAPE(t)
	launch := func(loader string) time.Duration {
		cmd := exec.Command("/bin/sh", ape, "10", "5")
		cmd.Env = append(os.Environ(), "APE_LOADER="+loader)
		start := time.Now()
		out, err := cmd.CombinedOutput()
		took := time.Since(start)
		require.NoError(t, err, "%s: %s", loader, out)
		require.Equal(t, "fizzbuzz", strings.TrimSpace(string(out)), "%s printed the wrong output", loader)
		return took
	}

	// One runner's launches vary by more than the limit, so the median needs many rounds.
	const rounds = 300
	times := make([][]time.Duration, len(loaders))
	for round := range rounds {
		for step := range loaders {
			idx := (round + step) % len(loaders)
			times[idx] = append(times[idx], launch(loaders[idx].path))
		}
	}
	median := make([]time.Duration, len(loaders))
	for idx := range loaders {
		slices.Sort(times[idx])
		median[idx] = times[idx][rounds/2]
	}
	millis := func(took time.Duration) float64 { return float64(took.Microseconds()) / 1000 }
	change := 100 * (float64(median[1]) - float64(median[0])) / float64(median[0])
	t.Logf("median per launch of %d rounds: old loader %.2f ms, current loader %.2f ms, %+.1f%%",
		rounds, millis(median[0]), millis(median[1]), change)
	if change > 5 {
		t.Errorf("a launch through the current loader takes %.2f ms, %+.1f%% against the old loader's %.2f ms: the limit is +5%%",
			millis(median[1]), change, millis(median[0]))
	}
}
