package apetest

import (
	"os"
	"os/exec"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestLoaderReexecCost measures what the loader's second exec costs at
// launch on this host. A normal start runs the loader twice: the second run
// gets the program's argv, so ps and htop name the program. A start already
// in second-run form skips the first exec. Both boot the same payload, so
// the difference between the medians is the cost of the re-exec.
func TestLoaderReexecCost(t *testing.T) {
	skipWhereNoLoaderBoots(t)
	loader := embeddedLoader(t)
	bin := copyAPE(t)

	start := func(secondRun bool) time.Duration {
		cmd := exec.Command(loader, bin, "10", "5")
		cmd.Env = os.Environ()
		if secondRun {
			cmd.Args = []string{bin, "10", "5"}
			cmd.Env = append(cmd.Env, "APE_REEXEC=")
		}
		began := time.Now()
		out, err := cmd.CombinedOutput()
		took := time.Since(began)
		require.NoError(t, err, "output: %s", out)
		require.Equal(t, "fizzbuzz", strings.TrimSpace(string(out)))
		return took
	}

	const runs = 41
	var twice, once []time.Duration
	for range runs {
		twice = append(twice, start(false))
		once = append(once, start(true))
	}
	slices.Sort(twice)
	slices.Sort(once)
	median := func(times []time.Duration) time.Duration { return times[len(times)/2] }
	t.Logf("launch of %d runs each: median %v with the re-exec, %v without, so the re-exec costs %v; fastest %v and %v",
		runs, median(twice), median(once), median(twice)-median(once), twice[0], once[0])
}
