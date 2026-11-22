package apetest

import _ "embed"

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// oldDarwinSection is the darwin boot before the cache: search the host, unpack the loader, test-run it, then exec it.
//
//go:embed testdata/old-darwin-section.sh
var oldDarwinSection string

// oldDarwinBoot writes a copy of the APE's script with the darwin section
// replaced by oldDarwinSection. Every other line stays the same. The loader
// is this APE's own. It runs without -u, and the test removes the copy
// outside the timed launch.
func oldDarwinBoot(t *testing.T, ape string) string {
	t.Helper()
	data, err := os.ReadFile(ape)
	require.NoError(t, err)
	head := string(data[:min(len(data), elfOffset)])
	require.Contains(t, head, "apereg()", "the script must define apereg, which the old section calls")

	const startMark, endMark = "  l=apeld-darwin-arm64\n", "  exit 121\n"
	start := strings.Index(head, startMark)
	require.NotEqual(t, -1, start, "the script has no darwin section")
	end := strings.Index(head[start:], endMark)
	require.NotEqual(t, -1, end, "the darwin section has no end")
	end += start + len(endMark)

	// The APE carried the loader gzipped, so the boot unpacks a gzipped copy.
	var packed bytes.Buffer
	zw, err := gzip.NewWriterLevel(&packed, gzip.BestCompression)
	require.NoError(t, err)
	_, err = zw.Write(darwinLoader(t))
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	tmp := t.TempDir()
	gz := filepath.Join(tmp, "apeld-darwin-arm64.gz")
	require.NoError(t, os.WriteFile(gz, packed.Bytes(), 0o644))
	section := strings.NewReplacer("@APE@", ape, "@GZ@", gz).Replace(oldDarwinSection)

	script := filepath.Join(tmp, "oldboot.sh")
	require.NoError(t, os.WriteFile(script, []byte(head[:start]+section+string(data[end:])), 0o755))
	return script
}

// The darwin boot runs the loader from a per-user cache, so that XNU names
// the process after the program. A launch is a fixed amount of work, and a
// busy host adds waiting to it and not work, so each boot is judged by the CPU
// time its launch spends over many rounds, and its wall clock is logged beside it.
func TestDarwinLaunchIsNoSlowerThanTheOldBoot(t *testing.T) {
	if loader := residentLoader(t); loader != "" {
		t.Fatalf("%s is on this host: the old boot would exec it and never unpack", loader)
	}
	dir := t.TempDir()
	// The cache lives only under a sticky directory, as /tmp is.
	require.NoError(t, os.Chmod(dir, 0o700|os.ModeSticky))
	ape := copyAPE(t)
	old := oldDarwinBoot(t, ape)
	env := append(os.Environ(), "APE_LOADER=", "APE_LOADERDIR="+dir)

	cache := filepath.Join(dir, fmt.Sprintf(".ape-apeld-darwin-arm64-*-%d", os.Getuid()))
	entry := filepath.Join(cache, filepath.Base(ape))
	unpacked := filepath.Join(dir, ".ape-apeld-darwin-arm64-00000000.*")
	matches := func(pattern string) []string {
		found, err := filepath.Glob(pattern)
		require.NoError(t, err)
		return found
	}
	// A launch's wall clock carries whatever else the runner had going. The CPU
	// time of its process tree is the work the boot did: the shell forks dd and
	// gzip, and the last exec runs the loader in the process the shell left, so
	// the kernel charges the whole launch to this one.
	type launchCost struct{ wall, cpu time.Duration }
	launch := func(script string) launchCost {
		cmd := exec.Command("/bin/sh", script, "10", "5")
		cmd.Env = env
		start := time.Now()
		out, err := cmd.CombinedOutput()
		wall := time.Since(start)
		require.NoError(t, err, "%s: %s", script, out)
		require.Equal(t, "fizzbuzz", strings.TrimSpace(string(out)), "%s printed the wrong output", script)
		usage, ok := cmd.ProcessState.SysUsage().(*syscall.Rusage)
		require.True(t, ok, "%s: the host reports no rusage for the launch", script)
		cpu := time.Duration(usage.Utime.Nano() + usage.Stime.Nano())
		require.NotZero(t, cpu, "%s: the host reports no CPU time for the launch", script)
		return launchCost{wall: wall, cpu: cpu}
	}

	modes := []struct {
		name string
		run  func() launchCost
	}{
		{"old boot", func() launchCost {
			took := launch(old)
			copies := matches(unpacked)
			require.Len(t, copies, 1, "the old boot must unpack the loader")
			require.NoError(t, os.Remove(copies[0]))
			return took
		}},
		{"cache wiped", func() launchCost {
			for _, path := range matches(cache) {
				require.NoError(t, os.RemoveAll(path))
			}
			took := launch(ape)
			require.Len(t, matches(entry), 1, "the first launch must put the loader in the cache")
			return took
		}},
		{"cache warm", func() launchCost {
			if len(matches(entry)) == 0 {
				launch(ape)
			}
			return launch(ape)
		}},
	}

	// One runner's launches vary by more than the limit, so each mode needs many rounds.
	const rounds = 150
	costs := make([][]launchCost, len(modes))
	for round := range rounds {
		for step := range modes {
			idx := (round + step) % len(modes)
			costs[idx] = append(costs[idx], modes[idx].run())
		}
	}
	// Every mode launches once in each round, one after another, so a round's
	// ratio holds the host of that moment, and the median of the rounds is what a
	// launch of the mode costs against the old boot on this host. The host's speed
	// drifts and its queue fills, and neither moves this ratio, which is why the
	// verdict reads it and not one mode's launch against another mode's.
	ratios := make([][]float64, len(modes))
	for round := range rounds {
		for idx := 1; idx < len(modes); idx++ {
			ratios[idx] = append(ratios[idx], float64(costs[idx][round].cpu)/float64(costs[0][round].cpu))
		}
	}
	cpu := make([]time.Duration, len(modes))
	wall := make([]time.Duration, len(modes))
	fastestWall := make([]time.Duration, len(modes))
	for idx := range modes {
		byCPU := slices.Clone(costs[idx])
		slices.SortFunc(byCPU, func(a, b launchCost) int { return cmp.Compare(a.cpu, b.cpu) })
		cpu[idx] = byCPU[rounds/2].cpu
		byWall := slices.Clone(costs[idx])
		slices.SortFunc(byWall, func(a, b launchCost) int { return cmp.Compare(a.wall, b.wall) })
		fastestWall[idx] = byWall[0].wall
		wall[idx] = byWall[rounds/2].wall
	}

	millis := func(took time.Duration) float64 { return float64(took.Microseconds()) / 1000 }
	t.Logf("median CPU per launch of %d rounds: old boot %.2f ms, cache wiped %.2f ms, cache warm %.2f ms",
		rounds, millis(cpu[0]), millis(cpu[1]), millis(cpu[2]))
	t.Logf("median wall clock per launch of %d rounds: old boot %.1f ms, cache wiped %.1f ms, cache warm %.1f ms",
		rounds, millis(wall[0]), millis(wall[1]), millis(wall[2]))
	t.Logf("fastest wall clock launch of %d rounds: old boot %.1f ms, cache wiped %.1f ms, cache warm %.1f ms",
		rounds, millis(fastestWall[0]), millis(fastestWall[1]), millis(fastestWall[2]))
	for idx := 1; idx < len(modes); idx++ {
		slices.Sort(ratios[idx])
		change := 100 * (ratios[idx][rounds/2] - 1)
		if change > 5 {
			t.Errorf("a launch with the %s spends %+.1f%% of the old boot's CPU time, median of %d rounds: the limit is +5%%",
				modes[idx].name, change, rounds)
		}
	}
}
