package apetest

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cgoProbeChecks are the check names testdata/cgoprobe emits; keep in sync.
var cgoProbeChecks = []string{"add", "printf", "errno", "callback", "thread", "concurrent"}

// TestCgoProbe runs testdata/cgoprobe, a fat APE whose payloads carry C code
// linked against libcosmo. On Windows libcosmo starts at the PE entry and
// calls the Go runtime, as its _start does on Unix.
func TestCgoProbe(t *testing.T) {
	skipIfExecUnsupported(t)
	src := os.Getenv("CGOPROBE_BIN")
	if src == "" {
		t.Skip("CGOPROBE_BIN not set; skipping the cgo probe")
	}

	// Run a pristine copy: executing an APE self-assimilates it in place.
	bin := filepath.Join(t.TempDir(), "cgoprobe.com")
	data, err := os.ReadFile(src)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(bin, data, 0755))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	cmd := commandForAPE(ctx, bin, []string{bin}, nil)
	cmd.WaitDelay = 30 * time.Second
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err = cmd.Run()
	out := stdout.String()
	t.Logf("cgoprobe output:\n%s", out)
	if stderr.Len() > 0 {
		t.Logf("cgoprobe stderr:\n%s", stderr.String())
	}
	require.NoError(t, err, "cgoprobe must exit 0")

	assert.NotContains(t, out, "FAIL", "no check may fail")
	for _, name := range cgoProbeChecks {
		assert.Contains(t, out, "ok "+name+":", "check %q must pass", name)
	}
	assert.Contains(t, out, "c printf: hello 42", "C stdio must reach stdout")
	// This host runs its own architecture's payload, never an emulated one.
	assert.Contains(t, out, "arch "+runtime.GOARCH, "the %s payload must run here", runtime.GOARCH)
}
