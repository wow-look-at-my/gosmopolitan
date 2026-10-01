package apetest

import (
	"debug/elf"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// requireSidecarChecks skips unless APE_REQUIRE_SIDECARS is set: these tests
// assert a just-built binary's sidecars, which only exist on the build
// runner before upload strips them (see docs/CI.md "build job"). Skip by
// default so a bare `go test ./...` against a downloaded artifact -- where
// missing sidecars are normal, not a bug -- never fails here.
func requireSidecarChecks(t *testing.T) {
	t.Helper()
	if os.Getenv("APE_REQUIRE_SIDECARS") == "" {
		t.Skip("APE_REQUIRE_SIDECARS not set; this build-time invariant only applies right after a build")
	}
}

func assertSidecarELF(t *testing.T, path string, machine elf.Machine) {
	t.Helper()
	f, err := elf.Open(path)
	require.NoError(t, err, "%s must exist and be a readable ELF", path)
	defer f.Close()
	assert.Equal(t, machine, f.Machine, "%s machine type", path)
}

// TestFatSidecarsExist requires the debug sidecar a default fat build must
// write next to FIZZBUZZ_BIN and RUNTIMEPROBE_BIN, and requires that the
// arm64 image leaves nothing beside it.
func TestFatSidecarsExist(t *testing.T) {
	requireSidecarChecks(t)
	for _, bin := range []string{binPath(t), os.Getenv("RUNTIMEPROBE_BIN")} {
		require.NotEmpty(t, bin, "RUNTIMEPROBE_BIN must be set alongside FIZZBUZZ_BIN")
		assertSidecarELF(t, bin+".dbg", elf.EM_X86_64)
		_, err := os.Stat(bin + ".aarch64.elf")
		assert.True(t, os.IsNotExist(err), "%s.aarch64.elf must not exist: the arm64 image gets no sidecar", bin)
	}
}

// TestSlimSidecarsExist requires a platform-subset build to write the amd64
// sidecar when it carries that payload and nothing when it does not. An
// arm64-only build writes no sidecar at all.
func TestSlimSidecarsExist(t *testing.T) {
	requireSidecarChecks(t)
	sel := slimPlatforms(t)
	bin := slimPath(t)

	if slimWantsArch(sel, "amd64") {
		assertSidecarELF(t, bin+".dbg", elf.EM_X86_64)
	} else if _, err := os.Stat(bin + ".dbg"); err == nil {
		t.Errorf("%s.dbg exists but amd64 was not selected (%v)", bin, sel)
	}

	if _, err := os.Stat(bin + ".aarch64.elf"); err == nil {
		t.Errorf("%s.aarch64.elf exists: the arm64 image gets no sidecar", bin)
	}
}
