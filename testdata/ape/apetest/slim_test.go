package apetest

import (
	"bytes"
	"context"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The APE built from a GOCOSMOPLATFORMS selection must carry the boot
// mechanisms of exactly the platforms named, and must NOT carry what was
// deselected. The negative half is the point: a toolchain that ignores the
// selection produces a working binary too, just not a slimmed one, so only
// asserting absence tells the two apart.
//
// SLIM_BIN is a fizzbuzz APE built with SLIM_PLATFORMS; both must be set or
// the suite skips. FAT_BIN, when set, is the unrestricted build of the same
// program, used as the size reference.

func slimPlatforms(t *testing.T) map[string]bool {
	t.Helper()
	slimPath(t) // skip the whole suite before demanding the selection
	spec := os.Getenv("SLIM_PLATFORMS")
	require.NotEmpty(t, spec, "SLIM_PLATFORMS must name the selection SLIM_BIN was built with")
	sel := map[string]bool{}
	for _, p := range strings.Split(spec, ",") {
		sel[strings.TrimSpace(p)] = true
	}
	return sel
}

// slimPath returns the APE under test, skipping when the runner did not
// build one - the default CI legs and any local run with only FIZZBUZZ_BIN.
func slimPath(t *testing.T) string {
	t.Helper()
	path := os.Getenv("SLIM_BIN")
	if path == "" {
		t.Skip("SLIM_BIN not set; skipping platform-selection tests")
	}
	return path
}

func slimBinary(t *testing.T) []byte {
	t.Helper()
	path := slimPath(t)
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return data
}

func slimHead(t *testing.T) []byte {
	t.Helper()
	bin := slimBinary(t)
	require.Greater(t, len(bin), 8192, "APE must be larger than the loader's scan window")
	return bin[:8192]
}

// slimBootHeaders decodes the printf boot headers of SLIM_BIN, the way
// decodeBootHeaders does for FIZZBUZZ_BIN.
func slimBootHeaders(t *testing.T) []elf.Machine {
	t.Helper()
	head := slimHead(t)
	var machines []elf.Machine
	for i := 0; ; {
		j := bytes.Index(head[i:], []byte("printf '"))
		if j < 0 {
			return machines
		}
		p := i + j + 8
		var out []byte
		for p < len(head) {
			c := head[p]
			p++
			if c == '\'' {
				break
			}
			if c == '\\' {
				v := byte(0)
				for n := 0; n < 3 && p < len(head) && head[p] >= '0' && head[p] <= '7'; n++ {
					v = v*8 + head[p] - '0'
					p++
				}
				c = v
			}
			out = append(out, c)
		}
		if len(out) >= 64 && bytes.Equal(out[:4], []byte{0x7f, 'E', 'L', 'F'}) {
			machines = append(machines, elf.Machine(le16(out[18:])))
		}
		i = p
	}
}

// slimPayloadMachines returns the machine type of every ELF payload stored
// in the file, found by scanning the 64K-aligned offsets layoutAPE places
// payloads at.
func slimPayloadMachines(t *testing.T) []elf.Machine {
	t.Helper()
	bin := slimBinary(t)
	const align = 0x10000
	var machines []elf.Machine
	for off := align; off+64 <= len(bin); off += align {
		if !bytes.Equal(bin[off:off+4], []byte{0x7f, 'E', 'L', 'F'}) {
			continue
		}
		machines = append(machines, elf.Machine(le16(bin[off+18:])))
	}
	return machines
}

func slimWantsArch(sel map[string]bool, arch string) bool {
	for p := range sel {
		if strings.HasSuffix(p, "/"+arch) {
			return true
		}
	}
	return false
}

// TestSlimPayloads is the negative control: an architecture no selected
// platform boots must not be in the file at all, neither as a payload nor
// as a boot header the loader would find.
func TestSlimPayloads(t *testing.T) {
	sel := slimPlatforms(t)
	payloads := slimPayloadMachines(t)
	boots := slimBootHeaders(t)

	for arch, machine := range map[string]elf.Machine{"amd64": elf.EM_X86_64, "arm64": elf.EM_AARCH64} {
		want := slimWantsArch(sel, arch)
		assert.Equal(t, want, slices.Contains(payloads, machine),
			"%s payload present=%v, want %v (payloads: %v)", arch, !want, want, payloads)
		assert.Equal(t, want, slices.Contains(boots, machine),
			"%s boot header present=%v, want %v (boot headers: %v)", arch, !want, want, boots)
	}
	require.NotEmpty(t, payloads, "APE must carry at least one payload")
}

// TestSlimMachO checks the macOS x86-64 assimilation pieces: the Mach-O
// header at machoOffset and the dd statement that copies it over the file's
// start, both present only for darwin/amd64.
func TestSlimMachO(t *testing.T) {
	sel := slimPlatforms(t)
	bin := slimBinary(t)
	want := sel["darwin/amd64"]

	const machoMagic64 = 0xFEEDFACF
	gotHeader := len(bin) > machoOffset+4 && le32(bin[machoOffset:machoOffset+4]) == machoMagic64
	assert.Equal(t, want, gotHeader, "Mach-O header at %#x", machoOffset)

	// conv=notrunc is what distinguishes the assimilation dd from the one
	// the macOS ARM64 branch uses to extract the loader source.
	gotDD := bytes.Contains(slimHead(t), []byte("conv=notrunc"))
	assert.Equal(t, want, gotDD, "dd assimilation statement in the bootstrap script")
}

// TestSlimApeLoader checks the gzipped APE loader source, which only
// darwin/arm64 compiles and runs.
func TestSlimApeLoader(t *testing.T) {
	sel := slimPlatforms(t)
	bin := slimBinary(t)
	const loaderOffset = 0x8000

	want := sel["darwin/arm64"]
	got := len(bin) > loaderOffset+2 && bin[loaderOffset] == 0x1f && bin[loaderOffset+1] == 0x8b
	assert.Equal(t, want, got, "gzipped APE loader source at %#x", loaderOffset)
}

// TestSlimPEHeader checks that the NT boot header is real only when
// windows/amd64 is selected: the do-nothing stub declares one section, the
// header that maps the payload declares three.
func TestSlimPEHeader(t *testing.T) {
	sel := slimPlatforms(t)
	bin := slimBinary(t)
	require.Equal(t, []byte("PE\x00\x00"), bin[0x80:0x84], "APE must stay parseable as a PE")

	sections := le16(bin[0x86:0x88])
	if sel["windows/amd64"] {
		assert.Equal(t, uint16(3), sections, "windows/amd64 needs the NT boot header, not the stub")
	} else {
		assert.Equal(t, uint16(1), sections, "without windows/amd64 the PE header must be the do-nothing stub")
	}
	assert.Equal(t, sel["windows/amd64"], bytes.Contains(slimHead(t), []byte("exec cmd //c")),
		"MSYS/Cygwin delegation to cmd.exe")
}

// TestSlimUnsupportedHostMessage checks the guard on a unix host that runs
// a payload the binary carries but was not built for. That host must be
// refused by name, and refused BEFORE the bootstrap self-assimilates: the
// printf writes a boot ELF header over the APE header, so a host that
// cannot then run the result is left with a file that is neither.
func TestSlimUnsupportedHostMessage(t *testing.T) {
	sel := slimPlatforms(t)
	partial := func(arch string, oses ...string) bool {
		if !slimWantsArch(sel, arch) {
			return false
		}
		for _, os := range oses {
			if !sel[os+"/"+arch] {
				return true
			}
		}
		return false
	}
	want := partial("amd64", "linux", "darwin") || partial("arm64", "linux", "darwin")
	const msg = "APE: unsupported host; this binary was built for"
	assert.Equal(t, want, bytes.Contains(slimHead(t), []byte(msg)),
		"a deselected unix host must be refused by name (selection %v)", sel)
}

// TestSlimSmallerThanFat checks that dropping an architecture actually
// removed its payload from the file. Header pieces are inside the fixed 64K
// APE header, so only an architecture drop changes the size.
func TestSlimSmallerThanFat(t *testing.T) {
	sel := slimPlatforms(t)
	if slimWantsArch(sel, "amd64") && slimWantsArch(sel, "arm64") {
		t.Skip("selection needs both payloads; the file is the same size as the unrestricted build")
	}
	fat := os.Getenv("FAT_BIN")
	if fat == "" {
		t.Skip("FAT_BIN not set; no unrestricted build to compare against")
	}
	fatInfo, err := os.Stat(fat)
	require.NoError(t, err)
	slim := len(slimBinary(t))
	assert.Less(t, slim, int(fatInfo.Size())*3/4,
		"single-architecture APE (%d bytes) must be well under the fat one (%d bytes)", slim, fatInfo.Size())
}

// TestSlimRuns executes the slimmed binary on this host, which is the only
// evidence that a smaller file is still a working one.
func TestSlimRuns(t *testing.T) {
	sel := slimPlatforms(t)
	host := runtime.GOOS + "/" + runtime.GOARCH
	if !sel[host] {
		t.Skipf("SLIM_BIN was not built for %s", host)
	}
	data := slimBinary(t)
	bin := filepath.Join(t.TempDir(), "fizzbuzz.com")
	require.NoError(t, os.WriteFile(bin, data, 0755))

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, bin, "10", "5")
	} else {
		cmd = exec.CommandContext(ctx, "/bin/sh", bin, "10", "5")
	}
	cmd.WaitDelay = 30 * time.Second
	out, err := cmd.Output()
	require.NoError(t, err, "slimmed APE must run on %s", host)
	assert.Equal(t, "fizzbuzz", strings.TrimSpace(string(out)))
}
