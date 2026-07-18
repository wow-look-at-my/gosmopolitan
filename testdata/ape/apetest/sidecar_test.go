package apetest

import (
	"debug/elf"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDebugSidecars verifies the per-architecture debug sidecars a default
// GOOS=cosmo fat build writes next to its output: <bin>.dbg (cosmo amd64)
// and <bin>.aarch64.elf (cosmo arm64), each a complete unstripped ELF
// carrying the symbol table and DWARF that the shipped APE no longer
// embeds. Sidecars are not shipped in CI artifacts, so the test skips
// cleanly when neither file is present next to FIZZBUZZ_BIN.
func TestDebugSidecars(t *testing.T) {
	bin := os.Getenv("FIZZBUZZ_BIN")
	require.NotEmpty(t, bin, "FIZZBUZZ_BIN environment variable must be set")

	sidecars := []struct {
		path    string
		machine elf.Machine
	}{
		{bin + ".dbg", elf.EM_X86_64},
		{bin + ".aarch64.elf", elf.EM_AARCH64},
	}
	missing := 0
	for _, sc := range sidecars {
		if _, err := os.Stat(sc.path); os.IsNotExist(err) {
			missing++
		}
	}
	if missing == len(sidecars) {
		t.Skipf("no debug sidecars next to %s (binary from a CI artifact?)", bin)
	}

	for _, sc := range sidecars {
		t.Run(filepath.Base(sc.path), func(t *testing.T) {
			f, err := elf.Open(sc.path)
			require.NoError(t, err, "sidecar must be a readable ELF")
			defer f.Close()

			assert.Equal(t, sc.machine, f.Machine, "sidecar machine type")
			assert.Equal(t, elf.ET_EXEC, f.Type, "sidecar must be an executable ELF")

			syms, err := f.Symbols()
			require.NoError(t, err, "sidecar must carry a symbol table")
			foundMain := false
			for _, s := range syms {
				if s.Name == "main.main" {
					foundMain = true
					break
				}
			}
			assert.True(t, foundMain, "sidecar symbol table must include main.main (%d symbols)", len(syms))

			assert.NotNil(t, f.Section(".text"), "sidecar must keep its section table")
			hasDwarf := f.Section(".debug_info") != nil || f.Section(".zdebug_info") != nil
			assert.True(t, hasDwarf, "sidecar must keep DWARF (.debug_info)")
		})
	}
}
