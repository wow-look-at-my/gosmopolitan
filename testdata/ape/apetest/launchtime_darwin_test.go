package apetest

import _ "embed"

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
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
// the process after the program. The rounds alternate the boots, so a busy
// host slows each of them alike.
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
	launch := func(script string) time.Duration {
		cmd := exec.Command("/bin/sh", script, "10", "5")
		cmd.Env = env
		start := time.Now()
		out, err := cmd.CombinedOutput()
		took := time.Since(start)
		require.NoError(t, err, "%s: %s", script, out)
		require.Equal(t, "fizzbuzz", strings.TrimSpace(string(out)), "%s printed the wrong output", script)
		return took
	}

	modes := []struct {
		name string
		run  func() time.Duration
	}{
		{"old boot", func() time.Duration {
			took := launch(old)
			copies := matches(unpacked)
			require.Len(t, copies, 1, "the old boot must unpack the loader")
			require.NoError(t, os.Remove(copies[0]))
			return took
		}},
		{"cache wiped", func() time.Duration {
			for _, path := range matches(cache) {
				require.NoError(t, os.RemoveAll(path))
			}
			took := launch(ape)
			require.Len(t, matches(entry), 1, "the first launch must put the loader in the cache")
			return took
		}},
		{"cache warm", func() time.Duration {
			if len(matches(entry)) == 0 {
				launch(ape)
			}
			return launch(ape)
		}},
	}

	// One runner's launches vary by more than the limit, so the median needs many rounds.
	const rounds = 150
	times := make([][]time.Duration, len(modes))
	for round := range rounds {
		for step := range modes {
			idx := (round + step) % len(modes)
			times[idx] = append(times[idx], modes[idx].run())
		}
	}
	median := make([]time.Duration, len(modes))
	for idx := range modes {
		slices.Sort(times[idx])
		median[idx] = times[idx][rounds/2]
	}

	millis := func(took time.Duration) float64 { return float64(took.Microseconds()) / 1000 }
	t.Logf("median per launch of %d rounds: old boot %.1f ms, cache wiped %.1f ms, cache warm %.1f ms",
		rounds, millis(median[0]), millis(median[1]), millis(median[2]))
	for idx := 1; idx < len(modes); idx++ {
		change := 100 * (float64(median[idx]) - float64(median[0])) / float64(median[0])
		t.Logf("%s against the old boot: %+.1f%%", modes[idx].name, change)
		if change > 5 {
			t.Errorf("a launch with the %s takes %.1f ms, %+.1f%% against the old boot's %.1f ms: the limit is +5%%",
				modes[idx].name, millis(median[idx]), change, millis(median[0]))
		}
	}
}
