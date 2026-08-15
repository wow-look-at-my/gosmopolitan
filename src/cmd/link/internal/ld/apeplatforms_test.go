// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"cmd/internal/cosmoape"
)

// setAPEPlatforms sets the -apeplatforms flag value for one test.
func setAPEPlatforms(t *testing.T, spec string) {
	t.Helper()
	old := *flagApePlatforms
	*flagApePlatforms = spec
	t.Cleanup(func() { *flagApePlatforms = old })
}

// assembleTest writes the given payloads as inputs (amd64 as a thin APE,
// arm64 as a raw ELF, matching what cmd/go hands the assembly step) and
// runs apeFatMerge under the given -apeplatforms selection. It returns the
// assembled file.
func assembleTest(t *testing.T, spec string, wantAMD, wantARM bool) []byte {
	t.Helper()
	dir := t.TempDir()

	var inputs []string
	if wantAMD {
		// The NT-shaped payload, so the thin APE carries the real PE
		// header windows/amd64 needs the assembly step to transplant.
		amdElf, peInfo := buildTestNTELF(t)
		p, err := payloadFromELF(amdElf)
		if err != nil {
			t.Fatal(err)
		}
		p.pe = peInfo
		amdIn := filepath.Join(dir, "amd.com")
		writeAPEFile(amdIn, []*apePayload{p})
		inputs = append(inputs, amdIn)
	}
	if wantARM {
		armIn := filepath.Join(dir, "arm.elf")
		armElf := buildTestELFForMachine(t, elfMachineARM64, testELFEntry, testELFPhdrs())
		if err := os.WriteFile(armIn, armElf, 0644); err != nil {
			t.Fatal(err)
		}
		inputs = append(inputs, armIn)
	}

	setAPEFatFlags(t, false, false)
	setAPEPlatforms(t, spec)
	out := filepath.Join(dir, "out.com")
	spec2 := inputs[0]
	if len(inputs) == 2 {
		spec2 += "," + inputs[1]
	}
	apeFatMerge(spec2, out)
	data, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// bootHeaderMachines decodes every printf boot header in the loader's
// 8192-byte scan window and returns its ELF machine type, the way
// ape-m1.c's scan does.
func bootHeaderMachines(t *testing.T, bin []byte) []uint16 {
	t.Helper()
	head := bin
	if len(head) > 8192 {
		head = head[:8192]
	}
	var machines []uint16
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
		if len(out) >= 64 && string(out[:4]) == elfMagic {
			machines = append(machines, binary.LittleEndian.Uint16(out[18:]))
		}
		i = p
	}
}

// payloadMachines returns the machine type of each ELF payload stored in
// the file, found at the aligned offsets layoutAPE uses.
func payloadMachines(t *testing.T, bin []byte) []uint16 {
	t.Helper()
	var machines []uint16
	for off := apePayloadAlign; off+64 <= len(bin); off += apePayloadAlign {
		if string(bin[off:off+4]) != elfMagic {
			continue
		}
		machines = append(machines, binary.LittleEndian.Uint16(bin[off+18:]))
	}
	return machines
}

func contains(s []uint16, v uint16) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// catchExitf runs fn with the linker's -h behavior, where Exitf panics
// instead of ending the process, and returns what it printed. Error paths
// are the point of a selection flag, so they need to be testable in
// process.
func catchExitf(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	oldH, oldErr, oldN := *flagH, os.Stderr, nerrors
	*flagH, os.Stderr = true, w
	defer func() { *flagH, os.Stderr, nerrors = oldH, oldErr, oldN }()

	out := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		buf.ReadFrom(r)
		out <- buf.String()
	}()

	func() {
		defer func() {
			if recover() == nil {
				t.Error("link succeeded, want it to fail")
			}
		}()
		fn()
	}()
	w.Close()
	return <-out
}

// TestAPEPlatformsHeaderPieces checks that each selection emits exactly the
// boot mechanisms its platforms need and nothing else. The absences are the
// contract: a piece kept for a deselected platform is a claim the binary no
// longer honors, and a piece dropped for a selected one is a host that dies
// with no diagnosable symptom.
func TestAPEPlatformsHeaderPieces(t *testing.T) {
	const machoMagic = 0xFEEDFACF
	tests := []struct {
		spec                        string
		amd, arm                    bool
		wantMacho, wantLoader       bool
		wantAMDBoot, wantARMBoot    bool
		wantWindowsShell            bool
		wantUnsupportedHostGuardMsg bool
	}{
		{
			spec: "linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64",
			amd:  true, arm: true,
			wantMacho: true, wantLoader: true,
			wantAMDBoot: true, wantARMBoot: true,
			wantWindowsShell: true,
		},
		{
			// The owner's set: both payloads, but no macOS Intel.
			spec: "linux/amd64,darwin/arm64,windows/amd64",
			amd:  true, arm: true,
			wantMacho: false, wantLoader: true,
			wantAMDBoot: true, wantARMBoot: true,
			wantWindowsShell:            true,
			wantUnsupportedHostGuardMsg: true,
		},
		{
			spec:      "linux/amd64,windows/amd64",
			amd:       true,
			wantMacho: false, wantLoader: false,
			wantAMDBoot: true, wantARMBoot: false,
			wantWindowsShell:            true,
			wantUnsupportedHostGuardMsg: true,
		},
		{
			spec:                        "linux/amd64",
			amd:                         true,
			wantAMDBoot:                 true,
			wantUnsupportedHostGuardMsg: true,
		},
		{
			// macOS Apple Silicon only: the arm64 boot header stays,
			// unreachable as shell but decodable by the APE loader.
			spec:                        "darwin/arm64",
			arm:                         true,
			wantLoader:                  true,
			wantARMBoot:                 true,
			wantUnsupportedHostGuardMsg: true,
		},
	}

	for _, tt := range tests {
		t.Run(strings.ReplaceAll(tt.spec, ",", "+"), func(t *testing.T) {
			bin := assembleTest(t, tt.spec, tt.amd, tt.arm)
			head := bin[:8192]

			if got := contains(payloadMachines(t, bin), elfMachineAMD64); got != tt.amd {
				t.Errorf("amd64 payload present = %v, want %v", got, tt.amd)
			}
			if got := contains(payloadMachines(t, bin), elfMachineARM64); got != tt.arm {
				t.Errorf("arm64 payload present = %v, want %v", got, tt.arm)
			}

			boots := bootHeaderMachines(t, bin)
			if got := contains(boots, elfMachineAMD64); got != tt.wantAMDBoot {
				t.Errorf("amd64 boot header present = %v, want %v", got, tt.wantAMDBoot)
			}
			if got := contains(boots, elfMachineARM64); got != tt.wantARMBoot {
				t.Errorf("arm64 boot header present = %v, want %v", got, tt.wantARMBoot)
			}

			gotMacho := binary.LittleEndian.Uint32(bin[apeMachoOffset:]) == machoMagic
			if gotMacho != tt.wantMacho {
				t.Errorf("Mach-O header present = %v, want %v", gotMacho, tt.wantMacho)
			}
			if gotDD := bytes.Contains(head, []byte("conv=notrunc")); gotDD != tt.wantMacho {
				t.Errorf("Mach-O dd statement present = %v, want %v", gotDD, tt.wantMacho)
			}

			gotLoader := bin[0x8000] == 0x1f && bin[0x8001] == 0x8b
			if gotLoader != tt.wantLoader {
				t.Errorf("gzipped APE loader present = %v, want %v", gotLoader, tt.wantLoader)
			}

			if got := bytes.Contains(head, []byte("exec cmd //c")); got != tt.wantWindowsShell {
				t.Errorf("MSYS/Cygwin cmd.exe delegation present = %v, want %v", got, tt.wantWindowsShell)
			}

			msg := []byte("APE: unsupported host; this binary was built for")
			if got := bytes.Contains(head, msg); got != tt.wantUnsupportedHostGuardMsg {
				t.Errorf("unsupported-host message present = %v, want %v", got, tt.wantUnsupportedHostGuardMsg)
			}
		})
	}
}

// TestAPEPEImageWithinFile checks that every PE section's raw data is
// inside the file, for a fat APE and for an amd64-only one. The NT loader
// refuses the whole image over a section that runs past EOF, and a stripped
// amd64 payload with nothing after it ends exactly at its loadable span -
// short of the .data raw size the PE header rounds up to FileAlignment.
func TestAPEPEImageWithinFile(t *testing.T) {
	for _, tt := range []struct {
		name     string
		spec     string
		amd, arm bool
	}{
		{"fat", "", true, true},
		{"amd64 only", "linux/amd64,windows/amd64", true, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			setAPEFatFlags(t, true, false) // -apestrip, the default build
			bin := assembleTest(t, tt.spec, tt.amd, tt.arm)

			nsect := binary.LittleEndian.Uint16(bin[0x86:0x88])
			optSize := binary.LittleEndian.Uint16(bin[0x94:0x96])
			sect := 0x98 + int(optSize)
			for i := 0; i < int(nsect); i++ {
				s := sect + 40*i
				name := strings.TrimRight(string(bin[s:s+8]), "\x00")
				raw := binary.LittleEndian.Uint32(bin[s+20:])
				rawsz := binary.LittleEndian.Uint32(bin[s+16:])
				if end := uint64(raw) + uint64(rawsz); end > uint64(len(bin)) {
					t.Errorf("section %s raw data ends at %#x, past the %#x-byte file", name, end, len(bin))
				}
			}
		})
	}
}

// TestAPEPlatformsDefaultUnchanged checks that an unset -apeplatforms
// assembles byte-identically to naming every platform: the selection is an
// opt-in restriction, never a change to what a plain build produces.
func TestAPEPlatformsDefaultUnchanged(t *testing.T) {
	unset := assembleTest(t, "", true, true)
	explicit := assembleTest(t, cosmoape.Default().String(), true, true)
	if !bytes.Equal(unset, explicit) {
		t.Error("unset -apeplatforms differs from an explicit full selection")
	}
}

// TestAPEPlatformsDerivedFromPayloads checks the no-flag behavior for a
// single input: the header claims only the platforms that input can serve,
// which is what a GOCOSMOFAT=0 build produced before the flag existed.
func TestAPEPlatformsDerivedFromPayloads(t *testing.T) {
	amdOnly := assembleTest(t, "", true, false)
	if boots := bootHeaderMachines(t, amdOnly); contains(boots, elfMachineARM64) {
		t.Error("amd64-only input produced an arm64 boot header")
	}
	if amdOnly[0x8000] == 0x1f && amdOnly[0x8001] == 0x8b {
		t.Error("amd64-only input embedded the macOS ARM64 loader source")
	}
	if binary.LittleEndian.Uint32(amdOnly[apeMachoOffset:]) != 0xFEEDFACF {
		t.Error("amd64-only input dropped the Mach-O header, which darwin/amd64 still needs")
	}
}

// TestAPEPlatformsRejects checks that a selection the inputs cannot satisfy
// ends the link. Each of these would otherwise ship a binary that claims a
// platform it cannot boot, or carries a payload nothing boots.
func TestAPEPlatformsRejects(t *testing.T) {
	tests := []struct {
		name     string
		spec     string
		amd, arm bool
		want     string
	}{
		{"missing arm64 payload", "linux/amd64,darwin/arm64", true, false, "has no input for"},
		{"missing amd64 payload", "linux/amd64", false, true, "has no input for"},
		{"unused arm64 payload", "linux/amd64", true, true, "no selected platform boots"},
		{"unknown platform", "linux/riscv64", true, true, "unknown platform"},
		{"empty entry", "linux/amd64,", true, true, "empty platform"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			msg := catchExitf(t, func() { assembleTest(t, tt.spec, tt.amd, tt.arm) })
			if !bytes.Contains([]byte(msg), []byte(tt.want)) {
				t.Errorf("error = %q, want it to mention %q", msg, tt.want)
			}
		})
	}
}
