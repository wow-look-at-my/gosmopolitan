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
	tools := cosmoToolchain{
		gcc: filepath.Join(bin, prefix+"-linux-cosmo-gcc"),
		lib: filepath.Join(filepath.Dir(bin), prefix+"-linux-cosmo", "lib"),
	}
	for _, need := range []string{tools.gcc, filepath.Join(tools.lib, "crt.o"), filepath.Join(tools.lib, "libcosmo.a")} {
		if _, err := os.Stat(need); err != nil {
			Exitf("cosmo cgo link: %s needs the cosmocc toolchain beside it: %v", driver, err)
		}
	}
	return tools
}

// cosmoHostlink is hostlink for GOOS=cosmo.
func (ctxt *Link) cosmoHostlink() {
	if ctxt.BuildMode != BuildModeExe {
		Exitf("cosmo cgo link: -buildmode=%s is not supported, only exe", ctxt.BuildMode)
	}
	tools := findCosmoToolchain(ctxt)
	script := filepath.Join(*flagTmpdir, "cosmo.lds")
	if err := os.WriteFile(script, []byte(cosmoLinkerScript(ctxt.Arch.Family)), 0666); err != nil {
		Exitf("cosmo cgo link: %v", err)
	}
	page := "4096"
	if ctxt.Arch.Family == sys.ARM64 {
		page = "16384"
	}
	argv := []string{
		tools.gcc,
		"-static", "-nostdlib", "-no-pie", "-fuse-ld=bfd",
		"-Wl,-z,noexecstack",
		"-Wl,-z,common-page-size=" + page,
		"-Wl,-z,max-page-size=" + page,
		"-Wl,-T," + script,
		// runtime/cgo wraps pthread_create, so every C thread gets Go's TLS slot.
		"-Wl,--wrap=pthread_create",
		"-o", *flagOutfile,
	}
	// Never -s here: cosmoNTBoot reads WinMain and the ape_idata bounds out of
	// the linked image's symbol table.
	if *FlagS || debug_s || *FlagW {
		argv = append(argv, "-Wl,-S")
	}

	hostObjs := ctxt.hostobjCopy()
	cleanTimeStamps(hostObjs)
	goObj := filepath.Join(*flagTmpdir, "go.o")
	cleanTimeStamps([]string{goObj})

	argv = append(argv, filepath.Join(tools.lib, "crt.o"), goObj)
	argv = append(argv, hostObjs...)
	argv = append(argv, ldflag...)
	argv = append(argv, flagExtldflags...)
	argv = append(argv, "-L"+tools.lib, "-lcosmo")

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

// cosmoNTBoot returns the PE entry and import tables of a cgo amd64 image.
// libcosmo starts at WinMain on NT. WinMain calls main, which is rt0_go, as
// _start does on Unix. The import tables are libcosmo's, which the linker
// script bounds with the ape_idata symbols.
func cosmoNTBoot(image []byte, base uint64) *apePEInfo {
	rva := func(name string) uint32 {
		value, found := cosmoLinkedSymbol(image, name)
		if !found {
			Exitf("APE NT boot: symbol %s is not in the externally linked image", name)
		}
		if value < base || value-base >= 1<<32 {
			Exitf("APE NT boot: %s at %#x is outside the PE image (base %#x)", name, value, base)
		}
		return uint32(value - base)
	}
	info := &apePEInfo{
		entryRVA:   rva("WinMain"),
		importsRVA: rva("ape_idata_idt"),
		iatRVA:     rva("ape_idata_iat"),
	}
	info.importsSize = rva("ape_idata_idtend") - info.importsRVA
	info.iatSize = rva("ape_idata_iatend") - info.iatRVA
	if info.importsSize < 40 || info.importsSize%20 != 0 {
		Exitf("APE NT boot: the import directory is %d bytes, want a whole number of 20-byte descriptors after kernel32's", info.importsSize)
	}
	if info.iatSize == 0 {
		Exitf("APE NT boot: the image has no import address table")
	}
	// The PE headers occupy the first page of the image, so NT maps no code there.
	file, err := elf.NewFile(bytes.NewReader(image))
	if err != nil {
		Exitf("APE NT boot: %v", err)
	}
	for _, sect := range file.Sections {
		if sect.Flags&elf.SHF_EXECINSTR != 0 && sect.Size != 0 && sect.Addr < base+peCosmoSectAlign {
			Exitf("APE NT boot: section %s at %#x starts in the page the PE headers take (base %#x)", sect.Name, sect.Addr, base)
		}
	}
	loads := apePayloadLoads(image)
	apeVaddrFileOff(loads, base+uint64(info.importsRVA), uint64(info.importsSize), "ape_idata_idt")
	apeVaddrFileOff(loads, base+uint64(info.iatRVA), uint64(info.iatSize), "ape_idata_iat")
	return info
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
