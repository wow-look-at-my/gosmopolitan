// Copyright The Go Authors. All rights reserved. Use of this source code is
// governed by a BSD-style license that can be found in the LICENSE file.

package ld

import (
	"bytes"
	"cmd/internal/objabi"
	"cmd/internal/sys"
	"debug/elf"
	"os"
	"os/exec"
	"path/filepath"
)

// A cgo GOOS=cosmo program links against libcosmo with the raw cosmocc gcc.

// cosmoToolchain names the cosmocc pieces of one architecture.
type cosmoToolchain struct {
	gcc string // <arch>-linux-cosmo-gcc
	lib string // <arch>-linux-cosmo/lib, which holds crt.o and libcosmo.a
}

// cosmoArchPrefix is the cosmocc name of a Go architecture.
func cosmoArchPrefix(arch sys.ArchFamily) string {
	if arch == sys.ARM64 {
		return "aarch64"
	}
	return "x86_64"
}

// findCosmoToolchain finds the raw gcc and the library directory next to the
// C compiler the go command passed as -extld.
func findCosmoToolchain(ctxt *Link) cosmoToolchain {
	driver := ctxt.extld()[0]
	path, err := exec.LookPath(driver)
	if err != nil {
		Exitf("cosmo cgo link: C compiler %s not found: %v", driver, err)
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	bin := filepath.Dir(path)
	prefix := cosmoArchPrefix(ctxt.Arch.Family)
	tc := cosmoToolchain{
		gcc: filepath.Join(bin, prefix+"-linux-cosmo-gcc"),
		lib: filepath.Join(filepath.Dir(bin), prefix+"-linux-cosmo", "lib"),
	}
	for _, need := range []string{tc.gcc, filepath.Join(tc.lib, "crt.o"), filepath.Join(tc.lib, "libcosmo.a")} {
		if _, err := os.Stat(need); err != nil {
			Exitf("cosmo cgo link: %s needs the cosmocc toolchain beside it: %v", driver, err)
		}
	}
	return tc
}

// cosmoHostlink is hostlink for GOOS=cosmo.
func (ctxt *Link) cosmoHostlink() {
	if ctxt.BuildMode != BuildModeExe {
		Exitf("cosmo cgo link: -buildmode=%s is not supported, only exe", ctxt.BuildMode)
	}
	tc := findCosmoToolchain(ctxt)
	script := filepath.Join(*flagTmpdir, "cosmo.lds")
	if err := os.WriteFile(script, []byte(cosmoLinkerScript(ctxt.Arch.Family)), 0666); err != nil {
		Exitf("cosmo cgo link: %v", err)
	}
	page := "4096"
	if ctxt.Arch.Family == sys.ARM64 {
		page = "16384"
	}
	argv := []string{
		tc.gcc,
		"-static", "-nostdlib", "-no-pie", "-fuse-ld=bfd",
		"-Wl,-z,noexecstack",
		"-Wl,-z,common-page-size=" + page,
		"-Wl,-z,max-page-size=" + page,
		"-Wl,-T," + script,
		// runtime/cgo wraps pthread_create, so every C thread gets Go's TLS slot.
		"-Wl,--wrap=pthread_create",
		"-o", *flagOutfile,
	}
	if *FlagS || debug_s {
		argv = append(argv, "-s")
	} else if *FlagW {
		argv = append(argv, "-Wl,-S")
	}

	hostObjs := ctxt.hostobjCopy()
	cleanTimeStamps(hostObjs)
	goObj := filepath.Join(*flagTmpdir, "go.o")
	cleanTimeStamps([]string{goObj})

	argv = append(argv, filepath.Join(tc.lib, "crt.o"), goObj)
	argv = append(argv, hostObjs...)
	argv = append(argv, ldflag...)
	argv = append(argv, flagExtldflags...)
	argv = append(argv, "-L"+tc.lib, "-lcosmo")

	if ctxt.Debugvlog != 0 {
		ctxt.Logf("host link: %q\n", argv)
	}
	cmd := exec.Command(argv[0], argv[1:]...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		Exitf("running %s failed: %v\n%s\n%s", argv[0], err, cmd, out)
	}
	if len(out) > 0 {
		ctxt.Logf("%s", out)
	}
}

// cosmoFixedTLS reports whether a Go TLS reference resolves to the fixed
// Tlsoffset even in an external link. Cosmo amd64 keeps g at gs:0x28.
func cosmoFixedTLS(target *Target) bool {
	return target.HeadType == objabi.Hcosmo && target.IsAMD64()
}

// cosmoLinkedSymbol returns the value of a symbol in a linked ELF image. The
// external linker chooses every address, so the loader's values are stale.
func cosmoLinkedSymbol(image []byte, name string) (uint64, bool) {
	file, err := elf.NewFile(bytes.NewReader(image))
	if err != nil {
		return 0, false
	}
	syms, err := file.Symbols()
	if err != nil {
		return 0, false
	}
	for _, sym := range syms {
		if sym.Name == name && sym.Section != elf.SHN_UNDEF {
			return sym.Value, true
		}
	}
	return 0, false
}
