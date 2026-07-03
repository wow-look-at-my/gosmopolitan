// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/sys"
	"encoding/binary"
	"fmt"
	"os"
	"strings"
)

// apeFatMerge implements the -apefat linker mode: it merges the given
// GOOS=cosmo binaries (one amd64, one arm64; each either an APE produced by
// this linker or a raw ELF), plus optionally one native Windows PE payload,
// into a single fat APE at outfile, skipping normal linking entirely.
func apeFatMerge(spec, outfile string) {
	if outfile == "" {
		Exitf("-apefat requires -o")
	}
	inputs := strings.Split(spec, ",")
	if len(inputs) != 2 && len(inputs) != 3 {
		Exitf("-apefat requires two cosmo inputs and optional Windows PE input")
	}
	var payloads []*apePayload
	var win *pePayload
	for _, in := range inputs {
		data, err := os.ReadFile(in)
		if err != nil {
			Exitf("-apefat: %v", err)
		}
		if isPlainPEPayload(data) {
			if win != nil {
				Exitf("-apefat: more than one Windows PE payload")
			}
			win, err = payloadFromPE(data)
			if err != nil {
				Exitf("-apefat: %s: %v", in, err)
			}
		} else {
			p, err := payloadFromAPEOrELF(data)
			if err != nil {
				Exitf("-apefat: %s: %v", in, err)
			}
			payloads = append(payloads, p)
		}
	}
	if len(payloads) != 2 {
		Exitf("-apefat requires exactly two cosmo inputs")
	}
	if payloads[0].arch == payloads[1].arch {
		Exitf("-apefat: inputs must be different architectures")
	}
	// Canonical layout: amd64 image first, so the Mach-O and PE headers
	// reference the payload right after the APE header.
	if payloads[0].arch != sys.AMD64 {
		payloads[0], payloads[1] = payloads[1], payloads[0]
	}
	writeAPEFile(outfile, payloads, win)
}

func isPlainPEPayload(data []byte) bool {
	return len(data) >= 8 && string(data[0:2]) == "MZ" && string(data[0:7]) != "MZqFpD="
}

// payloadFromAPEOrELF extracts an APE payload from data, which may be a raw
// ELF image or an APE file produced by this linker (whose single payload
// lives at apeHeaderSize with p_offset values shifted by apeHeaderSize).
func payloadFromAPEOrELF(data []byte) (*apePayload, error) {
	if len(data) > apeHeaderSize+64 && string(data[0:7]) == "MZqFpD=" {
		// Validate before shiftPOffsets touches the program headers.
		p, err := payloadFromELF(data[apeHeaderSize:])
		if err != nil {
			return nil, err
		}
		if hasSecondAPEPayload(data) {
			return nil, fmt.Errorf("input is already a fat APE; pass the original single-arch binaries")
		}
		delta := uint64(apeHeaderSize)
		p.elf = shiftPOffsets(p.elf, -delta) // unsigned wraparound subtracts
		return p, nil
	}
	return payloadFromELF(data)
}

// hasSecondAPEPayload reports whether the APE file data contains another ELF
// image beyond the extent of the first payload at apeHeaderSize, meaning it
// is already a fat APE. Merging such a file would silently ingest the first
// payload's slice spanning both images, so apeFatMerge rejects it. The first
// payload must already have passed payloadFromELF validation. layoutAPE
// places every additional payload at an apePayloadAlign boundary at or after
// the previous image's end, so scanning aligned offsets beyond the first
// image's segments finds it.
func hasSecondAPEPayload(data []byte) bool {
	elf := data[apeHeaderSize:]
	phoff := binary.LittleEndian.Uint64(elf[32:40])
	phnum := binary.LittleEndian.Uint16(elf[56:58])
	extent := uint64(apeHeaderSize + 64)
	for i := uint16(0); i < phnum; i++ {
		ph := elf[phoff+uint64(i)*56:]
		// p_offset values in a stored APE are absolute file offsets.
		off := binary.LittleEndian.Uint64(ph[8:16])
		filesz := binary.LittleEndian.Uint64(ph[32:40])
		end := off + filesz
		if end < off || end > uint64(len(data)) {
			end = uint64(len(data))
		}
		if end > extent {
			extent = end
		}
	}
	for off := (extent + apePayloadAlign - 1) &^ uint64(apePayloadAlign-1); off+4 <= uint64(len(data)); off += apePayloadAlign {
		if string(data[off:off+4]) == elfMagic {
			return true
		}
	}
	return false
}
