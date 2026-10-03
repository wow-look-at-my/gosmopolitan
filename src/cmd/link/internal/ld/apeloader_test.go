// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/cosmoape"
	"crypto/sha256"
	"debug/elf"
	"debug/macho"
	"encoding/hex"
	"testing"
)

// The bytes each embedded loader must have. apeld/build.sh produces them
// reproducibly, so a rebuild that changes one changes it on purpose and
// updates the pin here in the same commit. A loader reaches the host
// verbatim, and nothing on the host checks it, so this is the only place
// that can.
var apeLoaderSums = map[string]string{
	"apeld-linux-amd64":  "c798912fd52d374d5f35ae2f86ce5082ffd11b163891a36d4c402777bfc6723a",
	"apeld-linux-arm64":  "13779a091333025b829d944fd12d74be47a3864540013f881390353507dcd0c3",
	"apeld-darwin-arm64": "2d4b7228fac8d4c5132adaccf554e55adedc39727fc0b690f68ad72d13532bfd",
}

// apeLoaderBins is the embedded loader for each platform that has one.
func apeLoaderBins() map[string][]byte {
	return map[string][]byte{
		"apeld-linux-amd64":  apeldLinuxAMD64,
		"apeld-linux-arm64":  apeldLinuxARM64,
		"apeld-darwin-arm64": apeldDarwinARM64,
	}
}

// apeAllLoaderPlatforms is every platform that boots through a loader.
// cosmoape.Default() leaves linux/arm64 out, and the header has to hold
// all three at once for a build that names it.
func apeAllLoaderPlatforms() cosmoape.Set {
	set, err := cosmoape.Parse("linux/amd64,linux/arm64,darwin/arm64,windows/amd64")
	if err != nil {
		panic(err)
	}
	return set
}

func TestApeLoaderBinariesMatchTheirPins(t *testing.T) {
	bins := apeLoaderBins()
	if len(bins) != len(apeLoaderSums) {
		t.Fatalf("%d embedded loaders, %d pins", len(bins), len(apeLoaderSums))
	}
	for name, bin := range bins {
		sum := sha256.Sum256(bin)
		if got, want := hex.EncodeToString(sum[:]), apeLoaderSums[name]; got != want {
			t.Errorf("%s is %s, pinned at %s: rebuild it with apeld/build.sh and update the pin, or restore the committed binary",
				name, got, want)
		}
	}
}

// TestApeLoaderIsStatic holds the property that makes a loader usable on a
// host that carries nothing: it links against no interpreter. A loader
// that needed one would fail on exactly the minimal image an APE is meant
// to run on, and the failure would land on the host.
func TestApeLoaderIsStatic(t *testing.T) {
	for name, want := range map[string]elf.Machine{
		"apeld-linux-amd64": elf.EM_X86_64,
		"apeld-linux-arm64": elf.EM_AARCH64,
	} {
		f, err := elf.NewFile(bytes.NewReader(apeLoaderBins()[name]))
		if err != nil {
			t.Errorf("%s does not parse as ELF: %v", name, err)
			continue
		}
		if f.Machine != want {
			t.Errorf("%s has machine %v, want %v", name, f.Machine, want)
		}
		if f.Type != elf.ET_EXEC {
			t.Errorf("%s has type %v, want %v", name, f.Type, elf.ET_EXEC)
		}
		for _, p := range f.Progs {
			if p.Type == elf.PT_INTERP || p.Type == elf.PT_DYNAMIC {
				t.Errorf("%s carries a %v segment; it must be static", name, p.Type)
			}
		}
		f.Close()
	}
}

// TestApeLoaderDarwinLoadsOnlyLibSystem pins the darwin loader's whole
// dependency surface. dyld resolves a dylib by its literal install name,
// so every name here is a file that must exist on the host.
func TestApeLoaderDarwinLoadsOnlyLibSystem(t *testing.T) {
	f, err := macho.NewFile(bytes.NewReader(apeldDarwinARM64))
	if err != nil {
		t.Fatalf("apeld-darwin-arm64 does not parse as Mach-O: %v", err)
	}
	defer f.Close()
	if f.Cpu != macho.CpuArm64 {
		t.Errorf("apeld-darwin-arm64 is for %v, want %v", f.Cpu, macho.CpuArm64)
	}
	if f.Type != macho.TypeExec {
		t.Errorf("apeld-darwin-arm64 has type %v, want %v", f.Type, macho.TypeExec)
	}
	libs, err := f.ImportedLibraries()
	if err != nil {
		t.Fatalf("reading apeld-darwin-arm64's dylibs: %v", err)
	}
	for _, lib := range libs {
		if lib != "/usr/lib/libSystem.B.dylib" {
			t.Errorf("apeld-darwin-arm64 loads %s; libSystem is the only name a stock macOS is sure to have", lib)
		}
	}
}

// TestApeLoaderRegionsFitTheHeader walks every loader at once, the layout
// that packs the most into the 64K header, and checks that the regions
// stay in order and inside it. placeApeLoaders enforces the same thing at
// link time; this fails on a build machine rather than on someone's host.
func TestApeLoaderRegionsFitTheHeader(t *testing.T) {
	loaders := apeLoadersFor(apeAllLoaderPlatforms())
	if len(loaders) != len(apeLoaderSums) {
		t.Fatalf("got %d loaders for every platform that has one, want %d", len(loaders), len(apeLoaderSums))
	}
	// The script runs from apeScriptOffset, so the first loader must start
	// past where it can reach.
	end := apeScriptOffset
	for _, l := range loaders {
		if l.offset < end {
			t.Errorf("%s starts at %#x, inside the region that ends at %#x", l.name, l.offset, end)
		}
		end = l.offset + len(l.blob)
	}
	if end > apeHeaderSize {
		t.Errorf("the loaders reach %#x, past the %#x-byte APE header", end, apeHeaderSize)
	}
	placeApeLoaders(make([]byte, apeHeaderSize), loaders)
}

// TestApeLoaderTagFollowsTheBinary pins what the unpack path is keyed on.
// The tag names the cache file, so two loaders that differ must not share
// it: a host would keep booting whichever one it unpacked first.
func TestApeLoaderTagFollowsTheBinary(t *testing.T) {
	seen := map[string]string{}
	for _, l := range apeLoadersFor(apeAllLoaderPlatforms()) {
		if prev, ok := seen[l.tag]; ok {
			t.Errorf("%s and %s share the tag %s", prev, l.name, l.tag)
		}
		seen[l.tag] = l.name
		sum := sha256.Sum256(apeLoaderBins()[l.name])
		if want := hex.EncodeToString(sum[:4]); l.tag != want {
			t.Errorf("%s is tagged %s, want %s: the tag must be the binary's own hash, not the packed blob's", l.name, l.tag, want)
		}
	}
}
