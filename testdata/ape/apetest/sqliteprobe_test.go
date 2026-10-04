package apetest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sqliteProbeChecks are the check names sqliteprobe.go emits; keep in sync.
var sqliteProbeChecks = []string{"open", "wal", "create", "insert", "readback", "exclusion", "release", "all"}

// TestSQLiteProbe runs testdata/sqliteprobe, a real modernc.org/sqlite
// program. Its libc makes bare syscalls with Linux-shaped arguments, which
// is how a cosmo translation that only the syscall package's wrappers get
// right reaches a consumer.
func TestSQLiteProbe(t *testing.T) {
	skipIfExecUnsupported(t)
	src := os.Getenv("SQLITEPROBE_BIN")
	if src == "" {
		t.Skip("SQLITEPROBE_BIN not set; skipping the SQLite probe")
	}

	// Run a pristine copy: executing an APE self-assimilates it in place.
	bin := filepath.Join(t.TempDir(), "sqliteprobe.com")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bin, data, 0755))

	out, err := runAPEErr(t, bin, nil)
	t.Logf("sqliteprobe output:\n%s", out)
	require.NoError(t, err, "sqliteprobe must exit 0")
	assert.NotContains(t, out, "FAIL")
	for _, name := range sqliteProbeChecks {
		assert.Contains(t, out, "ok "+name, "check %q must pass", name)
	}
}
