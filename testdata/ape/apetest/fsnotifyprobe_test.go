package apetest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fsnotifyProbeChecks are the check names fsnotifyprobe.go emits; keep in sync.
var fsnotifyProbeChecks = []string{"newwatcher", "add", "create", "write", "rename", "renamed", "remove", "close", "all"}

// TestFsnotifyProbe runs testdata/fsnotifyprobe, a real fsnotify program. Every
// host runs it, so an unset FSNOTIFYPROBE_BIN is a failure.
func TestFsnotifyProbe(t *testing.T) {
	src := os.Getenv("FSNOTIFYPROBE_BIN")
	require.NotEmpty(t, src, "FSNOTIFYPROBE_BIN must name the fsnotify probe")

	// Run a pristine copy: executing an APE self-assimilates it in place.
	bin := filepath.Join(t.TempDir(), "fsnotifyprobe.com")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bin, data, 0755))

	out, err := runAPEErr(t, bin, nil)
	t.Logf("fsnotifyprobe output:\n%s", out)
	require.NoError(t, err, "fsnotifyprobe must exit 0")
	assert.NotContains(t, out, "FAIL")
	for _, name := range fsnotifyProbeChecks {
		assert.Contains(t, out, "ok "+name, "check %q must pass", name)
	}
}
