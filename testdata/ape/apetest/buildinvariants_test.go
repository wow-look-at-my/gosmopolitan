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

// TestFatSidecarsExist requires the debug sidecars a default fat build must
// write next to FIZZBUZZ_BIN and RUNTIMEPROBE_BIN.
func TestFatSidecarsExist(t *testing.T) {
	requireSidecarChecks(t)
	for _, bin := range []string{binPath(t), os.Getenv("RUNTIMEPROBE_BIN")} {
		require.NotEmpty(t, bin, "RUNTIMEPROBE_BIN must be set alongside FIZZBUZZ_BIN")
		assertSidecarELF(t, bin+".dbg", elf.EM_X86_64)
		assertSidecarELF(t, bin+".aarch64.elf", elf.EM_AARCH64)
	}
}

// TestSlimSidecarsExist requires a platform-subset build to write exactly the
// sidecars matching its selection: a restricted build is still stripped and
// still writes one sidecar per payload it carries, so a deselected
// architecture must have no sidecar file at all.
func TestSlimSidecarsExist(t *testing.T) {
	requireSidecarChecks(t)
	sel := slimPlatforms(t)
	bin := slimPath(t)

	wantAMD64 := slimWantsArch(sel, "amd64")
	wantARM64 := slimWantsArch(sel, "arm64")

	if wantAMD64 {
		assertSidecarELF(t, bin+".dbg", elf.EM_X86_64)
	} else if _, err := os.Stat(bin + ".dbg"); err == nil {
		t.Errorf("%s.dbg exists but amd64 was not selected (%v)", bin, sel)
	}

	if wantARM64 {
		assertSidecarELF(t, bin+".aarch64.elf", elf.EM_AARCH64)
	} else if _, err := os.Stat(bin + ".aarch64.elf"); err == nil {
		t.Errorf("%s.aarch64.elf exists but arm64 was not selected (%v)", bin, sel)
	}
}
