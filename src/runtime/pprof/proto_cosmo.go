// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

//go:build cosmo

package pprof

import (
	"encoding/binary"
	"errors"
	"internal/ape"
	"io"
	"os"
	"runtime"
	_ "unsafe" // for go:linkname
)

//go:linkname pprof_mainModuleText runtime.pprof_mainModuleText
func pprof_mainModuleText() (uintptr, uintptr)

// readMapping writes this process's mappings to b.pb. A Linux host
// publishes them in /proc; the other two do not, and the runtime's own
// text range is the mapping a profile needs to symbolize.
func (b *profileBuilder) readMapping() {
	if runtime.GOOS == "linux" {
		data, _ := os.ReadFile("/proc/self/maps")
		parseProcSelfMaps(data, b.addMapping)
		if len(b.mem) > 0 {
			return
		}
	}
	start, end, exe, buildID, err := readMainModuleMapping()
	if err != nil {
		b.addMappingEntry(0, 0, 0, "", "", true)
		return
	}
	// The third argument is the file offset the mapping starts at, which
	// for the image the loader placed at its own base is zero.
	b.addMapping(start, end, 0, exe, buildID)
}

// readMainModuleMapping reports where the main module is mapped. buildID
// stays empty: no host answers it the same way, and a wrong one names
// the wrong binary.
//
// A mapping begins at the base the image was loaded at, not at its text.
// A reader subtracts that base from its own load address to learn the
// slide, so naming text instead reports a slide of the distance from the
// image base to text and moves every symbol by it. The two differ here
// by the header page.
func readMainModuleMapping() (start, end uint64, exe, buildID string, err error) {
	text, etext := pprof_mainModuleText()
	if text == 0 || etext <= text {
		return 0, 0, "", "", errors.New("runtime reports no text range")
	}
	exe, err = os.Executable()
	if err != nil {
		return 0, 0, "", "", err
	}
	start = uint64(text)
	if base, ok := imageBase(exe, start); ok {
		start = base
	}
	return start, uint64(etext), exe, "", nil
}

// imageBase reports the address the segment holding text is linked at,
// which is the address it is loaded at: an APE payload is not position
// independent, so the file answers for the running process.
//
// A fat APE holds an image per architecture, and the sidecar beside it
// is already the one for this machine, so ask that first. Whichever file
// answers, the segment has to cover text, which is what tells the two
// architectures apart when both are readable.
func imageBase(exe string, text uint64) (uint64, bool) {
	if side := ape.Sidecar(exe); side != "" {
		if base, ok := imageBaseOf(side, text); ok {
			return base, true
		}
	}
	return imageBaseOf(exe, text)
}

// imageBaseOf reads name's ELF program headers and returns the base of
// the executable segment that covers text. A caller that gets false
// keeps whatever it had.
func imageBaseOf(name string, text uint64) (uint64, bool) {
	f, err := os.Open(name)
	if err != nil {
		return 0, false
	}
	defer f.Close()

	var r io.ReaderAt = f
	if p := ape.Payload(f); p != nil {
		r = p
	}
	var ehdr [64]byte
	if _, err := r.ReadAt(ehdr[:], 0); err != nil {
		return 0, false
	}
	// ELF64, little endian: every payload an APE carries is both.
	if string(ehdr[:4]) != "\x7fELF" || ehdr[4] != 2 || ehdr[5] != 1 {
		return 0, false
	}
	phoff := binary.LittleEndian.Uint64(ehdr[32:])
	phentsize := binary.LittleEndian.Uint16(ehdr[54:])
	phnum := binary.LittleEndian.Uint16(ehdr[56:])
	if phoff == 0 || phentsize < 56 {
		return 0, false
	}

	const (
		ptLoad = 1
		pfX    = 1
	)
	ph := make([]byte, phentsize)
	for i := 0; i < int(phnum); i++ {
		if _, err := r.ReadAt(ph, int64(phoff)+int64(i)*int64(phentsize)); err != nil {
			if err == io.EOF {
				break
			}
			return 0, false
		}
		if binary.LittleEndian.Uint32(ph[0:]) != ptLoad || binary.LittleEndian.Uint32(ph[4:])&pfX == 0 {
			continue
		}
		vaddr := binary.LittleEndian.Uint64(ph[16:])
		memsz := binary.LittleEndian.Uint64(ph[40:])
		if text < vaddr || text >= vaddr+memsz {
			continue // another architecture's image
		}
		// The reader page-aligns this too, so answer the same number.
		return vaddr &^ 4095, true
	}
	return 0, false
}
