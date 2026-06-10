// Copyright 2026 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package ld

import (
	"cmd/internal/sys"
	"os"
	"strings"
)

// apeFatMerge implements the -apefat linker mode: it merges the given
// GOOS=cosmo binaries (one amd64, one arm64; each either an APE produced by
// this linker or a raw ELF) into a single fat APE at outfile, skipping
// normal linking entirely.
func apeFatMerge(spec, outfile string) {
	if outfile == "" {
		Exitf("-apefat requires -o")
	}
	inputs := strings.Split(spec, ",")
	if len(inputs) != 2 {
		Exitf("-apefat requires exactly two comma-separated input binaries")
	}
	var payloads []*apePayload
	for _, in := range inputs {
		data, err := os.ReadFile(in)
		if err != nil {
			Exitf("-apefat: %v", err)
		}
		p, err := payloadFromAPEOrELF(data)
		if err != nil {
			Exitf("-apefat: %s: %v", in, err)
		}
		payloads = append(payloads, p)
	}
	if payloads[0].arch == payloads[1].arch {
		Exitf("-apefat: inputs must be different architectures")
	}
	// Canonical layout: amd64 image first, so the Mach-O and PE headers
	// reference the payload right after the APE header.
	if payloads[0].arch != sys.AMD64 {
		payloads[0], payloads[1] = payloads[1], payloads[0]
	}
	writeAPEFile(outfile, payloads)
}

// payloadFromAPEOrELF extracts an APE payload from data, which may be a raw
// ELF image or an APE file produced by this linker (whose single payload
// lives at apeHeaderSize with p_offset values shifted by apeHeaderSize).
func payloadFromAPEOrELF(data []byte) (*apePayload, error) {
	if len(data) > apeHeaderSize+64 && string(data[0:7]) == "MZqFpD=" {
		delta := uint64(apeHeaderSize)
		elf := shiftPOffsets(data[apeHeaderSize:], -delta) // unsigned wraparound subtracts
		return payloadFromELF(elf)
	}
	return payloadFromELF(data)
}
